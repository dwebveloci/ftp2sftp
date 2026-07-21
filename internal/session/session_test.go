package session_test

import (
	"testing"

	"github.com/Dmn117/ftp2sftp/internal/authorization"
	"github.com/Dmn117/ftp2sftp/internal/filesystem"
	"github.com/Dmn117/ftp2sftp/internal/session"
	"github.com/Dmn117/ftp2sftp/internal/sftpclient"
)

func newTestSession(t *testing.T, connect session.Connector) *session.Session {
	t.Helper()

	mapper := filesystem.NewMapper("/", "/home/facturas")
	policy := authorization.Policy{AllowUpload: true, MaxFileSize: 1024}
	gate := authorization.NewConcurrencyGate(2)

	return session.New("sess-1", "ax2012", "10.0.0.5", mapper, policy, gate, connect)
}

func TestNewSessionIdentity(t *testing.T) {
	s := newTestSession(t, nil)

	if s.ID() != "sess-1" || s.Username() != "ax2012" || s.ClientIP() != "10.0.0.5" {
		t.Errorf("unexpected identity fields: id=%q user=%q ip=%q", s.ID(), s.Username(), s.ClientIP())
	}

	if s.StartedAt().IsZero() {
		t.Error("StartedAt() should be set")
	}

	if s.Mapper() == nil {
		t.Error("Mapper() should not be nil")
	}
}

func TestSFTPDialsOnceAndReuses(t *testing.T) {
	calls := 0
	fake := &sftpclient.Client{}

	s := newTestSession(t, func() (*sftpclient.Client, error) {
		calls++

		return fake, nil
	})

	c1, err := s.SFTP()
	if err != nil {
		t.Fatalf("SFTP(): %v", err)
	}

	c2, err := s.SFTP()
	if err != nil {
		t.Fatalf("SFTP() second call: %v", err)
	}

	if c1 != c2 {
		t.Error("SFTP() should return the same connection on repeated calls")
	}

	if calls != 1 {
		t.Errorf("connect was called %d times, want 1", calls)
	}
}

func TestInvalidateSFTPForcesRedial(t *testing.T) {
	calls := 0

	s := newTestSession(t, func() (*sftpclient.Client, error) {
		calls++

		return &sftpclient.Client{}, nil
	})

	if _, err := s.SFTP(); err != nil {
		t.Fatalf("SFTP(): %v", err)
	}

	s.InvalidateSFTP()

	if _, err := s.SFTP(); err != nil {
		t.Fatalf("SFTP() after invalidate: %v", err)
	}

	if calls != 2 {
		t.Errorf("connect was called %d times after invalidation, want 2", calls)
	}
}

func TestSFTPAfterCloseFails(t *testing.T) {
	s := newTestSession(t, func() (*sftpclient.Client, error) {
		return &sftpclient.Client{}, nil
	})

	if _, err := s.SFTP(); err != nil {
		t.Fatalf("SFTP(): %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("second Close() should be a no-op, got: %v", err)
	}

	if _, err := s.SFTP(); err == nil {
		t.Fatal("SFTP() after Close() should fail")
	}
}
