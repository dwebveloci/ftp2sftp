# Security Model

## Activos

- Contenido de los archivos transferidos (facturas/documentos del
  negocio).
- Credenciales FTP (hash bcrypt en configuración).
- Llave privada SSH/SFTP (o contraseña, si el entorno lo exige).
- `known_hosts` (integridad de la identidad del servidor remoto).
- Disponibilidad del propio gateway (único punto de tránsito entre AX y
  el remoto).

## Actores

| Actor | Confianza |
|---|---|
| Cliente FTP (AX 2012) | No confiable: cualquier entrada se valida (rutas, tamaños, secuencias de comandos) |
| Operador que despliega/configura | Confiable, pero sus errores de configuración se mitigan con validación estricta al arranque (`internal/config`) |
| Servidor SFTP remoto | Confiable pero no controlado por el gateway: el gateway no delega en su chroot ninguna garantía propia |
| Atacante en la red del cliente FTP | Ve texto plano (credenciales FTP y contenido) si la red no está segmentada — ver [Exposición de FTP](#exposición-de-ftp-en-texto-plano) |

## Límites de confianza

Ver `docs/architecture/boundaries.md#límites-de-confianza` para el
diagrama. Resumen: FTP sin cifrar entre AX y el gateway; SFTP cifrado y
autenticado entre el gateway y el remoto; sin límite de red interno
dentro del proceso.

## Autenticación

### FTP (entrante)

- Usuario + contraseña verificados contra un hash **bcrypt** estático en
  configuración (`internal/auth`, `golang.org/x/crypto/bcrypt`). Nunca
  texto plano, nunca cifrado reversible.
- Mismo mensaje de error (`internal/auth.ErrInvalidCredentials`) para
  usuario inexistente y contraseña incorrecta — impide enumeración
  evidente de usuarios (RF-002).
- Comparación bcrypt se ejecuta incluso para usuarios inexistentes
  (contra un hash señuelo precalculado) para reducir la señal de tiempo
  más obvia entre "usuario no existe" y "usuario existe, contraseña
  incorrecta". No es tiempo verdaderamente constante (hay jitter de red y
  de mapa), pero elimina el atajo más barato.
- Fuerza bruta: `internal/auth.Limiter`, dos límites independientes (por
  IP y por usuario), bloqueo progresivo (multiplicador de hasta 32× el
  período base tras fallos repetidos). Umbrales fijos en
  `internal/ftpserver/gateway.go` (`authMaxFailures`, `authWindow`,
  `authLockout`) — no expuestos en configuración deliberadamente (ver
  `ADR/0008-limites-fuerza-bruta.md`).

### SSH/SFTP (saliente)

- Llave privada (preferida) o contraseña — nunca ambas a la vez
  (`internal/config/validate.go`).
- **Verificación de host key obligatoria, sin excepción, en todo
  entorno** (incluido desarrollo — ver `deploy/docker/README.md`, que
  documenta capturar el host key real del contenedor de prueba en vez de
  aceptarlo a ciegas). `internal/sshclient.Dial` construye el
  `HostKeyCallback` desde `known_hosts`; no existe ninguna bandera de
  configuración para desactivarlo.
- Sin restricción adicional de algoritmos SSH más allá de los valores por
  defecto de `golang.org/x/crypto/ssh` (razonablemente modernos, pero
  incluyen compatibilidad hacia atrás como `ssh-rsa`). Endurecimiento de
  algoritmos queda como tarea de producción — ver
  `FTP2SFTP-REQUIREMENTS.md` §20.

## Autorización

`internal/authorization.Policy`, por usuario, valor cero = deniega todo
(`AllowUpload`, `AllowDownload`, `AllowDelete`, `AllowMkdir`,
`AllowRename`, `AllowOverwrite`, `MaxFileSize`). Por defecto en el
ejemplo de configuración: solo `allowUpload` activo, el resto en `false`
(coincide con RF-011: borrado deshabilitado por defecto).

