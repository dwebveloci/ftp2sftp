package authorization_test

import (
	"sync"
	"testing"

	"github.com/Dmn117/ftp2sftp/internal/authorization"
)

func TestZeroValuePolicyDeniesEverything(t *testing.T) {
	var p authorization.Policy

	checks := map[string]func() error{
		"upload":    p.CheckUpload,
		"download":  p.CheckDownload,
		"delete":    p.CheckDelete,
		"mkdir":     p.CheckMkdir,
		"rename":    p.CheckRename,
		"overwrite": p.CheckOverwrite,
	}

	for name, check := range checks {
		if err := check(); err == nil {
			t.Errorf("zero-value Policy should deny %s by default", name)
		}
	}
}

func TestPolicyAllowsWhenConfigured(t *testing.T) {
	p := authorization.Policy{AllowUpload: true, MaxFileSize: 1024}

	if err := p.CheckUpload(); err != nil {
		t.Errorf("CheckUpload() should succeed when AllowUpload is true: %v", err)
	}

	if err := p.CheckDownload(); err == nil {
		t.Error("CheckDownload() should still fail when AllowDownload is false")
	}
}

func TestCheckFileSize(t *testing.T) {
	p := authorization.Policy{MaxFileSize: 100}

	if err := p.CheckFileSize(100); err != nil {
		t.Errorf("CheckFileSize(100) with limit 100 should pass: %v", err)
	}

	if err := p.CheckFileSize(101); err == nil {
		t.Error("CheckFileSize(101) with limit 100 should fail")
	}

	if err := p.CheckFileSize(-1); err != nil {
		t.Errorf("CheckFileSize(-1) (unknown size) should pass: %v", err)
	}
}

func TestConcurrencyGateLimitsAndReleases(t *testing.T) {
	g := authorization.NewConcurrencyGate(2)

	if !g.TryAcquire() {
		t.Fatal("first acquire should succeed")
	}

	if !g.TryAcquire() {
		t.Fatal("second acquire should succeed")
	}

	if g.TryAcquire() {
		t.Fatal("third acquire should fail: limit is 2")
	}

	g.Release()

	if !g.TryAcquire() {
		t.Fatal("acquire after release should succeed")
	}
}

func TestConcurrencyGateConcurrentUse(t *testing.T) {
	g := authorization.NewConcurrencyGate(5)

	var wg sync.WaitGroup

	acquired := make(chan bool, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			ok := g.TryAcquire()
			acquired <- ok

			if ok {
				g.Release()
			}
		}()
	}

	wg.Wait()
	close(acquired)

	if g.InUse() != 0 {
		t.Errorf("InUse() = %d after all goroutines released, want 0", g.InUse())
	}
}
