# FTP Behavior

Compatibilidad FTP, comandos soportados, modo pasivo y mapeo de errores.
Para la traducción hacia SFTP operación por operación, ver
`docs/protocols/ftp-sftp-gateway.md`.

## Estado de compatibilidad con AX 2012

**No validado contra una instancia real de AX 2012** en este entorno de
desarrollo. Lo que sí se validó (`tests/protocol/`, `tests/integration/`):
comportamiento FTP estándar contra un cliente FTP real e independiente
(`github.com/jlaffaye/ftp`) — login, PASV, LIST, STOR, RETR, rename,
mkdir, delete, múltiples sesiones concurrentes, credenciales inválidas,
traversal de rutas.

No declares compatibilidad completa con AX 2012 sin completar la
[validación manual](#validación-manual-con-ax-2012) de abajo.

**Confirmado por el operador (pendiente de captura real de AX)**: el flujo
de producción esperado es `STOR` sin `LIST`/`NLST` previo — AX sube
archivos a ciegas, no necesita listar el directorio antes. Esto se
verificó operativamente: contra el SFTP remoto real de preproducción,
`STOR` completa y comprometa el archivo correctamente (`rename` atómico,
sin temporales huérfanos) incluso apuntando a un directorio remoto con
~45,000 archivos donde `LIST` es poco práctico por el volumen de
round-trips que requiere (ver
`docs/security/security-model.md#denegación-de-servicio`). Si una captura
real de AX 2012 muestra que sí emite `LIST`/`NLST`, este supuesto queda
superado y debe reevaluarse `rootPath` o el timeout de
`sftp.readDirTimeout` para ese directorio específico.

## Comandos soportados

| Comando | Estado | Notas |
|---|---|---|
| `USER`, `PASS` | soportado | ver `internal/auth` |
| `SYST`, `FEAT`, `NOOP`, `QUIT` | soportado | manejados por `ftpserverlib` |
| `PWD`, `CWD`, `CDUP` | soportado | directorio virtual gestionado por `ftpserverlib`, confinamiento validado por `internal/filesystem` |
| `TYPE` | soportado (solo binario real) | ver [Modo de transferencia](#modo-de-transferencia-ascii-vs-binario) |
| `PASV`, `EPSV` | soportado | único modo de canal de datos habilitado |
| `PORT` (modo activo) | **deshabilitado** | ver [Modo pasivo](#modo-pasivo) |
| `LIST`, `NLST` | soportado | formato de listado depende de `ftpserverlib`, ver limitación abajo |
| `SIZE`, `MDTM` | soportado | vía `Stat` remoto |
| `STOR` | soportado | commit atómico, ver `ftp-sftp-gateway.md` |
| `APPE` | **rechazado explícitamente** | no encaja con la estrategia de commit atómico por nombre temporal; ver `internal/ftpserver/clientdriver.go` |
| `RETR` | soportado, incluye resume vía `REST` | subida (`STOR`) NO soporta resume, ver abajo |
| `MKD` | soportado, autorizable por usuario | |
| `MKDIR` (no estándar) | **rechazado** | fuera de RF-004; algunos clientes lo envían como sinónimo, `ftpserverlib` lo expone pero nuestro driver lo rechaza |
| `RMD`, `DELE` | soportado, deshabilitado por defecto | `permissions.allowDelete`, por defecto `false` (RF-011) |
| `RNFR`, `RNTO` | soportado, autorizable | estado de `RNFR` gestionado por `ftpserverlib`, no por `ftp2sftp` |
| `SITE`, `MLSD`, `MLST`, `STAT`, `MFMT` | **deshabilitados** | fuera de RF-004; deshabilitados vía `Settings` en `internal/ftpserver/settings.go`, no reimplementados |

## Modo pasivo

Solo `PASV`/`EPSV` está habilitado (`DisableActiveMode: true`). Motivo:
ninguna evidencia confirmada (decisión pendiente #1 de
`FTP2SFTP-REQUIREMENTS.md` §17) indica que AX 2012 requiera modo activo, y
pasivo es la opción más simple y más compatible con NAT/túneles salientes
desde el cliente. Si una captura real de AX muestra uso de `PORT`:

1. Cambiar `DisableActiveMode: false` en `internal/ftpserver/settings.go`.
2. Revisar `Settings.ActiveTransferPortNon20` (RFC 1579) según el entorno.
3. Añadir pruebas de protocolo para modo activo en `tests/protocol/`.
4. Documentar en `docs/deployment/deployment-model.md` el requisito de red
   adicional (el gateway necesitaría iniciar conexiones salientes hacia el
   cliente, algo que muchos túneles/NAT no permiten — ver esa misma
   sección para el análisis completo).

## Modo de transferencia: ASCII vs. binario

`DisableASCIIConversion: true`. Los archivos se transfieren byte a byte
sin importar el `TYPE` que el cliente solicite. Decisión deliberada: la
conversión CRLF↔LF de `TYPE A` reescribiría el contenido de archivos XML
o binarios de factura, y `FTP2SFTP-REQUIREMENTS.md` no pide soporte real
de ASCII. Si AX depende de una conversión real de fin de línea (poco
probable para XML), habría que revisar esta decisión con evidencia real.

## Formato de listado (`LIST`/`NLST`)

`ftpserverlib` genera el formato de listado desde el `os.FileInfo` que
devuelve `internal/ftpserver/clientdriver.go:ReadDir` (estilo Unix
`ls -l`). **No se ha validado que el parser de AX acepte este formato
exacto** — `FTP2SFTP-REQUIREMENTS.md` §5.3/§15.4 marca esto como
obligatorio de probar contra AX real antes de declarar compatibilidad.

## Resume: `STOR` vs. `RETR`

- `RETR` con `REST` previo: soportado (`internal/transfer.DownloadHandle.Seek`
  delega directamente en el archivo remoto — reanudar una descarga de un
  archivo ya completo en el remoto es una operación segura).
- `STOR` con `REST` previo: **no soportado**
  (`internal/transfer.UploadHandle.Seek` rechaza cualquier offset distinto
  de cero). Reanudar una subida no encaja con la estrategia de nombre
  temporal + commit atómico (RF-007): el temporal se trata siempre como
  de un solo intento completo.

## Mapeo de errores a códigos FTP

`ftpserverlib` decide el código FTP por comando (ver
`ADR/0002-libreria-servidor-ftp.md`); `ftp2sftp` solo garantiza que el
**mensaje** nunca filtra detalle interno. La tabla conceptual de
`FTP2SFTP-REQUIREMENTS.md` §11 se cumple así en la práctica:

| Situación de dominio | Código FTP real observado | Mecanismo |
|---|---|---|
| Autenticación fallida | 530 | siempre, vía `AuthUser` — mensaje genérico (`internal/auth.ErrInvalidCredentials`) |
| Ruta no encontrada o denegada | 550 | código por defecto de `ftpserverlib` para `Stat`/`Open`/`Remove`/etc. |
| Comando no soportado | 502 | manejado por `ftpserverlib` para verbos que no despacha |
| Error durante transferencia | 426/451 | `ftpserverlib` al fallar `io.Copy` entre canal de datos y `FileTransfer` |
| Tamaño excedido | 552 (`ErrStorageExceeded`) | no usado activamente: `internal/transfer` reporta el límite como error genérico 550-ish; ver nota abajo |

Nota: `ftpserverlib` reconoce un sentinel `ErrStorageExceeded` para
devolver 552, pero `internal/transfer.UploadHandle.Write` no lo usa
todavía (devuelve un `errs.Error` genérico, que cae en el código por
defecto del comando). Es una mejora de fidelidad de protocolo pendiente,
sin impacto de seguridad ni de corrección (el límite sigue aplicándose).

## Logging seguro de la secuencia de comandos (para validación con AX)

**Riesgo detectado y corregido durante la implementación**: `ftpserverlib`
registra la línea de comando cruda a nivel Debug (`"Received line"`), lo
que incluye el argumento de `PASS` — la contraseña del cliente FTP — en
texto plano. `internal/observability.NewProtocolLogger` envuelve el
handler de logging que se le pasa a `ftpserverlib`
(`FtpServer.Logger`) con una redacción específica: cualquier línea que
empiece con `PASS` (case-insensitive) se reemplaza por
`PASS ***REDACTED***` antes de que llegue a cualquier handler de log,
sin importar el nivel configurado.

Esto significa que **activar `observability.logLevel: debug` en
producción o en un entorno de prueba con AX real es seguro** y es la vía
recomendada para capturar la secuencia real de comandos que AX envía
(decisión pendiente #3 de `FTP2SFTP-REQUIREMENTS.md` §17): los logs
mostrarán cada línea de comando entrante y saliente, con la contraseña
siempre redactada.

## Validación manual con AX 2012

Antes de declarar compatibilidad completa:

1. Configurar `observability.logLevel: debug` temporalmente (ver arriba:
   es seguro, las contraseñas se redactan).
2. Apuntar AX 2012 al gateway (solo en red de prueba controlada).
3. Ejecutar el flujo real de AX: conexión, autenticación, navegación,
   subida de al menos un archivo real, cualquier operación posterior a
   `STOR` que AX realice (verificación de tamaño, listado, etc.).
4. Recolectar de los logs: secuencia exacta de comandos (con `PASS`
   redactado), modo pasivo/activo realmente usado, formato de rutas
   enviado, respuestas del servidor, codificación aparente de nombres de
   archivo, comportamiento tras `STOR`.
5. Comparar contra la tabla de comandos soportados de este documento;
   documentar cualquier comando adicional requerido como una nueva fila
   en `FTP2SFTP-REQUIREMENTS.md` RF-004 antes de implementarlo.
6. Volver `observability.logLevel` a `info` para operación normal (Debug
   es verboso, no solo por seguridad sino por volumen).
