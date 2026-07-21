# Architecture Overview

Ver primero `docs/architecture/discovery.md` (descubrimiento previo al
diseño) y `FTP2SFTP-REQUIREMENTS.md` (fuente de verdad de requisitos).

## Propósito de negocio

Permitir que Microsoft Dynamics AX 2012 siga operando por FTP mientras el
servidor destino mantiene SFTP como único mecanismo de transferencia
seguro. `ftp2sftp` traduce operaciones (no bytes) entre ambos protocolos,
aplicando autenticación, autorización, confinamiento de rutas y
commit atómico de subidas.

## Componentes

```mermaid
flowchart LR
    AX["Cliente FTP\n(AX 2012)"] -->|"FTP\ntexto plano"| FS[ftpserver\nftpserverlib driver]
    FS --> SESS[session]
    FS --> AUTHN[auth]
    FS --> AUTHZ[authorization]
    FS --> FSYS[filesystem]
    FS --> XFER[transfer]
    SESS --> SSHC[sshclient]
    XFER --> SFTPC[sftpclient]
    SFTPC --> SSHC
    SSHC -->|"SSH/SFTP\ncifrado"| REMOTE["Servidor SFTP\nremoto"]
    FS --> OBS[observability]
    FS --> HEALTH[health]
    CFG[config] -.valida al inicio.-> FS
```

Cada paquete bajo `internal/` corresponde a un nodo de este diagrama; ver
`docs/architecture/boundaries.md` para responsabilidades exactas y qué NO
posee cada uno.

## Decisión estructural: `ftpserverlib` como motor de protocolo FTP

`internal/ftpserver` no implementa el protocolo FTP (framing de comandos,
PASV/EPSV, códigos de respuesta): usa `github.com/fclairamb/ftpserverlib`
como motor de protocolo, y solo implementa las interfaces `MainDriver` y
`ClientDriver` (= `afero.Fs` + extensiones `GetHandle`/`ReadDir`/
`RemoveDir`). Esto es intencional: reimplementar FTP desde cero es
exactamente el tipo de riesgo de protocolo que `CLAUDE.md` pide evitar
("no implementar un protocolo complejo desde cero sin evaluar costo,
compatibilidad y riesgo"). Ver `ADR/0002-libreria-servidor-ftp.md`.

Consecuencia arquitectónica importante: `ftpserverlib` ya gestiona el
directorio de trabajo (`CWD`/`PWD`/`CDUP`) y el estado pendiente de
`RNFR` por conexión — `internal/session` deliberadamente NO duplica ese
estado (ver el comentario de paquete en `internal/session/session.go`).
Lo que `internal/filesystem.Mapper` sigue validando en cada operación es
el confinamiento a la raíz virtual del usuario, algo que la librería no
conoce.

## Flujo: subida (`STOR`)

```mermaid
sequenceDiagram
    participant AX as Cliente FTP
    participant FS as ftpserver (driver)
    participant AUTHZ as authorization
    participant XFER as transfer
    participant SFTP as sftpclient
    participant REMOTE as SFTP remoto

    AX->>FS: STOR archivo.xml
    FS->>FS: resolver y confinar ruta virtual
    FS->>AUTHZ: CheckUpload() + ConcurrencyGate.TryAcquire()
    FS->>XFER: NewUpload(remotePath)
    XFER->>SFTP: Stat(final) [si !overwrite]
    XFER->>SFTP: CreateTemp(archivo.xml.part-<sessionID>)
    AX->>FS: bytes (canal de datos)
    FS->>XFER: Write(bytes) [io.Copy interno de ftpserverlib]
    AX->>FS: fin de canal de datos
    FS->>XFER: Close()
    XFER->>SFTP: Commit(temp, final, overwrite)
    SFTP->>REMOTE: rename (posix-rename si está disponible)
    XFER-->>FS: Result{Phase: committed, sha256, bytes}
    FS-->>AX: 226 Transfer complete
```

Si algo falla en cualquier paso posterior a `CreateTemp`, `Close()` limpia
el temporal y nunca comete el archivo final (criterio de aceptación MVP
#6/#7). Ver `docs/protocols/ftp-sftp-gateway.md` para el detalle
operación por operación y `ADR/0005-estrategia-subida-atomica.md` para la
estrategia de commit.

## Flujo: login

```mermaid
sequenceDiagram
    participant AX as Cliente FTP
    participant GW as Gateway (MainDriver)
    participant AUTH as auth

    AX->>GW: USER ax2012
    AX->>GW: PASS ******
    GW->>AUTH: Authenticate(clientIP, user, pass)
    AUTH->>AUTH: rate limit (por IP y por usuario)
    AUTH->>AUTH: bcrypt.CompareHashAndPassword
    AUTH-->>GW: ok | ErrInvalidCredentials | ErrRateLimited
    Note over AUTH,GW: mismo mensaje de error para\nusuario inexistente y contraseña\nincorrecta (RF-002: sin enumeración)
    GW->>GW: crear session.Session (conexión SFTP diferida)
    GW-->>AX: 230 Password ok
```

## Flujo: conexión SFTP diferida por sesión

Cada sesión FTP autenticada obtiene su propia conexión SSH/SFTP,
establecida de forma perezosa en la primera operación que la necesita
(`PWD`/`CWD` ya requieren `Stat` remoto) y reutilizada durante el resto de
la sesión — ver `ADR/0004-estrategia-sesion-sftp.md` para la justificación
de "una sesión SSH por sesión FTP" frente a un pool.

```mermaid
sequenceDiagram
    participant SESS as session.Session
    participant SSHC as sshclient
    participant SFTPC as sftpclient
    participant REMOTE as Servidor remoto

    Note over SESS: primera operación que requiere SFTP
    SESS->>SSHC: Dial() [known_hosts, llave/contraseña, timeout]
    SSHC->>REMOTE: handshake SSH + verificación host key
    SSHC-->>SESS: *ssh.Client
    SESS->>SFTPC: New(sshClient, root)
    SFTPC-->>SESS: *sftpclient.Client (cacheado)
    Note over SESS: operaciones siguientes reutilizan\nla misma conexión
    Note over SESS: ClientDisconnected → session.Close()
```

## Flujo: graceful shutdown

```mermaid
sequenceDiagram
    participant OS as SIGTERM
    participant MAIN as main.go
    participant GW as Gateway
    participant FTPSRV as ftpserverlib.Server

    OS->>MAIN: SIGTERM
    MAIN->>GW: PrepareShutdown() [rechaza conexiones nuevas]
    MAIN->>FTPSRV: Stop() [cierra listeners de control y pasivos]
    MAIN->>GW: WaitForSessions(deadline)
    alt sesiones drenan a tiempo
        GW-->>MAIN: listo
    else se agota el plazo
        MAIN->>GW: CloseAllConnections() [cierre forzado]
        GW-->>MAIN: listo
    end
    MAIN->>MAIN: apagar servidor de health
```

Ver `cmd/ftp2sftp/main.go` (`shutdown`) para la implementación exacta y
`FTP2SFTP-REQUIREMENTS.md` §14.4 para el requisito.

## Deuda técnica conocida

- No hay pool de conexiones SSH; si AX abre una conexión de control por
  archivo, el costo de handshake por archivo debe medirse antes de
  optimizar (ver `docs/architecture/discovery.md` §6).
- Las métricas de duración (`*_duration_seconds`) son sumas simples, no
  histogramas reales — ver `ADR/0007-observabilidad.md`.
