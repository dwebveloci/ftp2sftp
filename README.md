# ftp2sftp

Gateway de aplicación en Go que expone un servidor FTP compatible con
Microsoft Dynamics AX 2012 y traduce sus operaciones a SFTP sobre SSH hacia
un servidor remoto. No es un proxy TCP transparente, ni un cliente FTP, ni
un servidor SFTP: traduce operaciones (autenticación, rutas, subida,
listado, descarga, borrado, renombrado), no bytes.

Lee primero `FTP2SFTP-REQUIREMENTS.md` (fuente de verdad de alcance,
seguridad y criterios de aceptación) y `docs/architecture/discovery.md`
(descubrimiento técnico previo al diseño).

## Estado

MVP funcional: compila, pasa pruebas unitarias, de integración y de
protocolo FTP extremo a extremo (ver `docs/testing/testing-strategy.md`).
**La compatibilidad con AX 2012 real está pendiente de validación** — ver
[Validación con AX 2012 real](#validación-con-ax-2012-real).

## Arquitectura en una línea

```
AX 2012 --FTP (texto plano)--> ftp2sftp --SFTP/SSH (cifrado)--> servidor remoto
```

`ftp2sftp` es un monolito modular en Go. Cada paquete bajo `internal/`
tiene una responsabilidad única (ver `docs/architecture/overview.md` y
`docs/architecture/boundaries.md` para el detalle y los diagramas).

## Requisitos

- Go 1.25+ (fijado en `go.mod`; `go build`/`go test` descargan el
  toolchain exacto automáticamente si no está instalado).
- Docker y Docker Compose, solo para el entorno de desarrollo o el
  despliegue en contenedor.

## Desarrollo local (sin Docker)

```bash
go build ./...
go vet ./...
gofmt -l .            # debe no imprimir nada
go test ./...
go test -race ./...
```

No se requiere una base de datos ni un servicio externo para las pruebas:
las pruebas de integración y de protocolo levantan un servidor SSH/SFTP
real dentro del propio proceso de prueba (`tests/testutil`), no un mock —
ver `docs/testing/testing-strategy.md`.

Para ejecutar el binario localmente necesitas un archivo de configuración
válido y una llave privada/`known_hosts` reales hacia algún servidor SFTP
alcanzable. La forma más simple de tener todo eso listo es el flujo Docker
de abajo.

## Ejecutar con Docker (entorno de desarrollo)

Levanta `ftp2sftp` junto a un servidor SFTP desechable
(`atmoz/sftp`), solo para desarrollo. Requiere una preparación única de
llaves — ningún secreto se versiona en git.

```bash
# preparación única: genera llave SSH dev-only + known_hosts real
# (instrucciones completas en deploy/docker/README.md)
cat deploy/docker/README.md

docker compose up --build
```

- FTP: `127.0.0.1:2121` (usuario `ax2012`, contraseña `dev-only-password`
  — ver `configs/config.dev.yaml`).
- Health/readiness/metrics: `127.0.0.1:8080/healthz`, `/readyz`,
  `/metrics`.

Ver `docs/deployment/deployment-model.md` para la topología de red real
esperada en producción (Cloudflare Tunnel, red privada, límites de FTP
sobre túneles TCP) — deliberadamente distinta de este compose de
desarrollo.

### Probar contra un SFTP remoto real (`docker-compose.preprod.yml`)

Para verificar el gateway contra un servidor SFTP real (no el desechable
de arriba) antes de desplegar, usa `docker-compose.preprod.yml`. Requiere
una carpeta local `pre-prod/` (nunca versionada — ver `.gitignore`) con tu
propio `config.yml` y un `known_hosts.remote` capturado del host real:

```bash
docker compose -f docker-compose.preprod.yml up --build
```

`server.passivePortStart/End` y `controlPort` en `pre-prod/config.yml`
deben coincidir con los puertos publicados en ese archivo.

## Configuración

`configs/config.example.yaml` documenta cada campo. El servicio valida la
configuración al arrancar y **no inicia** si falta algo obligatorio, el
rango pasivo es insuficiente, `known_hosts` no existe, hay usuarios
duplicados o un hash de contraseña no es bcrypt válido (`internal/config`).

Puntos que no son obvios del YAML:

- `sftp.privateKeyFile` y `sftp.password` son mutuamente excluyentes; se
  prefiere la llave (`FTP2SFTP-REQUIREMENTS.md` §9.2).
- `server.passivePortEnd - passivePortStart` debe ser mayor o igual que
  `server.maxConnections`, o la validación falla explícitamente en vez de
  degradar en producción de forma confusa.
- `users[].passwordHash` debe ser un hash bcrypt (nunca texto plano ni
  cifrado reversible).

## Herramientas de operador (CLI)

El mismo binario incluye subcomandos para preparar y verificar una
configuración sin arrancar el listener FTP (RF-018). Reutilizan
exactamente el mismo código de validación/conexión que usa el gateway al
arrancar, así que un resultado en verde aquí es una garantía real, no una
segunda opinión que pueda desalinearse.

```bash
# Valida un archivo de configuración (mismo chequeo que hace el arranque real).
ftp2sftp config validate -config configs/config.dev.yaml

# Genera un hash bcrypt para users[].passwordHash. Pide la contraseña por
# terminal sin eco (dos veces, para detectar errores de tecleo); nunca la
# acepta como argumento, para no dejarla en el historial de shell ni en
# la lista de procesos.
ftp2sftp config hash-password

# Prueba la conexión SSH/SFTP real (incluye verificación de host key) hacia
# el destino de cada usuario configurado, o de uno solo con -user.
ftp2sftp config check-connectivity -config configs/config.dev.yaml -user ax2012
```

Estos subcomandos son utilidades de preparación/diagnóstico local para
quien despliega el gateway: no abren ningún puerto de red propio y no
requieren el proceso principal en ejecución. No son una API ni un panel
de administración — ese tipo de superficie está deliberadamente fuera de
alcance (`FTP2SFTP-REQUIREMENTS.md` §4).

## Comandos FTP soportados

`USER PASS SYST FEAT PWD CWD CDUP TYPE PASV EPSV LIST NLST SIZE MDTM STOR
RETR MKD RMD DELE RNFR RNTO NOOP QUIT`. Modo activo (`PORT`) está
deshabilitado en el MVP — ver `docs/protocols/ftp-behavior.md` para el
porqué y la vía de extensión. `APPE`, `SITE`, `MLSD/MLST`, `STAT` y el
comando no estándar `MKDIR` están deshabilitados o rechazados
deliberadamente (fuera del alcance de RF-004).

## Validación con AX 2012 real

**No se afirma compatibilidad completa con AX 2012**: este entorno no tuvo
acceso a una instancia real de AX 2012. Lo que sí se validó:

- Comportamiento FTP estándar (login, PASV, LIST, STOR, RETR, rename,
  múltiples sesiones, credenciales inválidas, traversal) contra un cliente
  FTP real e independiente (`github.com/jlaffaye/ftp`) — ver
  `tests/protocol/`.
- Formato de host key y verificación SSH contra un servidor SFTP real —
  ver `tests/integration/`.

Antes de declarar compatibilidad con AX 2012, sigue la lista de
validación manual en `docs/protocols/ftp-behavior.md#validación-manual-con-ax-2012`,
que incluye cómo capturar de forma segura (sin loguear credenciales) la
secuencia real de comandos que AX envía.

## Documentación

- `docs/architecture/overview.md`, `docs/architecture/boundaries.md` —
  componentes, límites, flujos.
- `docs/protocols/ftp-behavior.md` — compatibilidad FTP/AX, modo pasivo,
  mapeo de errores.
- `docs/protocols/ftp-sftp-gateway.md` — traducción FTP→SFTP operación por
  operación.
- `docs/security/security-model.md` — modelo de amenazas, autenticación,
  secretos, path traversal.
- `docs/deployment/deployment-model.md` — Docker, redes, Cloudflare
  Tunnel, límites de FTP sobre túneles TCP.
- `docs/testing/testing-strategy.md` — qué se probó y cómo, qué queda
  pendiente.
- `ADR/` — decisiones de arquitectura con contexto, alternativas y
  consecuencias.

## Limitaciones conocidas del MVP

- Modo activo (`PORT`) no implementado.
- `APPE` (upload resume) no soportado; `RETR` con `REST` (download resume)
  sí.
- Un solo proceso, sin alta disponibilidad activa-activa (fuera de
  alcance del MVP, `FTP2SFTP-REQUIREMENTS.md` §4).
- Sobrescritura no atómica cuando el servidor remoto no soporta la
  extensión `posix-rename@openssh.com` (ver ADR de subida atómica).
- Auditoría solo vía logs estructurados a stdout; no hay almacén de
  auditoría propio.

Riesgos residuales y decisiones pendientes completas en
`docs/architecture/discovery.md` §10 y `docs/testing/testing-strategy.md`.

## Licencia

MIT — ver `LICENSE`.
