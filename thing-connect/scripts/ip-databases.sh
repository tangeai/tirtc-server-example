#!/usr/bin/env bash
# Only the named offline datasets belong in a release; never copy local/.
publish_ip_databases() (
    set -eu
    local source="$1" target="$2" name stage old found=0
    for name in qqwry.dat GeoLite2-City.mmdb GeoLite2-ASN.mmdb; do
        if [ -e "$source/$name" ] || [ -L "$source/$name" ]; then
            [ -f "$source/$name" ] && [ ! -L "$source/$name" ] || return 1
            found=1
        fi
    done
    [ "$found" -eq 1 ] || exit 0
    [ ! -L "$source" ] && [ ! -L "$target" ] || return 1
    mkdir -p -- "$(dirname "$target")" || return 1
    stage="$(mktemp -d "${target}.new.XXXXXX")" || return 1
    old="${stage}.old"
    trap 'rm -rf -- "$stage"; if [ -d "$old" ] && [ ! -e "$target" ]; then mv -- "$old" "$target"; fi' EXIT
    # A partial dataset refresh preserves other installed databases.
    for name in qqwry.dat GeoLite2-City.mmdb GeoLite2-ASN.mmdb; do
        if [ -f "$source/$name" ]; then
            cp -- "$source/$name" "$stage/$name" || return 1
        elif [ -f "$target/$name" ]; then
            cp -- "$target/$name" "$stage/$name" || return 1
        fi
    done
    chmod 0750 "$stage" || return 1
    find "$stage" -type f -exec chmod 0640 {} + || return 1
    [ ! -e "$target" ] || mv -- "$target" "$old" || return 1
    mv -- "$stage" "$target" || return 1
    rm -rf -- "$old"
)
