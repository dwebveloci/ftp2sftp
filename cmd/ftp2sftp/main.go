// Command ftp2sftp runs the FTP-to-SFTP gateway: it exposes an FTP server
// compatible with Microsoft Dynamics AX 2012 on one side and forwards
// authorized operations to a remote SFTP server on the other.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	libftpserver "github.com/fclairamb/ftpserverlib"

	"github.com/Dmn117/ftp2sftp/internal/config"
	"github.com/Dmn117/ftp2sftp/internal/ftpserver"
	"github.com/Dmn117/ftp2sftp/internal/health"
	"github.com/Dmn117/ftp2sftp/internal/observability"
	"github.com/Dmn117/ftp2sftp/internal/sftpclient"
	"github.com/Dmn117/ftp2sftp/internal/sshclient"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "config" {
		os.Exit(runConfigCommand(os.Args[2:]))
	}

	os.Exit(run())
}

// run contains the actual startup/shutdown sequence, returning a process
// exit code. Keeping it out of main avoids os.Exit short-circuiting
// deferred cleanup and makes the sequence easier to reason about.
func run() int {
	healthcheck := flag.Bool("healthcheck", false,
		"perform a local /healthz check against this process and exit (used by the Docker HEALTHCHECK; "+
			"the runtime image has no shell/curl, so the binary checks itself)")
	configPath := flag.String("config", defaultConfigPath(), "path to the gateway YAML configuration file")
	flag.Parse()

	if *healthcheck {
		return runHealthcheck(*configPath)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ftp2sftp: configuración inválida: %v\n", err)

		return 1
	}

	logger := observability.NewLogger(cfg.Observability)
	metrics := observability.NewMetrics()

	logger.Info("starting ftp2sftp", "configPath", *configPath, "users", len(cfg.Users))

	gateway, err := ftpserver.New(cfg, logger, metrics)
	if err != nil {
		logger.Error("failed to build gateway", "err", err.Error())

		return 1
	}

	srv := libftpserver.NewFtpServer(gateway)
	// A dedicated, redacting logger: ftpserverlib logs the raw FTP command
	// line (including PASS's argument) at Debug level as part of its own
	// protocol tracing. See internal/observability.NewProtocolLogger.
	srv.Logger = observability.NewProtocolLogger(cfg.Observability)

	healthSrv := health.NewServer(
		cfg.Health.ListenAddress,
		metrics,
		logger,
		cfg.Health.ReadinessRequiresSFTP,
		readinessCheck(cfg),
	)

	if err := srv.Listen(); err != nil {
		logger.Error("failed to bind FTP listener", "err", err.Error())

		return 1
	}

	logger.Info("FTP listener bound", "addr", srv.Addr())

	ftpServeErr := make(chan error, 1)
	go func() { ftpServeErr <- srv.Serve() }()

	healthServeErr := make(chan error, 1)
	go func() { healthServeErr <- healthSrv.ListenAndServe() }()

	healthSrv.SetAlive(true)
	logger.Info("health server listening", "addr", cfg.Health.ListenAddress)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sig := <-sigCh:
		logger.Info("shutdown signal received", "signal", sig.String())
	case err := <-ftpServeErr:
		if err != nil {
			logger.Error("FTP server stopped unexpectedly", "err", err.Error())
		}

		healthSrv.SetAlive(false)
	case err := <-healthServeErr:
		if err != nil {
			logger.Error("health server stopped unexpectedly", "err", err.Error())
		}
	}

	return shutdown(cfg, logger, gateway, srv, healthSrv)
}

