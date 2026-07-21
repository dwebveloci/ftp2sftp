package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/Dmn117/ftp2sftp/internal/config"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writeFile(%s): %v", path, err)
	}
}

func validYAML(t *testing.T, dir, knownHosts, privateKey string) string {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword([]byte("s3cret-test-only"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}

	return `
server:
  listenAddress: "0.0.0.0"
  controlPort: 2121
  passiveAddress: "ftp.internal.example"
  passivePortStart: 30000
  passivePortEnd: 30100
  maxConnections: 10
  idleTimeout: "5m"
  dataConnectionTimeout: "30s"
  shutdownTimeout: "15s"

transfer:
  bufferSize: 65536
  temporarySuffix: ".part"
  calculateSha256: true

observability:
  logLevel: "info"
  logFormat: "json"

health:
  listenAddress: ":8080"
  readinessRequiresSftp: false

users:
  - username: "ax2012"
    passwordHash: "` + string(hash) + `"
    virtualRoot: "/"
    maxFileSize: 1073741824
    maxConcurrentTransfers: 2
    permissions:
      allowUpload: true
      allowDownload: false
      allowDelete: false
      allowMkdir: false
      allowRename: false
      allowOverwrite: false
    sftp:
      host: "sftp.internal.example"
      port: 22
      username: "admin_facturas"
      privateKeyFile: "` + privateKey + `"
      knownHostsFile: "` + knownHosts + `"
      rootPath: "/home/briva.mx/public_html/guias/facturas"
      connectTimeout: "10s"
`
}

func TestLoadValidConfig(t *testing.T) {
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "known_hosts")
	privateKey := filepath.Join(dir, "id_ed25519")
	writeFile(t, knownHosts, "sftp.internal.example ssh-ed25519 AAAATESTKEY\n")
	writeFile(t, privateKey, "not-a-real-key-just-a-placeholder-file\n")

	cfgPath := filepath.Join(dir, "config.yaml")
	writeFile(t, cfgPath, validYAML(t, dir, knownHosts, privateKey))

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() returned error for a valid config: %v", err)
	}

	if cfg.Server.ControlPort != 2121 {
		t.Errorf("ControlPort = %d, want 2121", cfg.Server.ControlPort)
	}

	if len(cfg.Users) != 1 || cfg.Users[0].Username != "ax2012" {
		t.Fatalf("unexpected users: %+v", cfg.Users)
	}

	if cfg.Server.IdleTimeout.Duration().String() != "5m0s" {
		t.Errorf("IdleTimeout = %v, want 5m0s", cfg.Server.IdleTimeout.Duration())
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := config.Load("/nonexistent/config.yaml"); err == nil {
		t.Fatal("Load() with missing file should fail")
	}
}

func TestValidateRejectsNarrowPassivePortRange(t *testing.T) {
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "known_hosts")
	privateKey := filepath.Join(dir, "id_ed25519")
	writeFile(t, knownHosts, "host key\n")
	writeFile(t, privateKey, "key\n")

	hash, _ := bcrypt.GenerateFromPassword([]byte("x"), bcrypt.DefaultCost)

	cfg := &config.Config{
		Server: config.ServerConfig{
			ListenAddress:    "0.0.0.0",
			ControlPort:      21,
			PassiveAddress:   "ftp.example.com",
			PassivePortStart: 30000,
			PassivePortEnd:   30002, // only 3 ports
			MaxConnections:   50,    // needs at least 50 ports
		},
		Transfer:      config.TransferConfig{BufferSize: 65536, TemporarySuffix: ".part"},
		Observability: config.ObservabilityConfig{LogLevel: "info", LogFormat: "json"},
		Users: []config.UserConfig{{
			Username:               "u1",
			PasswordHash:           string(hash),
			VirtualRoot:            "/",
			MaxFileSize:            1024,
			MaxConcurrentTransfers: 1,
			SFTP: config.SFTPTargetConfig{
				Host: "sftp.example.com", Port: 22, Username: "u1",
				PrivateKeyFile: privateKey, KnownHostsFile: knownHosts,
				RootPath: "/home/u1",
			},
		}},
	}

	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("Validate() should reject a passive port range smaller than maxConnections")
	}
}

