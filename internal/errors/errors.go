// Package errors defines the domain error taxonomy shared across ftp2sftp.
//
// Every error that can cross a protocol boundary (FTP response text, HTTP
// health responses) must be constructed here so that its Error() message is
// safe to expose to a client. Internal causes (raw SFTP/SSH errors, file
// paths, driver details) are kept out of Error() and are only reachable via
// Unwrap(), which callers must only pass to logging, never to a protocol
// response.
package errors

import (
	stderrors "errors"
	"fmt"
)

// Kind classifies a domain error. It mirrors the error taxonomy in
// FTP2SFTP-REQUIREMENTS.md section 11.
type Kind string

const (
	KindConfig                 Kind = "config"
	KindAuthentication         Kind = "authentication"
	KindAuthorization          Kind = "authorization"
	KindInvalidPath            Kind = "invalid_path"
	KindUnsupportedCommand     Kind = "unsupported_command"
	KindConflict               Kind = "conflict"
	KindNotFound               Kind = "not_found"
	KindSSHConnection          Kind = "ssh_connection"
	KindHostKeyMismatch        Kind = "host_key_mismatch"
	KindSFTPAuthentication     Kind = "sftp_authentication"
	KindTimeout                Kind = "timeout"
	KindDisconnected           Kind = "disconnected"
	KindPartialWrite           Kind = "partial_write"
	KindRemoteStorageFull      Kind = "remote_storage_full"
	KindRemotePermissionDenied Kind = "remote_permission_denied"
	KindRateLimited            Kind = "rate_limited"
	KindInternal               Kind = "internal"
)

// Error is a domain error with a wire-safe message and an optional internal
// cause kept separate from Error().
type Error struct {
	Kind    Kind
	Op      string
	Message string
	cause   error
}

// New creates a domain error with no internal cause attached.
func New(kind Kind, op, message string) *Error {
	return &Error{Kind: kind, Op: op, Message: message}
}

// Wrap creates a domain error that carries an internal cause for logging.
// The cause is intentionally excluded from Error() to avoid leaking
// internal details across a protocol boundary.
func Wrap(kind Kind, op, message string, cause error) *Error {
	return &Error{Kind: kind, Op: op, Message: message, cause: cause}
}

// Error returns the wire-safe message only. It never includes the wrapped
// cause.
func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}

	return string(e.Kind)
}

// Unwrap exposes the internal cause for errors.Is/As and for logging. Do not
// forward the result of Unwrap to a protocol client.
func (e *Error) Unwrap() error {
	return e.cause
}

// Is allows errors.Is(err, &Error{Kind: KindX}) style comparisons by kind.
func (e *Error) Is(target error) bool {
	var t *Error
	if !stderrors.As(target, &t) {
		return false
	}

	return t.Kind == e.Kind
}

// LogValue formats the error for structured logging, including the op,
// kind and internal cause (if any). This is safe for logs but must never be
// sent to a protocol client.
func (e *Error) LogValue() string {
	if e.cause != nil {
		return fmt.Sprintf("op=%s kind=%s message=%q cause=%q", e.Op, e.Kind, e.Message, e.cause.Error())
	}

	return fmt.Sprintf("op=%s kind=%s message=%q", e.Op, e.Kind, e.Message)
}

// KindOf returns the Kind of err if it is (or wraps) a *Error, or
// KindInternal otherwise.
func KindOf(err error) Kind {
	var e *Error
	if stderrors.As(err, &e) {
		return e.Kind
	}

	return KindInternal
}

// Is reports whether err is a domain error of the given kind.
func Is(err error, kind Kind) bool {
	return KindOf(err) == kind
}
