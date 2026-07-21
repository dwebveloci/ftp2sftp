// Package filesystem normalizes FTP virtual paths and maps them to remote
// SFTP paths, without ever allowing a resolved path to escape the
// configured virtual or remote root.
//
// This package only manipulates strings; it has no network access. Defense
// against remote symlinks that could point outside the remote root lives in
// internal/sftpclient, which is the layer that can actually query the
// remote filesystem.
package filesystem

import (
	"path"
	"strings"

	errs "github.com/Dmn117/ftp2sftp/internal/errors"
)

// Mapper confines a single FTP user to a virtual root and translates
// virtual paths to the corresponding path on the remote SFTP server.
type Mapper struct {
	virtualRoot string
	remoteRoot  string
}

// NewMapper builds a Mapper for a user's virtual root and the remote root
// their traffic is confined to. Both roots must be clean absolute paths;
// this is enforced by config validation, so NewMapper treats a violation as
// a programming error via a safe fallback rather than a runtime error.
func NewMapper(virtualRoot, remoteRoot string) *Mapper {
	return &Mapper{
		virtualRoot: cleanRoot(virtualRoot),
		remoteRoot:  cleanRoot(remoteRoot),
	}
}

func cleanRoot(p string) string {
	if p == "" {
		p = "/"
	}

	cleaned := path.Clean("/" + p)

	return cleaned
}

// ResolveVirtual resolves a client-supplied path argument (as sent to CWD,
// STOR, RETR, LIST, MKD, RNFR/RNTO, DELE, RMD, SIZE, MDTM) against the
// session's current virtual directory. It returns the cleaned, absolute
// virtual path. The result is always confined to the user's virtual root;
// any attempt to escape it (via "..", an absolute path outside the root, or
// a NUL byte) is rejected.
func (m *Mapper) ResolveVirtual(cwd, arg string) (string, error) {
	const op = "filesystem.ResolveVirtual"

	if strings.ContainsRune(arg, 0) {
		return "", errs.New(errs.KindInvalidPath, op, "ruta inválida")
	}

	if cwd == "" {
		cwd = m.virtualRoot
	}

	var joined string
	if path.IsAbs(arg) {
		joined = arg
	} else {
		joined = path.Join(cwd, arg)
	}

	resolved := path.Clean("/" + joined)

	if !m.withinVirtualRoot(resolved) {
		return "", errs.New(errs.KindInvalidPath, op, "ruta fuera de la raíz permitida")
	}

	return resolved, nil
}

// ToRemote converts an already-resolved absolute virtual path (as returned
// by ResolveVirtual) into the corresponding absolute remote SFTP path.
func (m *Mapper) ToRemote(virtualPath string) (string, error) {
	const op = "filesystem.ToRemote"

	if !m.withinVirtualRoot(virtualPath) {
		return "", errs.New(errs.KindInvalidPath, op, "ruta fuera de la raíz permitida")
	}

	rel := strings.TrimPrefix(virtualPath, m.virtualRoot)
	rel = strings.TrimPrefix(rel, "/")

	remote := path.Clean(path.Join(m.remoteRoot, rel))
	if !m.withinRemoteRoot(remote) {
		return "", errs.New(errs.KindInvalidPath, op, "ruta remota fuera de la raíz permitida")
	}

	return remote, nil
}

// ToVirtual converts a remote SFTP path (e.g. a directory entry returned by
// ReadDir) back into virtual path space for use in LIST/NLST output. If the
// remote path is not under the configured remote root, it is rejected: that
// would indicate a symlink or listing escaped the confined tree.
func (m *Mapper) ToVirtual(remotePath string) (string, error) {
	const op = "filesystem.ToVirtual"

	cleaned := path.Clean("/" + remotePath)
	if !m.withinRemoteRoot(cleaned) {
		return "", errs.New(errs.KindInvalidPath, op, "ruta remota fuera de la raíz permitida")
	}

	rel := strings.TrimPrefix(cleaned, m.remoteRoot)
	rel = strings.TrimPrefix(rel, "/")

	return path.Clean(path.Join(m.virtualRoot, rel)), nil
}

// VirtualRoot returns the user's cleaned, absolute virtual root.
func (m *Mapper) VirtualRoot() string { return m.virtualRoot }

// RemoteRoot returns the cleaned, absolute remote root.
func (m *Mapper) RemoteRoot() string { return m.remoteRoot }

func (m *Mapper) withinVirtualRoot(p string) bool {
	return WithinRoot(m.virtualRoot, p)
}

func (m *Mapper) withinRemoteRoot(p string) bool {
	return WithinRoot(m.remoteRoot, p)
}

// WithinRoot reports whether p (already clean and absolute) is root itself
// or a descendant of root (also clean and absolute). It is exported for
// reuse by internal/sftpclient, which needs the same containment check to
// defend against remote symlinks that resolve outside the confined root.
func WithinRoot(root, p string) bool {
	if root == "/" {
		return path.IsAbs(p)
	}

	if p == root {
		return true
	}

	return strings.HasPrefix(p, root+"/")
}
