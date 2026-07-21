package sshclient_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	errs "github.com/Dmn117/ftp2sftp/internal/errors"
	"github.com/Dmn117/ftp2sftp/internal/sshclient"
)

// testServer is a minimal in-process SSH server used to exercise the
// client's handshake, host key verification and authentication paths
// without any external dependency (Docker-based SFTP integration coverage
// lives separately under tests/integration).
type testServer struct {
	addr     string
	hostKey  ssh.Signer
	password string
	listener net.Listener
}

func startTestServer(t *testing.T, password string) *testServer {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating host key: %v", err)
	}

	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("NewSignerFromKey: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := &testServer{addr: ln.Addr().String(), hostKey: signer, password: password, listener: ln}

	serverConfig := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if string(pass) == password {
				return nil, nil
			}

			return nil, errAuthFailed
		},
	}
	serverConfig.AddHostKey(signer)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			go func() {
				sshConn, chans, reqs, err := ssh.NewServerConn(conn, serverConfig)
				if err != nil {
					return
				}

				defer sshConn.Close()
				go ssh.DiscardRequests(reqs)

				for newCh := range chans {
					_ = newCh.Reject(ssh.UnknownChannelType, "test server accepts no channels")
				}
			}()
		}
	}()

	t.Cleanup(func() { _ = ln.Close() })

	return srv
}

var errAuthFailed = &testAuthError{}

type testAuthError struct{}

func (*testAuthError) Error() string { return "invalid password" }

func (s *testServer) writeKnownHosts(t *testing.T, correctKey bool) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")

	key := s.hostKey.PublicKey()

	if !correctKey {
		_, wrongPriv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("generating decoy key: %v", err)
		}

		wrongSigner, err := ssh.NewSignerFromKey(wrongPriv)
		if err != nil {
			t.Fatalf("NewSignerFromKey: %v", err)
		}

		key = wrongSigner.PublicKey()
	}

	line := knownhosts.Line([]string{s.addr}, key)

	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("writing known_hosts: %v", err)
	}

	return path
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()

	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", addr, err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parsing port %q: %v", portStr, err)
	}

	return host, port
}

func TestDialSucceedsWithMatchingHostKeyAndPassword(t *testing.T) {
	srv := startTestServer(t, "correct-password")
	knownHostsFile := srv.writeKnownHosts(t, true)
	host, port := splitHostPort(t, srv.addr)

	client, err := sshclient.Dial(sshclient.Config{
		Host: host, Port: port, Username: "tester",
		Password:       "correct-password",
		KnownHostsFile: knownHostsFile,
		ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Dial() failed with matching host key and correct password: %v", err)
	}

	defer client.Close()
}

func TestDialFailsOnHostKeyMismatch(t *testing.T) {
	srv := startTestServer(t, "correct-password")
	knownHostsFile := srv.writeKnownHosts(t, false) // wrong key on purpose
	host, port := splitHostPort(t, srv.addr)

	_, err := sshclient.Dial(sshclient.Config{
		Host: host, Port: port, Username: "tester",
		Password:       "correct-password",
		KnownHostsFile: knownHostsFile,
		ConnectTimeout: 5 * time.Second,
	})
	if err == nil {
		t.Fatal("Dial() should fail when the host key does not match known_hosts")
	}

	if errs.KindOf(err) != errs.KindHostKeyMismatch {
		t.Fatalf("expected KindHostKeyMismatch, got %v (%v)", errs.KindOf(err), err)
	}
}

func TestDialFailsOnWrongPassword(t *testing.T) {
	srv := startTestServer(t, "correct-password")
	knownHostsFile := srv.writeKnownHosts(t, true)
	host, port := splitHostPort(t, srv.addr)

	_, err := sshclient.Dial(sshclient.Config{
		Host: host, Port: port, Username: "tester",
		Password:       "wrong-password",
		KnownHostsFile: knownHostsFile,
		ConnectTimeout: 5 * time.Second,
	})
	if err == nil {
		t.Fatal("Dial() should fail with a wrong password")
	}
}

func TestDialFailsWithoutAnyAuthMethodConfigured(t *testing.T) {
	srv := startTestServer(t, "correct-password")
	knownHostsFile := srv.writeKnownHosts(t, true)
	host, port := splitHostPort(t, srv.addr)

	_, err := sshclient.Dial(sshclient.Config{
		Host: host, Port: port, Username: "tester",
		KnownHostsFile: knownHostsFile,
		ConnectTimeout: 5 * time.Second,
	})
	if err == nil {
		t.Fatal("Dial() should fail when neither privateKeyFile nor password is set")
	}

	if errs.KindOf(err) != errs.KindConfig {
		t.Fatalf("expected KindConfig, got %v (%v)", errs.KindOf(err), err)
	}
}

func TestDialFailsOnConnectTimeout(t *testing.T) {
	// 192.0.2.0/24 is TEST-NET-1 (RFC 5737): reserved for documentation,
	// guaranteed unroutable, so the dial will hang until our timeout fires
	// instead of racing a real network response.
	dir := t.TempDir()
	knownHostsFile := filepath.Join(dir, "known_hosts")

	if err := os.WriteFile(knownHostsFile, []byte(""), 0o600); err != nil {
		t.Fatalf("writing known_hosts: %v", err)
	}

	start := time.Now()

	_, err := sshclient.Dial(sshclient.Config{
		Host: "192.0.2.1", Port: 22, Username: "tester",
		Password:       "x",
		KnownHostsFile: knownHostsFile,
		ConnectTimeout: 300 * time.Millisecond,
	})

	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Dial() to an unroutable address should fail")
	}

	if elapsed > 5*time.Second {
		t.Fatalf("Dial() took %v, connect timeout was not respected", elapsed)
	}
}
