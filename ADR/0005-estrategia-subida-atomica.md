# ADR-0005: Nombre temporal remoto + rename para commit de subidas

- Status: accepted
- Date: 2026-07-20

## Context

RF-006/RF-007 exigen que un archivo subido nunca sea visible con su
nombre final mientras está incompleto, y que una transferencia fallida no
deje un archivo final corrupto (criterios de aceptación del MVP #6/#7).
SFTPv3 (la versión más ampliamente soportada) no garantiza que `rename`
sobrescriba un destino existente de forma atómica; esa garantía solo
existe vía la extensión `posix-rename@openssh.com`, no universal.

## Decision

- `STOR` escribe siempre a un nombre temporal remoto
  `<archivo>.part-<sessionId>` (RF-007), nunca directamente al nombre
  final.
- Al completar, `internal/sftpclient.Client.Commit` intenta
  `PosixRename` si el servidor remoto anuncia la extensión (atómico,
  sobrescribe si `overwrite` está habilitado).
- Si el servidor no anuncia la extensión: si `overwrite` está
  deshabilitado y el destino existe, se rechaza con conflicto; si está
  habilitado, se hace `Stat` + `Remove` (si existe) + `Rename` — **no
  atómico** en ese caso, riesgo residual aceptado y documentado.
- Ante cualquier fallo posterior a la creación del temporal, se elimina
  el temporal (`internal/transfer.UploadHandle.Close`), nunca se deja un
  archivo final parcial.

## Alternatives considered

- **Escribir directamente al nombre final**: descartado, viola
  directamente RF-006/RF-007 y el criterio de aceptación #6.
- **Asumir `PosixRename` siempre disponible**: descartado; no es
  universal en SFTPv3 y asumirlo sin verificar habría sido exactamente el
  tipo de suposición no verificada que `docs/architecture/discovery.md`
  §6 identificó como riesgo técnico crítico durante el descubrimiento.
- **Calcular un checksum antes de commitear y comparar**: se implementa
  SHA-256 opcional (`transfer.calculateSha256`) para trazabilidad, pero
  no sustituye la atomicidad del rename — es un complemento, no una
  alternativa (RF-012 lo plantea así explícitamente).

## Consequences

- El nombre temporal incluye el `sessionId` (generado con
  `crypto/rand`, no un contador), evitando colisiones incluso tras un
  reinicio del proceso.
- Un archivo `.part-<sessionId>` nunca aparece en `LIST`/`NLST`
  (`internal/ftpserver/clientdriver.go:ReadDir` los filtra
  explícitamente) — cierra un hueco de seguridad/UX identificado en la
  fase de descubrimiento (visibilidad de temporales en listados).

## Security consequences

El caso no-atómico (remove + rename) tiene una ventana — breve — donde el
archivo final no existe entre el `Remove` y el `Rename`. Un lector
concurrente en esa ventana vería "archivo no encontrado", no un archivo
corrupto ni una mezcla de contenidos — se considera un riesgo aceptable
frente a la alternativa de no permitir overwrite en absoluto.

## Operational consequences

Temporales huérfanos tras una caída del proceso (`SIGKILL`, OOM) no se
limpian automáticamente al reiniciar — decisión pendiente, ver
`docs/architecture/discovery.md` §8 y §17 (#19 de
`FTP2SFTP-REQUIREMENTS.md`). Métrica `temporary_files_pending` existe
para observar esto operativamente, pero no hay barrido automático.

## Follow-up actions

- Verificar contra el servidor SFTP de producción real si anuncia
  `posix-rename@openssh.com` (`internal/sftpclient.Client.HasPosixRename`)
  antes de habilitar `allowOverwrite` en producción.
- Evaluar un job de reconciliación que liste y elimine temporales
  huérfanos más antiguos que un umbral configurable.
