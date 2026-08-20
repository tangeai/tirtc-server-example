#!/usr/bin/env bash

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT_DIR"

ALL_SERVICES=(device-server user-server voip-server ai-server call-server admin-server)
if [ "$#" -eq 0 ]; then
    SERVICES=("${ALL_SERVICES[@]}")
else
    SERVICES=("$@")
fi

is_service() {
    local candidate="$1" known
    for known in "${ALL_SERVICES[@]}"; do
        [ "$candidate" = "$known" ] && return 0
    done
    return 1
}

contains_service() {
    local wanted="$1" candidate
    for candidate in "${SERVICES[@]}"; do
        [ "$candidate" = "$wanted" ] && return 0
    done
    return 1
}

for service in "${SERVICES[@]}"; do
    if ! is_service "$service"; then
        echo "[ERROR] unknown service: $service" >&2
        exit 1
    fi
done

if contains_service user-server; then
    npm ci
    npm run build:css
fi

if contains_service admin-server; then
    npm --prefix admin/admin-web ci
    npm --prefix admin/admin-web run build
fi

version="${BUILD_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || printf 'development')}"
commit="${BUILD_COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || printf 'unknown')}"
ldflags="-s -w -X thing-connect/internal/servicestatus.BuildVersion=$version -X thing-connect/internal/servicestatus.BuildCommit=$commit"

mkdir -p bin
for service in "${SERVICES[@]}"; do
    package="./$service"
    [ "$service" != "admin-server" ] || package="./admin/admin-server"
    echo "[INFO] building $service ($version, $commit)"
    CGO_ENABLED=0 go build -trimpath -ldflags "$ldflags" -o "bin/$service" "$package"
done

echo "[INFO] build artifacts: $ROOT_DIR/bin"
