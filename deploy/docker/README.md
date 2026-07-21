# Entorno de desarrollo con Docker Compose

Este directorio da soporte al `docker-compose.yml` de la raíz del proyecto,
que levanta `ftp2sftp` junto a un servidor SFTP desechable
(`atmoz/sftp`) solo para desarrollo local. No representa la topología de
producción (ver `docs/deployment/deployment-model.md`).

Ningún secreto de este flujo se versiona en git: la llave SSH y el
`known_hosts` reales se generan una sola vez en tu máquina.

## Preparación (una sola vez)

```bash
mkdir -p deploy/docker/dev-keys

# 1. Llave de cliente para el usuario "facturas" del SFTP de pruebas.
ssh-keygen -t ed25519 -f deploy/docker/dev-keys/facturas_id_ed25519 -N "" -C "ftp2sftp-dev"
cp deploy/docker/dev-keys/facturas_id_ed25519.pub deploy/docker/dev-keys/facturas.pub

# 2. Levanta solo el SFTP de pruebas para poder capturar su host key real.
docker compose up -d sftp-test

# 3. Captura el host key real (nunca aceptes uno a ciegas). El contenedor
#    publica el puerto 22 en 127.0.0.1:2222 solo para este paso.
ssh-keyscan -p 2222 -t ed25519 127.0.0.1 \
  | sed 's/^\[127.0.0.1\]:2222/sftp-test/' > deploy/docker/known_hosts

# Verifica que la línea capturada referencia "sftp-test" (el nombre DNS
# interno de la red de docker-compose, que es lo que ftp2sftp usa
# realmente para conectarse) y no "127.0.0.1".
cat deploy/docker/known_hosts
```

## Levantar todo

```bash
docker compose up --build
```

- FTP (control): `127.0.0.1:2121`, usuario `ax2012`, contraseña
  `dev-only-password` (ver `configs/config.dev.yaml`).
- Health/readiness/metrics: `127.0.0.1:8080/healthz`,
  `127.0.0.1:8080/readyz`, `127.0.0.1:8080/metrics`.
- SFTP de pruebas (solo para inspección manual): `127.0.0.1:2222`.

## Probar manualmente

```bash
curl -sf 127.0.0.1:8080/healthz
curl -sf 127.0.0.1:8080/readyz

# Cliente FTP de línea de comandos (modo pasivo obligatorio: ver
# docs/protocols/ftp-behavior.md).
ftp -p 127.0.0.1 2121
```

## Rotar la llave o el host key

Repite el paso 1 (llave) o los pasos 2-3 (host key) y reinicia
`ftp2sftp` con `docker compose restart ftp2sftp`. Un cambio real de host
key en `sftp-test` sin actualizar `deploy/docker/known_hosts` provocará
que `ftp2sftp` rechace la conexión — es el comportamiento correcto y
esperado (RNF-001: está prohibido aceptar cualquier host key).
