#!/usr/bin/env bash
# Real processes and loopback HTTP; no production configuration or cloud calls.
set -Eeuo pipefail
SCRIPTS="$(cd "$(dirname "$0")" && pwd)"
TEST_ROOT="$(mktemp -d)"
export DEPLOY_ROOT="$TEST_ROOT/deploy"
CONTROLLER="$DEPLOY_ROOT/service-local.sh"
NGINX_PID=""
RESTART_PID=""
cleanup() {
    local result=$?
    if [ "$result" != 0 ]; then
        [ ! -f "$TEST_ROOT/restart.log" ] || cat "$TEST_ROOT/restart.log" >&2
        [ ! -f "$TEST_ROOT/logs/error.log" ] || tail -20 "$TEST_ROOT/logs/error.log" >&2
    fi
    if [ -n "$RESTART_PID" ]; then wait "$RESTART_PID" || true; fi
    if [ -n "$NGINX_PID" ]; then kill -QUIT "$NGINX_PID" 2>/dev/null || true; fi
    [ ! -x "$CONTROLLER" ] || "$CONTROLLER" stop-all >/dev/null 2>&1 || true
    rm -rf -- "$TEST_ROOT"
}
trap cleanup EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
mkdir -p "$DEPLOY_ROOT/admin-server" "$DEPLOY_ROOT/user-server" "$DEPLOY_ROOT/logs"
cp "$SCRIPTS/service-local.sh" "$SCRIPTS/service-catalog.sh" "$DEPLOY_ROOT/"
cp "$SCRIPTS/../internal/installer/service_catalog.tsv" "$DEPLOY_ROOT/service-catalog.tsv"
chmod +x "$CONTROLLER"
# Choose a free pair with the documented +10 offset instead of occupying the
# developer's installed service ports.
PORT="$(python3 - <<'PY'
import socket
while True:
    a = socket.socket(); a.bind(('127.0.0.1', 0)); port = a.getsockname()[1]
    if port > 65525: a.close(); continue
    b = socket.socket()
    try: b.bind(('127.0.0.1', port+10))
    except OSError: a.close(); b.close(); continue
    print(port); a.close(); b.close(); break
PY
)"
sed -i "s/user-server\t9002\t/user-server\t$PORT\t/" "$DEPLOY_ROOT/service-catalog.tsv"
cat >"$DEPLOY_ROOT/admin-server/admin-server" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$DEPLOY_ROOT/preflight.log"
exit "${TEST_PREFLIGHT_STATUS:-0}"
SH
chmod +x "$DEPLOY_ROOT/admin-server/admin-server"
cat >"$DEPLOY_ROOT/user-server/user-server" <<'PY'
#!/usr/bin/env python3
import http.server, os, signal
identity = os.environ['SERVICE_INSTANCE_ID']
class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200); self.end_headers()
        self.wfile.write(identity.encode())
    def log_message(self, *args): pass
server = http.server.HTTPServer(('127.0.0.1', int(os.environ['THING_CONNECT_HTTP_PORT'])), Handler)
signal.signal(signal.SIGTERM, lambda *_: exit(0))
server.serve_forever()
PY
chmod +x "$DEPLOY_ROOT/user-server/user-server"
printf 'fixture: true\n' >"$DEPLOY_ROOT/user-server/config.yaml"
for args in 'admin-server 2' 'user-server 0' 'user-server 33' 'user-server-2 2'; do
    if "$CONTROLLER" instances $args >/dev/null 2>&1; then fail "accepted invalid count: $args"; fi
done
"$CONTROLLER" instances user-server 2
[ "$("$CONTROLLER" ports user-server)" = "$(printf '%s\n%s' "$PORT" "$((PORT+10))")" ] || fail ports
for action in start restart; do
    status=0
    TEST_PREFLIGHT_STATUS=78 "$CONTROLLER" "$action" user-server >"$TEST_ROOT/pause.log" 2>&1 || status=$?
    [ "$status" = 78 ] || fail "$action returned $status for a configuration pause"
