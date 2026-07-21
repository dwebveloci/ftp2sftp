# ADR-0007: Métricas propias en memoria en vez de `prometheus/client_golang`

- Status: accepted
- Date: 2026-07-20

## Context

RNF-006 pide métricas de conexiones, autenticación, transferencias,
errores, duración y bytes, expuestas de forma consultable por una
plataforma de monitoreo. `CLAUDE.md` pide minimizar dependencias
externas sin justificación real, evaluando explícitamente por qué la
biblioteca estándar no basta.

## Decision

Implementar un registro de métricas propio y mínimo
(`internal/observability.Metrics`: `Counter`/`Gauge` basados en
`sync/atomic`) que renderiza a mano el formato de texto de exposición de
Prometheus (`# HELP`/`# TYPE`/valor), en vez de adoptar
`github.com/prometheus/client_golang`.

Las métricas de duración (`*_duration_seconds`) se modelan como una suma
en milisegundos más un contador (patrón `_sum`/`_count` de un resumen,
sin buckets de histograma reales), no como histogramas Prometheus
completos.

## Alternatives considered

- **`prometheus/client_golang`**: es la opción estándar y madura del
  ecosistema, y habría dado histogramas reales con poco esfuerzo. Se
  descartó para el MVP porque trae un árbol de dependencias transitivas
  no trivial para una necesidad que, en su forma mínima (contadores y
  gauges expuestos en formato de texto), no lo requiere. No es una
  decisión permanente: si se necesitan histogramas reales con buckets
  configurables, esta es la primera alternativa a reconsiderar.
- **Sin métricas, solo logs**: no habría cumplido RNF-006 ni la lista
  explícita de métricas sugeridas en `FTP2SFTP-REQUIREMENTS.md` §16.

## Consequences

Cobertura completa de los nombres de métrica sugeridos en
`FTP2SFTP-REQUIREMENTS.md` §16, con semántica de duración simplificada
(promedio derivable de `_sum`/`_count`, sin percentiles).

## Security consequences

Ninguna directa. El endpoint `/metrics` no expone valores sensibles (solo
contadores agregados) — ver `internal/health/server.go`.

## Operational consequences

El endpoint es compatible con cualquier recolector que hable el formato
de texto de Prometheus por scraping HTTP estándar, sin acoplarse a una
plataforma específica (decisión pendiente #17 de
`FTP2SFTP-REQUIREMENTS.md` §17, deliberadamente no resuelta por este
proyecto).

## Follow-up actions

Adoptar `prometheus/client_golang` si se necesitan histogramas reales con
buckets configurables (p. ej. para SLOs de latencia de transferencia).
