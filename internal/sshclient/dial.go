// Package sshclient establishes the outbound SSH connection to the remote
// SFTP server: host key verification against known_hosts, key or password
// authentication, and connect timeouts. It never implements SSH itself; it
// is a thin, defensive wrapper around golang.org/x/crypto/ssh.
//
// Accepting an unknown or changed host key is never an option this package
// exposes, in production or otherwise.
package sshclient

import (
	stderrors "errors"
	"net"
	"os"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	errs "github.com/Dmn117/ftp2sftp/internal/errors"
)

// Config is everything needed to establish and authenticate one SSH
// connection to a remote SFTP server.
type Config struct {
	Host                     string
	Port                     int
	Username                 string
	PrivateKeyFile           string
	PrivateKeyPassphraseFile string
	Password                 string
	KnownHostsFile           string
	ConnectTimeout           time.Duration
}

// Dial opens and authenticates an SSH connection. The returned client owns
// the underlying TCP connection; callers are responsible for calling
// Close().
func Dial(cfg Config) (*ssh.Client, error) {
	const op = "sshclient.Dial"

	hostKeyCallback, err := knownhosts.New(cfg.KnownHostsFile)
	if err != nil {
		return nil, errs.Wrap(errs.KindConfig, op, "no se pudo cargar el archivo known_hosts", err)
	}

	authMethods, err := buildAuthMethods(cfg)
	if err != nil {
		return nil, err
	}

	timeout := cfg.ConnectTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	clientConfig := &ssh.ClientConfig{
		User:            cfg.Username,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         timeout,
	}

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))

	dialer := net.Dialer{Timeout: timeout}

	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return nil, errs.Wrap(errs.KindSSHConnection, op, "no se pudo conectar al servidor SFTP remoto", err)
	}

	_ = conn.SetDeadline(time.Now().Add(timeout))

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, clientConfig)
	if err != nil {
		_ = conn.Close()

		var keyErr *knownhosts.KeyError
		if stderrors.As(err, &keyErr) {
			return nil, errs.Wrap(errs.KindHostKeyMismatch, op,
				"la identidad del servidor SFTP remoto no coincide con known_hosts", err)
		}

		return nil, errs.Wrap(errs.KindSFTPAuthentication, op,
			"falló la autenticación o el handshake SSH con el servidor remoto", err)
	}

	_ = conn.SetDeadline(time.Time{})

	return ssh.NewClient(sshConn, chans, reqs), nil
}

func buildAuthMethods(cfg Config) ([]ssh.AuthMethod, error) {
	const op = "sshclient.buildAuthMethods"

	switch {
	case cfg.PrivateKeyFile != "":
		signer, err := loadSigner(cfg.PrivateKeyFile, cfg.PrivateKeyPassphraseFile)
		if err != nil {
			return nil, err
		}

		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil

	case cfg.Password != "":
		return []ssh.AuthMethod{ssh.Password(cfg.Password)}, nil

	default:
		return nil, errs.New(errs.KindConfig, op, "no se configuró ningún mecanismo de autenticación SFTP")
	}
}

func loadSigner(keyFile, passphraseFile string) (ssh.Signer, error) {
	const op = "sshclient.loadSigner"

	keyBytes, err := os.ReadFile(keyFile) //nolint:gosec // operator-provided config path
	if err != nil {
		return nil, errs.Wrap(errs.KindConfig, op, "no se pudo leer la llave privada SFTP", err)
	}

	if passphraseFile == "" {
		signer, err := ssh.ParsePrivateKey(keyBytes)
		if err != nil {
			return nil, errs.Wrap(errs.KindConfig, op, "la llave privada SFTP no es válida", err)
		}

		return signer, nil
	}

	passphrase, err := os.ReadFile(passphraseFile) //nolint:gosec // operator-provided config path
	if err != nil {
		return nil, errs.Wrap(errs.KindConfig, op, "no se pudo leer la passphrase de la llave privada SFTP", err)
	}

	signer, err := ssh.ParsePrivateKeyWithPassphrase(keyBytes, trimNewline(passphrase))
	if err != nil {
		return nil, errs.Wrap(errs.KindConfig, op, "la llave privada SFTP no es válida o la passphrase es incorrecta", err)
	}

	return signer, nil
}

func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}

	return b
}
