package health_test

import (
	"context"
	stderrors "errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Dmn117/ftp2sftp/internal/health"
	"github.com/Dmn117/ftp2sftp/internal/observability"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// startServer boots a health.Server on an ephemeral loopback port, waits
// until it accepts connections, and returns its base URL plus a cleanup
// function that shuts it down.
func startServer(t *testing.T, s *health.Server, addr string) (baseURL string, stop func()) {
	t.Helper()

	// health.NewServer binds lazily inside ListenAndServe, so pick a free
	// port ourselves and pass it in via addr instead of ":0", to know the
	// URL up front.
	go func() {
		_ = s.ListenAndServe()
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()

			break
		}

		time.Sleep(10 * time.Millisecond)
	}

	stop = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	}

	return "http://" + addr, stop
}

func freeAddr(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("finding a free port: %v", err)
	}

	addr := ln.Addr().String()
	_ = ln.Close()

	return addr
}

func TestHealthzReflectsAliveFlag(t *testing.T) {
	addr := freeAddr(t)
	s := health.NewServer(addr, observability.NewMetrics(), discardLogger(), false, nil)
	base, stop := startServer(t, s, addr)

	defer stop()

	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}

	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("healthz before SetAlive(true): status = %d, want 503", resp.StatusCode)
	}

	s.SetAlive(true)

	resp, err = http.Get(base + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}

	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz after SetAlive(true): status = %d, want 200", resp.StatusCode)
	}
}

func TestReadyzWithoutSFTPRequirement(t *testing.T) {
	addr := freeAddr(t)
	s := health.NewServer(addr, observability.NewMetrics(), discardLogger(), false, nil)
	s.SetAlive(true)
	base, stop := startServer(t, s, addr)

	defer stop()

	resp, err := http.Get(base + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}

	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("readyz without SFTP requirement: status = %d, want 200", resp.StatusCode)
	}
}

func TestReadyzFailsWhenSFTPCheckFails(t *testing.T) {
	addr := freeAddr(t)
	check := func(ctx context.Context) error { return stderrors.New("sftp unreachable") }
	s := health.NewServer(addr, observability.NewMetrics(), discardLogger(), true, check)
	s.SetAlive(true)
	base, stop := startServer(t, s, addr)

	defer stop()

	resp, err := http.Get(base + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}

	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("readyz with failing SFTP check: status = %d, want 503", resp.StatusCode)
	}
}

func TestReadyzSucceedsWhenSFTPCheckSucceeds(t *testing.T) {
	addr := freeAddr(t)
	check := func(ctx context.Context) error { return nil }
	s := health.NewServer(addr, observability.NewMetrics(), discardLogger(), true, check)
	s.SetAlive(true)
	base, stop := startServer(t, s, addr)

	defer stop()

	resp, err := http.Get(base + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}

	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("readyz with passing SFTP check: status = %d, want 200", resp.StatusCode)
	}
}

func TestReadyzDoesNotBlockLongerThanTimeout(t *testing.T) {
	addr := freeAddr(t)
	check := func(ctx context.Context) error {
		<-ctx.Done()

		return ctx.Err()
	}
	s := health.NewServer(addr, observability.NewMetrics(), discardLogger(), true, check)
	s.SetAlive(true)
	base, stop := startServer(t, s, addr)

	defer stop()

	start := time.Now()

	resp, err := http.Get(base + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}

	_ = resp.Body.Close()

	elapsed := time.Since(start)

	if elapsed > 10*time.Second {
		t.Fatalf("readyz took %v, readiness timeout was not enforced", elapsed)
	}

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("readyz with a hanging check: status = %d, want 503", resp.StatusCode)
	}
}

func TestMetricsEndpointServesPrometheusText(t *testing.T) {
	addr := freeAddr(t)
	m := observability.NewMetrics()
	m.FTPConnectionsTotal.Inc()

	s := health.NewServer(addr, m, discardLogger(), false, nil)
	base, stop := startServer(t, s, addr)

	defer stop()

	resp, err := http.Get(base + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics: status = %d, want 200", resp.StatusCode)
	}

	if len(body) == 0 {
		t.Fatal("metrics body should not be empty")
	}
}