func TestValidateRejectsDuplicateUsers(t *testing.T) {
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "known_hosts")
	privateKey := filepath.Join(dir, "id_ed25519")
	writeFile(t, knownHosts, "host key\n")
	writeFile(t, privateKey, "key\n")

	hash, _ := bcrypt.GenerateFromPassword([]byte("x"), bcrypt.DefaultCost)
	user := config.UserConfig{
		Username: "dup", PasswordHash: string(hash), VirtualRoot: "/",
		MaxFileSize: 1024, MaxConcurrentTransfers: 1,
		SFTP: config.SFTPTargetConfig{
			Host: "h", Port: 22, Username: "u", PrivateKeyFile: privateKey,
			KnownHostsFile: knownHosts, RootPath: "/home/u",
		},
	}

	cfg := &config.Config{
		Server: config.ServerConfig{
			ListenAddress: "0.0.0.0", ControlPort: 21, PassiveAddress: "ftp.example.com",
			PassivePortStart: 30000, PassivePortEnd: 30100, MaxConnections: 5,
		},
		Transfer:      config.TransferConfig{BufferSize: 65536, TemporarySuffix: ".part"},
		Observability: config.ObservabilityConfig{LogLevel: "info", LogFormat: "json"},
		Users:         []config.UserConfig{user, user},
	}

	if err := config.Validate(cfg); err == nil {
		t.Fatal("Validate() should reject duplicate usernames")
	}
}

func TestValidateRejectsBothKeyAndPassword(t *testing.T) {
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "known_hosts")
	privateKey := filepath.Join(dir, "id_ed25519")
	writeFile(t, knownHosts, "host key\n")
	writeFile(t, privateKey, "key\n")

	hash, _ := bcrypt.GenerateFromPassword([]byte("x"), bcrypt.DefaultCost)

	cfg := &config.Config{
		Server: config.ServerConfig{
			ListenAddress: "0.0.0.0", ControlPort: 21, PassiveAddress: "ftp.example.com",
			PassivePortStart: 30000, PassivePortEnd: 30100, MaxConnections: 5,
		},
		Transfer:      config.TransferConfig{BufferSize: 65536, TemporarySuffix: ".part"},
		Observability: config.ObservabilityConfig{LogLevel: "info", LogFormat: "json"},
		Users: []config.UserConfig{{
			Username: "u1", PasswordHash: string(hash), VirtualRoot: "/",
			MaxFileSize: 1024, MaxConcurrentTransfers: 1,
			SFTP: config.SFTPTargetConfig{
				Host: "h", Port: 22, Username: "u", PrivateKeyFile: privateKey,
				Password: "also-set", KnownHostsFile: knownHosts, RootPath: "/home/u",
			},
		}},
	}

	if err := config.Validate(cfg); err == nil {
		t.Fatal("Validate() should reject both privateKeyFile and password set at once")
	}
}

func TestValidateRejectsInvalidBcryptHash(t *testing.T) {
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "known_hosts")
	writeFile(t, knownHosts, "host key\n")

	cfg := &config.Config{
		Server: config.ServerConfig{
			ListenAddress: "0.0.0.0", ControlPort: 21, PassiveAddress: "ftp.example.com",
			PassivePortStart: 30000, PassivePortEnd: 30100, MaxConnections: 5,
		},
		Transfer:      config.TransferConfig{BufferSize: 65536, TemporarySuffix: ".part"},
		Observability: config.ObservabilityConfig{LogLevel: "info", LogFormat: "json"},
		Users: []config.UserConfig{{
			Username: "u1", PasswordHash: "plaintext-not-a-hash", VirtualRoot: "/",
			MaxFileSize: 1024, MaxConcurrentTransfers: 1,
			SFTP: config.SFTPTargetConfig{
				Host: "h", Port: 22, Username: "u", Password: "x",
				KnownHostsFile: knownHosts, RootPath: "/home/u",
			},
		}},
	}

	if err := config.Validate(cfg); err == nil {
		t.Fatal("Validate() should reject a non-bcrypt passwordHash")
	}
}

func TestValidateRejectsMissingKnownHosts(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{
			ListenAddress: "0.0.0.0", ControlPort: 21, PassiveAddress: "ftp.example.com",
			PassivePortStart: 30000, PassivePortEnd: 30100, MaxConnections: 5,
		},
		Transfer:      config.TransferConfig{BufferSize: 65536, TemporarySuffix: ".part"},
		Observability: config.ObservabilityConfig{LogLevel: "info", LogFormat: "json"},
		Users: []config.UserConfig{{
			Username: "u1", PasswordHash: mustHash("x"), VirtualRoot: "/",
			MaxFileSize: 1024, MaxConcurrentTransfers: 1,
			SFTP: config.SFTPTargetConfig{
				Host: "h", Port: 22, Username: "u", Password: "x",
				RootPath: "/home/u", // no KnownHostsFile
			},
		}},
	}

	if err := config.Validate(cfg); err == nil {
		t.Fatal("Validate() should reject a missing known_hosts file")
	}
}

func mustHash(pw string) string {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	if err != nil {
		panic(err)
	}

	return string(h)
}
