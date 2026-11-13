#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/ip-databases.sh"
work="$(mktemp -d)"
trap 'rm -rf -- "$work"' EXIT
mkdir "$work/source"
printf old > "$work/source/qqwry.dat"
printf private > "$work/source/config.test.yaml"
publish_ip_databases "$work/source" "$work/target"
[ "$(cat "$work/target/qqwry.dat")" = old ]
[ ! -e "$work/target/config.test.yaml" ]
publish_ip_databases "$work/absent" "$work/target"
[ "$(cat "$work/target/qqwry.dat")" = old ]
printf new > "$work/source/GeoLite2-City.mmdb"
publish_ip_databases "$work/source" "$work/target"
[ "$(cat "$work/target/GeoLite2-City.mmdb")" = new ]
ln -s config.test.yaml "$work/source/GeoLite2-ASN.mmdb"
if publish_ip_databases "$work/source" "$work/target"; then exit 1; fi
[ "$(cat "$work/target/qqwry.dat")" = old ]
echo 'IP database deployment tests passed'
