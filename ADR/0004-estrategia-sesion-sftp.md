# ADR-0004: Una sesión SSH/SFTP por sesión FTP, establecida de forma diferida

- Status: accepted
- Date: 2026-07-20

## Context

`FTP2SFTP-REQUIREMENTS.md` §10.2 plantea explícitamente la disyuntiva
entre una sesión SSH aislada por sesión FTP (aislamiento y simplicidad,
mayor costo de handshake) y un pool de conexiones reutilizadas (menor
latencia, mayor complejidad de aislamiento). El propio requisito indica
preferir simplicidad para el MVP salvo evidencia de rendimiento en
contra.

## Decision

Una conexión SSH/SFTP por sesión FTP, establecida **de forma perezosa**
en la primera operación que realmente la necesita (`session.Session.SFTP`),
no en el momento de autenticación FTP, y reutilizada para el resto de la
sesión. Si se detecta un error de conexión, se invalida y cierra
explícitamente (`InvalidateSFTP`), y la siguiente operación reconecta.

## Alternatives considered

- **Pool de conexiones SSH compartido entre sesiones FTP**: descartado
  para el MVP por la complejidad de aislamiento y por no tener evidencia
  de rendimiento que lo justifique (`FTP2SFTP-REQUIREMENTS.md` §10.2 pide
  explícitamente no construir esto sin mediciones).
- **Conexión SSH al autenticar, no diferida**: se descartó porque
  penaliza a toda sesión FTP con el costo de un handshake SSH incluso si
  el cliente solo hace `SYST`/`FEAT`/`QUIT` sin tocar el filesystem
  remoto.

## Consequences

- Si AX abre una conexión de control FTP por archivo (patrón común en
  integraciones por lotes legadas), este modelo implica un handshake SSH
  completo por archivo — riesgo de rendimiento identificado y **no
  medido** en este entorno (no hay AX real disponible). Ver
  `docs/architecture/discovery.md` §6 y `docs/testing/testing-strategy.md`.
- El límite de conexiones SSH concurrentes hacia el remoto queda acotado
  por `server.maxConnections` (conexiones FTP) más
  `users[].maxConcurrentTransfers` como segundo factor indirecto — no hay
  un límite explícito e independiente de conexiones SSH; ver
  `docs/architecture/discovery.md` §8 para el hueco original identificado
  en la fase de descubrimiento.

## Security consequences

Aislamiento fuerte entre sesiones: ninguna sesión FTP puede observar ni
reutilizar accidentalmente el canal SSH de otra.

## Operational consequences

Cierre explícito y garantizado en `ClientDisconnected`
(`internal/ftpserver/gateway.go`), incluyendo el caso de graceful
shutdown forzado (`Gateway.CloseAllConnections`).

## Follow-up actions

Medir el costo real de handshake por archivo contra el patrón de
conexión real de AX 2012 antes de considerar un pool; no implementar un
pool sin esa medición (instrucción explícita de
`FTP2SFTP-REQUIREMENTS.md` §10.2).
