#!/usr/bin/env bash

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT_DIR"

# shellcheck source=scripts/service-catalog.sh
source "$ROOT_DIR/scripts/service-catalog.sh"
load_service_catalog "$ROOT_DIR/internal/installer/service_catalog.tsv"
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

# Invalidate the deployable release before any frontend or Go build starts.
# A failed build can therefore never leave an older completeness marker next
# to partially refreshed static assets.
mkdir -p bin
rm -f bin/.release-commit

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

BUILD_STAGE="$(mktemp -d "$ROOT_DIR/.build-stage.XXXXXX")"
cleanup_build_stage() {
    case "$BUILD_STAGE" in
        "$ROOT_DIR"/.build-stage.*) rm -rf -- "$BUILD_STAGE" ;;
        *) echo "[ERROR] refusing to clean unexpected build stage: $BUILD_STAGE" >&2 ;;
    esac
}
trap cleanup_build_stage EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
for service in "${SERVICES[@]}"; do
    package="./$service"
    [ "$service" != "admin-server" ] || package="./admin/admin-server"
    echo "[INFO] building $service ($version, $commit)"
    CGO_ENABLED=0 go build -trimpath -ldflags "$ldflags" -o "$BUILD_STAGE/$service" "$package"
done

# No build artifact becomes deployable until every requested target succeeds.
for service in "${SERVICES[@]}"; do
    mv "$BUILD_STAGE/$service" "bin/$service.tmp"
    mv -f "bin/$service.tmp" "bin/$service"
done

full_build=1
for service in "${ALL_SERVICES[@]}"; do
    contains_service "$service" || full_build=0
done
if [ "$full_build" = "1" ]; then
    full_commit="$(git rev-parse HEAD 2>/dev/null || printf 'unknown')"
    printf '%s\n' "$full_commit" >bin/.release-commit.tmp
    mv -f bin/.release-commit.tmp bin/.release-commit
fi

echo "[INFO] build artifacts: $ROOT_DIR/bin"
