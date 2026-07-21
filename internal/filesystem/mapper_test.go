package filesystem_test

import (
	"testing"

	"github.com/Dmn117/ftp2sftp/internal/filesystem"
)

func TestResolveVirtualBasicNavigation(t *testing.T) {
	m := filesystem.NewMapper("/", "/home/briva.mx/public_html/guias/facturas")

	cases := []struct {
		cwd, arg, want string
	}{
		{"/", "facturas", "/facturas"},
		{"/facturas", "2026", "/facturas/2026"},
		{"/facturas/2026", "..", "/facturas"},
		{"/facturas/2026", "../../otro", "/otro"},
		{"/anything", "/facturas/2026/archivo.xml", "/facturas/2026/archivo.xml"},
		{"/", ".", "/"},
	}

	for _, tc := range cases {
		got, err := m.ResolveVirtual(tc.cwd, tc.arg)
		if err != nil {
			t.Errorf("ResolveVirtual(%q, %q) unexpected error: %v", tc.cwd, tc.arg, err)
			continue
		}

		if got != tc.want {
			t.Errorf("ResolveVirtual(%q, %q) = %q, want %q", tc.cwd, tc.arg, got, tc.want)
		}
	}
}

func TestResolveVirtualNeverEscapesRoot(t *testing.T) {
	m := filesystem.NewMapper("/", "/home/facturas")

	attacks := []struct {
		cwd, arg string
	}{
		{"/", "../../../etc/passwd"},
		{"/facturas/2026", "../../../../../../etc/passwd"},
		{"/", "/../../etc/passwd"},
		{"/", "../../../../../../../../etc/passwd"},
	}

	for _, tc := range attacks {
		got, err := m.ResolveVirtual(tc.cwd, tc.arg)
		if err != nil {
			continue // rejecting outright is also acceptable
		}

		if got == "" || got[0] != '/' {
			t.Fatalf("ResolveVirtual(%q, %q) produced an unrooted path: %q", tc.cwd, tc.arg, got)
		}

		remote, err := m.ToRemote(got)
		if err != nil {
			continue
		}

		if remote != "/home/facturas" && len(remote) <= len("/home/facturas") {
			t.Fatalf("attack path %q/%q escaped remote root: resolved=%q remote=%q", tc.cwd, tc.arg, got, remote)
		}

		if remote[:len("/home/facturas")] != "/home/facturas" {
			t.Fatalf("attack path %q/%q escaped remote root: remote=%q", tc.cwd, tc.arg, remote)
		}
	}
}

func TestResolveVirtualRejectsNulByte(t *testing.T) {
	m := filesystem.NewMapper("/", "/home/facturas")

	if _, err := m.ResolveVirtual("/", "archivo\x00.xml"); err == nil {
		t.Fatal("ResolveVirtual should reject a path containing a NUL byte")
	}
}

func TestToRemoteMapping(t *testing.T) {
	m := filesystem.NewMapper("/", "/home/briva.mx/public_html/guias/facturas")

	remote, err := m.ToRemote("/2026/archivo.xml")
	if err != nil {
		t.Fatalf("ToRemote: %v", err)
	}

	want := "/home/briva.mx/public_html/guias/facturas/2026/archivo.xml"
	if remote != want {
		t.Errorf("ToRemote() = %q, want %q", remote, want)
	}
}

func TestToVirtualRejectsPathOutsideRemoteRoot(t *testing.T) {
	m := filesystem.NewMapper("/", "/home/facturas")

	if _, err := m.ToVirtual("/etc/passwd"); err == nil {
		t.Fatal("ToVirtual should reject a remote path outside the remote root")
	}
}

func TestToVirtualRoundTrip(t *testing.T) {
	m := filesystem.NewMapper("/", "/home/facturas")

	remote, err := m.ToRemote("/2026/archivo.xml")
	if err != nil {
		t.Fatalf("ToRemote: %v", err)
	}

	virtual, err := m.ToVirtual(remote)
	if err != nil {
		t.Fatalf("ToVirtual: %v", err)
	}

	if virtual != "/2026/archivo.xml" {
		t.Errorf("round trip = %q, want /2026/archivo.xml", virtual)
	}
}

func TestNonRootVirtualRoot(t *testing.T) {
	m := filesystem.NewMapper("/incoming", "/srv/sftp/incoming")

	resolved, err := m.ResolveVirtual("/incoming", "archivo.xml")
	if err != nil {
		t.Fatalf("ResolveVirtual: %v", err)
	}

	if resolved != "/incoming/archivo.xml" {
		t.Fatalf("resolved = %q", resolved)
	}

	remote, err := m.ToRemote(resolved)
	if err != nil {
		t.Fatalf("ToRemote: %v", err)
	}

	if remote != "/srv/sftp/incoming/archivo.xml" {
		t.Fatalf("remote = %q", remote)
	}

	if _, err := m.ResolveVirtual("/incoming", "../../etc/passwd"); err == nil {
		t.Fatal("escaping a non-root virtual root should be rejected")
	}
}
