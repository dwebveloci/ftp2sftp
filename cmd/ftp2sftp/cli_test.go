package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Dmn117/ftp2sftp/tests/testutil"
)

// captureStdout redirects os.Stdout for the duration of fn and returns
// whatever it wrote. The CLI subcommands write their result to stdout by
// design (RF-018: scriptable, parseable by an operator's tooling), so
// asserting on it is asserting on the actual contract, not an
// implementation detail.
func captureStdout(t *testing.T, fn func() int) (string, int) {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}

	orig := os.Stdout
	os.Stdout = w

	code := fn()

	os.Stdout = orig
	_ = w.Close()

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading captured stdout: %v", err)
	}

	return string(out), code
}

func TestCmdConfigValidate_ValidFile(t *testing.T) {
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(knownHosts, []byte("placeholder\n"), 0o600); err != nil {
		t.Fatalf("writing known_hosts stub: %v", err)
	}

	// validate never dials the SFTP target, so a reachable host/port is
	// not required here — only a well-formed configuration.
	path := writeConnectivityConfig(t, "tester", "127.0.0.1", 22, "whoever", "whatever", "/tmp", knownHosts)

	out, code := captureStdout(t, func() int { return cmdConfigValidate([]string{"-config", path}) })

	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stdout: %s)", code, out)
	}

	if !bytes.Contains([]byte(out), []byte("OK")) {
		t.Errorf("expected stdout to mention OK, got %q", out)
	}
}

func TestCmdConfigValidate_MissingFile(t *testing.T) {
	code := cmdConfigValidate([]string{"-config", filepath.Join(t.TempDir(), "does-not-exist.yaml")})

	if code != 1 {
		t.Fatalf("expected exit 1 for a missing file, got %d", code)
	}
}

func TestCmdConfigValidate_InvalidContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("users: []\n"), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	code := cmdConfigValidate([]string{"-config", path})

	if code != 1 {
		t.Fatalf("expected exit 1 for a config with no users, got %d", code)
	}
}

// TestCmdConfigCheckConnectivity_Success exercises the full CLI path
// (config load -> probeUserSFTP -> report) against a real in-process
// SSH+SFTP server, the same harness the integration suite uses, so this
// is a genuine connectivity check rather than a mock of one.
func TestCmdConfigCheckConnectivity_Success(t *testing.T) {
	srv := testutil.StartSFTPServer(t, "tester", "s3cret")
	knownHosts := srv.WriteKnownHosts(t, true)
	host, port := srv.HostPort(t)

	path := writeConnectivityConfig(t, "aduser", host, port, srv.Username, srv.Password, srv.Root, knownHosts)

	out, code := captureStdout(t, func() int {
		return cmdConfigCheckConnectivity([]string{"-config", path, "-timeout", "5s"})
	})

	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stdout: %s)", code, out)
	}

	if !bytes.Contains([]byte(out), []byte("OK")) {
		t.Errorf("expected stdout to report OK, got %q", out)
	}
}

// TestCmdConfigCheckConnectivity_WrongHostKey confirms the CLI surfaces a
// host-key mismatch as a failure instead of silently accepting it — the
// same host-key policy the running gateway enforces (RNF-001).
func TestCmdConfigCheckConnectivity_WrongHostKey(t *testing.T) {
	srv := testutil.StartSFTPServer(t, "tester", "s3cret")
	wrongKnownHosts := srv.WriteKnownHosts(t, false)
	host, port := srv.HostPort(t)

	path := writeConnectivityConfig(t, "aduser", host, port, srv.Username, srv.Password, srv.Root, wrongKnownHosts)

	out, code := captureStdout(t, func() int {
		return cmdConfigCheckConnectivity([]string{"-config", path, "-timeout", "5s"})
	})

	if code != 1 {
		t.Fatalf("expected exit 1 for a host-key mismatch, got %d (stdout: %s)", code, out)
	}

	if !bytes.Contains([]byte(out), []byte("FAIL")) {
		t.Errorf("expected stdout to report FAIL, got %q", out)
	}
}

func TestCmdConfigCheckConnectivity_UnknownUserFilter(t *testing.T) {
	srv := testutil.StartSFTPServer(t, "tester", "s3cret")
	knownHosts := srv.WriteKnownHosts(t, true)
	host, port := srv.HostPort(t)

	path := writeConnectivityConfig(t, "aduser", host, port, srv.Username, srv.Password, srv.Root, knownHosts)

	code := cmdConfigCheckConnectivity([]string{"-config", path, "-user", "nobody"})

	if code != 2 {
		t.Fatalf("expected exit 2 for a -user that does not exist in the config, got %d", code)
	}
}

func writeConnectivityConfig(
	t *testing.T,
	ftpUsername, sftpHost string, sftpPort int, sftpUsername, sftpPassword, rootPath, knownHosts string,
) string {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword([]byte("whatever"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt.GenerateFromPassword: %v", err)
	}

	yaml := fmt.Sprintf(`
server:
  listenAddress: "0.0.0.0"
  controlPort: 2121
  passiveAddress: "127.0.0.1"
  passivePortStart: 30000
  passivePortEnd: 30009
  maxConnections: 5
  idleTimeout: "5m"
  dataConnectionTimeout: "30s"
  shutdownTimeout: "10s"
transfer:
  bufferSize: 65536
  temporarySuffix: ".part"
  calculateSha256: false
observability:
  logLevel: "info"
  logFormat: "text"
health:
  listenAddress: "0.0.0.0:8080"
  readinessRequiresSftp: false
users:
  - username: %q
    passwordHash: %q
    virtualRoot: "/"
    maxFileSize: 1048576
    maxConcurrentTransfers: 1
    permissions:
      allowUpload: true
    sftp:
      host: %q
      port: %d
      username: %q
      password: %q
      knownHostsFile: %q
      rootPath: %q
      connectTimeout: "5s"
`, ftpUsername, string(hash), sftpHost, sftpPort, sftpUsername, sftpPassword, knownHosts, rootPath)

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	return path
}

// TestReadPasswordFromOperator_PipedInput exercises the non-terminal path
// (stdin is a pipe, e.g. `echo ... | ftp2sftp config hash-password` in a
// script): no TTY exists to suppress echo on, so it must fall back to
// reading a single line instead of blocking forever waiting for a TTY
// read.
func TestReadPasswordFromOperator_PipedInput(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}

	orig := os.Stdin
	os.Stdin = r

	defer func() { os.Stdin = orig }()

	done := make(chan struct{})

	var (
		got  []byte
		err2 error
	)

	go func() {
		got, err2 = readPasswordFromOperator()
		close(done)
	}()

	if _, err := w.Write([]byte("s3cret-pw\n")); err != nil {
		t.Fatalf("writing to pipe: %v", err)
	}

	_ = w.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("readPasswordFromOperator did not return for piped input")
	}

	if err2 != nil {
		t.Fatalf("readPasswordFromOperator: %v", err2)
	}

	if string(got) != "s3cret-pw" {
		t.Errorf("got %q, want %q", got, "s3cret-pw")
	}
}
