// Package sftpclient wraps a *sftp.Client with domain error translation and
// a defense against remote symlinks that resolve outside the confined
// remote root. It never executes a remote shell and never disables any SFTP
// safety check; every operation goes through the SFTP subsystem only.
package sftpclient

import (
	"context"
	stderrors "errors"
	"io"
	"os"
	"path"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	errs "github.com/Dmn117/ftp2sftp/internal/errors"
	"github.com/Dmn117/ftp2sftp/internal/filesystem"
)

const posixRenameExtension = "posix-rename@openssh.com"

// Client is a per-session SFTP client confined to a remote root. It owns
// both the SFTP subsystem and the underlying SSH connection: closing it
// closes both.
type Client struct {
	ssh  *ssh.Client
	sftp *sftp.Client
	root string
}

// New opens an SFTP subsystem channel over an already-authenticated SSH
// connection. The Client takes ownership of sshClient: closing the Client
// closes sshClient too.
func New(sshClient *ssh.Client, root string) (*Client, error) {
	const op = "sftpclient.New"

	raw, err := sftp.NewClient(sshClient)
	if err != nil {
		_ = sshClient.Close()

		return nil, errs.Wrap(errs.KindSSHConnection, op, "no se pudo abrir el canal SFTP", err)
	}

	return &Client{ssh: sshClient, sftp: raw, root: path.Clean("/" + root)}, nil
}

// Close closes the SFTP subsystem and the underlying SSH connection. It is
// nil-safe for the zero value (a Client that was never dialed) so callers
// holding a not-yet-connected placeholder can close unconditionally.
func (c *Client) Close() error {
	var sftpErr, sshErr error

	if c.sftp != nil {
		sftpErr = c.sftp.Close()
	}

	if c.ssh != nil {
		sshErr = c.ssh.Close()
	}

	if sftpErr != nil {
		return sftpErr
	}

	return sshErr
}

// HasPosixRename reports whether the remote server advertises the
// posix-rename@openssh.com extension, which allows an atomic rename that
// replaces an existing destination. Exposed for logging/diagnostics.
func (c *Client) HasPosixRename() bool {
	_, ok := c.sftp.HasExtension(posixRenameExtension)

	return ok
}

// Stat returns file info for path, after checking it does not resolve
// through a symlink that escapes the confined remote root.
func (c *Client) Stat(remotePath string) (os.FileInfo, error) {
	const op = "sftpclient.Stat"

	if err := c.checkNoEscapingSymlink(remotePath); err != nil {
		return nil, err
	}

	info, err := c.sftp.Stat(remotePath)
	if err != nil {
		return nil, translateError(op, err)
	}

	return info, nil
}

// ReadDir lists a directory, excluding any entry that is a symlink
// resolving outside the confined remote root. It is bounded by timeout:
// pkg/sftp pages a directory listing as a sequential loop of round trips
// to the remote server (one SSH_FXP_READDIR request per batch of
// entries), with no built-in limit on how many round trips or how long
// that takes. Against a remote directory with tens of thousands of
// entries this was observed, in practice, to run long enough that the
// remote server itself killed the SSH channel mid-listing, surfacing as
// an opaque "failed to send packet: EOF" — this timeout turns that into a
// prompt, well-defined KindTimeout error instead. See
// docs/security/security-model.md for the broader residual risk this
// only partially closes (other SFTP operations still have no per-call
// deadline; pkg/sftp v1.13.11 only exposes a context-aware variant for
// ReadDir).
func (c *Client) ReadDir(remotePath string, timeout time.Duration) ([]os.FileInfo, error) {
	const op = "sftpclient.ReadDir"

	if err := c.checkNoEscapingSymlink(remotePath); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	entries, err := c.sftp.ReadDirContext(ctx, remotePath)
	if err != nil {
		return nil, translateError(op, err)
	}

	safe := make([]os.FileInfo, 0, len(entries))

	for _, entry := range entries {
		if entry.Mode()&os.ModeSymlink != 0 {
			childPath := path.Join(remotePath, entry.Name())
			if err := c.checkNoEscapingSymlink(childPath); err != nil {
				continue // silently excluded; caller logs the directory op, not each skip
			}
		}

		safe = append(safe, entry)
	}

	return safe, nil
}

