# ADR-0001: Go como lenguaje de implementación

- Status: accepted
- Date: 2026-07-20

## Context

`ftp2sftp` es un daemon de red de larga duración: mantiene conexiones FTP
y SSH/SFTP concurrentes, con requisitos explícitos de control de
goroutines, backpressure, timeouts y graceful shutdown
(`FTP2SFTP-REQUIREMENTS.md` §2.2, §10). `CLAUDE.md` permite elegir entre
TypeScript/Node.js, C#/.NET y Go según criterios objetivos, no
preferencia.

## Decision

Go, con despliegue como binario estático en contenedor Docker
(`FTP2SFTP-REQUIREMENTS.md` §2.2 ya lo indicaba como preferencia
explícita del proyecto).

## Alternatives considered

- **TypeScript/Node.js**: fuerte para I/O bound y orquestación HTTP, pero
  el control explícito de ciclo de vida de conexiones TCP/SSH de bajo
  nivel, límites de concurrencia y un binario de despliegue mínimo son
  más naturales en Go. El ecosistema Node para SFTP/SSH es menos maduro
  que `golang.org/x/crypto/ssh` + `pkg/sftp`.
- **.NET**: viable, pero sin ventaja de integración con ecosistema
  Microsoft aquí (el destino es un servidor SFTP genérico, no un sistema
  .NET), y el tamaño de runtime/imagen es mayor que un binario Go
  estático.

## Consequences

- Concurrencia explícita (goroutines, canales, `context.Context`) en vez
  de un modelo basado en promesas/async — coincide con el requisito de
  "propiedad y cierre explícitos" de conexiones y goroutines.
- Compilación estática permite una imagen runtime `distroless` sin
  ningún runtime de lenguaje instalado (ver `ADR` de despliegue en
  `docs/deployment/deployment-model.md`).
- Ecosistema maduro y ampliamente auditado para SSH/SFTP
  (`golang.org/x/crypto`, mantenido por el equipo de Go).

## Security consequences

Menor superficie de ataque en runtime (sin intérprete, sin gestor de
paquetes en la imagen final).

## Operational consequences

Build reproducible vía `go.sum` con versiones fijadas; sin necesidad de
`node_modules` ni de un runtime de VM en producción.

## Follow-up actions

Ninguna.
