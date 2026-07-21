package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"

	"github.com/Dmn117/ftp2sftp/internal/config"
)

// runConfigCommand implements the "config" subcommand group: operator
// utilities that inspect or prepare a gateway configuration without
// starting the FTP listener (RF-018). Each subcommand reuses the same
// code path the running gateway uses at startup (internal/config.Load,
// probeUserSFTP) rather than reimplementing validation or connectivity
// checks, so a passing command is a real guarantee about gateway
// behavior, not a second opinion that could drift from it.
func runConfigCommand(args []string) int {
	if len(args) == 0 {
		printConfigUsage()

		return 2
	}

	switch args[0] {
	case "validate":
		return cmdConfigValidate(args[1:])
	case "hash-password":
		return cmdConfigHashPassword(args[1:])
	case "check-connectivity":
		return cmdConfigCheckConnectivity(args[1:])
	case "-h", "--help", "help":
		printConfigUsage()

		return 0
	default:
		fmt.Fprintf(os.Stderr, "ftp2sftp config: subcomando desconocido %q\n\n", args[0])
		printConfigUsage()

		return 2
	}
}

func printConfigUsage() {
	fmt.Fprintln(os.Stderr, `uso: ftp2sftp config <subcomando> [flags]

Subcomandos:
  validate            Carga y valida un archivo de configuración (mismo
                       código que ejecuta el gateway al arrancar).
  hash-password        Genera un hash bcrypt para users[].passwordHash a
                       partir de una contraseña leída de forma segura.
  check-connectivity   Prueba, para cada usuario configurado (o uno con
                       -user), la conexión SSH y el acceso SFTP a su
                       destino remoto, sin arrancar el listener FTP.`)
}

func cmdConfigValidate(args []string) int {
	fs := flag.NewFlagSet("config validate", flag.ContinueOnError)
	path := fs.String("config", defaultConfigPath(), "ruta al archivo YAML de configuración")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config inválida (%s):\n%v\n", *path, err)

		return 1
	}

	fmt.Printf("OK: %s es válida (%d usuario(s))\n", *path, len(cfg.Users))

	return 0
}

func cmdConfigHashPassword(args []string) int {
	fs := flag.NewFlagSet("config hash-password", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	password, err := readPasswordFromOperator()
	if err != nil {
		fmt.Fprintf(os.Stderr, "no se pudo leer la contraseña: %v\n", err)

		return 1
	}

	if len(password) == 0 {
		fmt.Fprintln(os.Stderr, "la contraseña no puede estar vacía")

		return 1
	}

	hash, err := bcrypt.GenerateFromPassword(password, bcrypt.DefaultCost)
	if err != nil {
		fmt.Fprintf(os.Stderr, "no se pudo generar el hash: %v\n", err)

		return 1
	}

	fmt.Println(string(hash))

	return 0
}

// readPasswordFromOperator reads the password without echoing it when
// stdin is a terminal (the normal interactive case), asking for it twice
// to catch typos before the hash ends up in a committed config file. When
// stdin is not a terminal (piped input, e.g. from a script) there is
// nothing to suppress echo on, so it reads a single line instead; callers
// using that path are responsible for not leaking the plaintext through
// shell history or process arguments themselves — this command never
// accepts the password as a CLI argument for exactly that reason.
func readPasswordFromOperator() ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return nil, err
		}

		return []byte(strings.TrimRight(line, "\r\n")), nil
	}

	fmt.Fprint(os.Stderr, "Contraseña: ")

	pw1, err := term.ReadPassword(fd)

	fmt.Fprintln(os.Stderr)

	if err != nil {
		return nil, err
	}

	fmt.Fprint(os.Stderr, "Confirmar contraseña: ")

	pw2, err := term.ReadPassword(fd)

	fmt.Fprintln(os.Stderr)

	if err != nil {
		return nil, err
	}

	if string(pw1) != string(pw2) {
		return nil, fmt.Errorf("las contraseñas no coinciden")
	}

	return pw1, nil
}

func cmdConfigCheckConnectivity(args []string) int {
	fs := flag.NewFlagSet("config check-connectivity", flag.ContinueOnError)
	path := fs.String("config", defaultConfigPath(), "ruta al archivo YAML de configuración")
	username := fs.String("user", "", "probar solo este usuario (por defecto: todos)")
	timeout := fs.Duration("timeout", 10*time.Second, "tiempo máximo de conexión por usuario")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config inválida (%s): %v\n", *path, err)

		return 1
	}

	matched := false
	failed := false

	for _, u := range cfg.Users {
		if *username != "" && u.Username != *username {
			continue
		}

		matched = true

		if err := probeUserSFTP(u, *timeout); err != nil {
			fmt.Printf("FAIL %-20s -> %s:%d  %v\n", u.Username, u.SFTP.Host, u.SFTP.Port, err)

			failed = true

			continue
		}

		fmt.Printf("OK   %-20s -> %s:%d\n", u.Username, u.SFTP.Host, u.SFTP.Port)
	}

	if *username != "" && !matched {
		fmt.Fprintf(os.Stderr, "usuario %q no existe en %s\n", *username, *path)

		return 2
	}

	if failed {
		return 1
	}

	return 0
}
