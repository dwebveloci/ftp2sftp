package ftpserver

import (
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	libftpserver "github.com/fclairamb/ftpserverlib"

	"github.com/Dmn117/ftp2sftp/internal/config"
	errs "github.com/Dmn117/ftp2sftp/internal/errors"
	"github.com/Dmn117/ftp2sftp/internal/observability"
)

// fakeClientContext is a minimal ftpserverlib.ClientContext implementation
// used to exercise Gateway's MainDriver methods without a real TCP server.
type fakeClientContext struct {
	remoteAddr net.Addr
	path       string
	extra      any
}

func newFakeClientContext(ip string) *fakeClientContext {
	return &fakeClientContext{remoteAddr: &net.TCPAddr{IP: net.ParseIP(ip), Port: 12345}}
}

func (f *fakeClientContext) Path() string             { return f.path }
func (f *fakeClientContext) SetPath(v string)         { f.path = v }
func (f *fakeClientContext) SetListPath(string)       {}
func (f *fakeClientContext) SetDebug(bool)            {}
func (f *fakeClientContext) Debug() bool              { return false }
func (f *fakeClientContext) ID() uint32               { return 1 }
func (f *fakeClientContext) RemoteAddr() net.Addr     { return f.remoteAddr }
func (f *fakeClientContext) LocalAddr() net.Addr      { return &net.TCPAddr{} }
func (f *fakeClientContext) GetClientVersion() string { return "test-client" }
func (f *fakeClientContext) Close() error             { return nil }
func (f *fakeClientContext) HasTLSForControl() bool   { return false }
func (f *fakeClientContext) HasTLSForTransfers() bool { return false }
func (f *fakeClientContext) GetLastCommand() string   { return "" }
func (f *fakeClientContext) SetExtra(extra any)       { f.extra = extra }
func (f *fakeClientContext) Extra() any               { return f.extra }
func (f *fakeClientContext) GetLastDataChannel() libftpserver.DataChannel {
	return libftpserver.DataChannelPassive
}
func (f *fakeClientContext) SetTLSRequirement(libftpserver.TLSRequirement) error { return nil }

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testConfig(t *testing.T, maxConnections int, users ...config.UserConfig) *config.Config {
	t.Helper()

	return &config.Config{
		Server: config.ServerConfig{
			ListenAddress: "0.0.0.0", ControlPort: 2121, PassiveAddress: "ftp.internal.example",
			PassivePortStart: 30000, PassivePortEnd: 30100, MaxConnections: maxConnections,
			IdleTimeout: config.Duration(5 * time.Minute), DataConnectionTimeout: config.Duration(30 * time.Second),
		},
		Transfer: config.TransferConfig{BufferSize: 65536, TemporarySuffix: ".part"},
		Users:    users,
	}
}

func testUser(t *testing.T, username, password string) config.UserConfig {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}

	return config.UserConfig{
		Username: username, PasswordHash: string(hash), VirtualRoot: "/",
		MaxFileSize: 1024, MaxConcurrentTransfers: 2,
		Permissions: config.PermissionsConfig{AllowUpload: true},
		SFTP: config.SFTPTargetConfig{
			Host: "sftp.internal.example", Port: 22, Username: "remote",
			Password: "unused-in-this-test", RootPath: "/home/remote",
			ConnectTimeout: config.Duration(5 * time.Second),
		},
	}
}

func TestClientConnectedEnforcesMaxConnections(t *testing.T) {
	cfg := testConfig(t, 1)

	gw, err := New(cfg, discardLogger(), observability.NewMetrics())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	first := newFakeClientContext("10.0.0.1")
	if _, err := gw.ClientConnected(first); err != nil {
		t.Fatalf("first ClientConnected should succeed: %v", err)
	}

	second := newFakeClientContext("10.0.0.2")
	if _, err := gw.ClientConnected(second); err == nil {
		t.Fatal("second ClientConnected should be rejected: maxConnections is 1")
	}

	if gw.activeConnections.Load() != 1 {
		t.Errorf("activeConnections = %d, want 1", gw.activeConnections.Load())
	}

	gw.ClientDisconnected(first)

	if gw.activeConnections.Load() != 0 {
		t.Errorf("activeConnections after disconnect = %d, want 0", gw.activeConnections.Load())
	}

	// A rejected connection's ClientDisconnected must not double-decrement.
	gw.ClientDisconnected(second)

	if gw.activeConnections.Load() != 0 {
		t.Errorf("activeConnections after disconnecting a rejected client = %d, want 0", gw.activeConnections.Load())
	}
}

