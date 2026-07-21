// Package ftpserver adapts ftp2sftp's domain modules (auth, authorization,
// filesystem, session, sftpclient, transfer) to the ftpserverlib driver
// interfaces (MainDriver + ClientDriver). It owns no protocol parsing and
// no SFTP wire logic itself; it only wires policy decisions to the
// libraries that implement each protocol.
package ftpserver

import (
	"crypto/tls"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	libftpserver "github.com/fclairamb/ftpserverlib"

	"github.com/Dmn117/ftp2sftp/internal/auth"
	"github.com/Dmn117/ftp2sftp/internal/authorization"
	"github.com/Dmn117/ftp2sftp/internal/config"
	errs "github.com/Dmn117/ftp2sftp/internal/errors"
	"github.com/Dmn117/ftp2sftp/internal/filesystem"
	"github.com/Dmn117/ftp2sftp/internal/observability"
	"github.com/Dmn117/ftp2sftp/internal/session"
	"github.com/Dmn117/ftp2sftp/internal/sftpclient"
	"github.com/Dmn117/ftp2sftp/internal/sshclient"
	"github.com/Dmn117/ftp2sftp/internal/transfer"
)

// Rate-limiting thresholds for FTP authentication (RF-002). These are
// deliberately not exposed in configuration: FTP2SFTP-REQUIREMENTS.md
// section 13's example configuration does not call for tuning them, and
// adding config surface without a demonstrated need is discouraged by
// CLAUDE.md. Revisit if operational experience shows they need tuning.
const (
	authMaxFailures = 5
	authWindow      = time.Minute
	authLockout     = 10 * time.Second
)

var (
	errShuttingDown       = errs.New(errs.KindDisconnected, "ftpserver.ClientConnected", "el servicio está cerrando, intente más tarde")
	errTooManyConnections = errs.New(errs.KindRateLimited, "ftpserver.ClientConnected", "demasiadas conexiones activas, intente más tarde")
	errNoUserRuntime      = errs.New(errs.KindInternal, "ftpserver.AuthUser", "error interno de configuración")
)

// userRuntime is the resolved, ready-to-use runtime state for one
// configured FTP user: everything AuthUser needs to build a Session.
type userRuntime struct {
	cfg    config.UserConfig
	mapper *filesystem.Mapper
	policy authorization.Policy
	gate   *authorization.ConcurrencyGate
}

// connState is stored via ClientContext.SetExtra/Extra to bridge
// MainDriver's per-connection callbacks (ClientConnected/AuthUser/
// ClientDisconnected), which do not otherwise share state.
type connState struct {
	admitted bool
	sess     *session.Session
}

// Gateway implements ftpserverlib.MainDriver. One Gateway serves every FTP
// connection for the process.
type Gateway struct {
	cfg      *config.Config
	settings *libftpserver.Settings
	users    map[string]*userRuntime
	authent  *auth.Authenticator
	logger   *slog.Logger
	metrics  *observability.Metrics

	activeConnections atomic.Int64
	shuttingDown      atomic.Bool
	sessions          sync.WaitGroup

	connsMu sync.Mutex
	conns   map[uint32]libftpserver.ClientContext
}

// New builds a Gateway from validated configuration.
func New(cfg *config.Config, logger *slog.Logger, metrics *observability.Metrics) (*Gateway, error) {
	records := make([]auth.UserRecord, 0, len(cfg.Users))
	users := make(map[string]*userRuntime, len(cfg.Users))

	for _, u := range cfg.Users {
		records = append(records, auth.UserRecord{Username: u.Username, PasswordHash: u.PasswordHash})

		users[u.Username] = &userRuntime{
			cfg:    u,
			mapper: filesystem.NewMapper(u.VirtualRoot, u.SFTP.RootPath),
			policy: authorization.Policy{
				AllowUpload:    u.Permissions.AllowUpload,
				AllowDownload:  u.Permissions.AllowDownload,
				AllowDelete:    u.Permissions.AllowDelete,
				AllowMkdir:     u.Permissions.AllowMkdir,
				AllowRename:    u.Permissions.AllowRename,
				AllowOverwrite: u.Permissions.AllowOverwrite,
				MaxFileSize:    u.MaxFileSize,
			},
			gate: authorization.NewConcurrencyGate(u.MaxConcurrentTransfers),
		}
	}

	store := auth.NewStore(records)
	byIP := auth.NewLimiter(authMaxFailures, authWindow, authLockout)
	byUser := auth.NewLimiter(authMaxFailures, authWindow, authLockout)

	return &Gateway{
		cfg:      cfg,
		settings: buildSettings(cfg.Server),
		users:    users,
		authent:  auth.NewAuthenticator(store, byIP, byUser),
		logger:   logger,
		metrics:  metrics,
		conns:    make(map[uint32]libftpserver.ClientContext),
	}, nil
}

