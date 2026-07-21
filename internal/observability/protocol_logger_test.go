package observability_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/Dmn117/ftp2sftp/internal/config"
	"github.com/Dmn117/ftp2sftp/internal/observability"
)

func TestProtocolLoggerRedactsPassword(t *testing.T) {
	var buf bytes.Buffer

	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	testLogger := slog.New(observability.WrapRedacting(handler))

	testLogger.Debug("Received line", "line", "PASS s3cret-value")
	testLogger.Debug("Received line", "line", "USER ax2012")
	testLogger.Debug("Received line", "line", "pass another-secret")

	out := buf.String()

	if strings.Contains(out, "s3cret-value") || strings.Contains(out, "another-secret") {
		t.Fatalf("password leaked into log output:\n%s", out)
	}

	if !strings.Contains(out, "PASS ***REDACTED***") {
		t.Errorf("expected redacted PASS marker in output:\n%s", out)
	}

	if !strings.Contains(out, "USER ax2012") {
		t.Errorf("non-sensitive command should pass through unredacted:\n%s", out)
	}
}

func TestProtocolLoggerCapsAtConfiguredLevel(t *testing.T) {
	logger := observability.NewProtocolLogger(config.ObservabilityConfig{LogLevel: "error", LogFormat: "json"})
	if logger == nil {
		t.Fatal("NewProtocolLogger returned nil")
	}
}