// OpenRead opens remotePath for reading (RETR). The returned handle also
// implements io.Seeker, needed for download resume (REST before RETR).
func (c *Client) OpenRead(remotePath string) (io.ReadSeekCloser, error) {
	const op = "sftpclient.OpenRead"

	if err := c.checkNoEscapingSymlink(remotePath); err != nil {
		return nil, err
	}

	f, err := c.sftp.OpenFile(remotePath, os.O_RDONLY)
	if err != nil {
		return nil, translateError(op, err)
	}

	return f, nil
}

// CreateTemp creates (or truncates) remotePath for writing. It is used only
// for the temporary upload name (RF-007); the final name is reached via
// Commit, never by writing to it directly.
func (c *Client) CreateTemp(remotePath string) (io.WriteCloser, error) {
	const op = "sftpclient.CreateTemp"

	f, err := c.sftp.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return nil, translateError(op, err)
	}

	return f, nil
}

// Commit renames tempPath to finalPath, completing an upload. When the
// remote server supports the posix-rename@openssh.com extension, the
// rename is atomic and, if overwrite is true, replaces any existing
// finalPath in one step. Without that extension, an overwrite is performed
// as a best-effort remove-then-rename, which is not atomic; see
// docs/architecture/ADR for the accepted risk.
func (c *Client) Commit(tempPath, finalPath string, overwrite bool) error {
	const op = "sftpclient.Commit"

	if err := c.checkNoEscapingSymlink(finalPath); err != nil {
		return err
	}

	if c.HasPosixRename() {
		if !overwrite {
			if err := c.rejectIfExists(finalPath); err != nil {
				return err
			}
		}

		if err := c.sftp.PosixRename(tempPath, finalPath); err != nil {
			return translateError(op, err)
		}

		return nil
	}

	_, statErr := c.sftp.Stat(finalPath)
	exists := statErr == nil

	if exists && !overwrite {
		return errs.New(errs.KindConflict, op, "el archivo destino ya existe")
	}

	if exists {
		if err := c.sftp.Remove(finalPath); err != nil {
			return translateError(op, err)
		}
	}

	if err := c.sftp.Rename(tempPath, finalPath); err != nil {
		return translateError(op, err)
	}

	return nil
}

func (c *Client) rejectIfExists(remotePath string) error {
	if _, err := c.sftp.Stat(remotePath); err == nil {
		return errs.New(errs.KindConflict, "sftpclient.Commit", "el archivo destino ya existe")
	}

	return nil
}

// Remove deletes a file (DELE).
func (c *Client) Remove(remotePath string) error {
	const op = "sftpclient.Remove"

	if err := c.checkNoEscapingSymlink(remotePath); err != nil {
		return err
	}

	if err := c.sftp.Remove(remotePath); err != nil {
		return translateError(op, err)
	}

	return nil
}

// RemoveDirectory removes an empty directory (RMD).
func (c *Client) RemoveDirectory(remotePath string) error {
	const op = "sftpclient.RemoveDirectory"

	if err := c.checkNoEscapingSymlink(remotePath); err != nil {
		return err
	}

	if err := c.sftp.RemoveDirectory(remotePath); err != nil {
		return translateError(op, err)
	}

	return nil
}

// Mkdir creates a directory (MKD).
func (c *Client) Mkdir(remotePath string) error {
	const op = "sftpclient.Mkdir"

	if err := c.sftp.Mkdir(remotePath); err != nil {
		return translateError(op, err)
	}

	return nil
}

