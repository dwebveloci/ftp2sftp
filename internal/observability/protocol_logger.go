package observability

import (
	"context"
	"log/slog"
	"strings"

	"github.com/Dmn117/ftp2sftp/internal/config"
)

// sensitiveCommands lists FTP verbs whose argument must never reach a log,
// under any log level. ftpserverlib logs the raw command line verbatim at
// Debug level ("Received line", "line", <full line>) as part of its own
// protocol tracing — useful for capturing the exact command sequence AX
// 2012 sends (FTP2SFTP-REQUIREMENTS.md section 21 asks for exactly this
// capability), but PASS carries the client's password in that same line.
var sensitiveCommands = []string{"PASS"}

// NewProtocolLogger builds the logger passed to ftpserverlib
// (FtpServer.Logger). It shares the configured level/format with the
// application logger, but wraps the handler so that a sensitive command's
// argument is always redacted before it reaches any log line — including
// at Debug level, which is what an operator would enable specifically to
// capture AX 2012's command sequence for compatibility validation. This
// makes "enable full protocol logging" a safe action rather than a
// credential leak.
func NewProtocolLogger(cfg config.ObservabilityConfig) *slog.Logger {
	base := NewLogger(cfg)

	return slog.New(WrapRedacting(base.Handler()))
}

// WrapRedacting wraps an existing handler with the same command-line
// redaction NewProtocolLogger applies. Exported so tests can verify the
// redaction behavior against a handler backed by an in-memory buffer
// instead of the real stdout writer NewLogger uses.
func WrapRedacting(next slog.Handler) slog.Handler {
	return &redactingHandler{next: next}
}

type redactingHandler struct {
	next slog.Handler
}

func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *redactingHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == "Received line" || r.Message == "Sending answer" {
		r = redactLineAttr(r)
	}

	return h.next.Handle(ctx, r)
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &redactingHandler{next: h.next.WithAttrs(attrs)}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{next: h.next.WithGroup(name)}
}

func redactLineAttr(r slog.Record) slog.Record {
	nr := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)

	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "line" {
			nr.AddAttrs(slog.String("line", redactIfSensitive(a.Value.String())))

			return true
		}

		nr.AddAttrs(a)

		return true
	})

	return nr
}

func redactIfSensitive(line string) string {
	trimmed := strings.TrimSpace(line)

	for _, cmd := range sensitiveCommands {
		if strings.EqualFold(trimmed, cmd) {
			return cmd // bare "PASS" with no argument: nothing to redact
		}

		prefix := cmd + " "
		if len(trimmed) > len(prefix) && strings.EqualFold(trimmed[:len(prefix)], prefix) {
			return cmd + " ***REDACTED***"
		}
	}

	return line
}
