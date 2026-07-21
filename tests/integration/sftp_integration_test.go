// Package integration exercises internal/sshclient, internal/sftpclient
// and internal/transfer against a real SSH+SFTP server (see
// tests/testutil), covering the scenarios required by
// FTP2SFTP-REQUIREMENTS.md section 15.2: connection, valid/invalid host
// key, authentication, upload, download, listing, rename, permissions,
// disconnection, timeout, partial file.
package integration

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	errs "github.com/Dmn117/ftp2sftp/internal/errors"
	"github.com/Dmn117/ftp2sftp/internal/sftpclient"
	"github.com/Dmn117/ftp2sftp/internal/sshclient"
	"github.com/Dmn117/ftp2sftp/internal/transfer"
	"github.com/Dmn117/ftp2sftp/tests/testutil"
)

func dialClient(t *testing.T, srv *testutil.SFTPServer, knownHosts string) *sftpclient.Client {
	t.Helper()

	host, port := srv.HostPort(t)

	sshClient, err := sshclient.Dial(sshclient.Config{
		Host: host, Port: port, Username: srv.Username, Password: srv.Password,
		KnownHostsFile: knownHosts, ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("sshclient.Dial: %v", err)
	}

	client, err := sftpclient.New(sshClient, srv.Root)
	if err != nil {
		t.Fatalf("sftpclient.New: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return client
}

func TestConnectAndAuthenticate(t *testing.T) {
	srv := testutil.StartSFTPServer(t, "gateway", "correct-password")
	knownHosts := srv.WriteKnownHosts(t, true)

	client := dialClient(t, srv, knownHosts)

	if err := client.Ping(); err != nil {
		t.Fatalf("Ping() on a fresh connection: %v", err)
	}
}

func TestConnectRejectsInvalidHostKey(t *testing.T) {
	srv := testutil.StartSFTPServer(t, "gateway", "correct-password")
	knownHosts := srv.WriteKnownHosts(t, false)
	host, port := srv.HostPort(t)

	_, err := sshclient.Dial(sshclient.Config{
		Host: host, Port: port, Username: srv.Username, Password: srv.Password,
		KnownHostsFile: knownHosts, ConnectTimeout: 5 * time.Second,
	})
	if errs.KindOf(err) != errs.KindHostKeyMismatch {
		t.Fatalf("expected KindHostKeyMismatch for a wrong host key, got %v (%v)", errs.KindOf(err), err)
	}
}

func TestConnectRejectsWrongPassword(t *testing.T) {
	srv := testutil.StartSFTPServer(t, "gateway", "correct-password")
	knownHosts := srv.WriteKnownHosts(t, true)
	host, port := srv.HostPort(t)

	_, err := sshclient.Dial(sshclient.Config{
		Host: host, Port: port, Username: srv.Username, Password: "wrong-password",
		KnownHostsFile: knownHosts, ConnectTimeout: 5 * time.Second,
	})
	if err == nil {
		t.Fatal("Dial with a wrong password should fail")
	}
}

func TestUploadCommitsAndIsReadableAfterward(t *testing.T) {
	srv := testutil.StartSFTPServer(t, "gateway", "correct-password")
	client := dialClient(t, srv, srv.WriteKnownHosts(t, true))

	finalPath := filepath.Join(srv.Root, "facturas", "archivo.xml")

	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		t.Fatalf("preparing remote directory: %v", err)
	}

	upload, err := transfer.NewUpload(client, finalPath, transfer.UploadOptions{
		SessionID: "sess1", TemporarySuffix: ".part", CalculateSHA256: true,
	})
	if err != nil {
		t.Fatalf("NewUpload: %v", err)
	}

	if _, err := upload.Write([]byte("<factura/>")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if err := upload.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("reading committed file: %v", err)
	}

	if string(data) != "<factura/>" {
		t.Errorf("committed content = %q", data)
	}

	entries, err := os.ReadDir(filepath.Dir(finalPath))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	for _, e := range entries {
		if e.Name() != "archivo.xml" {
			t.Errorf("unexpected leftover entry after commit: %s", e.Name())
		}
	}
}

func TestUploadFailureLeavesNoFinalFile(t *testing.T) {
	srv := testutil.StartSFTPServer(t, "gateway", "correct-password")
	client := dialClient(t, srv, srv.WriteKnownHosts(t, true))

	finalPath := filepath.Join(srv.Root, "archivo.xml")

	upload, err := transfer.NewUpload(client, finalPath, transfer.UploadOptions{
		SessionID: "sess1", TemporarySuffix: ".part", MaxSize: 2,
	})
	if err != nil {
		t.Fatalf("NewUpload: %v", err)
	}

	if _, err := upload.Write([]byte("too much data for the limit")); err == nil {
		t.Fatal("Write beyond MaxSize should fail")
	}

	_ = upload.Close()

	if _, err := os.Stat(finalPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("final file must not exist after a failed upload, stat err = %v", err)
	}

	remaining, _ := os.ReadDir(srv.Root)
	for _, e := range remaining {
		t.Errorf("temporary file was not cleaned up after failure: %s", e.Name())
	}
}

func TestDownloadReadsCommittedFile(t *testing.T) {
	srv := testutil.StartSFTPServer(t, "gateway", "correct-password")
	client := dialClient(t, srv, srv.WriteKnownHosts(t, true))

	remotePath := filepath.Join(srv.Root, "archivo.xml")
	if err := os.WriteFile(remotePath, []byte("contenido"), 0o644); err != nil {
		t.Fatalf("seeding remote file: %v", err)
	}

	download, err := transfer.NewDownload(client, remotePath, transfer.DownloadOptions{SessionID: "sess1"})
	if err != nil {
		t.Fatalf("NewDownload: %v", err)
	}

	defer download.Close()

	buf := make([]byte, 64)

	n, _ := download.Read(buf)
	if string(buf[:n]) != "contenido" {
		t.Errorf("read = %q, want contenido", buf[:n])
	}
}

func TestListDirectory(t *testing.T) {
	srv := testutil.StartSFTPServer(t, "gateway", "correct-password")
	client := dialClient(t, srv, srv.WriteKnownHosts(t, true))

	if err := os.WriteFile(filepath.Join(srv.Root, "a.xml"), []byte("a"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(srv.Root, "b.xml"), []byte("b"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	entries, err := client.ReadDir(srv.Root, 5*time.Second)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("ReadDir returned %d entries, want 2", len(entries))
	}
}

// TestListDirectoryTimesOutCleanly is a regression test for a real
// incident: against a remote directory with tens of thousands of
// entries, pkg/sftp's sequential READDIR paging ran long enough that the
// remote server killed the SSH channel mid-listing, surfacing as an
// opaque "failed to send packet: EOF" instead of a clean, expected error.
// ReadDir now takes an explicit timeout; this proves an already-expired
// deadline is honored (deterministic regardless of how fast the actual
// remote is) and mapped to KindTimeout rather than leaking the raw
// pkg/sftp/context error.
func TestListDirectoryTimesOutCleanly(t *testing.T) {
	srv := testutil.StartSFTPServer(t, "gateway", "correct-password")
	client := dialClient(t, srv, srv.WriteKnownHosts(t, true))

	_, err := client.ReadDir(srv.Root, 0)
	if err == nil {
		t.Fatal("expected ReadDir with an already-expired timeout to fail")
	}

	if errs.KindOf(err) != errs.KindTimeout {
		t.Fatalf("expected KindTimeout, got kind=%s err=%v", errs.KindOf(err), err)
	}
}

func TestRenameCommitsAtomically(t *testing.T) {
	srv := testutil.StartSFTPServer(t, "gateway", "correct-password")
	client := dialClient(t, srv, srv.WriteKnownHosts(t, true))

	oldPath := filepath.Join(srv.Root, "old.xml")
	newPath := filepath.Join(srv.Root, "new.xml")

	if err := os.WriteFile(oldPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := client.Rename(oldPath, newPath, false); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("renamed file should exist at new path: %v", err)
	}

	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("old path should no longer exist after rename")
	}
}

func TestRenameRejectsExistingDestinationWithoutOverwrite(t *testing.T) {
	srv := testutil.StartSFTPServer(t, "gateway", "correct-password")
	client := dialClient(t, srv, srv.WriteKnownHosts(t, true))

	oldPath := filepath.Join(srv.Root, "old.xml")
	newPath := filepath.Join(srv.Root, "new.xml")

	if err := os.WriteFile(oldPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := os.WriteFile(newPath, []byte("y"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := client.Rename(oldPath, newPath, false); err == nil {
		t.Fatal("Rename onto an existing file without overwrite should fail")
	}
}

func TestRemotePermissionDenied(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}

	srv := testutil.StartSFTPServer(t, "gateway", "correct-password")
	client := dialClient(t, srv, srv.WriteKnownHosts(t, true))

	restricted := filepath.Join(srv.Root, "restricted")
	if err := os.MkdirAll(restricted, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := os.Chmod(restricted, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	t.Cleanup(func() { _ = os.Chmod(restricted, 0o755) })

	_, err := client.Stat(filepath.Join(restricted, "x.xml"))
	if errs.KindOf(err) != errs.KindRemotePermissionDenied {
		t.Fatalf("expected KindRemotePermissionDenied, got %v (%v)", errs.KindOf(err), err)
	}
}

func TestConnectTimesOutAgainstUnroutableHost(t *testing.T) {
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "known_hosts")

	if err := os.WriteFile(knownHosts, nil, 0o600); err != nil {
		t.Fatalf("writing known_hosts: %v", err)
	}

	start := time.Now()

	_, err := sshclient.Dial(sshclient.Config{
		Host: "192.0.2.1", Port: 22, Username: "x", Password: "x",
		KnownHostsFile: knownHosts, ConnectTimeout: 300 * time.Millisecond,
	})

	if err == nil {
		t.Fatal("Dial to an unroutable address should fail")
	}

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Dial took %v, connect timeout was not respected", elapsed)
	}
}

func TestDisconnectionClosesUnderlyingConnection(t *testing.T) {
	srv := testutil.StartSFTPServer(t, "gateway", "correct-password")
	client := dialClient(t, srv, srv.WriteKnownHosts(t, true))

	if err := client.Ping(); err != nil {
		t.Fatalf("Ping before close: %v", err)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := client.Ping(); err == nil {
		t.Fatal("Ping after Close should fail")
	}
}
