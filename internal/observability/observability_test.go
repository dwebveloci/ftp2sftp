package observability_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Dmn117/ftp2sftp/internal/config"
	"github.com/Dmn117/ftp2sftp/internal/observability"
)

func TestNewLoggerDoesNotPanicForEachFormat(t *testing.T) {
	for _, format := range []string{"json", "text", "unknown-defaults-to-json"} {
		logger := observability.NewLogger(config.ObservabilityConfig{LogLevel: "debug", LogFormat: format})
		if logger == nil {
			t.Fatalf("NewLogger(%q) returned nil", format)
		}

		logger.Info("smoke test", "format", format)
	}
}

func TestMetricsWriteTextIncludesAllCounters(t *testing.T) {
	m := observability.NewMetrics()
	m.FTPConnectionsTotal.Inc()
	m.FTPConnectionsActive.Set(3)
	m.TransferBytesTotal.Add(1024)

	var buf bytes.Buffer
	if err := m.WriteText(&buf); err != nil {
		t.Fatalf("WriteText: %v", err)
	}

	out := buf.String()

	for _, want := range []string{
		"ftp_connections_total 1",
		"ftp_connections_active 3",
		"transfer_bytes_total 1024",
		"# TYPE ftp_connections_total counter",
		"# TYPE ftp_connections_active gauge",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("metrics output missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestCounterAndGaugeConcurrentUse(t *testing.T) {
	var c observability.Counter

	var g observability.Gauge

	done := make(chan struct{})

	for i := 0; i < 100; i++ {
		go func() {
			c.Inc()
			g.Inc()
			done <- struct{}{}
		}()
	}

	for i := 0; i < 100; i++ {
		<-done
	}

	if c.Value() != 100 {
		t.Errorf("Counter.Value() = %d, want 100", c.Value())
	}

	if g.Value() != 100 {
		t.Errorf("Gauge.Value() = %d, want 100", g.Value())
	}
}

func TestNewCorrelationIDIsUniqueAndHex(t *testing.T) {
	seen := make(map[string]bool)

	for i := 0; i < 1000; i++ {
		id := observability.NewCorrelationID()

		if len(id) != 16 {
			t.Fatalf("NewCorrelationID() length = %d, want 16 hex chars", len(id))
		}

		if seen[id] {
			t.Fatalf("NewCorrelationID() produced a duplicate: %s", id)
		}

		seen[id] = true
	}
}