func TestClientConnectedRejectsWhileShuttingDown(t *testing.T) {
	cfg := testConfig(t, 10)

	gw, err := New(cfg, discardLogger(), observability.NewMetrics())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	gw.PrepareShutdown()

	if _, err := gw.ClientConnected(newFakeClientContext("10.0.0.1")); err == nil {
		t.Fatal("ClientConnected should reject new connections while shutting down")
	}
}

func TestAuthUserSuccessTracksSession(t *testing.T) {
	cfg := testConfig(t, 10, testUser(t, "ax2012", "s3cret"))

	gw, err := New(cfg, discardLogger(), observability.NewMetrics())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cc := newFakeClientContext("10.0.0.1")

	if _, err := gw.ClientConnected(cc); err != nil {
		t.Fatalf("ClientConnected: %v", err)
	}

	driver, err := gw.AuthUser(cc, "ax2012", "s3cret")
	if err != nil {
		t.Fatalf("AuthUser with correct credentials failed: %v", err)
	}

	if driver == nil {
		t.Fatal("AuthUser should return a non-nil ClientDriver on success")
	}

	if cc.Path() != "/" {
		t.Errorf("cc.Path() = %q, want / (the user's virtual root)", cc.Path())
	}

	// ClientDisconnected must balance the sessions.Add(1) from AuthUser, or
	// WaitForSessions would hang forever on a real shutdown.
	gw.ClientDisconnected(cc)

	done := make(chan struct{})
	go func() {
		gw.sessions.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("gw.sessions.Wait() did not return: Add/Done are unbalanced")
	}
}

func TestAuthUserFailureDoesNotTrackSession(t *testing.T) {
	cfg := testConfig(t, 10, testUser(t, "ax2012", "s3cret"))

	gw, err := New(cfg, discardLogger(), observability.NewMetrics())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cc := newFakeClientContext("10.0.0.1")

	if _, err := gw.AuthUser(cc, "ax2012", "wrong-password"); err == nil {
		t.Fatal("AuthUser with a wrong password should fail")
	}

	done := make(chan struct{})
	go func() {
		gw.sessions.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a failed AuthUser must not increment the sessions WaitGroup")
	}
}

func TestWaitForSessionsReturnsOnDoneSignal(t *testing.T) {
	cfg := testConfig(t, 10, testUser(t, "ax2012", "s3cret"))

	gw, err := New(cfg, discardLogger(), observability.NewMetrics())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	gw.sessions.Add(1) // simulate one session that never disconnects

	done := make(chan struct{})
	close(done) // already closed: WaitForSessions must return immediately

	start := time.Now()
	gw.WaitForSessions(done)
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Fatalf("WaitForSessions took %v, should have returned immediately on a closed done channel", elapsed)
	}

	gw.sessions.Done() // avoid leaking the goroutine spawned inside WaitForSessions
}

func TestAuthUserRateLimitsAfterRepeatedFailures(t *testing.T) {
	cfg := testConfig(t, 10, testUser(t, "ax2012", "s3cret"))

	gw, err := New(cfg, discardLogger(), observability.NewMetrics())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cc := newFakeClientContext("10.0.0.1")

	for i := 0; i < authMaxFailures; i++ {
		_, _ = gw.AuthUser(cc, "ax2012", "wrong")
	}

	_, err = gw.AuthUser(cc, "ax2012", "s3cret") // correct password, but locked out
	if errs.KindOf(err) != errs.KindRateLimited {
		t.Fatalf("expected KindRateLimited after repeated failures, got %v (%v)", errs.KindOf(err), err)
	}
}
