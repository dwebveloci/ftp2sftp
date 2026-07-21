package ftpserver

import (
	"os"
	"strings"
	"time"

	"github.com/spf13/afero"

	errs "github.com/Dmn117/ftp2sftp/internal/errors"
	"github.com/Dmn117/ftp2sftp/internal/observability"
	"github.com/Dmn117/ftp2sftp/internal/session"
	"github.com/Dmn117/ftp2sftp/internal/sftpclient"
	"github.com/Dmn117/ftp2sftp/internal/transfer"

	libftpserver "github.com/fclairamb/ftpserverlib"
)

var errNotImplemented = errs.New(errs.KindUnsupportedCommand, "ftpserver.ClientDriver", "operación no soportada")

// clientDriver implements ftpserverlib.ClientDriver (which is afero.Fs)
// plus the GetHandle/ReadDir/RemoveDir extensions, for one authenticated
// FTP connection. Every path it receives from ftpserverlib is already
// absolute and lexically cleaned by the library; what it still must
// enforce is containment within the user's configured virtual root, which
// the library has no notion of (see internal/session doc comment).
type clientDriver struct {
	gw   *Gateway
	sess *session.Session
}

func newClientDriver(gw *Gateway, sess *session.Session) *clientDriver {
	return &clientDriver{gw: gw, sess: sess}
}

// resolveAndContain re-validates that name is confined to the user's
// virtual root. name is already absolute (ftpserverlib guarantees this),
// so ResolveVirtual is invoked with an irrelevant cwd of "/".
func (d *clientDriver) resolveAndContain(name string) (string, error) {
	return d.sess.Mapper().ResolveVirtual("/", name)
}

// remotePath resolves and maps a raw driver path all the way to the
// confined remote SFTP path, also returning the cleaned virtual path for
// logging.
func (d *clientDriver) remotePath(name string) (virtualPath, remotePath string, err error) {
	virtualPath, err = d.resolveAndContain(name)
	if err != nil {
		return "", "", err
	}

	remotePath, err = d.sess.Mapper().ToRemote(virtualPath)
	if err != nil {
		return "", "", err
	}

	return virtualPath, remotePath, nil
}

// sftp returns the session's SFTP connection, translating a dial failure
// into a domain error.
func (d *clientDriver) sftp() (*sftpclient.Client, error) {
	return d.sess.SFTP()
}

// maybeInvalidate closes and discards the session's SFTP connection when
// err indicates the connection itself, rather than the requested
// operation, is the problem, so the next command reconnects instead of
// repeatedly failing against a dead connection.
func (d *clientDriver) maybeInvalidate(err error) {
	if err == nil {
		return
	}

	switch errs.KindOf(err) {
	case errs.KindSSHConnection, errs.KindTimeout, errs.KindDisconnected:
		if d.sess.IsConnected() {
			d.sess.InvalidateSFTP()
			d.gw.metrics.SFTPConnectionsActive.Dec()
		}
	}
}

func (d *clientDriver) audit(command, virtualPath string, err error) {
	d.gw.metrics.FTPCommandsTotal.Inc()

	attrs := []any{
		"sessionId", d.sess.ID(), "ftpUser", d.sess.Username(), "clientIp", d.sess.ClientIP(),
		"command", command, "virtualPath", virtualPath,
	}

	if err != nil {
		d.gw.logger.Warn("command failed", append(attrs, "err", err.Error())...)

		return
	}

	d.gw.logger.Info("command completed", attrs...)
}

// --- afero.Fs ---

func (d *clientDriver) Name() string { return "ftp2sftp" }

func (d *clientDriver) Stat(name string) (os.FileInfo, error) {
	_, remote, err := d.remotePath(name)
	if err != nil {
		return nil, err
	}

	sc, err := d.sftp()
	if err != nil {
		return nil, err
	}

	info, err := sc.Stat(remote)
	d.maybeInvalidate(err)

	if err != nil {
		return nil, err
	}

	return info, nil
}

func (d *clientDriver) Mkdir(name string, _ os.FileMode) error {
	virtualPath, remote, err := d.remotePath(name)
	if err != nil {
		return err
	}

	if err := d.sess.Policy().CheckMkdir(); err != nil {
		d.audit("MKD", virtualPath, err)

		return err
	}

	sc, err := d.sftp()
	if err != nil {
		return err
	}

	err = sc.Mkdir(remote)
	d.maybeInvalidate(err)
	d.audit("MKD", virtualPath, err)

	return err
}

