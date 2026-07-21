# ADR-0002: `fclairamb/ftpserverlib` como motor de protocolo FTP

- Status: accepted
- Date: 2026-07-20

## Context

`ftp2sftp` necesita un servidor FTP con canal de control y canal de datos
(activo/pasivo), códigos de respuesta correctos, y suficiente extensión
para inyectar autenticación, autorización y almacenamiento propios.
`CLAUDE.md` exige evaluar explícitamente si conviene implementar el
servidor FTP desde cero o usar una biblioteca especializada, y advierte
contra reimplementar protocolos complejos sin evaluar costo,
compatibilidad y riesgo.

## Decision

Usar `github.com/fclairamb/ftpserverlib` (MIT, activamente mantenida,
usada en producción por otros gateways FTP) como motor de protocolo.
`internal/ftpserver` implementa únicamente sus interfaces `MainDriver`
(autenticación, ciclo de vida de conexión) y `ClientDriver` (= `afero.Fs`
más las extensiones `GetHandle`/`ReadDir`/`RemoveDir`), delegando en la
librería: framing de comandos, `PASV`/`EPSV`, códigos de respuesta por
comando, y el estado de directorio actual/`RNFR` por conexión.

## Alternatives considered

- **Implementar el servidor FTP desde cero**: control total, pero
  reimplementar framing de comandos, modo pasivo, códigos de respuesta
  correctos por RFC 959 y sus variantes reales de interoperabilidad es
  exactamente el riesgo de protocolo que `CLAUDE.md` pide evitar. Se
  descartó.
- **Otras bibliotecas Go de servidor FTP**: evaluadas por popularidad y
  mantenimiento; `ftpserverlib` fue la que ofreció el mejor ajuste con el
  patrón de "filesystem virtual" (`afero.Fs`) que necesitamos para
  traducir cada operación a SFTP sin materializar un filesystem local
  real.

## Consequences

- Reducción drástica de código de protocolo propio: `internal/ftpserver`
  no parsea comandos FTP ni construye respuestas manualmente.
- Acoplamiento a las interfaces exactas de `ftpserverlib`
  (`MainDriver`/`ClientDriver`/extensiones) — un cambio de versión mayor
  de la librería puede requerir ajustes en el adaptador.
- La librería decide el código FTP por defecto para cada comando; nuestro
  control sobre el mapeo de errores se limita al **mensaje** (que
  garantizamos seguro), no al código exacto en todos los casos — ver
  `docs/protocols/ftp-behavior.md#mapeo-de-errores-a-códigos-ftp`.
- `internal/session` no duplica el estado de directorio actual ni de
  `RNFR`: la librería ya lo gestiona correctamente por conexión (ver
  `docs/architecture/boundaries.md`).

## Security consequences

`ftpserverlib` registra la línea de comando cruda (incluida `PASS`) a
nivel Debug como parte de su propio tracing de protocolo. Mitigado con un
logger dedicado que redacta `PASS` antes de cualquier salida
(`internal/observability.NewProtocolLogger`) — ver
`docs/security/security-model.md`.

## Operational consequences

`Settings` de la librería permite deshabilitar explícitamente comandos
fuera de alcance (`MLSD`, `MLST`, `MFMT`, `SITE`, `STAT`, modo activo) sin
reimplementar nada — usado en `internal/ftpserver/settings.go` para
mantener la superficie de comandos ceñida a RF-004.

## Follow-up actions

Revisar el CHANGELOG de `ftpserverlib` antes de actualizar de versión
mayor.
