// Package session holds the per-connection state that outlives a single
// FTP command: session identity (for RF-015 audit correlation and the
// RF-007 temporary file name) and a lazily-established SFTP connection
// reused for the lifetime of the FTP control connection (section 10.2 of
// FTP2SFTP-REQUIREMENTS.md: one SSH/SFTP session per FTP session for the
// MVP).
//
// Current working directory and the pending RNFR target are deliberately
// NOT duplicated here: ftpserverlib already tracks both per connection
// (ClientContext.Path()/SetPath() and its internal ctxRnfr), scoped
// correctly to each client. Every path this package's Mapper receives from
// the ftpserver driver is already absolute and cleaned by the library; what
// Mapper still must enforce is containment within the user's configured
// virtual root, which the library has no notion of.
package session

import (
	"sync"
	"time"

	"github.com/Dmn117/ftp2sftp/internal/authorization"
	errs "github.com/Dmn117/ftp2sftp/internal/errors"
	"github.com/Dmn117/ftp2sftp/internal/filesystem"
	"github.com/Dmn117/ftp2sftp/internal/sftpclient"
)

var errClosedSession = errs.New(errs.KindDisconnected, "session.SFTP", "la sesión ya fue cerrada")

// Connector lazily establishes the session's SFTP connection. It is
// injected so Session stays unit-testable without a real network
// connection; the ftpserver wiring layer supplies a Connector backed by
// sshclient.Dial + sftpclient.New.
type Connector func() (*sftpclient.Client, error)

// Session is the state associated with one authenticated FTP control
// connection.
type Session struct {
	id        string
	username  string
	clientIP  string
	startedAt time.Time

	mapper  *filesystem.Mapper
	policy  authorization.Policy
	gate    *authorization.ConcurrencyGate
	connect Connector

	mu     sync.Mutex
	sftp   *sftpclient.Client
	closed bool
}

// New creates a Session for one authenticated FTP connection.
func New(
	id, username, clientIP string,
	mapper *filesystem.Mapper,
	policy authorization.Policy,
	gate *authorization.ConcurrencyGate,
	connect Connector,
) *Session {
	return &Session{
		id:        id,
		username:  username,
		clientIP:  clientIP,
		startedAt: time.Now(),
		mapper:    mapper,
		policy:    policy,
		gate:      gate,
		connect:   connect,
	}
}

// ID returns the session's correlation identifier, used in the RF-007
// temporary file name and in every audit event.
func (s *Session) ID() string { return s.id }

// Username returns the authenticated FTP username.
func (s *Session) Username() string { return s.username }

// ClientIP returns the FTP client's source address.
func (s *Session) ClientIP() string { return s.clientIP }

// StartedAt returns when the session was created.
func (s *Session) StartedAt() time.Time { return s.startedAt }

// Mapper returns the user's virtual/remote path mapper.
func (s *Session) Mapper() *filesystem.Mapper { return s.mapper }

// Policy returns the user's authorization policy.
func (s *Session) Policy() authorization.Policy { return s.policy }

// ConcurrencyGate returns the user's shared transfer concurrency gate.
func (s *Session) ConcurrencyGate() *authorization.ConcurrencyGate { return s.gate }

// SFTP returns the session's SFTP connection, dialing it on first use and
// reusing it afterward.
func (s *Session) SFTP() (*sftpclient.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, errClosedSession
	}

	if s.sftp != nil {
		return s.sftp, nil
	}

	client, err := s.connect()
	if err != nil {
		return nil, err
	}

	s.sftp = client

	return client, nil
}

// InvalidateSFTP closes the current SFTP connection, if any, and discards
// it, so the next call to SFTP dials a fresh one. Use this after observing
// a connection-level error from an SFTP operation; closing here (rather
// than leaving it to the caller, which has no other reference to the
// discarded client) is what prevents a broken connection from leaking its
// underlying socket and goroutines.
func (s *Session) InvalidateSFTP() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sftp != nil {
		_ = s.sftp.Close()
		s.sftp = nil
	}
}

// IsConnected reports whether the session currently holds an established
// SFTP connection, without dialing one. Intended for metrics accounting.
func (s *Session) IsConnected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.sftp != nil
}

// Close releases the session's SFTP/SSH connection, if one was
// established. Safe to call more than once.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true

	if s.sftp != nil {
		err := s.sftp.Close()
		s.sftp = nil

		return err
	}

	return nil
}
