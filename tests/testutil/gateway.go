package testutil

import (
	"testing"
	"time"

	libftpserver "github.com/fclairamb/ftpserverlib"
	"golang.org/x/crypto/bcrypt"

	"github.com/Dmn117/ftp2sftp/internal/config"
	"github.com/Dmn117/ftp2sftp/internal/ftpserver"
	"github.com/Dmn117/ftp2sftp/internal/observability"
)

// Gateway is a running ftp2sftp gateway, wired to a testutil.SFTPServer,
// listening on loopback for the FTP protocol test suite.
type Gateway struct {
	Config      *config.Config
	Addr        string
	FTPUsername string
	FTPPassword string
}

// StartGateway builds and starts a full Gateway (config -> ftpserver.New ->
// libftpserver.FtpServer) backed by sftpSrv, on an ephemeral loopback port.
// configure, if non-nil, may adjust the single configured FTP user (e.g.
// to restrict permissions or the virtual root) before the gateway starts.
func StartGateway(t *testing.T, sftpSrv *SFTPServer, knownHosts string, configure func(*config.UserConfig)) *Gateway {
	t.Helper()

	const ftpPassword = "ax2012-secret"

	hash, err := bcrypt.GenerateFromPassword([]byte(ftpPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}

	sftpHost, sftpPort := sftpSrv.HostPort(t)

	user := config.UserConfig{
		Username: "ax2012", PasswordHash: string(hash), VirtualRoot: "/",
		MaxFileSize: 10 * 1024 * 1024, MaxConcurrentTransfers: 4,
		Permissions: config.PermissionsConfig{
			AllowUpload: true, AllowDownload: true, AllowMkdir: true,
			AllowRename: true, AllowDelete: true, AllowOverwrite: true,
		},
		SFTP: config.SFTPTargetConfig{
			Host: sftpHost, Port: sftpPort, Username: sftpSrv.Username, Password: sftpSrv.Password,
			KnownHostsFile: knownHosts, RootPath: sftpSrv.Root,
			ConnectTimeout: config.Duration(5 * time.Second),
			ReadDirTimeout: config.Duration(5 * time.Second),
		},
	}

	if configure != nil {
		configure(&user)
	}

	cfg := &config.Config{
		Server: config.ServerConfig{
			ListenAddress: "127.0.0.1", ControlPort: 0, PassiveAddress: "127.0.0.1",
			PassivePortStart: 40000, PassivePortEnd: 40199, MaxConnections: 10,
			IdleTimeout: config.Duration(30 * time.Second), DataConnectionTimeout: config.Duration(10 * time.Second),
			ShutdownTimeout: config.Duration(5 * time.Second),
		},
		Transfer:      config.TransferConfig{BufferSize: 65536, TemporarySuffix: ".part", CalculateSHA256: true},
		Observability: config.ObservabilityConfig{LogLevel: "error", LogFormat: "text"},
		Users:         []config.UserConfig{user},
	}

	logger := observability.NewLogger(cfg.Observability)

	gw, err := ftpserver.New(cfg, logger, observability.NewMetrics())
	if err != nil {
		t.Fatalf("ftpserver.New: %v", err)
	}

	srv := libftpserver.NewFtpServer(gw)
	srv.Logger = observability.NewProtocolLogger(cfg.Observability)

	if err := srv.Listen(); err != nil {
		t.Fatalf("srv.Listen: %v", err)
	}

	go func() { _ = srv.Serve() }()

	t.Cleanup(func() { _ = srv.Stop() })

	return &Gateway{Config: cfg, Addr: srv.Addr(), FTPUsername: user.Username, FTPPassword: ftpPassword}
}
