# ADR-0009: Autenticación FTP local (bcrypt en configuración estática)

- Status: accepted
- Date: 2026-07-20

## Context

RF-002/§9.1 exigen que la contraseña FTP no se almacene en texto plano,
usando hash seguro o un almacén autorizado. `FTP2SFTP-REQUIREMENTS.md`
§17 deja abierta la pregunta de cuántos usuarios FTP existirán y cómo se
almacenarán las credenciales (decisiones pendientes #8, #15, #16), sin
mandato de usar una base de datos o un directorio externo (LDAP/AD).

## Decision

Usuarios y hashes bcrypt de contraseña en el archivo de configuración
YAML estático (`users[].passwordHash`, `internal/config` +
`internal/auth`), validados al arranque. No hay almacén externo
(base de datos, LDAP) en el MVP.

## Alternatives considered

- **Base de datos de usuarios**: descartada para el MVP — `CLAUDE.md`
  desaconseja infraestructura (una base de datos) sin necesidad
  demostrada, y `FTP2SFTP-REQUIREMENTS.md` §4 excluye explícitamente
  "administración multiempresa avanzada" del alcance inicial. El número
  esperado de usuarios FTP (probablemente uno, la integración con AX) no
  justifica el costo operativo de una base de datos (migraciones,
  backup, conexión adicional).
- **LDAP/AD**: descartado por la misma razón; no hay evidencia de que el
  entorno real disponga de o requiera integración con un directorio
  corporativo para este caso de uso específico.

## Consequences

Agregar o rotar un usuario FTP requiere editar el archivo de
configuración montado y reiniciar (o recargar, si se implementa recarga
en caliente — no implementada). Aceptable para el volumen de usuarios
esperado (RF-014: "la primera versión puede usar configuración estática
segura").

## Security consequences

- bcrypt (`golang.org/x/crypto/bcrypt`), nunca texto plano ni cifrado
  reversible — cumple RF-002/§9.1 directamente.
- `internal/config.Validate` rechaza al arranque cualquier
  `passwordHash` que no sea un hash bcrypt válido
  (`bcrypt.Cost(hash)` debe no fallar), evitando que un operador
  despliegue accidentalmente una contraseña en texto plano.

## Operational consequences

El archivo de configuración con los hashes (no las contraseñas) se monta
de solo lectura; no se versiona en git con hashes reales
(`configs/*.yaml` en este repositorio usan únicamente contraseñas de
ejemplo explícitamente marcadas como tales).

## Follow-up actions

Si el número de usuarios FTP crece significativamente o se requiere
gestión de usuarios sin reiniciar el proceso, reconsiderar un almacén
externo — no antes, sin esa necesidad demostrada.