// MkdirAll backs the non-standard "MKDIR" command some clients send. It is
// not in the RF-004 command list, so it is intentionally unsupported
// rather than silently implementing recursive creation.
func (d *clientDriver) MkdirAll(string, os.FileMode) error {
	return errNotImplemented
}

func (d *clientDriver) Remove(name string) error {
	virtualPath, remote, err := d.remotePath(name)
	if err != nil {
		return err
	}

	if err := d.sess.Policy().CheckDelete(); err != nil {
		d.audit("DELE", virtualPath, err)

		return err
	}

	sc, err := d.sftp()
	if err != nil {
		return err
	}

	err = sc.Remove(remote)
	d.maybeInvalidate(err)
	d.audit("DELE", virtualPath, err)

	return err
}

// RemoveAll is not offered over FTP (no recursive delete command in
// RF-004); DELE/RMD go through Remove/RemoveDir instead.
func (d *clientDriver) RemoveAll(string) error {
	return errNotImplemented
}

// RemoveDir implements ClientDriverExtensionRemoveDir, letting ftpserverlib
// route RMD here separately from DELE's Remove.
func (d *clientDriver) RemoveDir(name string) error {
	virtualPath, remote, err := d.remotePath(name)
	if err != nil {
		return err
	}

	if err := d.sess.Policy().CheckDelete(); err != nil {
		d.audit("RMD", virtualPath, err)

		return err
	}

	sc, err := d.sftp()
	if err != nil {
		return err
	}

	err = sc.RemoveDirectory(remote)
	d.maybeInvalidate(err)
	d.audit("RMD", virtualPath, err)

	return err
}

func (d *clientDriver) Rename(oldName, newName string) error {
	oldVirtual, oldRemote, err := d.remotePath(oldName)
	if err != nil {
		return err
	}

	newVirtual, newRemote, err := d.remotePath(newName)
	if err != nil {
		return err
	}

	policy := d.sess.Policy()
	if err := policy.CheckRename(); err != nil {
		d.audit("RNTO", oldVirtual+" -> "+newVirtual, err)

		return err
	}

	sc, err := d.sftp()
	if err != nil {
		return err
	}

	err = sc.Rename(oldRemote, newRemote, policy.AllowOverwrite)
	d.maybeInvalidate(err)
	d.audit("RNTO", oldVirtual+" -> "+newVirtual, err)

	return err
}

// Open, OpenFile and Create are required by afero.Fs but never actually
// invoked: this driver implements ClientDriverExtentionFileTransfer
// (GetHandle) and ClientDriverExtensionFileList (ReadDir), which
// ftpserverlib always prefers over these when present.
func (d *clientDriver) Open(string) (afero.File, error) { return nil, errNotImplemented }
func (d *clientDriver) OpenFile(string, int, os.FileMode) (afero.File, error) {
	return nil, errNotImplemented
}
func (d *clientDriver) Create(string) (afero.File, error)          { return nil, errNotImplemented }
func (d *clientDriver) Chmod(string, os.FileMode) error            { return errNotImplemented }
func (d *clientDriver) Chown(string, int, int) error               { return errNotImplemented }
func (d *clientDriver) Chtimes(string, time.Time, time.Time) error { return errNotImplemented }

// --- extensions ---

// ReadDir implements ClientDriverExtensionFileList (LIST/NLST/MLSD).
func (d *clientDriver) ReadDir(name string) ([]os.FileInfo, error) {
	virtualPath, remote, err := d.remotePath(name)
	if err != nil {
		return nil, err
	}

	sc, err := d.sftp()
	if err != nil {
		return nil, err
	}

	timeout := d.gw.users[d.sess.Username()].cfg.SFTP.ReadDirTimeout.Duration()

	entries, err := sc.ReadDir(remote, timeout)
	d.maybeInvalidate(err)
	d.audit("LIST", virtualPath, err)

	if err != nil {
		return nil, err
	}

	// Never let an in-flight upload's RF-007 temporary artifact appear in
	// a listing.
	marker := d.gw.cfg.Transfer.TemporarySuffix + "-"
	visible := make([]os.FileInfo, 0, len(entries))

	for _, e := range entries {
		if strings.Contains(e.Name(), marker) {
			continue
		}

		visible = append(visible, e)
	}

	return visible, nil
}