// Rename renames oldPath to newPath (RNFR/RNTO) without the upload commit
// semantics of Commit (no temp-name assumption, straightforward user
// rename request).
func (c *Client) Rename(oldPath, newPath string, overwrite bool) error {
	const op = "sftpclient.Rename"

	if err := c.checkNoEscapingSymlink(oldPath); err != nil {
		return err
	}

	if err := c.checkNoEscapingSymlink(newPath); err != nil {
		return err
	}

	if c.HasPosixRename() {
		if !overwrite {
			if err := c.rejectIfExists(newPath); err != nil {
				return err
			}
		}

		if err := c.sftp.PosixRename(oldPath, newPath); err != nil {
			return translateError(op, err)
		}

		return nil
	}

	if _, err := c.sftp.Stat(newPath); err == nil {
		if !overwrite {
			return errs.New(errs.KindConflict, op, "el destino del renombrado ya existe")
		}

		if err := c.sftp.Remove(newPath); err != nil {
			return translateError(op, err)
		}
	}

	if err := c.sftp.Rename(oldPath, newPath); err != nil {
		return translateError(op, err)
	}

	return nil
}

// checkNoEscapingSymlink rejects remotePath if it exists and is a symlink
// whose target resolves outside the confined remote root. It only checks
// the leaf of remotePath, not each ancestor directory component; a symlink
// placed in an ancestor directory is not detected by this check alone (see
// docs/security/security-model.md for the accepted residual risk). The
// remote SFTP account's own chroot/confinement, required by
// FTP2SFTP-REQUIREMENTS.md section 2.3, remains the primary control.
func (c *Client) checkNoEscapingSymlink(remotePath string) error {
	const op = "sftpclient.checkNoEscapingSymlink"

	lstat, err := c.sftp.Lstat(remotePath)
	if err != nil {
		if stderrors.Is(err, os.ErrNotExist) {
			return nil
		}

		return translateError(op, err)
	}

	if lstat.Mode()&os.ModeSymlink == 0 {
		return nil
	}

	target, err := c.sftp.ReadLink(remotePath)
	if err != nil {
		return translateError(op, err)
	}

	resolved := target
	if !path.IsAbs(resolved) {
		resolved = path.Join(path.Dir(remotePath), resolved)
	}

	resolved = path.Clean(resolved)

	if !filesystem.WithinRoot(c.root, resolved) {
		return errs.New(errs.KindInvalidPath, op, "la ruta remota es un enlace simbólico fuera de la raíz permitida")
	}

	return nil
}

// translateError maps a pkg/sftp / SSH error into a domain error with a
// wire-safe message. The original error is preserved as the cause for
// logging only.
func translateError(op string, err error) error {
	if err == nil {
		return nil
	}

	if stderrors.Is(err, io.EOF) {
		return err // callers expect the plain io.EOF sentinel
	}

	if stderrors.Is(err, context.DeadlineExceeded) {
		return errs.Wrap(errs.KindTimeout, op, "tiempo de espera agotado en la operación SFTP remota", err)
	}

	switch {
	case stderrors.Is(err, os.ErrNotExist):
		return errs.Wrap(errs.KindNotFound, op, "ruta remota no encontrada", err)
	case stderrors.Is(err, os.ErrPermission):
		return errs.Wrap(errs.KindRemotePermissionDenied, op, "permiso denegado en el servidor remoto", err)
	}

	var netErr interface{ Timeout() bool }
	if stderrors.As(err, &netErr) && netErr.Timeout() {
		return errs.Wrap(errs.KindTimeout, op, "tiempo de espera agotado en el servidor remoto", err)
	}

	return errs.Wrap(errs.KindInternal, op, "error en la operación SFTP remota", err)
}

// Ping performs a cheap remote round trip (Stat on the confined root) to
// verify the SFTP session is still usable. Callers that need a bounded
// wait (e.g. an HTTP readiness handler) must run it in a goroutine with
// their own timeout, since the underlying SFTP client has no per-call
// context support.
func (c *Client) Ping() error {
	_, err := c.sftp.Stat(c.root)

	return err
}
