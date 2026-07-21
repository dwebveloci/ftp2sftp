// Package testutil provides a real, in-process SSH+SFTP server backed by
// the local filesystem, used by the integration and protocol test suites
// as a reproducible substitute for a Dockerized SFTP server (per
// FTP2SFTP-REQUIREMENTS.md section 15.2: "Docker o una estrategia
// reproducible"). Every byte exchanged uses the real golang.org/x/crypto/ssh
// and github.com/pkg/sftp wire implementations; only the transport socket
// is loopback TCP instead of a container.
//
// A docker-compose-based SFTP service is still provided under
// deploy/docker for manual verification and local development, since it
// exercises a genuinely independent SFTP server implementation.
package testutil

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// SFTPServer is a running in-process SSH+SFTP server rooted at a temp
// directory on the real filesystem.
type SFTPServer struct {
	Addr     string
	Root     string
	Username string
	Password string
	HostKey  ssh.Signer

	listener net.Listener
}

// StartSFTPServer starts the server and registers its shutdown with
// t.Cleanup. Every accepted SSH connection may open exactly one "session"
// channel with an "sftp" subsystem request, mirroring a real OpenSSH
// server closely enough for our client code to be genuinely exercised.
func StartSFTPServer(t *testing.T, username, password string) *SFTPServer {
	t.Helper()

	root := t.TempDir()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating host key: %v", err)
	}

	hostKey, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("NewSignerFromKey: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := &SFTPServer{
		Addr: ln.Addr().String(), Root: root, Username: username, Password: password,
		HostKey: hostKey, listener: ln,
	}

	serverConfig := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if string(pass) == password {
				return nil, nil
			}

			return nil, errInvalidCredentials
		},
	}
	serverConfig.AddHostKey(hostKey)

	go srv.acceptLoop(t, serverConfig)

	t.Cleanup(func() { _ = ln.Close() })

	return srv
}

var errInvalidCredentials = &credentialsError{}

type credentialsError struct{}

func (*credentialsError) Error() string { return "invalid username or password" }

func (s *SFTPServer) acceptLoop(t *testing.T, cfg *ssh.ServerConfig) {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}

		go s.handleConn(t, conn, cfg)
	}
}

func (s *SFTPServer) handleConn(t *testing.T, conn net.Conn, cfg *ssh.ServerConfig) {
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}

	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)

	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(ssh.UnknownChannelType, "only session channels are supported")

			continue
		}

		ch, chReqs, err := newCh.Accept()
		if err != nil {
			continue
		}

		go s.handleSession(t, ch, chReqs)
	}
}

func (s *SFTPServer) handleSession(t *testing.T, ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()

	for req := range reqs {
		if req.Type != "subsystem" || string(req.Payload[4:]) != "sftp" {
			_ = req.Reply(false, nil)

			continue
		}

		_ = req.Reply(true, nil)

		server, err := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(s.Root))
		if err != nil {
			return
		}

		_ = server.Serve()

		return
	}
}

// WriteKnownHosts writes a known_hosts file for this server's host key and
// returns its path. Pass correctKey=false to simulate a changed/incorrect
// host key, for negative host-key-verification tests.
func (s *SFTPServer) WriteKnownHosts(t *testing.T, correctKey bool) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")

	key := s.HostKey.PublicKey()

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

	line := knownhosts.Line([]string{s.Addr}, key)

	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("writing known_hosts: %v", err)
	}

	return path
}

// HostPort splits Addr into host and numeric port, failing the test on
// error.
func (s *SFTPServer) HostPort(t *testing.T) (string, int) {
	t.Helper()

	host, portStr, err := net.SplitHostPort(s.Addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", s.Addr, err)
	}

	port := 0
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}

	return host, port
}
