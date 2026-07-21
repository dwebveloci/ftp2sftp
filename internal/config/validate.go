package config

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// Validate checks that cfg is complete and internally consistent enough to
// start the gateway safely. It accumulates every problem it finds instead
// of stopping at the first one, so an operator can fix a broken
// configuration in a single pass.
func Validate(cfg *Config) error {
	var problems []string

	problems = append(problems, validateServer(cfg.Server)...)
	problems = append(problems, validateTransfer(cfg.Transfer)...)
	problems = append(problems, validateObservability(cfg.Observability)...)

	if len(cfg.Users) == 0 {
		problems = append(problems, "users: debe existir al menos un usuario FTP")
	}

	seen := make(map[string]bool, len(cfg.Users))

	for i, u := range cfg.Users {
		if seen[u.Username] {
			problems = append(problems, fmt.Sprintf("users[%d]: usuario duplicado %q", i, u.Username))
		}

		seen[u.Username] = true

		problems = append(problems, validateUser(i, u)...)
	}

	if len(problems) > 0 {
		return fmt.Errorf("configuración inválida:\n  - %s", strings.Join(problems, "\n  - "))
	}

	return nil
}

func validateServer(s ServerConfig) []string {
	var problems []string

	if s.ListenAddress == "" {
		problems = append(problems, "server.listenAddress: es obligatorio")
	}

	if s.ControlPort < 1 || s.ControlPort > 65535 {
		problems = append(problems, "server.controlPort: debe estar entre 1 y 65535")
	}

	if s.PassiveAddress == "" {
		problems = append(problems, "server.passiveAddress: es obligatorio (dirección anunciada en PASV/EPSV)")
	}

	if s.PassivePortStart < 1 || s.PassivePortStart > 65535 {
		problems = append(problems, "server.passivePortStart: debe estar entre 1 y 65535")
	}

	if s.PassivePortEnd < 1 || s.PassivePortEnd > 65535 {
		problems = append(problems, "server.passivePortEnd: debe estar entre 1 y 65535")
	}

	if s.PassivePortStart > 0 && s.PassivePortEnd > 0 {
		if s.PassivePortEnd <= s.PassivePortStart {
			problems = append(problems, "server.passivePortEnd: debe ser mayor que passivePortStart")
		} else if rangeSize := s.PassivePortEnd - s.PassivePortStart + 1; rangeSize < s.MaxConnections {
			problems = append(problems, fmt.Sprintf(
				"server: el rango pasivo (%d puertos) es menor que maxConnections (%d); "+
					"transferencias concurrentes fallarían por falta de puertos libres",
				rangeSize, s.MaxConnections))
		}
	}

	if s.MaxConnections < 1 {
		problems = append(problems, "server.maxConnections: debe ser mayor que 0")
	}

	return problems
}

func validateTransfer(t TransferConfig) []string {
	var problems []string

	if t.BufferSize < 4096 {
		problems = append(problems, "transfer.bufferSize: debe ser de al menos 4096 bytes")
	}

	if t.TemporarySuffix == "" {
		problems = append(problems, "transfer.temporarySuffix: no puede estar vacío")
	}

	return problems
}

func validateObservability(o ObservabilityConfig) []string {
	var problems []string

	switch o.LogFormat {
	case "json", "text":
	default:
		problems = append(problems, fmt.Sprintf("observability.logFormat: valor no soportado %q (use json o text)", o.LogFormat))
	}

	switch o.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		problems = append(problems, fmt.Sprintf(
			"observability.logLevel: valor no soportado %q (use debug, info, warn o error)", o.LogLevel))
	}

	return problems
}

func validateUser(i int, u UserConfig) []string {
	var problems []string

	prefix := fmt.Sprintf("users[%d] (%s)", i, u.Username)

	if u.Username == "" {
		problems = append(problems, fmt.Sprintf("users[%d]: username es obligatorio", i))
	}

	if u.PasswordHash == "" {
		problems = append(problems, prefix+": passwordHash es obligatorio")
	} else if _, err := bcrypt.Cost([]byte(u.PasswordHash)); err != nil {
		problems = append(problems, prefix+": passwordHash no es un hash bcrypt válido")
	}

	if !strings.HasPrefix(u.VirtualRoot, "/") {
		problems = append(problems, prefix+": virtualRoot debe ser una ruta absoluta")
	}

	if strings.Contains(u.VirtualRoot, "..") {
		problems = append(problems, prefix+": virtualRoot no puede contener '..'")
	}

	if u.MaxFileSize <= 0 {
		problems = append(problems, prefix+": maxFileSize debe ser mayor que 0")
	}

	if u.MaxConcurrentTransfers <= 0 {
		problems = append(problems, prefix+": maxConcurrentTransfers debe ser mayor que 0")
	}

	problems = append(problems, validateSFTPTarget(prefix, u.SFTP)...)

	return problems
}

func validateSFTPTarget(prefix string, s SFTPTargetConfig) []string {
	var problems []string

	if s.Host == "" {
		problems = append(problems, prefix+": sftp.host es obligatorio")
	}

	if s.Port < 1 || s.Port > 65535 {
		problems = append(problems, prefix+": sftp.port debe estar entre 1 y 65535")
	}

	if s.Username == "" {
		problems = append(problems, prefix+": sftp.username es obligatorio")
	}

	if !strings.HasPrefix(s.RootPath, "/") {
		problems = append(problems, prefix+": sftp.rootPath debe ser una ruta absoluta")
	}

	if s.KnownHostsFile == "" {
		problems = append(problems, prefix+": sftp.knownHostsFile es obligatorio; "+
			"está prohibido aceptar cualquier host key")
	} else if info, err := os.Stat(s.KnownHostsFile); err != nil {
		problems = append(problems, prefix+fmt.Sprintf(": sftp.knownHostsFile no accesible: %v", err))
	} else if info.IsDir() {
		problems = append(problems, prefix+": sftp.knownHostsFile apunta a un directorio")
	}

	hasKey := s.PrivateKeyFile != ""
	hasPassword := s.Password != ""

	switch {
	case !hasKey && !hasPassword:
		problems = append(problems, prefix+": debe configurarse sftp.privateKeyFile o sftp.password")
	case hasKey && hasPassword:
		problems = append(problems, prefix+": configure sftp.privateKeyFile o sftp.password, no ambos")
	case hasKey:
		if info, err := os.Stat(s.PrivateKeyFile); err != nil {
			problems = append(problems, prefix+fmt.Sprintf(": sftp.privateKeyFile no accesible: %v", err))
		} else if info.IsDir() {
			problems = append(problems, prefix+": sftp.privateKeyFile apunta a un directorio")
		}
	}

	return problems
}
