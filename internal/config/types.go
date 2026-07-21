// Package config defines the external configuration schema for ftp2sftp,
// loads it from a YAML file and validates it before the gateway starts.
//
// Configuration is the only place business-sensitive topology (network
// exposure, per-user SFTP targets, permissions) is decided. Nothing in this
// package invents defaults for security-relevant fields; those must be set
// explicitly or Load fails.
package config

import (
	"fmt"
	"time"
)

// Duration wraps time.Duration so it can be expressed as a YAML string such
// as "5m" or "30s" (gopkg.in/yaml.v3 does not decode int64-backed types from
// duration strings on its own).
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}

	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("duración inválida %q: %w", s, err)
	}

	*d = Duration(parsed)

	return nil
}

// Duration returns the standard library duration value.
func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

// Config is the root configuration for the gateway.
type Config struct {
	Server        ServerConfig        `yaml:"server"`
	Users         []UserConfig        `yaml:"users"`
	Transfer      TransferConfig      `yaml:"transfer"`
	Observability ObservabilityConfig `yaml:"observability"`
	Health        HealthConfig        `yaml:"health"`
}

// ServerConfig controls the FTP listener and its passive data channel.
type ServerConfig struct {
	ListenAddress         string   `yaml:"listenAddress"`
	ControlPort           int      `yaml:"controlPort"`
	PassiveAddress        string   `yaml:"passiveAddress"`
	PassivePortStart      int      `yaml:"passivePortStart"`
	PassivePortEnd        int      `yaml:"passivePortEnd"`
	MaxConnections        int      `yaml:"maxConnections"`
	IdleTimeout           Duration `yaml:"idleTimeout"`
	DataConnectionTimeout Duration `yaml:"dataConnectionTimeout"`
	ShutdownTimeout       Duration `yaml:"shutdownTimeout"`
}

// PermissionsConfig gates the mutating FTP operations for a user. The zero
// value denies everything, matching the "deny by default" requirement.
type PermissionsConfig struct {
	AllowUpload    bool `yaml:"allowUpload"`
	AllowDownload  bool `yaml:"allowDownload"`
	AllowDelete    bool `yaml:"allowDelete"`
	AllowMkdir     bool `yaml:"allowMkdir"`
	AllowRename    bool `yaml:"allowRename"`
	AllowOverwrite bool `yaml:"allowOverwrite"`
}

// SFTPTargetConfig describes the remote SFTP server a user's traffic is
// forwarded to. Each user has its own target because RF-014 requires
// per-user SFTP destination, user and authentication mechanism.
type SFTPTargetConfig struct {
	Host                     string   `yaml:"host"`
	Port                     int      `yaml:"port"`
	Username                 string   `yaml:"username"`
	PrivateKeyFile           string   `yaml:"privateKeyFile"`
	PrivateKeyPassphraseFile string   `yaml:"privateKeyPassphraseFile"`
	Password                 string   `yaml:"password"`
	KnownHostsFile           string   `yaml:"knownHostsFile"`
	RootPath                 string   `yaml:"rootPath"`
	ConnectTimeout           Duration `yaml:"connectTimeout"`
	ReadDirTimeout           Duration `yaml:"readDirTimeout"`
}

// UserConfig is one FTP account and everything needed to serve it.
type UserConfig struct {
	Username               string            `yaml:"username"`
	PasswordHash           string            `yaml:"passwordHash"`
	VirtualRoot            string            `yaml:"virtualRoot"`
	SFTP                   SFTPTargetConfig  `yaml:"sftp"`
	Permissions            PermissionsConfig `yaml:"permissions"`
	MaxFileSize            int64             `yaml:"maxFileSize"`
	MaxConcurrentTransfers int               `yaml:"maxConcurrentTransfers"`
}

// TransferConfig controls streaming and commit behavior shared by all
// users.
type TransferConfig struct {
	BufferSize      int    `yaml:"bufferSize"`
	TemporarySuffix string `yaml:"temporarySuffix"`
	CalculateSHA256 bool   `yaml:"calculateSha256"`
}

// ObservabilityConfig controls structured logging.
type ObservabilityConfig struct {
	LogLevel  string `yaml:"logLevel"`
	LogFormat string `yaml:"logFormat"`
}

// HealthConfig controls the internal HTTP health/readiness/metrics server.
type HealthConfig struct {
	ListenAddress         string `yaml:"listenAddress"`
	ReadinessRequiresSFTP bool   `yaml:"readinessRequiresSftp"`
}
