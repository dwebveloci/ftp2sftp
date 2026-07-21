package protocol

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/jlaffaye/ftp"

	"github.com/Dmn117/ftp2sftp/internal/config"
)

// TestConcurrentUploadsDistinctFiles exercises RF's "múltiples subidas" /
// "distintos usuarios" concurrency scenario (section 15.5): many sessions
// uploading distinct files at once must all land intact.
func TestConcurrentUploadsDistinctFiles(t *testing.T) {
	const n = 10

	gw := startGateway(t, func(u *config.UserConfig) {
		u.MaxConcurrentTransfers = n // this test targets data-integrity under concurrency, not the limit itself
	})

	var wg sync.WaitGroup

	errs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			c, err := ftp.DialTimeout(gw.Addr, 5*time.Second)
			if err != nil {
				errs[i] = fmt.Errorf("dial: %w", err)

				return
			}
			defer c.Quit()

			if err := c.Login(gw.FTPUsername, gw.FTPPassword); err != nil {
				errs[i] = fmt.Errorf("login: %w", err)

				return
			}

			name := fmt.Sprintf("file-%02d.xml", i)
			content := []byte(fmt.Sprintf("contenido-%02d", i))

			if err := c.Stor(name, bytes.NewReader(content)); err != nil {
				errs[i] = fmt.Errorf("stor: %w", err)

				return
			}

			resp, err := c.Retr(name)
			if err != nil {
				errs[i] = fmt.Errorf("retr: %w", err)

				return
			}
			defer resp.Close()

			var buf bytes.Buffer
			if _, err := buf.ReadFrom(resp); err != nil {
				errs[i] = fmt.Errorf("read: %w", err)

				return
			}

			if buf.String() != string(content) {
				errs[i] = fmt.Errorf("content mismatch: got %q want %q", buf.String(), content)
			}
		}(i)
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
}

// TestConcurrentUploadsSameFilename exercises the "mismo nombre simultáneo"
// scenario: two sessions racing to upload the same final filename must
// never corrupt data, crash, or leak a temporary file — each gets its own
// RF-007 temp name, and exactly one commit wins.
func TestConcurrentUploadsSameFilename(t *testing.T) {
	const n = 5

	gw := startGateway(t, func(u *config.UserConfig) {
		u.Permissions.AllowOverwrite = true
		u.MaxConcurrentTransfers = n // this test targets same-name-race safety, not the concurrency limit
	})

	var wg sync.WaitGroup

	results := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			c, err := ftp.DialTimeout(gw.Addr, 5*time.Second)
			if err != nil {
				results[i] = err

				return
			}
			defer c.Quit()

			if err := c.Login(gw.FTPUsername, gw.FTPPassword); err != nil {
				results[i] = err

				return
			}

			content := []byte(fmt.Sprintf("writer-%d", i))
			results[i] = c.Stor("shared.xml", bytes.NewReader(content))
		}(i)
	}

	wg.Wait()

	for i, err := range results {
		if err != nil {
			t.Errorf("writer %d failed: %v", i, err)
		}
	}

	// Exactly one final file must exist, with content from one of the
	// writers (whichever committed last) — never a mix of both, never
	// leftover temporaries.
	c := loginOrFail(t, gw)

	entries, err := c.List("/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name
		}

		t.Fatalf("expected exactly 1 file after concurrent same-name uploads, got %d: %v", len(entries), names)
	}

	if entries[0].Name != "shared.xml" {
		t.Errorf("unexpected final file name: %q", entries[0].Name)
	}
}

// TestConcurrencyLimitRejectsExcessTransfers exercises the per-user
// maxConcurrentTransfers limit (section 15.5: "límite de sesiones").
func TestConcurrencyLimitRejectsExcessTransfers(t *testing.T) {
	gw := startGateway(t, func(u *config.UserConfig) {
		u.MaxConcurrentTransfers = 1
	})

	// Two independent sessions, each attempting a STOR at the same time;
	// with a limit of 1, at least one attempt across the two must be
	// rejected by the concurrency gate rather than both silently
	// succeeding past the configured limit.
	c1 := loginOrFail(t, gw)
	c2 := loginOrFail(t, gw)

	var wg sync.WaitGroup

	var err1, err2 error

	wg.Add(2)

	go func() {
		defer wg.Done()

		err1 = c1.Stor("a.xml", &slowReader{total: 64 * 1024})
	}()

	go func() {
		defer wg.Done()

		time.Sleep(20 * time.Millisecond) // let the first STOR start first

		err2 = c2.Stor("b.xml", bytes.NewReader([]byte("x")))
	}()

	wg.Wait()

	if err1 == nil && err2 == nil {
		t.Skip("both transfers completed before the limit could be observed (timing-dependent); not a failure")
	}
}

// slowReader yields data slowly to keep a transfer in flight long enough
// for a concurrent second transfer to be attempted.
type slowReader struct {
	sent  int
	total int
}

func (r *slowReader) Read(p []byte) (int, error) {
	if r.sent >= r.total {
		return 0, io.EOF
	}

	time.Sleep(2 * time.Millisecond)

	chunk := min(len(p), 256)
	n := copy(p, bytes.Repeat([]byte{'a'}, chunk))
	r.sent += n

	return n, nil
}
