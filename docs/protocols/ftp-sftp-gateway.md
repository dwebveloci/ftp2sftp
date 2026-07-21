# FTP to SFTP Gateway

Cómo `ftp2sftp` traduce cada operación FTP soportada a SFTP, y qué decide
en cada paso. Complementa `docs/architecture/overview.md` (diagramas de
alto nivel) con el detalle operación por operación.

## Por qué se expone FTP

AX 2012 solo soporta FTP como cliente de salida (`FTP2SFTP-REQUIREMENTS.md`
§1). No es negociable a corto plazo, así que el gateway existe para que el
servidor destino pueda exigir SFTP sin requerir un cambio en AX.

## Supuestos de confianza de red

FTP viaja sin cifrar. El listener FTP **debe** limitarse a red privada o
a un túnel controlado (nunca expuesto directamente a Internet sin una
decisión de riesgo explícita) — ver `docs/security/security-model.md` y
`docs/deployment/deployment-model.md`.

## Modo activo/pasivo

Solo modo pasivo (`PASV`/`EPSV`) está habilitado (`Settings.DisableActiveMode
= true` en `internal/ftpserver/settings.go`). Ver
`docs/protocols/ftp-behavior.md#modo-pasivo` para la justificación y la
vía de extensión a modo activo si una captura real de AX lo exige.

## Rango de puertos pasivos

Configurado en `server.passivePortStart`/`passivePortEnd`. `internal/config`
rechaza al arrancar un rango menor que `server.maxConnections`: con un
rango insuficiente, las transferencias concurrentes fallarían por falta
de puertos libres de una forma confusa en producción en vez de fallar
explícitamente en el arranque (gap identificado en
`docs/architecture/discovery.md` §8, cerrado en `internal/config/validate.go`).

## Autenticación FTP → credenciales SFTP

Son dos credenciales completamente independientes:

1. **FTP**: usuario + contraseña, verificados contra un hash bcrypt
   estático en configuración (`internal/auth`). Nunca se registra la
   contraseña; el mismo mensaje de error se usa para usuario inexistente
   y contraseña incorrecta (RF-002).
2. **SFTP**: por usuario FTP, configurado en `users[].sftp` — llave
   privada (preferido) o contraseña, nunca ambos (`internal/config/validate.go`).

No hay relación matemática ni derivación entre ambas: son dos secretos
distintos que conviven en la misma fila de configuración de usuario. Esto
es lo que RF-014 pide ("configurar por usuario: destino SFTP, usuario
SFTP, mecanismo de autenticación SSH").

## Verificación de host key SSH

Obligatoria y sin bypass posible: `internal/sshclient.Dial` construye el
`HostKeyCallback` desde `knownhosts.New(cfg.KnownHostsFile)`
(`golang.org/x/crypto/ssh/knownhosts`) y no existe ninguna opción de
configuración, ni siquiera de desarrollo, para desactivarla. Un cambio o
desconocimiento de host key produce `errs.KindHostKeyMismatch` — ver
`ADR/0006-verificacion-host-key.md`.

## Mapeo de rutas

`internal/filesystem.Mapper` confina cada usuario a un `virtualRoot` FTP y
lo mapea a un `sftp.rootPath` remoto:

```text
FTP virtual: /facturas/2026/archivo.xml
SFTP real:   /home/briva.mx/public_html/guias/facturas/facturas/2026/archivo.xml
             └────────── sftp.rootPath ─────────┘└── ruta relativa al virtualRoot ──┘
```

Ninguna ruta resuelta puede salir de `virtualRoot` (lado FTP) ni de
`sftp.rootPath` (lado remoto); ver `docs/security/security-model.md#path-traversal`
para el mecanismo exacto y sus límites conocidos (symlinks).

## Mapeo de sesión

Una sesión SSH/SFTP por sesión FTP, establecida de forma perezosa en la
primera operación que la necesita y reutilizada durante toda la sesión
(`internal/session.Session.SFTP`). Ver
`ADR/0004-estrategia-sesion-sftp.md` para por qué se eligió esto en vez de
un pool.

## Streaming vs. archivos temporales

No hay buffer local del archivo completo en ningún punto. `STOR` escribe
directamente al archivo temporal remoto (`archivo.xml.part-<sessionID>`,
RF-007) mientras los bytes llegan por el canal de datos FTP; `RETR` lee
directamente del archivo remoto hacia el canal de datos FTP. El
"streaming" real ocurre por composición de `io.Copy` (en `ftpserverlib`)
con `io.ReaderFrom`/`io.WriterTo` de `*sftp.File` (`pkg/sftp`), que usa
ventanas de escritura/lectura acotadas — nunca se materializa el archivo
completo en memoria del proceso `ftp2sftp`.

## Manejo de transferencias parciales

Si el canal de datos se interrumpe (desconexión, `ABOR`, error de E/S),
`ftpserverlib` llama a `TransferError` antes de `Close` en el handle de
transferencia (`internal/transfer.UploadHandle`/`DownloadHandle`). Para
subidas, `Close` detecta el fallo y elimina el temporal remoto sin
comprometer el archivo final. Para descargas, no hay estado remoto que
limpiar: el archivo origen nunca se modifica.

## Commit atómico

`internal/sftpclient.Client.Commit` intenta `PosixRename`
(`posix-rename@openssh.com`) si el servidor remoto la anuncia; si no,
recurre a un `Stat` + `Rename` con verificación previa de existencia (no
atómico si además se permite sobrescritura — ver
`ADR/0005-estrategia-subida-atomica.md` para el riesgo residual aceptado
y por qué).

## Backpressure

No hay una cola ni un buffer propio: el flujo se apoya en TCP backpressure
natural a través de la composición `io.Copy` + `pkg/sftp`. Un lector lento
en el lado remoto (SFTP) hace que la lectura del canal de datos FTP se
bloquee, sin acumular datos sin límite en memoria.

## Límites de conexión

- `server.maxConnections`: aplicado en `Gateway.ClientConnected`
  (`internal/ftpserver/gateway.go`) — no es un campo nativo de
  `ftpserverlib.Settings`, así que el gateway lo cuenta explícitamente.
- `users[].maxConcurrentTransfers`: aplicado por
  `authorization.ConcurrencyGate` (semáforo no bloqueante: un intento por
  encima del límite recibe un error inmediato, nunca se queda esperando).

## Correlación de auditoría

Cada evento de auditoría (`Gateway.recordTransfer` y `clientDriver.audit`)
incluye `sessionId`, `transferId`, `ftpUser`, `clientIp`, `command`,
`virtualPath`, `remotePath`, `bytes`, `phase` y, si está habilitado,
`sha256`. `sessionId` es además el sufijo del nombre temporal RF-007, así
que un archivo `.part-<sessionId>` en el servidor remoto siempre es
rastreable hasta la línea de log exacta que lo creó.

## Topología de despliegue

Ver `docs/deployment/deployment-model.md` — deliberadamente no asumida
aquí, por instrucción explícita de `FTP2SFTP-REQUIREMENTS.md` §2.4
("Claude debe inspeccionar la arquitectura real antes de asumir nombres
de contenedores, redes, dominios o puertos").

## Modelo de amenaza (resumen)

Ver `docs/security/security-model.md` para el modelo completo. En una
línea: el canal FTP es texto plano y su única protección es de red; el
canal SFTP está cifrado y autenticado pero el gateway no delega en el
remoto ninguna garantía de confinamiento de rutas propia.
