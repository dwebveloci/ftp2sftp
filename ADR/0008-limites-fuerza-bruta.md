# ADR-0008: Umbrales de fuerza bruta fijos en código, no en configuración

- Status: accepted
- Date: 2026-07-20

## Context

RF-002 exige límite o retraso progresivo ante intentos de autenticación
fallidos. `FTP2SFTP-REQUIREMENTS.md` §13 (ejemplo de configuración) no
incluye campos para ajustar estos umbrales, y `CLAUDE.md` desaconseja
añadir superficie de configuración sin necesidad demostrada.

## Decision

`internal/ftpserver/gateway.go` define constantes (`authMaxFailures = 5`,
`authWindow = 1m`, `authLockout = 10s`, con backoff progresivo hasta 32×
en `internal/auth.Limiter`) en vez de exponerlas en el YAML de
configuración. Dos limitadores independientes: por IP de origen y por
nombre de usuario (`internal/auth.NewAuthenticator`), ambos deben
permitir el intento.

## Alternatives considered

- **Exponer los umbrales en `config.yaml`**: se descartó para el MVP por
  falta de necesidad demostrada — ningún dato operativo real justifica un
  valor distinto a los elegidos. Añadir el campo ahora sería
  configuración especulativa.

## Consequences

Cambiar estos umbrales requiere recompilar, no solo reconfigurar. Se
considera aceptable para el MVP; ver seguimiento abajo.

## Security consequences

Valores conservadores (5 fallos antes de bloqueo, ventana de 1 minuto)
equilibran mitigar fuerza bruta sin bloquear fácilmente a un usuario
legítimo con una contraseña mal tecleada dos o tres veces.

## Operational consequences

Un operador que necesite ajustar estos valores en producción debe
modificar el código y desplegar una nueva versión — fricción operativa
aceptada deliberadamente frente a la alternativa de superficie de
configuración no solicitada.

## Follow-up actions

Si la operación real revela que estos umbrales necesitan ajuste frecuente
(falsos positivos bloqueando a AX tras fallos transitorios de red, por
ejemplo), promoverlos a campos de configuración validados en
`internal/config`.
