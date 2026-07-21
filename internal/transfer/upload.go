package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"sync"

	errs "github.com/Dmn117/ftp2sftp/internal/errors"
)

// UploadOptions configures a single STOR.
type UploadOptions struct {
	SessionID       string
	TransferID      string
	VirtualPath     string
	MaxSize         int64 // <=0 means unlimited
	Overwrite       bool
	CalculateSHA256 bool
	TemporarySuffix string
	OnComplete      func(Result)
	// Release is called exactly once, from Close, to free any resource the
	// caller reserved for this transfer (e.g. a per-user concurrency
	// slot). It may be nil.
	Release func()
}

// NewUpload prepares a temporary remote file for a STOR and returns a
// handle implementing io.Reader, io.Writer, io.Seeker and io.Closer, ready
// to be handed to the FTP server library as the transfer's FileTransfer.
//
// The temporary file is only ever visible under its RF-007 temporary name;
// Close renames it to finalRemotePath on success and removes it on
// failure, so an incomplete upload never becomes visible under its final
// name (MVP acceptance criterion #6).
func NewUpload(fs RemoteFS, finalRemotePath string, opts UploadOptions) (*UploadHandle, error) {
	const op = "transfer.NewUpload"

	if !opts.Overwrite {
		if _, err := fs.Stat(finalRemotePath); err == nil {
			return nil, errs.New(errs.KindConflict, op, "el archivo destino ya existe")
		} else if !isNotFound(err) {
			return nil, err
		}
	}

	tempPath := TempPath(finalRemotePath, opts.SessionID, opts.TemporarySuffix)

	file, err := fs.CreateTemp(tempPath)
	if err != nil {
		return nil, err
	}

	var hasher hash.Hash
	if opts.CalculateSHA256 {
		hasher = sha256.New()
	}

	return &UploadHandle{
		fs:        fs,
		file:      file,
		tempPath:  tempPath,
		finalPath: finalRemotePath,
		opts:      opts,
		hasher:    hasher,
	}, nil
}

// UploadHandle is the FileTransfer implementation used for STOR. It is not
// safe for concurrent use by multiple goroutines (the FTP server library
// never does so for a single transfer).
type UploadHandle struct {
	mu     sync.Mutex
	fs     RemoteFS
	file   io.WriteCloser
	hasher hash.Hash

	tempPath  string
	finalPath string
	opts      UploadOptions

	written int64
	failed  bool
	failErr error
	closed  bool
}

// Write streams p to the temporary remote file, enforcing the configured
// maximum size as bytes arrive rather than trusting a client-declared
// length.
func (u *UploadHandle) Write(p []byte) (int, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.opts.MaxSize > 0 && u.written+int64(len(p)) > u.opts.MaxSize {
		u.failed = true
		u.failErr = errs.New(errs.KindAuthorization, "transfer.Write", "el archivo excede el tamaño máximo permitido")

		return 0, u.failErr
	}

	n, err := u.file.Write(p)
	u.written += int64(n)

	if n > 0 && u.hasher != nil {
		u.hasher.Write(p[:n])
	}

	if err != nil {
		u.failed = true
		u.failErr = err
	}

	return n, err
}

// Read is not supported for an upload handle.
func (u *UploadHandle) Read([]byte) (int, error) {
	return 0, errs.New(errs.KindInternal, "transfer.Read", "operación no soportada durante una subida")
}

// Seek only accepts a no-op seek to the start: upload resume (STOR with a
// prior REST) is not supported in the MVP (see docs/protocols for the
// documented limitation and extension path).
func (u *UploadHandle) Seek(offset int64, whence int) (int64, error) {
	if offset == 0 && whence == io.SeekStart {
		return 0, nil
	}

	return 0, errs.New(errs.KindUnsupportedCommand, "transfer.Seek", "reanudar subidas (REST) no está soportado")
}

// TransferError implements ftpserverlib's FileTransferError extension: it
// is called before Close when the data connection drops, the client sends
// ABOR, or an I/O error interrupts the copy.
func (u *UploadHandle) TransferError(err error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.failed = true

	if u.failErr == nil {
		u.failErr = err
	}
}

// Close finalizes the transfer: on success it commits the temporary file
// to its final name (RF-007); on failure it removes the temporary file.
// Either way it reports a Result via opts.OnComplete and releases the
// caller's reserved resource exactly once.
func (u *UploadHandle) Close() error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.closed {
		return nil
	}

	u.closed = true

	if u.opts.Release != nil {
		defer u.opts.Release()
	}

	closeErr := u.file.Close()
	if closeErr != nil {
		u.failed = true

		if u.failErr == nil {
			u.failErr = closeErr
		}
	}

	result := Result{
		TransferID:  u.opts.TransferID,
		SessionID:   u.opts.SessionID,
		VirtualPath: u.opts.VirtualPath,
		RemotePath:  u.finalPath,
		Bytes:       u.written,
	}

	if u.hasher != nil {
		result.SHA256 = hex.EncodeToString(u.hasher.Sum(nil))
	}

	if u.failed {
		result.Phase = PhaseFailed
		result.Err = u.failErr

		_ = u.fs.Remove(u.tempPath) // best effort; nothing else can be done here

		if u.opts.OnComplete != nil {
			u.opts.OnComplete(result)
		}

		return u.failErr
	}

	result.Phase = PhaseWritten

	if err := u.fs.Commit(u.tempPath, u.finalPath, u.opts.Overwrite); err != nil {
		result.Phase = PhaseFailed
		result.Err = err

		_ = u.fs.Remove(u.tempPath)

		if u.opts.OnComplete != nil {
			u.opts.OnComplete(result)
		}

		return err
	}

	result.Phase = PhaseCommitted

	if u.opts.OnComplete != nil {
		u.opts.OnComplete(result)
	}

	return nil
}
