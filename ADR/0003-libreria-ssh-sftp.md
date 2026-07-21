# ADR-0003: `golang.org/x/crypto/ssh` + `pkg/sftp` para el lado SSH/SFTP

- Status: accepted
- Date: 2026-07-20

## Context

`FTP2SFTP-REQUIREMENTS.md` §18 y `CLAUDE.md` prohíben implementar SSH o
criptografía desde cero para producción. Se necesita una implementación
SSH cliente madura con verificación de host key y una capa SFTP sobre
ella.

## Decision

- `golang.org/x/crypto/ssh` para el transporte SSH (mantenido por el
  equipo de Go, es la base de facto del ecosistema Go para SSH).
- `github.com/pkg/sftp` para el protocolo SFTP sobre ese transporte
  (biblioteca más usada y madura del ecosistema Go para SFTP, con
  soporte cliente y servidor, usada aquí solo como cliente en producción
  y como servidor únicamente en las pruebas de integración —
  `tests/testutil`).
- `golang.org/x/crypto/ssh/knownhosts` para la verificación de host key
  contra un archivo `known_hosts` real, sin implementación propia.

## Alternatives considered

- **Invocar el binario `ssh`/`sftp` del sistema vía `exec.Command`**:
  evita reimplementar el protocolo, pero introduce una dependencia de
  entorno frágil (versión de OpenSSH del sistema, parsing de su salida
  para errores), y complica el streaming controlado que el proyecto
  necesita para `STOR`/`RETR`. Descartado.
- **Implementación SSH propia**: descartado explícitamente por política
  del proyecto (`CLAUDE.md`, `FTP2SFTP-REQUIREMENTS.md` §18).

## Consequences

- `*sftp.File` implementa `io.Reader`/`Writer`/`Seeker`/`Closer` y
  además `io.ReaderFrom`/`WriterTo`, lo que permite que `io.Copy` (usado
  internamente por `ftpserverlib` para mover bytes entre el canal de
  datos FTP y el archivo remoto) evite materializar el archivo completo
  en memoria sin código adicional propio — ver
  `docs/protocols/ftp-sftp-gateway.md#streaming-vs-archivos-temporales`.
- El código de rename atómico depende de si el servidor remoto anuncia la
  extensión `posix-rename@openssh.com`; no es universal en SFTPv3 — ver
  `ADR/0005-estrategia-subida-atomica.md`.

## Security consequences

Verificación de host key es obligatoria y no configurable para
desactivarse — ver `ADR/0006-verificacion-host-key.md`. No se restringen
explícitamente los algoritmos SSH más allá de los valores por defecto de
la biblioteca (endurecimiento pendiente para producción,
`FTP2SFTP-REQUIREMENTS.md` §20).

## Operational consequences

Ambas dependencias están fijadas por versión exacta en `go.sum`. Sin
CGO: la resolución de nombres DNS y la criptografía son 100% Go,
compatibles con la imagen `distroless` estática.

## Follow-up actions

Evaluar restricción explícita de `HostKeyAlgorithms`/`KeyExchanges` en
`ssh.ClientConfig` antes de producción, si una revisión de seguridad lo
exige.
