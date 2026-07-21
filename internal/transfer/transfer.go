// Package transfer orchestrates a single FTP upload or download: temporary
// remote naming, bounded streaming (delegated to the FTP/SFTP libraries,
// which never buffer a whole file), optional SHA-256, atomic-as-possible
// commit, and cleanup on failure.
//
// This package depends only on a narrow RemoteFS interface, not on the
// concrete internal/sftpclient.Client, so its orchestration logic can be
// unit-tested without a real SFTP server. internal/sftpclient.Client is the
// only production implementation.
package transfer

import (
	"io"
	"os"
	"path"

	errs "github.com/Dmn117/ftp2sftp/internal/errors"
)

// RemoteFS is the subset of sftpclient.Client that transfer needs.
type RemoteFS interface {
	Stat(remotePath string) (os.FileInfo, error)
	OpenRead(remotePath string) (io.ReadSeekCloser, error)
	CreateTemp(remotePath string) (io.WriteCloser, error)
	Commit(tempPath, finalPath string, overwrite bool) error
	Remove(remotePath string) error
}

// Phase is the last lifecycle stage a transfer reached, used for audit
// events (RF-015) and to distinguish a clean failure from a successful
// commit.
type Phase string

const (
	PhaseStarted   Phase = "started"
	PhaseWritten   Phase = "written"
	PhaseCommitted Phase = "committed"
	PhaseFailed    Phase = "failed"
)

// Result summarizes a finished transfer for audit logging.
type Result struct {
	TransferID  string
	SessionID   string
	VirtualPath string
	RemotePath  string
	Bytes       int64
	SHA256      string
	Phase       Phase
	Err         error
}

// TempPath builds the RF-007 temporary remote name for an upload:
// "<basename><suffix>-<sessionID>" in the same remote directory as the
// final path, e.g. "archivo.xml" + ".part" + "-" + sessionID.
func TempPath(finalRemotePath, sessionID, suffix string) string {
	dir := path.Dir(finalRemotePath)
	base := path.Base(finalRemotePath)

	return path.Join(dir, base+suffix+"-"+sessionID)
}

func isNotFound(err error) bool {
	return errs.Is(err, errs.KindNotFound)
}
