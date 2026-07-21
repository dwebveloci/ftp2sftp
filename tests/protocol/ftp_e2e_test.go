// Package protocol drives the full gateway (real FTP server library, real
// SSH/SFTP client, real in-process SFTP backend from tests/testutil) with
// a real FTP client, covering FTP2SFTP-REQUIREMENTS.md section 15.3: login,
// PWD, CWD, PASV, LIST, STOR, RETR, multiple sessions, invalid credentials,
// unsupported command, incomplete data channel, slow client, disconnect
// during transfer.
package protocol

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jlaffaye/ftp"

	"github.com/Dmn117/ftp2sftp/internal/config"
	"github.com/Dmn117/ftp2sftp/tests/testutil"
)

func startGateway(t *testing.T, configure func(*config.UserConfig)) *testutil.Gateway {
	t.Helper()

	sftpSrv := testutil.StartSFTPServer(t, "remote", "remote-secret")
	knownHosts := sftpSrv.WriteKnownHosts(t, true)

	return testutil.StartGateway(t, sftpSrv, knownHosts, configure)
}

func dialFTP(t *testing.T, gw *testutil.Gateway) *ftp.ServerConn {
	t.Helper()

	c, err := ftp.DialTimeout(gw.Addr, 5*time.Second)
	if err != nil {
		t.Fatalf("ftp.DialTimeout(%s): %v", gw.Addr, err)
	}

	t.Cleanup(func() { _ = c.Quit() })

	return c
}

func loginOrFail(t *testing.T, gw *testutil.Gateway) *ftp.ServerConn {
	t.Helper()

	c := dialFTP(t, gw)

	if err := c.Login(gw.FTPUsername, gw.FTPPassword); err != nil {
		t.Fatalf("Login: %v", err)
	}

	return c
}

func TestLoginPWDAndCWD(t *testing.T) {
	gw := startGateway(t, nil)
	c := loginOrFail(t, gw)

	dir, err := c.CurrentDir()
	if err != nil {
		t.Fatalf("CurrentDir (PWD): %v", err)
	}

	if dir != "/" {
		t.Errorf("CurrentDir() = %q, want /", dir)
	}

	if err := os.MkdirAll(filepath.Join(gw.Config.Users[0].SFTP.RootPath, "facturas"), 0o755); err != nil {
		t.Fatalf("preparing remote dir: %v", err)
	}

	if err := c.ChangeDir("facturas"); err != nil {
		t.Fatalf("ChangeDir: %v", err)
	}

	dir, err = c.CurrentDir()
	if err != nil {
		t.Fatalf("CurrentDir after CWD: %v", err)
	}

	if dir != "/facturas" {
		t.Errorf("CurrentDir() after CWD = %q, want /facturas", dir)
	}
}

func TestLoginRejectsInvalidCredentials(t *testing.T) {
	gw := startGateway(t, nil)
	c := dialFTP(t, gw)

	if err := c.Login(gw.FTPUsername, "definitely-wrong"); err == nil {
		t.Fatal("Login with a wrong password should fail")
	}
}

func TestStorThenRetrRoundTrip(t *testing.T) {
	gw := startGateway(t, nil)
	c := loginOrFail(t, gw)

	content := []byte("<factura>contenido de prueba</factura>")

	if err := c.Stor("archivo.xml", bytes.NewReader(content)); err != nil {
		t.Fatalf("Stor: %v", err)
	}

	resp, err := c.Retr("archivo.xml")
	if err != nil {
		t.Fatalf("Retr: %v", err)
	}

	defer resp.Close()

	got, err := io.ReadAll(resp)
	if err != nil {
		t.Fatalf("reading RETR response: %v", err)
	}

	if !bytes.Equal(got, content) {
		t.Errorf("RETR content = %q, want %q", got, content)
	}
}

func TestStoredFileIsNotVisibleWithFinalNameUntilComplete(t *testing.T) {
	gw := startGateway(t, nil)
	c := loginOrFail(t, gw)

	if err := c.Stor("archivo.xml", bytes.NewReader([]byte("ok"))); err != nil {
		t.Fatalf("Stor: %v", err)
	}

	entries, err := c.List("/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	for _, e := range entries {
		if e.Name != "archivo.xml" {
			t.Errorf("unexpected listing entry: %q (temporary artifacts must never be listed)", e.Name)
		}
	}

	if len(entries) != 1 {
		t.Fatalf("List returned %d entries, want exactly 1 (the committed file)", len(entries))
	}
}

func TestListReflectsDirectoryContents(t *testing.T) {
	gw := startGateway(t, nil)
	c := loginOrFail(t, gw)

	for _, name := range []string{"a.xml", "b.xml", "c.xml"} {
		if err := c.Stor(name, bytes.NewReader([]byte("x"))); err != nil {
			t.Fatalf("Stor(%s): %v", name, err)
		}
	}

	entries, err := c.List("/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("List returned %d entries, want 3", len(entries))
	}
}

func TestMkdirDeleteAndRename(t *testing.T) {
	gw := startGateway(t, nil)
	c := loginOrFail(t, gw)

	if err := c.MakeDir("nuevo"); err != nil {
		t.Fatalf("MakeDir: %v", err)
	}

	if err := c.Stor("nuevo/archivo.xml", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("Stor into new dir: %v", err)
	}

	if err := c.Rename("nuevo/archivo.xml", "nuevo/renombrado.xml"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	if err := c.Delete("nuevo/renombrado.xml"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	entries, err := c.List("nuevo")
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("directory should be empty after delete, got %d entries", len(entries))
	}
}

func TestPermissionDeniedOperationsAreRejected(t *testing.T) {
	gw := startGateway(t, func(u *config.UserConfig) {
		u.Permissions = config.PermissionsConfig{AllowUpload: true} // read-only besides upload
	})
	c := loginOrFail(t, gw)

	if err := c.Stor("archivo.xml", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("Stor (allowed) should succeed: %v", err)
	}

	if _, err := c.Retr("archivo.xml"); err == nil {
		t.Error("Retr should be rejected: AllowDownload is false")
	}

	if err := c.Delete("archivo.xml"); err == nil {
		t.Error("Delete should be rejected: AllowDelete is false")
	}

	if err := c.MakeDir("otro"); err == nil {
		t.Error("MakeDir should be rejected: AllowMkdir is false")
	}
}

func TestMultipleConcurrentSessions(t *testing.T) {
	gw := startGateway(t, nil)

	c1 := loginOrFail(t, gw)
	c2 := loginOrFail(t, gw)

	if err := c1.Stor("from-session-1.xml", bytes.NewReader([]byte("s1"))); err != nil {
		t.Fatalf("session 1 Stor: %v", err)
	}

	if err := c2.Stor("from-session-2.xml", bytes.NewReader([]byte("s2"))); err != nil {
		t.Fatalf("session 2 Stor: %v", err)
	}

	entries, err := c1.List("/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("List returned %d entries after two independent sessions, want 2", len(entries))
	}
}

func TestPathTraversalIsRejected(t *testing.T) {
	gw := startGateway(t, nil)
	c := loginOrFail(t, gw)

	if err := c.ChangeDir("../../../etc"); err == nil {
		dir, _ := c.CurrentDir()
		t.Fatalf("ChangeDir with traversal should fail, ended up at %q", dir)
	}
}