// GetHandle implements ClientDriverExtentionFileTransfer (STOR/RETR).
func (d *clientDriver) GetHandle(name string, flags int, offset int64) (libftpserver.FileTransfer, error) {
	virtualPath, remote, err := d.remotePath(name)
	if err != nil {
		return nil, err
	}

	sc, err := d.sftp()
	if err != nil {
		return nil, err
	}

	if flags&os.O_WRONLY != 0 {
		return d.beginUpload(sc, virtualPath, remote, flags)
	}

	return d.beginDownload(sc, virtualPath, remote)
}

func (d *clientDriver) beginUpload(sc *sftpclient.Client, virtualPath, remotePath string, flags int) (libftpserver.FileTransfer, error) {
	if flags&os.O_APPEND != 0 {
		return nil, errs.New(errs.KindUnsupportedCommand, "ftpserver.GetHandle", "la operación APPE no está soportada")
	}

	policy := d.sess.Policy()
	if err := policy.CheckUpload(); err != nil {
		d.audit("STOR", virtualPath, err)

		return nil, err
	}

	gate := d.sess.ConcurrencyGate()
	if !gate.TryAcquire() {
		err := errs.New(errs.KindRateLimited, "ftpserver.GetHandle", "límite de transferencias concurrentes del usuario alcanzado")
		d.audit("STOR", virtualPath, err)

		return nil, err
	}

	var released bool

	release := func() {
		if released {
			return
		}

		released = true
		gate.Release()
		d.gw.metrics.TransferActive.Dec()
	}

	d.gw.metrics.TransferActive.Inc()
	d.gw.metrics.TransferTotal.Inc()
	d.gw.metrics.TemporaryFilesPending.Inc()

	transferID := observability.NewCorrelationID()

	upload, err := transfer.NewUpload(sc, remotePath, transfer.UploadOptions{
		SessionID:       d.sess.ID(),
		TransferID:      transferID,
		VirtualPath:     virtualPath,
		MaxSize:         policy.MaxFileSize,
		Overwrite:       policy.AllowOverwrite,
		CalculateSHA256: d.gw.cfg.Transfer.CalculateSHA256,
		TemporarySuffix: d.gw.cfg.Transfer.TemporarySuffix,
		OnComplete:      func(r transfer.Result) { d.gw.recordTransfer("STOR", d.sess, r) },
		Release:         release,
	})
	if err != nil {
		d.gw.metrics.TemporaryFilesPending.Dec()
		release()
		d.audit("STOR", virtualPath, err)

		return nil, err
	}

	return upload, nil
}

func (d *clientDriver) beginDownload(sc *sftpclient.Client, virtualPath, remotePath string) (libftpserver.FileTransfer, error) {
	policy := d.sess.Policy()
	if err := policy.CheckDownload(); err != nil {
		d.audit("RETR", virtualPath, err)

		return nil, err
	}

	gate := d.sess.ConcurrencyGate()
	if !gate.TryAcquire() {
		err := errs.New(errs.KindRateLimited, "ftpserver.GetHandle", "límite de transferencias concurrentes del usuario alcanzado")
		d.audit("RETR", virtualPath, err)

		return nil, err
	}

	var released bool

	release := func() {
		if released {
			return
		}

		released = true
		gate.Release()
		d.gw.metrics.TransferActive.Dec()
	}

	d.gw.metrics.TransferActive.Inc()
	d.gw.metrics.TransferTotal.Inc()

	transferID := observability.NewCorrelationID()

	download, err := transfer.NewDownload(sc, remotePath, transfer.DownloadOptions{
		SessionID:       d.sess.ID(),
		TransferID:      transferID,
		VirtualPath:     virtualPath,
		CalculateSHA256: d.gw.cfg.Transfer.CalculateSHA256,
		OnComplete:      func(r transfer.Result) { d.gw.recordTransfer("RETR", d.sess, r) },
		Release:         release,
	})
	if err != nil {
		release()
		d.audit("RETR", virtualPath, err)

		return nil, err
	}

	return download, nil
}

// Compile-time check that the production SFTP client satisfies what
// transfer.NewUpload/NewDownload need; transfer's tests use a fake instead.
var _ transfer.RemoteFS = (*sftpclient.Client)(nil)