// GetSettings implements ftpserverlib.MainDriver.
func (g *Gateway) GetSettings() (*libftpserver.Settings, error) {
	return g.settings, nil
}

// GetTLSConfig implements ftpserverlib.MainDriver. FTPS is out of scope
// for the MVP (FTP2SFTP-REQUIREMENTS.md section 4).
//
// This must return a non-nil error, never (nil, nil): ftpserverlib's AUTH
// command handler (handle_misc.go: handleAUTH) only takes its error branch
// when err != nil; if err == nil it unconditionally does
// tls.Server(conn, tlsConfig), even when tlsConfig is nil. crypto/tls does
// not tolerate a nil *tls.Config on the server side and panics on the
// first handshake read — which takes down the whole process, not just the
// offending connection. A plain, unauthenticated AUTH TLS (which many
// clients, including FileZilla, send by default before login) would
// therefore crash the gateway for every session. Returning an error here
// makes the library reply "502 Cannot get a TLS config" instead.
func (g *Gateway) GetTLSConfig() (*tls.Config, error) {
	return nil, errs.New(errs.KindUnsupportedCommand, "ftpserver.GetTLSConfig", "FTPS no está soportado")
}

// ClientConnected implements ftpserverlib.MainDriver: it enforces
// server.maxConnections (not a built-in Settings field) and rejects new
// connections while shutting down.
func (g *Gateway) ClientConnected(cc libftpserver.ClientContext) (string, error) {
	g.metrics.FTPConnectionsTotal.Inc()

	clientIP := hostOnly(cc.RemoteAddr())

	if g.shuttingDown.Load() {
		return "el servicio está cerrando, intente más tarde", errShuttingDown
	}

	active := g.activeConnections.Add(1)
	if int(active) > g.cfg.Server.MaxConnections {
		g.activeConnections.Add(-1)
		g.logger.Warn("connection rejected: max connections reached", "clientIp", clientIP)

		return "demasiadas conexiones activas, intente más tarde", errTooManyConnections
	}

	cc.SetExtra(&connState{admitted: true})
	g.metrics.FTPConnectionsActive.Inc()
	g.logger.Info("client connected", "clientIp", clientIP)

	g.connsMu.Lock()
	g.conns[cc.ID()] = cc
	g.connsMu.Unlock()

	return "ftp2sftp gateway listo", nil
}

// ClientDisconnected implements ftpserverlib.MainDriver. It is called for
// every connection, even one ClientConnected rejected, so accounting must
// only undo what was actually counted (tracked via connState).
func (g *Gateway) ClientDisconnected(cc libftpserver.ClientContext) {
	g.connsMu.Lock()
	delete(g.conns, cc.ID())
	g.connsMu.Unlock()

	cs, ok := cc.Extra().(*connState)
	if !ok || cs == nil {
		return
	}

	if cs.admitted {
		g.activeConnections.Add(-1)
		g.metrics.FTPConnectionsActive.Dec()
	}

	if cs.sess != nil {
		wasConnected := cs.sess.IsConnected()

		if err := cs.sess.Close(); err != nil {
			g.logger.Warn("error closing session SFTP connection", "sessionId", cs.sess.ID(), "err", err.Error())
		}

		if wasConnected {
			g.metrics.SFTPConnectionsActive.Dec()
		}

		g.logger.Info("session closed", "sessionId", cs.sess.ID(), "ftpUser", cs.sess.Username())
		g.sessions.Done()
	}
}

// AuthUser implements ftpserverlib.MainDriver.
func (g *Gateway) AuthUser(cc libftpserver.ClientContext, username, password string) (libftpserver.ClientDriver, error) {
	clientIP := hostOnly(cc.RemoteAddr())
	g.metrics.FTPCommandsTotal.Inc()
	g.metrics.FTPAuthAttemptsTotal.Inc()

	if err := g.authent.Authenticate(clientIP, username, password); err != nil {
		g.metrics.FTPAuthFailuresTotal.Inc()

		if errs.Is(err, errs.KindRateLimited) {
			g.metrics.RateLimitRejectionsTotal.Inc()
		}

		g.logger.Warn("authentication failed", "clientIp", clientIP, "user", username)

		return nil, err
	}

	runtime, ok := g.users[username]
	if !ok {
		g.logger.Error("authenticated user has no runtime configuration", "user", username)

		return nil, errNoUserRuntime
	}

	sessionID := observability.NewCorrelationID()
	sess := session.New(sessionID, username, clientIP, runtime.mapper, runtime.policy, runtime.gate, g.connector(runtime))

	if cs, ok := cc.Extra().(*connState); ok && cs != nil {
		cs.sess = sess
	}

	g.sessions.Add(1)
	cc.SetPath(runtime.mapper.VirtualRoot())

	g.logger.Info("session authenticated", "sessionId", sessionID, "ftpUser", username, "clientIp", clientIP)

	return newClientDriver(g, sess), nil
}