done
# Restoring a release that already ran must not depend on the preflight.
checks="$(wc -l <"$DEPLOY_ROOT/preflight.log")"
TEST_PREFLIGHT_STATUS=78 THING_CONNECT_SKIP_START_PREFLIGHT=1 "$CONTROLLER" start user-server >"$TEST_ROOT/skip.log" 2>&1 ||
    fail "restore start was blocked by the preflight: $(cat "$TEST_ROOT/skip.log")"
[ "$(wc -l <"$DEPLOY_ROOT/preflight.log")" = "$checks" ] || fail 'restore start ran the preflight'
"$CONTROLLER" stop user-server >/dev/null
"$CONTROLLER" start user-server
grep -q -- '-check-service-instances 2' "$DEPLOY_ROOT/preflight.log" || fail 'missing multi-instance preflight'
[ "$(curl -fsS "http://127.0.0.1:$PORT/health/ready")" = local-user-server ] || fail 'primary identity'
[ "$(curl -fsS "http://127.0.0.1:$((PORT+10))/health/ready")" = local-user-server-2 ] || fail 'secondary identity'
[ "$("$CONTROLLER" status user-server | grep -c ' RUNNING ')" = 2 ] || fail 'group status'
if "$CONTROLLER" instances user-server 1 >/dev/null 2>&1; then fail 'resized running group'; fi
primary="$(cat "$DEPLOY_ROOT/var/local-services/user-server.pid")"
secondary="$(cat "$DEPLOY_ROOT/var/local-services/user-server-2.pid")"
"$CONTROLLER" restart user-server-2
[ "$primary" = "$(cat "$DEPLOY_ROOT/var/local-services/user-server.pid")" ] || fail 'secondary restart disrupted primary'
[ "$secondary" != "$(cat "$DEPLOY_ROOT/var/local-services/user-server-2.pid")" ] || fail 'secondary not restarted'

# A failed first readiness must leave the next instance running.
(
    source "$SCRIPTS/service-local.sh"
    load_instance_counts
    preflight_service() { :; }
    stop_one() { echo "stop $1" >>"$TEST_ROOT/sequence"; }
    start_one() { echo "start $1" >>"$TEST_ROOT/sequence"; }
    wait_ready() { return 1; }
    if control_service restart user-server; then fail 'accepted failed readiness'; fi
    [ "$(cat "$TEST_ROOT/sequence")" = $'stop user-server\nstart user-server' ] || fail 'continued after failed readiness'
)

# Kill a child, not its supervisor, and observe automatic recovery.
child="$(pgrep -P "$primary" -f user-server | head -1)"
[ -n "$child" ] || fail 'missing supervised child'
kill -KILL "$child"
for attempt in {1..50}; do
    if curl -fsS "http://127.0.0.1:$PORT/health/ready" >/dev/null 2>&1; then break; fi
    sleep 0.1
done
curl -fsS "http://127.0.0.1:$PORT/health/ready" >/dev/null

# Render the actual gateway configuration. With Nginx, exercise both upstreams
# during a real rolling restart; the CI job installs Nginx for this check.
(
    source "$SCRIPTS/deploy-local.sh"
    LOCAL_SOURCE_ROOT="$(cd "$SCRIPTS/../.." && pwd)"
    local_gateway_config 18080 >"$TEST_ROOT/gateway.conf"
    GATEWAY_PUBLIC_ONLY=1 local_gateway_config 18080 >"$TEST_ROOT/public.conf"
    grep -q '^    listen 127.0.0.1:18080;' "$TEST_ROOT/public.conf" || fail 'public listener missing'
    if grep -q '^    listen 127.0.0.1:9100;' "$TEST_ROOT/public.conf"; then fail 'public-only gateway claimed internal port'; fi
    ports="$(local_gateway_backend_ports "$TEST_ROOT/gateway.conf")"
    grep -qx "$PORT" <<<"$ports" || fail 'gateway backend ports missing the instance'
    ! grep -qx 9011 <<<"$ports" || fail 'template comment port counted as a backend'
)
grep -q "upstream user_server { server 127.0.0.1:$PORT max_fails=0; server 127.0.0.1:$((PORT+10)) max_fails=0; }" "$TEST_ROOT/gateway.conf" || fail 'gateway inventory'
grep -q '^    listen 127.0.0.1:9100;' "$TEST_ROOT/gateway.conf" || fail 'internal gateway missing'
if command -v nginx >/dev/null; then
    read -r gateway_port internal_port < <(python3 - <<'PY'
import socket
a, b = socket.socket(), socket.socket()
a.bind(('127.0.0.1', 0)); b.bind(('127.0.0.1', 0))
print(a.getsockname()[1], b.getsockname()[1])
PY
)
    mkdir -p "$TEST_ROOT/logs"
    sed "s/127.0.0.1:18080/127.0.0.1:$gateway_port/;s/127.0.0.1:9100/127.0.0.1:$internal_port/" "$TEST_ROOT/gateway.conf" >"$TEST_ROOT/nginx.conf"
    nginx -t -p "$TEST_ROOT/" -c nginx.conf
    nginx -p "$TEST_ROOT/" -c nginx.conf
    NGINX_PID="$(cat "$TEST_ROOT/nginx.pid")"
    for attempt in {1..6}; do curl -fsS "http://127.0.0.1:$gateway_port/"; echo; done >"$TEST_ROOT/responses"
    grep -qx local-user-server "$TEST_ROOT/responses" || fail 'primary absent from load balancer'
    grep -qx local-user-server-2 "$TEST_ROOT/responses" || fail 'secondary absent from load balancer'
    [ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$gateway_port/v1/ai/internal/test")" = 404 ] || fail 'public internal route exposed'
    [ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$internal_port/v1/user/test")" = 404 ] || fail 'internal gateway exposes public routes'
    "$CONTROLLER" restart user-server >"$TEST_ROOT/restart.log" 2>&1 &
    RESTART_PID=$!
    python3 - "$gateway_port" <<'PY'
import sys, time, urllib.request
until = time.monotonic() + 5
count = 0
while time.monotonic() < until:
    with urllib.request.urlopen('http://127.0.0.1:'+sys.argv[1]+'/', timeout=2) as r:
        assert r.status == 200
        assert r.read() in (b'local-user-server', b'local-user-server-2')
    count += 1
    time.sleep(.02)
assert count > 20, count
print('PASS: rolling restart HTTP requests:', count)
PY
    wait "$RESTART_PID"
    RESTART_PID=""
else
    [ "${CI:-}" != true ] || fail 'CI requires Nginx'
    echo 'SKIP: Nginx unavailable; gateway routing and rolling HTTP test not executed'
fi
"$CONTROLLER" stop-all
[ "$("$CONTROLLER" status user-server | grep -c STOPPED)" = 2 ] || fail 'stop-all missed secondary'
if command -v nginx >/dev/null; then
    # Stopped instances free their ports, so the gateway must still refuse them.
    if (
        source "$SCRIPTS/deploy-local.sh"
        LOCAL_SOURCE_ROOT="$(cd "$SCRIPTS/../.." && pwd)"
        GATEWAY_PORT="$((PORT+10))" local_gateway gateway
    ) >"$TEST_ROOT/gateway-conflict.log" 2>&1; then
        fail 'gateway accepted an instance port'
    fi
    grep -q '冲突' "$TEST_ROOT/gateway-conflict.log" || fail 'gateway conflict not explained'
fi
"$CONTROLLER" instances user-server 1
[ "$("$CONTROLLER" ports user-server)" = "$PORT" ] || fail 'scale down failed'
cp "$DEPLOY_ROOT/service-catalog.tsv" "$TEST_ROOT/catalog.saved"
sed -i "s/user-server\t$PORT\t/user-server\t9090\t/" "$DEPLOY_ROOT/service-catalog.tsv"
if "$CONTROLLER" instances user-server 2 >"$TEST_ROOT/reserved.log" 2>&1; then fail 'instance took the internal gateway port'; fi
grep -q 内网入口 "$TEST_ROOT/reserved.log" || fail 'internal gateway conflict not explained'
cp "$TEST_ROOT/catalog.saved" "$DEPLOY_ROOT/service-catalog.tsv"
echo 'PASS: local multi-instance lifecycle and gateway'