Límite de concurrencia por usuario (`authorization.ConcurrencyGate`): un
semáforo no bloqueante — una transferencia por encima del límite recibe
un error inmediato, nunca queda esperando indefinidamente (evita
agotamiento de recursos por acumulación de conexiones en espera).

## Gestión de secretos

| Secreto | Cómo se provee | Cómo NO se provee |
|---|---|---|
| Hash de contraseña FTP | En el YAML de configuración montado (es un hash, no un secreto reversible) | Nunca texto plano |
| Llave privada SFTP | Archivo montado de solo lectura (`sftp.privateKeyFile`, típicamente `/run/secrets/...`) | Nunca en variables de entorno, nunca en la imagen Docker |
| Contraseña SFTP (si se usa) | Archivo de configuración montado, no versionado | Nunca en variables de entorno documentadas en `.env.example` |
| `known_hosts` | Archivo montado, generado una vez por el operador contra el host real | Nunca "aceptar cualquiera" |

`.env.example` solo documenta `CONFIG_FILE` (una ruta, no un secreto).
Ningún archivo de este repositorio contiene una credencial funcional;
`configs/config.example.yaml` y `configs/config.dev.yaml` usan hashes
bcrypt de contraseñas de ejemplo explícitamente marcadas como tales.

## Path traversal

Defensa en dos capas independientes:

1. **`internal/filesystem.Mapper`** (sin red): normaliza con `path.Clean`
   sobre una ruta siempre anclada a `/`, lo que matemáticamente no puede
   producir una ruta no absoluta; luego verifica explícitamente que el
   resultado esté dentro de `virtualRoot` y, tras mapear, dentro de
   `remoteRoot`. Cubre rutas con `..`, rutas absolutas fuera de raíz y
   bytes NUL.
2. **`internal/sftpclient.Client.checkNoEscapingSymlink`** (con red):
   antes de operar sobre una ruta existente, hace `Lstat`; si es un
   symlink, resuelve el destino (`ReadLink`) y verifica que siga dentro
   de `remoteRoot`.

**Limitación conocida y aceptada**: `checkNoEscapingSymlink` solo revisa
el nodo hoja, no cada componente ancestro de la ruta (caminar cada
directorio padre con `Lstat` en cada operación tendría un costo de red
por segmento no justificado para el MVP). Un symlink colocado en un
directorio ancestro dentro de la raíz remota, apuntando fuera de ella, no
se detecta con esta capa sola. La mitigación primaria sigue siendo la
exigida por `FTP2SFTP-REQUIREMENTS.md` §2.3: el usuario SFTP remoto debe
estar restringido exclusivamente a la ruta autorizada del lado del
servidor (chroot o equivalente) — el gateway es defensa en profundidad,
no el único control.

Cobertura de prueba: `internal/filesystem/mapper_test.go`
(`TestResolveVirtualNeverEscapesRoot` con ataques `../../../etc/passwd`
explícitos) y `tests/protocol/ftp_e2e_test.go`
(`TestPathTraversalIsRejected`, extremo a extremo vía un cliente FTP
real).

## Exposición de FTP en texto plano

FTP transmite credenciales y contenido sin cifrar. Por diseño, el
listener FTP debe limitarse a red privada o a un túnel controlado — ver
`docs/deployment/deployment-model.md`. No hay ninguna mitigación de
protocolo posible dentro de este gateway para ese hecho (FTPS está fuera
de alcance del MVP, `FTP2SFTP-REQUIREMENTS.md` §4).

## Logs y datos sensibles

- Nunca se registra la contraseña FTP, ni siquiera al fallar la
  autenticación.
- Nunca se registra la llave privada SFTP ni la contraseña SFTP.
- **Corrección aplicada durante la implementación**: `ftpserverlib`
  registra a nivel Debug la línea de comando cruda recibida, lo que
  incluiría el argumento de `PASS` en texto plano. Se mitigó con
  `internal/observability.NewProtocolLogger`, que redacta cualquier línea
  `PASS ...` antes de que llegue a cualquier handler de logging,
  independientemente del nivel configurado — ver
  `docs/protocols/ftp-behavior.md#logging-seguro-de-la-secuencia-de-comandos-para-validación-con-ax`.
