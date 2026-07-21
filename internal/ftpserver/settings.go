package ftpserver

import (
	"fmt"

	libftpserver "github.com/fclairamb/ftpserverlib"

	"github.com/Dmn117/ftp2sftp/internal/config"
)

// buildSettings translates ServerConfig into ftpserverlib's Settings.
//
// Deliberate choices, documented here rather than scattered as magic
// booleans:
//   - DisableActiveMode: true. PORT/active mode is not required by any
//     confirmed AX 2012 behavior (still an open question, see
//     FTP2SFTP-REQUIREMENTS.md section 17, #1) and passive-only reduces
//     the connection-handling surface for the MVP.
//   - DisableASCIIConversion: true. STOR/RETR must move bytes exactly as
//     received; silently rewriting line endings on XML/binary invoice
//     files would corrupt them. See docs/protocols/ftp-behavior.md.
//   - DisableMLSD/DisableMLST/DisableMFMT/DisableSite/DisableSTAT: true.
//     None of these are in the RF-004 candidate command list; disabling
//     them keeps the exposed command surface to what was actually
//     requested ("no implementar comandos sin necesidad de
//     compatibilidad").
//   - ActiveConnectionsCheck / PasvConnectionsCheck are left at their zero
//     value, which is ftpserverlib's IPMatchRequired: the data connection
//     peer must match the control connection peer. This is the secure
//     default and needs no extra code.
func buildSettings(cfg config.ServerConfig) *libftpserver.Settings {
	return &libftpserver.Settings{
		ListenAddr: fmt.Sprintf("%s:%d", cfg.ListenAddress, cfg.ControlPort),
		PublicHost: cfg.PassiveAddress,
		Banner:     "ftp2sftp gateway",

		PassiveTransferPortRange: &libftpserver.PortRange{
			Start: cfg.PassivePortStart,
			End:   cfg.PassivePortEnd,
		},

		IdleTimeout:         int(cfg.IdleTimeout.Duration().Seconds()),
		ConnectionTimeout:   int(cfg.DataConnectionTimeout.Duration().Seconds()),
		DisableActiveMode:   true,
		DefaultTransferType: libftpserver.TransferTypeBinary,

		DisableASCIIConversion: true,
		DisableMLSD:            true,
		DisableMLST:            true,
		DisableMFMT:            true,
		DisableSite:            true,
		DisableSTAT:            true,
		DisableSYST:            false,
	}
}
