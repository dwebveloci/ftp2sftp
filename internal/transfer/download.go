package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	stderrors "errors"
	"hash"
	"io"
	"sync"

	errs "github.com/Dmn117/ftp2sftp/internal/errors"
)

// DownloadOptions configures a single RETR.
type DownloadOptions struct {
	SessionID       string
	TransferID      string
	VirtualPath     string
	CalculateSHA256 bool
	OnComplete      func(Result)
	Release         func()
}

// NewDownload opens remoteFile for reading and returns a handle
// implementing io.Reader, io.Writer, io.Seeker and io.Closer, ready to be
// handed to the FTP server library as the transfer's FileTransfer. Unlike
// uploads, seeking (download resume via REST) is supported: it is a safe
// operation on an existing, already-complete remote file.
func NewDownload(fs RemoteFS, remotePath string, opts DownloadOptions) (*DownloadHandle, error) {
	file, err := fs.OpenRead(remotePath)
	if err != nil {
		return nil, err
	}

	var hasher hash.Hash
	if opts.CalculateSHA256 {
		hasher = sha256.New()
	}

	return &DownloadHandle{file: file, remotePath: remotePath, opts: opts, hasher: hasher}, nil
}

// DownloadHandle is the FileTransfer implementation used for RETR.
type DownloadHandle struct {
	mu     sync.Mutex
	file   io.ReadSeekCloser
	hasher hash.Hash

	remotePath string
	opts       DownloadOptions

	read    int64
	failed  bool
	failErr error
	closed  bool
}

// Read streams from the remote file.
func (d *DownloadHandle) Read(p []byte) (int, error) {
	n, err := d.file.Read(p)

	if n > 0 {
		d.mu.Lock()
		d.read += int64(n)

		if d.hasher != nil {
			d.hasher.Write(p[:n])
		}

		d.mu.Unlock()
	}

	if err != nil && !stderrors.Is(err, io.EOF) {
		d.mu.Lock()
		d.failed = true
		d.failErr = err
		d.mu.Unlock()
	}

	return n, err
}

// Write is not supported for a download handle.
func (d *DownloadHandle) Write([]byte) (int, error) {
	return 0, errs.New(errs.KindInternal, "transfer.Write", "operación no soportada durante una descarga")
}

// Seek supports download resume (RETR after REST).
func (d *DownloadHandle) Seek(offset int64, whence int) (int64, error) {
	return d.file.Seek(offset, whence)
}

// TransferError implements ftpserverlib's FileTransferError extension.
func (d *DownloadHandle) TransferError(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.failed = true

	if d.failErr == nil {
		d.failErr = err
	}
}

// Close finalizes the download and reports a Result via opts.OnComplete.
func (d *DownloadHandle) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return nil
	}

	d.closed = true

	if d.opts.Release != nil {
		defer d.opts.Release()
	}

	closeErr := d.file.Close()

	result := Result{
		TransferID:  d.opts.TransferID,
		SessionID:   d.opts.SessionID,
		VirtualPath: d.opts.VirtualPath,
		RemotePath:  d.remotePath,
		Bytes:       d.read,
		Phase:       PhaseWritten,
	}

	if d.hasher != nil {
		result.SHA256 = hex.EncodeToString(d.hasher.Sum(nil))
	}

	if d.failed {
		result.Phase = PhaseFailed
		result.Err = d.failErr
	} else if closeErr != nil {
		result.Phase = PhaseFailed
		result.Err = closeErr
	} else {
		result.Phase = PhaseCommitted // a download has no separate commit step; reuse the "done" phase
	}

	if d.opts.OnComplete != nil {
		d.opts.OnComplete(result)
	}

	if d.failed {
		return d.failErr
	}

	return closeErr
}