- Los errores de dominio (`internal/errors.Error`) separan
  deliberadamente el mensaje seguro para el cliente (`Error()`) de la
  causa interna (`Unwrap()`, solo para logs) — ningún error interno de
  SFTP/SSH se reenvía verbatim al cliente FTP.

## Eventos de auditoría

`internal/ftpserver/clientdriver.go:audit` y `Gateway.recordTransfer`
emiten un evento estructurado por operación relevante, con `sessionId`,
`transferId` (transferencias), `ftpUser`, `clientIp`, `command`,
`virtualPath`, `remotePath`, `bytes`, `phase`, y `sha256` si está
habilitado — ver RF-015. No hay almacén de auditoría propio: los eventos
van a stdout como JSON estructurado, delegando persistencia/consulta a la
plataforma de logs del operador (decisión pendiente #17 de
`FTP2SFTP-REQUIREMENTS.md` §17, no resuelta por este proyecto).

## Denegación de servicio

**Corrección aplicada tras pruebas manuales reales**: `ftpserverlib` solo
trata como error del driver el caso en que `GetTLSConfig()` devuelva un
`error` no nulo; si devuelve `(nil, nil)` — la respuesta "obvia" para un
gateway que no soporta FTPS — su manejador de `AUTH` envuelve la conexión
igualmente con `tls.Server(conn, nil)`, y `crypto/tls` no tolera una
config nula del lado servidor: el siguiente intento de handshake provoca
un *nil pointer dereference* que tumba **todo el proceso** (todas las
sesiones activas, no solo la del atacante). `AUTH TLS` es un comando que
varios clientes reales —incluido FileZilla, con su configuración de
cifrado por defecto— envían automáticamente **antes de autenticarse**, así
que esto era una denegación de servicio no autenticada, explotable por
cualquier cliente en la red, detectada durante las pruebas manuales de
este proyecto contra un servidor FTP real. Corregido en
`internal/ftpserver/gateway.go:GetTLSConfig` devolviendo un error explícito
(`errs.KindUnsupportedCommand`), lo que hace que la librería responda con
un código de rechazo normal en vez de intentar la envoltura TLS. Cobertura
de regresión: `tests/protocol/auth_tls_test.go`
(`TestAuthTLSIsRejectedWithoutCrashing`), que reproduce la secuencia
`AUTH TLS` sin autenticarse y confirma tanto el rechazo como que el
proceso sigue sirviendo sesiones normales después.

- `server.maxConnections`: aplicado explícitamente en
  `Gateway.ClientConnected` (no es un campo nativo de
  `ftpserverlib.Settings`).
- `users[].maxConcurrentTransfers`: semáforo no bloqueante por usuario.
- `transfer.maxFileSize` por usuario: aplicado byte a byte durante el
  streaming (`UploadHandle.Write`), no confiando en un tamaño declarado
  por el cliente.
- Timeouts de conexión SSH, de datos FTP y de sesión inactiva, todos
  configurables (`server.idleTimeout`, `dataConnectionTimeout`,
  `sftp.connectTimeout`).
- El limitador de fuerza bruta (`internal/auth.Limiter`) purga entradas
  obsoletas cada 256 llamadas para no crecer sin límite bajo un ataque
  distribuido con muchas IPs distintas.

**Riesgo residual — parcialmente cerrado tras un incidente real**: no hay
un timeout genérico por operación SFTP individual una vez que la conexión
SSH está establecida. `sftp.connectTimeout` cubre el *handshake* inicial
(`internal/sshclient.Dial`), pero `pkg/sftp` v1.13.11 solo expone una
variante consciente de `context.Context` para `ReadDir`
(`ReadDirContext`); `Stat`, `OpenFile`, `Write`, etc. no tienen equivalente
y siguen sin deadline propio.

