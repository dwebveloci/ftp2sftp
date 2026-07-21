# ADR-0006: Verificación de host key SSH sin excepción, en todo entorno

- Status: accepted
- Date: 2026-07-20

## Context

`FTP2SFTP-REQUIREMENTS.md` §9.3 y `CLAUDE.md` prohíben explícitamente
aceptar cualquier host key SSH en producción. La tentación común en
entornos de desarrollo es usar un callback que acepta cualquier host key
("insecure ignore host key") para simplificar pruebas locales.

## Decision

`internal/sshclient.Dial` construye el `HostKeyCallback` exclusivamente
desde `golang.org/x/crypto/ssh/knownhosts.New(cfg.KnownHostsFile)`. No
existe ningún parámetro de configuración, bandera de build, ni variable
de entorno que permita desactivar esta verificación, en ningún entorno,
incluido desarrollo local (ver `deploy/docker/README.md`, que documenta
capturar el host key real del contenedor de prueba con `ssh-keyscan` en
vez de aceptarlo a ciegas).

## Alternatives considered

- **Callback que acepta cualquier host key en modo "dev"**: descartado.
  Introducir ese modo, aunque esté apagado por defecto, crea el riesgo de
  que quede activado accidentalmente en producción (variable de entorno
  mal configurada, copiar-pegar un compose de desarrollo). El costo de
  mantener dos caminos de código no se justifica frente al costo de
  fallar una conexión de desarrollo hasta generar `known_hosts`
  correctamente — un paso de un solo comando.
- **TOFU (trust-on-first-use) automático**: descartado por la misma razón
  que "aceptar cualquiera": abre una ventana de compromiso en la primera
  conexión sin que el operador lo note.

## Consequences

Un cambio de host key del servidor remoto (rotación, migración,
suplantación) siempre rompe la conexión hasta que el operador actualice
`known_hosts` deliberadamente. Este es el comportamiento **correcto y
esperado**, no un defecto — ver
`docs/security/security-model.md#rotación-de-credenciales`.

## Security consequences

Cierra por completo la clase de vulnerabilidad "MITM en la conexión
SFTP saliente". El error se clasifica explícitamente como
`errs.KindHostKeyMismatch`, distinto de un fallo de autenticación
genérico, para que quede claro en logs/auditoría que el host no coincide
con `known_hosts` (`internal/sshclient/dial.go`).

## Operational consequences

El operador debe generar `known_hosts` una sola vez por servidor remoto
(típicamente vía `ssh-keyscan`) antes del primer despliegue, y
actualizarlo de forma coordinada si el servidor remoto rota su host key.

## Follow-up actions

Ninguna: comportamiento final, no hay bandera pendiente de implementar.
