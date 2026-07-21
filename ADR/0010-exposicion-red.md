# ADR-0010: El listener FTP nunca se expone públicamente sin decisión explícita

- Status: accepted
- Date: 2026-07-20

## Context

FTP transmite credenciales y contenido sin cifrar. `FTP2SFTP-REQUIREMENTS.md`
§9.5 exige que el listener se limite a red interna o túnel controlado, y
que no se exponga públicamente "sin una decisión de riesgo explícita".
`CLAUDE.md` refuerza: no exponer un puerto públicamente cuando una red
privada o un túnel puede satisfacer el requisito de forma más segura.

## Decision

`ftp2sftp` no incluye ningún mecanismo de cifrado de canal FTP (FTPS está
fuera de alcance del MVP, `FTP2SFTP-REQUIREMENTS.md` §4). La mitigación
es exclusivamente de red: el `docker-compose.yml` de desarrollo publica
el puerto FTP solo en loopback/red local; la documentación de despliegue
(`docs/deployment/deployment-model.md`) describe la topología esperada
en producción (red privada o túnel Cloudflare) como requisito, no como
opción.

## Alternatives considered

- **Implementar FTPS en el MVP**: evaluado y descartado — fuera de
  alcance explícito (`FTP2SFTP-REQUIREMENTS.md` §4), y
  `FTP2SFTP-REQUIREMENTS.md` §9.5 lo deja como algo a "evaluar" a futuro,
  no a implementar ahora.
- **Exponer el puerto FTP públicamente con controles compensatorios
  (rate limiting, fail2ban externo)**: no es equivalente a no exponerlo;
  seguiría permitiendo interceptación de credenciales/contenido en
  tránsito por cualquier observador de red entre el cliente real y el
  gateway si esa ruta pasa por Internet. Rechazado como opción por
  defecto.

## Consequences

Cualquier despliegue que exponga el puerto FTP más allá de una red
privada o túnel controlado está desviándose deliberadamente de este ADR
y debe documentar esa decisión de riesgo explícitamente, como pide
`FTP2SFTP-REQUIREMENTS.md` §9.5 — este proyecto no ofrece un modo
"seguro para exposición pública" del listener FTP.

## Security consequences

Sin esta restricción de red, todo el modelo de autenticación (bcrypt,
rate limiting) protege contra adivinar credenciales pero no contra
interceptarlas en tránsito — la exposición de red es, por diseño, la
mitigación primaria contra ese riesgo específico, no una capa opcional.

## Operational consequences

Ver `docs/deployment/deployment-model.md#redes` para las restricciones
concretas de túnel (por qué un túnel TCP simple al puerto de control no
basta para el canal de datos pasivo).

## Follow-up actions

Si en el futuro se requiere exposición fuera de una red controlada,
revisar primero soporte FTPS (`Settings.TLSRequired` de `ftpserverlib`
ya lo soporta a nivel de librería, sin implementación propia necesaria)
antes de considerar exponer FTP en texto plano.
