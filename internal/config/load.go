package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Load reads, parses, defaults and validates the configuration file at
// path. It never starts the gateway with an invalid configuration: any
// problem is returned as a single descriptive error.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is an operator-provided startup argument
	if err != nil {
		return nil, fmt.Errorf("leyendo archivo de configuración %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parseando configuración %q: %w", path, err)
	}

	applyDefaults(&cfg)

	if err := Validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// applyDefaults fills in operational (non security-relevant) fields left
// unset. Security- or topology-relevant fields are never defaulted here;
// Validate rejects them if missing.
func applyDefaults(cfg *Config) {
	if cfg.Server.IdleTimeout == 0 {
		cfg.Server.IdleTimeout = Duration(5 * time.Minute)
	}

	if cfg.Server.DataConnectionTimeout == 0 {
		cfg.Server.DataConnectionTimeout = Duration(30 * time.Second)
	}

	if cfg.Server.ShutdownTimeout == 0 {
		cfg.Server.ShutdownTimeout = Duration(15 * time.Second)
	}

	if cfg.Transfer.BufferSize == 0 {
		cfg.Transfer.BufferSize = 64 * 1024
	}

	if cfg.Transfer.TemporarySuffix == "" {
		cfg.Transfer.TemporarySuffix = ".part"
	}

	if cfg.Observability.LogLevel == "" {
		cfg.Observability.LogLevel = "info"
	}

	if cfg.Observability.LogFormat == "" {
		cfg.Observability.LogFormat = "json"
	}

	if cfg.Health.ListenAddress == "" {
		cfg.Health.ListenAddress = ":8080"
	}

	for i := range cfg.Users {
		u := &cfg.Users[i]

		if u.VirtualRoot == "" {
			u.VirtualRoot = "/"
		}

		if u.SFTP.Port == 0 {
			u.SFTP.Port = 22
		}

		if u.SFTP.ConnectTimeout == 0 {
			u.SFTP.ConnectTimeout = Duration(10 * time.Second)
		}

		if u.SFTP.ReadDirTimeout == 0 {
			u.SFTP.ReadDirTimeout = Duration(60 * time.Second)
		}

		if u.MaxConcurrentTransfers == 0 {
			u.MaxConcurrentTransfers = 4
		}
	}
}
