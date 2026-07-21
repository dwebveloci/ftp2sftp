// Package authorization decides, for an already-authenticated FTP user,
// which mutating operations are permitted and enforces per-user size and
// concurrency limits. It never resolves or validates paths (that is
// internal/filesystem's job) and never talks to the network.
package authorization

import (
	"fmt"

	errs "github.com/Dmn117/ftp2sftp/internal/errors"
)

// Policy is the set of permissions and limits configured for one FTP user.
// The zero value denies every mutating operation, matching "deny by
// default".
type Policy struct {
	AllowUpload    bool
	AllowDownload  bool
	AllowDelete    bool
	AllowMkdir     bool
	AllowRename    bool
	AllowOverwrite bool
	MaxFileSize    int64
}

const opDenied = "authorization.check"

// CheckUpload returns nil if STOR is permitted for this user.
func (p Policy) CheckUpload() error {
	if !p.AllowUpload {
		return errs.New(errs.KindAuthorization, opDenied, "subida no autorizada")
	}

	return nil
}

// CheckDownload returns nil if RETR is permitted for this user.
func (p Policy) CheckDownload() error {
	if !p.AllowDownload {
		return errs.New(errs.KindAuthorization, opDenied, "descarga no autorizada")
	}

	return nil
}

// CheckDelete returns nil if DELE/RMD is permitted for this user.
func (p Policy) CheckDelete() error {
	if !p.AllowDelete {
		return errs.New(errs.KindAuthorization, opDenied, "eliminación no autorizada")
	}

	return nil
}

// CheckMkdir returns nil if MKD is permitted for this user.
func (p Policy) CheckMkdir() error {
	if !p.AllowMkdir {
		return errs.New(errs.KindAuthorization, opDenied, "creación de directorio no autorizada")
	}

	return nil
}

// CheckRename returns nil if RNFR/RNTO is permitted for this user.
func (p Policy) CheckRename() error {
	if !p.AllowRename {
		return errs.New(errs.KindAuthorization, opDenied, "renombrado no autorizado")
	}

	return nil
}

// CheckOverwrite returns nil if STOR/RNTO may replace an existing target.
func (p Policy) CheckOverwrite() error {
	if !p.AllowOverwrite {
		return errs.New(errs.KindConflict, opDenied, "el archivo destino ya existe")
	}

	return nil
}

// CheckFileSize returns nil if size is within the user's configured limit.
// A size of -1 (unknown, e.g. streamed upload without a declared length) is
// always allowed here; the transfer module enforces the limit as bytes are
// streamed.
func (p Policy) CheckFileSize(size int64) error {
	if size < 0 {
		return nil
	}

	if size > p.MaxFileSize {
		return errs.New(errs.KindAuthorization, opDenied,
			fmt.Sprintf("el archivo excede el tamaño máximo permitido (%d bytes)", p.MaxFileSize))
	}

	return nil
}