// connector builds the lazy SFTP dial function for a user, wiring
// sshclient.Dial and sftpclient.New together and tracking connection
// metrics.
func (g *Gateway) connector(u *userRuntime) session.Connector {
	return func() (*sftpclient.Client, error) {
		sshClient, err := sshclient.Dial(sshclient.Config{
			Host:                     u.cfg.SFTP.Host,
			Port:                     u.cfg.SFTP.Port,
			Username:                 u.cfg.SFTP.Username,
			PrivateKeyFile:           u.cfg.SFTP.PrivateKeyFile,
			PrivateKeyPassphraseFile: u.cfg.SFTP.PrivateKeyPassphraseFile,
			Password:                 u.cfg.SFTP.Password,
			KnownHostsFile:           u.cfg.SFTP.KnownHostsFile,
			ConnectTimeout:           u.cfg.SFTP.ConnectTimeout.Duration(),
		})
		if err != nil {
			g.metrics.SFTPConnectionFailuresTotal.Inc()

			return nil, err
		}

		client, err := sftpclient.New(sshClient, u.cfg.SFTP.RootPath)
		if err != nil {
			g.metrics.SFTPConnectionFailuresTotal.Inc()

			return nil, err
		}

		g.metrics.SFTPConnectionsActive.Inc()

		return client, nil
	}
}

// PrepareShutdown marks the gateway as shutting down: ClientConnected
// starts rejecting new connections. It does not touch existing sessions;
// callers drain those separately with WaitForSessions.
func (g *Gateway) PrepareShutdown() {
	g.shuttingDown.Store(true)
}

// WaitForSessions blocks until every currently-authenticated session has
// disconnected, or done is closed (e.g. by a shutdown deadline timer),
// whichever happens first. The caller is responsible for forcefully
// closing remaining connections if it gives up waiting.
func (g *Gateway) WaitForSessions(done <-chan struct{}) {
	finished := make(chan struct{})

	go func() {
		g.sessions.Wait()
		close(finished)
	}()

	select {
	case <-finished:
	case <-done:
	}
}

// recordTransfer finalizes metrics and emits the RF-015 audit event for a
// completed (or failed) upload/download.
func (g *Gateway) recordTransfer(command string, sess *session.Session, r transfer.Result) {
	if command == "STOR" {
		g.metrics.TemporaryFilesPending.Dec()
	}

	g.metrics.TransferBytesTotal.Add(uint64(r.Bytes))

	attrs := []any{
		"sessionId", r.SessionID, "transferId", r.TransferID, "ftpUser", sess.Username(),
		"clientIp", sess.ClientIP(), "command", command, "virtualPath", r.VirtualPath,
		"remotePath", r.RemotePath, "bytes", r.Bytes, "phase", string(r.Phase),
	}

	if r.SHA256 != "" {
		attrs = append(attrs, "sha256", r.SHA256)
	}

	if r.Phase == transfer.PhaseFailed {
		g.metrics.TransferFailuresTotal.Inc()

		if r.Err != nil {
			attrs = append(attrs, "err", r.Err.Error())
		}

		g.logger.Error("transfer failed", attrs...)

		return
	}

	g.logger.Info("transfer completed", attrs...)
}

// CloseAllConnections forcibly closes every currently tracked FTP
// connection. Callers use this as the last step of graceful shutdown, once
// the drain deadline from WaitForSessions has passed: RF-014.4 step 3,
// "cancelar las restantes al expirar". Closing a connection here triggers
// ftpserverlib's normal disconnect path, so ClientDisconnected still runs
// and releases each session's SFTP connection.
func (g *Gateway) CloseAllConnections() {
	g.connsMu.Lock()
	conns := make([]libftpserver.ClientContext, 0, len(g.conns))

	for _, cc := range g.conns {
		conns = append(conns, cc)
	}

	g.connsMu.Unlock()

	for _, cc := range conns {
		if err := cc.Close(); err != nil {
			g.logger.Warn("error force-closing connection during shutdown", "err", err.Error())
		}
	}
}

func hostOnly(addr net.Addr) string {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}

	return host
}
