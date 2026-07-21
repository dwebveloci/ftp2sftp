// Package health exposes an internal HTTP server with liveness, readiness
// and metrics endpoints (RF-016, RF-017), separate from the FTP control
// port. It is meant to be reachable only from an orchestrator's probe or an
// internal monitoring network, never from the FTP client network.
package health

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/Dmn117/ftp2sftp/internal/observability"
)

// ReadinessCheck probes an external dependency (the configured SFTP
// targets). It must return quickly; Server bounds it with its own timeout
// regardless of what the check itself does.
type ReadinessCheck func(ctx context.Context) error

// Server is the internal health/readiness/metrics HTTP endpoint.
type Server struct {
	httpServer             *http.Server
	metrics                *observability.Metrics
	logger                 *slog.Logger
	alive                  atomic.Bool
	readinessRequiresCheck bool
	readinessCheck         ReadinessCheck
	readinessTimeout       time.Duration
}

// NewServer builds a health server bound to addr. If readinessRequiresCheck
// is true, /readyz also calls readinessCheck (typically a lightweight probe
// against the configured SFTP targets) and fails readiness if it errors or
// times out; RF-016 requires that this never gates /healthz.
func NewServer(
	addr string,
	metrics *observability.Metrics,
	logger *slog.Logger,
	readinessRequiresCheck bool,
	readinessCheck ReadinessCheck,
) *Server {
	s := &Server{
		metrics:                metrics,
		logger:                 logger,
		readinessRequiresCheck: readinessRequiresCheck,
		readinessCheck:         readinessCheck,
		readinessTimeout:       5 * time.Second,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	mux.HandleFunc("/metrics", s.handleMetrics)

	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	return s
}

// SetAlive updates the liveness flag. Call it with true once the FTP
// listener is bound and configuration validated, and with false if the FTP
// accept loop ever exits unexpectedly, so an orchestrator can restart the
// process.
func (s *Server) SetAlive(alive bool) {
	s.alive.Store(alive)
}

// ListenAndServe starts the HTTP server. It blocks until Shutdown is called
// or an unrecoverable error occurs.
func (s *Server) ListenAndServe() error {
	err := s.httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}

	return err
}

// Shutdown gracefully stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	if !s.alive.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not alive\n"))

		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if !s.alive.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not alive\n"))

		return
	}

	if s.readinessRequiresCheck && s.readinessCheck != nil {
		ctx, cancel := context.WithTimeout(r.Context(), s.readinessTimeout)
		defer cancel()

		if err := s.readinessCheck(ctx); err != nil {
			s.logger.Warn("readiness check failed", "err", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready: dependency check failed\n"))

			return
		}
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready\n"))
}

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	if err := s.metrics.WriteText(w); err != nil {
		s.logger.Warn("failed to write metrics response", "err", err)
	}
}