`ReadDir` (comando `LIST`/`NLST`) sí quedó acotado por
`sftp.readDirTimeout` (por defecto 60s) tras reproducirse en pruebas
manuales contra un servidor remoto real: una carpeta con ~45,000 archivos
provocó que `pkg/sftp` encadenara cientos de round-trips secuenciales de
`SSH_FXP_READDIR`, y el servidor remoto cerró el canal SSH a mitad del
listado, apareciendo como un `EOF` opaco en vez de un error controlado.
Con el timeout, ese mismo escenario ahora falla de forma predecible con
`KindTimeout` y además invalida la conexión SFTP de la sesión
(`clientDriver.maybeInvalidate`), forzando una reconexión limpia en el
siguiente comando en vez de arriesgar una conexión en estado incierto.
Cobertura de regresión:
`tests/integration/sftp_integration_test.go:TestListDirectoryTimesOutCleanly`.

Esto no resuelve el riesgo para el resto de operaciones (`Stat`,
`OpenFile`, escritura/lectura de un archivo): un servidor remoto que
acepta la conexión TCP/SSH pero deja de responder al protocolo SFTP a
mitad de una de esas operaciones puede seguir bloqueándola
indefinidamente. El impacto sigue acotado a la sesión FTP afectada (cada
conexión FTP corre en su propia goroutine; no bloquea a otras sesiones),
pero continúa siendo un vector de agotamiento de recursos si un atacante
controla o compromete el extremo remoto. Envolver el resto de llamadas de
`pkg/sftp` requeriría un mecanismo propio de cancelación (la librería no
lo ofrece para esas operaciones), con el riesgo de introducir sus propios
bugs de cierre a medias del canal SFTP subyacente — no se implementó en
el MVP por ese costo, ahora con evidencia concreta de que `ReadDir` era el
caso con mayor probabilidad real de manifestarse (crece con el número de
archivos en el directorio listado, no con el tamaño de un archivo
individual).

Nota operativa relacionada: apuntar `rootPath` directamente a un
directorio con decenas de miles de archivos hace que `LIST`/`NLST` sea
inherentemente costoso (cientos de round-trips de red) incluso sin fallar
— considerar una subcarpeta más acotada como raíz virtual si el cliente
FTP necesita listar interactivamente, o confirmar que el flujo de
producción real (p. ej. AX 2012 subiendo con `STOR` sin listar antes) no
depende de `LIST` en absoluto.

## Rotación de credenciales

- **Hash de contraseña FTP**: cambiar en el YAML montado y reiniciar (o
  recargar, si se implementa recarga en caliente — no implementada en el
  MVP).
- **Llave privada SFTP**: reemplazar el archivo montado y reiniciar. Las
  sesiones FTP activas seguirán usando la conexión SSH ya establecida
  hasta que se desconecten (por diseño: una sesión SSH por sesión FTP,
  sin reconexión automática de credenciales en caliente).
- **`known_hosts`**: si el servidor remoto rota su host key (evento
  operativo planeado), el operador debe actualizar `known_hosts` *antes*
  del cambio o el gateway rechazará la conexión — comportamiento
  correcto y esperado, no un bug.

## Respuesta a incidentes (alcance del MVP)

No hay mecanismo de revocación de sesión en caliente más allá de
reiniciar el proceso (lo que fuerza `CloseAllConnections` tras el plazo
de graceful shutdown). Un compromiso de la llave privada SFTP requiere:
1. rotarla en el servidor remoto y en la configuración montada,
2. reiniciar el gateway,
3. revisar los eventos de auditoría (`transferId`/`sessionId`) para
   determinar el alcance del acceso indebido.

## Runtime del contenedor

Ver `docs/deployment/deployment-model.md#hardening-del-contenedor` para
usuario no root, imagen distroless, `HEALTHCHECK` sin shell, y por qué el
puerto de control FTP no se expone en `21` dentro del contenedor.
