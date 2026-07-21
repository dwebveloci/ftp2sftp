# ADR-0011: Solo modo pasivo (`PASV`/`EPSV`); modo activo deshabilitado

- Status: accepted
- Date: 2026-07-20

## Context

`FTP2SFTP-REQUIREMENTS.md` §17 (#1) deja abierto si AX 2012 usa modo
activo, pasivo o ambos — sin captura real disponible en este entorno. La
instrucción del proyecto es no implementar modo activo salvo que la
arquitectura o pruebas existentes indiquen que AX lo necesita, dejando
documentada la limitación y una vía de extensión.

## Decision

`Settings.DisableActiveMode = true` en `internal/ftpserver/settings.go`.
Solo `PASV`/`EPSV` están disponibles. Ver
`docs/protocols/ftp-behavior.md#modo-pasivo` para la vía de extensión
exacta si una captura real de AX muestra uso de `PORT`.

## Alternatives considered

- **Habilitar ambos modos por si acaso**: descartado — amplía la
  superficie de conexión (el gateway tendría que iniciar conexiones
  salientes hacia el cliente en modo activo) sin evidencia de que se
  necesite, contradiciendo "no implementar sin necesidad de
  compatibilidad" (`FTP2SFTP-REQUIREMENTS.md` §5.3).
- **Solo modo activo**: descartado — modo pasivo es más compatible con
  NAT/túneles salientes desde el cliente, el escenario de red más
  probable para AX 2012 en un entorno corporativo.

## Consequences

Si la validación manual con AX 2012 (`docs/protocols/ftp-behavior.md#validación-manual-con-ax-2012`)
revela que AX requiere `PORT`, este ADR queda superado y debe registrarse
uno nuevo con la decisión de habilitarlo, incluyendo el análisis de
`ActiveTransferPortNon20` (RFC 1579) y el impacto en la topología de red
descrita en `docs/deployment/deployment-model.md` (modo activo requiere
que el gateway pueda iniciar conexiones salientes hacia el cliente, algo
que muchos túneles/NAT no permiten).

## Security consequences

Modo pasivo reduce la superficie: el gateway nunca inicia una conexión
saliente hacia un cliente FTP no confiable.

## Operational consequences

El rango de puertos pasivos (`server.passivePortStart`–`passivePortEnd`)
debe reenviarse completo a través de cualquier túnel/NAT — ver
`docs/deployment/deployment-model.md#limitación-explícita-ftp-sobre-un-túnel-tcp-simple`.

## Follow-up actions

Ejecutar la captura real de AX 2012 (bloqueada por falta de acceso a una
instancia real en este entorno) antes de cerrar la decisión pendiente #1
de `FTP2SFTP-REQUIREMENTS.md` §17.