// shutdown implements the graceful shutdown sequence from
// FTP2SFTP-REQUIREMENTS.md section 14.4:
//  1. stop accepting new FTP connections;
//  2. allow in-flight transfers to finish within server.shutdownTimeout;
//  3. force-close whatever is left once that deadline passes;
//  4. stop the health server last, so /healthz keeps reporting truthfully
//     for as long as anything is still draining.
func shutdown(
	cfg *config.Config,
	logger *slog.Logger,
	gateway *ftpserver.Gateway,
	srv *libftpserver.FtpServer,
	healthSrv *health.Server,
) int {
	healthSrv.SetAlive(false)
	gateway.PrepareShutdown()

	if err := srv.Stop(); err != nil {
		logger.Warn("error stopping FTP listener", "err", err.Error())
	}

	deadline := cfg.Server.ShutdownTimeout.Duration()
	logger.Info("draining active sessions", "timeout", deadline.String())

	deadlineCh := make(chan struct{})
	timer := time.AfterFunc(deadline, func() { close(deadlineCh) })
	defer timer.Stop()

	drained := make(chan struct{})

	go func() {
		gateway.WaitForSessions(deadlineCh)
		close(drained)
	}()

	select {
	case <-drained:
		logger.Info("all sessions drained cleanly")
	case <-deadlineCh:
		logger.Warn("shutdown deadline reached with sessions still active; forcing close")
		gateway.CloseAllConnections()
		<-drained
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := healthSrv.Shutdown(ctx); err != nil {
		logger.Warn("error stopping health server", "err", err.Error())
	}

	logger.Info("shutdown complete")

	return 0
}

// readinessCheck probes every configured user's SFTP target with a fresh,
// short-lived connection (deliberately not reusing any session's
// connection, keeping health checks independent of FTP traffic). It
// reports not-ready if any configured backend is unreachable; RF-017
// leaves this policy to configuration via health.readinessRequiresSftp, so
// this is only invoked when that flag is true.
func readinessCheck(cfg *config.Config) health.ReadinessCheck {
	return func(ctx context.Context) error {
		for _, u := range cfg.Users {
			if err := ctx.Err(); err != nil {
				return err
			}

			if err := probeUserSFTP(u, 3*time.Second); err != nil {
				return fmt.Errorf("usuario %s: %w", u.Username, err)
			}
		}

		return nil
	}
}

// probeUserSFTP opens a fresh, short-lived SSH+SFTP connection to a user's
// configured remote target and pings it, then closes it. It is the single
// code path behind both RF-017 readiness checks and the
// "config check-connectivity" operator command, so a passing check is a
// real guarantee about what the gateway will do at startup — never a
// separate reimplementation that could drift from it.
func probeUserSFTP(u config.UserConfig, timeout time.Duration) error {
	sshClient, err := sshclient.Dial(sshclient.Config{
		Host:                     u.SFTP.Host,
		Port:                     u.SFTP.Port,
		Username:                 u.SFTP.Username,
		PrivateKeyFile:           u.SFTP.PrivateKeyFile,
		PrivateKeyPassphraseFile: u.SFTP.PrivateKeyPassphraseFile,
		Password:                 u.SFTP.Password,
		KnownHostsFile:           u.SFTP.KnownHostsFile,
		ConnectTimeout:           timeout,
	})
	if err != nil {
		return err
	}

	client, err := sftpclient.New(sshClient, u.SFTP.RootPath)
	if err != nil {
		return err
	}

	pingErr := client.Ping()
	_ = client.Close()

	return pingErr
}

func defaultConfigPath() string {
	if p := os.Getenv("CONFIG_FILE"); p != "" {
		return p
	}

	return "/app/config/config.yaml"
}

// runHealthcheck implements the Docker HEALTHCHECK entry point. The
// runtime image is distroless (no shell, no curl/wget), so the health
// check has to be the same binary calling itself over loopback instead of
// an external HTTP client tool.
func runHealthcheck(configPath string) int {
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ftp2sftp healthcheck: %v\n", err)

		return 1
	}

	host, port, err := net.SplitHostPort(cfg.Health.ListenAddress)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ftp2sftp healthcheck: invalid health.listenAddress: %v\n", err)

		return 1
	}

	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}

	client := &http.Client{Timeout: 3 * time.Second}

	resp, err := client.Get(fmt.Sprintf("http://%s/healthz", net.JoinHostPort(host, port)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ftp2sftp healthcheck: %v\n", err)

		return 1
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "ftp2sftp healthcheck: /healthz returned %d\n", resp.StatusCode)

		return 1
	}

	return 0
}
