// Package observability provides structured logging, a minimal in-process
// metrics registry exposed in Prometheus text format, and correlation ID
// generation. It never logs passwords, private keys, or file content
// (RF-015, section 5.4 of FTP2SFTP-REQUIREMENTS.md).
package observability

import (
	"log/slog"
	"os"

	"github.com/Dmn117/ftp2sftp/internal/config"
)

// NewLogger builds the process-wide structured logger from configuration.
func NewLogger(cfg config.ObservabilityConfig) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)}

	var handler slog.Handler
	if cfg.LogFormat == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}

func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
