package observability

import (
	"fmt"
	"io"
	"sync/atomic"
)

// Counter is a monotonically increasing value, safe for concurrent use.
type Counter struct{ v atomic.Uint64 }

// Inc increments the counter by 1.
func (c *Counter) Inc() { c.v.Add(1) }

// Add increments the counter by n.
func (c *Counter) Add(n uint64) { c.v.Add(n) }

// Value returns the current value.
func (c *Counter) Value() uint64 { return c.v.Load() }

// Gauge is a value that can go up or down, safe for concurrent use.
type Gauge struct{ v atomic.Int64 }

// Set assigns the gauge's value.
func (g *Gauge) Set(n int64) { g.v.Store(n) }

// Inc increments the gauge by 1.
func (g *Gauge) Inc() { g.v.Add(1) }

// Dec decrements the gauge by 1.
func (g *Gauge) Dec() { g.v.Add(-1) }

// Value returns the current value.
func (g *Gauge) Value() int64 { return g.v.Load() }

// Metrics is the fixed set of counters and gauges from
// FTP2SFTP-REQUIREMENTS.md section 16. Duration metrics are kept as a
// millisecond sum plus a count (a minimal, hand-rolled summary) rather than
// full Prometheus histograms: implementing correct histogram bucketing
// without a client library was judged disproportionate for the MVP: see
// ADR on observability. Adopt github.com/prometheus/client_golang if true
// histograms become necessary.
type Metrics struct {
	FTPConnectionsTotal  Counter
	FTPConnectionsActive Gauge

	FTPAuthAttemptsTotal Counter
	FTPAuthFailuresTotal Counter

	FTPCommandsTotal Counter

	FTPSessionDurationMillisSum Counter
	FTPSessionDurationCount     Counter

	TransferTotal         Counter
	TransferActive        Gauge
	TransferBytesTotal    Counter
	TransferFailuresTotal Counter

	TransferDurationMillisSum Counter
	TransferDurationCount     Counter

	SFTPConnectionsActive       Gauge
	SFTPConnectionFailuresTotal Counter

	SFTPOperationDurationMillisSum Counter
	SFTPOperationDurationCount     Counter

	TemporaryFilesPending    Gauge
	RateLimitRejectionsTotal Counter
}

// NewMetrics returns a zero-valued Metrics registry.
func NewMetrics() *Metrics {
	return &Metrics{}
}

// WriteText renders every metric in Prometheus text exposition format.
func (m *Metrics) WriteText(w io.Writer) error {
	lines := []struct {
		name, help, typ string
		value           string
	}{
		{"ftp_connections_total", "Total FTP control connections accepted.", "counter", u(m.FTPConnectionsTotal.Value())},
		{"ftp_connections_active", "FTP control connections currently open.", "gauge", i(m.FTPConnectionsActive.Value())},
		{"ftp_auth_attempts_total", "Total FTP authentication attempts.", "counter", u(m.FTPAuthAttemptsTotal.Value())},
		{"ftp_auth_failures_total", "Total failed FTP authentication attempts.", "counter", u(m.FTPAuthFailuresTotal.Value())},
		{"ftp_commands_total", "Total FTP commands processed.", "counter", u(m.FTPCommandsTotal.Value())},
		{"ftp_sessions_duration_seconds_sum", "Sum of FTP session durations.", "counter", millisToSeconds(m.FTPSessionDurationMillisSum.Value())},
		{"ftp_sessions_duration_seconds_count", "Count of completed FTP sessions.", "counter", u(m.FTPSessionDurationCount.Value())},
		{"transfer_total", "Total transfers attempted (STOR+RETR).", "counter", u(m.TransferTotal.Value())},
		{"transfer_active", "Transfers currently in flight.", "gauge", i(m.TransferActive.Value())},
		{"transfer_bytes_total", "Total bytes transferred.", "counter", u(m.TransferBytesTotal.Value())},
		{"transfer_duration_seconds_sum", "Sum of transfer durations.", "counter", millisToSeconds(m.TransferDurationMillisSum.Value())},
		{"transfer_duration_seconds_count", "Count of completed transfers.", "counter", u(m.TransferDurationCount.Value())},
		{"transfer_failures_total", "Total failed transfers.", "counter", u(m.TransferFailuresTotal.Value())},
		{"sftp_connections_active", "SFTP/SSH connections currently open.", "gauge", i(m.SFTPConnectionsActive.Value())},
		{"sftp_connection_failures_total", "Total failed SFTP/SSH connection attempts.", "counter", u(m.SFTPConnectionFailuresTotal.Value())},
		{"sftp_operation_duration_seconds_sum", "Sum of SFTP operation durations.", "counter", millisToSeconds(m.SFTPOperationDurationMillisSum.Value())},
		{"sftp_operation_duration_seconds_count", "Count of completed SFTP operations.", "counter", u(m.SFTPOperationDurationCount.Value())},
		{"temporary_files_pending", "Temporary upload files not yet committed or cleaned up.", "gauge", i(m.TemporaryFilesPending.Value())},
		{"rate_limit_rejections_total", "Total authentication attempts rejected by rate limiting.", "counter", u(m.RateLimitRejectionsTotal.Value())},
	}

	for _, l := range lines {
		if _, err := fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n%s %s\n", l.name, l.help, l.name, l.typ, l.name, l.value); err != nil {
			return err
		}
	}

	return nil
}

func u(v uint64) string { return fmt.Sprintf("%d", v) }
func i(v int64) string  { return fmt.Sprintf("%d", v) }

func millisToSeconds(millis uint64) string {
	return fmt.Sprintf("%.3f", float64(millis)/1000.0)
}
