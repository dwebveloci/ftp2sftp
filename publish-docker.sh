#!/usr/bin/env bash

# ==============================================================
# publish-docker.sh
# Build y push de imagen Docker a Docker Hub.
# Copia este archivo al repo y edita solo la seccion de abajo.
# ==============================================================

set -euo pipefail

# ---------------------------------------------------------------
# CONFIGURACION
# ---------------------------------------------------------------
IMAGE_NAME="adminveloci/ftp2sftp"   # ej. adminveloci/wes_scan_back
NPM_BUILD_SCRIPT=""               # script en package.json (o deja vacio para omitir npm)
PROJECT_NAME="FTP2SFTP"             # nombre para los logs
# ---------------------------------------------------------------

# Colores
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; BLUE='\033[0;34m'; DIM='\033[2m'; BOLD='\033[1m'; RESET='\033[0m'

print_divider() {
    printf '%b\n' "${DIM}============================================================${RESET}"
}

print_header() {
    echo
    print_divider
    printf '%b\n' "${BOLD}${CYAN}🚀 PUBLICACION DOCKER${RESET}"
    printf '%b\n' "${BOLD}${PROJECT_NAME}${RESET}"
    print_divider
    echo
}

print_summary() {
    printf '%b\n' "${BOLD}📋 Resumen de publicacion${RESET}"
    printf '   %-10s %s\n' "Proyecto :" "$PROJECT_NAME"
    printf '   %-10s %s\n' "Imagen   :" "$IMAGE_NAME"
    printf '   %-10s %s\n' "Version  :" "$VERSION"
    if [[ -n "$NPM_BUILD_SCRIPT" ]]; then
        printf '   %-10s %s\n' "Build    :" "npm run $NPM_BUILD_SCRIPT"
    else
        printf '   %-10s %s\n' "Build    :" "omitido"
    fi
    printf '   %-10s %s\n' "Tags     :" "${VERSION}, latest"
    echo
}

print_step() {
    local step="$1"
    local total="$2"
    local icon="$3"
    local title="$4"

    echo
    printf '%b\n' "${BLUE}[${step}/${total}]${RESET} ${BOLD}${icon} ${title}${RESET}"
}

print_command() {
    printf '%b\n' "   ${DIM}Comando:${RESET} ${YELLOW}$1${RESET}"
}

ok()   { printf '%b\n' "${GREEN}✅ $*${RESET}"; }
warn() { printf '%b\n' "${YELLOW}⚠️  $*${RESET}"; }
fail() { printf '%b\n' "${RED}❌ ERROR:${RESET} $*" >&2; }

# ---------- validaciones ----------
require_cmd() {
    command -v "$1" >/dev/null 2>&1 || { fail "Comando no encontrado: $1"; exit 1; }
}

require_cmd docker

[[ -f "Dockerfile" ]]   || { fail "No se encontro Dockerfile. Ejecuta el script desde la raiz del repo."; exit 1; }

# ---------- version ----------
while true; do
    echo
    read -r -p "🏷️  Version a publicar: " VERSION
    VERSION="${VERSION// /}"
    [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.][0-9]+)?$ ]] && break
    warn "Formato invalido. Usa X.Y.Z o X.Y.Z.W"
done

TOTAL_STEPS=3
[[ -n "$NPM_BUILD_SCRIPT" ]] && TOTAL_STEPS=4
CURRENT_STEP=1

print_header
print_summary

# ---------- npm build (opcional) ----------
if [[ -n "$NPM_BUILD_SCRIPT" ]]; then
    require_cmd npm
    print_step "$CURRENT_STEP" "$TOTAL_STEPS" "🧪" "Build de la aplicacion"
    print_command "npm run $NPM_BUILD_SCRIPT"
    export CI=true
    npm run "$NPM_BUILD_SCRIPT"
    ok "npm build completado"
    ((CURRENT_STEP++))
fi

# ---------- docker build ----------
print_step "$CURRENT_STEP" "$TOTAL_STEPS" "📦" "Build de la imagen Docker"
print_command "docker build --no-cache -t ${IMAGE_NAME}:${VERSION} -t ${IMAGE_NAME}:latest ."
docker build --no-cache -t "${IMAGE_NAME}:${VERSION}" -t "${IMAGE_NAME}:latest" .
ok "docker build completado"
((CURRENT_STEP++))

# ---------- docker push ----------
print_step "$CURRENT_STEP" "$TOTAL_STEPS" "⬆️" "Push de la version ${VERSION}"
print_command "docker push ${IMAGE_NAME}:${VERSION}"
docker push "${IMAGE_NAME}:${VERSION}"
ok "push ${VERSION} completado"
((CURRENT_STEP++))

print_step "$CURRENT_STEP" "$TOTAL_STEPS" "⬆️" "Push de la etiqueta latest"
print_command "docker push ${IMAGE_NAME}:latest"
docker push "${IMAGE_NAME}:latest"
ok "push latest completado"

echo
print_divider
ok "Publicacion de $PROJECT_NAME ($VERSION) completada."
print_divider
