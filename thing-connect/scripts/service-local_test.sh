#!/usr/bin/env bash

set -Eeuo pipefail

TEST_ROOT="$(mktemp -d)"
DEPLOY_ROOT="$TEST_ROOT/deploy"
CONTROLLER="$DEPLOY_ROOT/service-local.sh"
SOURCE_SCRIPT="$(cd "$(dirname "$0")" && pwd)/service-local.sh"
SOURCE_LOADER="$(cd "$(dirname "$0")" && pwd)/service-catalog.sh"
SOURCE_CATALOG="$(cd "$(dirname "$0")/.." && pwd)/internal/installer/service_catalog.tsv"

cleanup() {
    if [ -x "$CONTROLLER" ]; then
        DEPLOY_ROOT="$DEPLOY_ROOT" "$CONTROLLER" stop-all >/dev/null 2>&1 || true
    fi
    rm -rf -- "$TEST_ROOT"
}
trap cleanup EXIT

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

prepare_controller() {
    mkdir -p "$DEPLOY_ROOT"
    cp "$SOURCE_SCRIPT" "$CONTROLLER"
    cp "$SOURCE_LOADER" "$DEPLOY_ROOT/service-catalog.sh"
    cp "$SOURCE_CATALOG" "$DEPLOY_ROOT/service-catalog.tsv"
    chmod 0755 "$CONTROLLER"
}

write_fake_service() {
    local service="$1"
    mkdir -p "$DEPLOY_ROOT/$service"
    printf '%s\n' \
        '#!/usr/bin/env bash' \
        'set -Eeuo pipefail' \
        'mkdir -p "${DEPLOY_ROOT:?}/var/local-services"' \
        'printf "%s\n" "$*" >"$DEPLOY_ROOT/var/local-services/fake-args"' \
        'count_file="$DEPLOY_ROOT/var/local-services/fake-start-count"' \
        'count=0' \
        '[ ! -f "$count_file" ] || count="$(<"$count_file")"' \
        'count=$((count + 1))' \
        'printf "%s\n" "$count" >"$count_file"' \
        'if [ "${FAKE_ALWAYS_FAIL:-0}" = "1" ]; then exit 1; fi' \
        'if [ "${FAKE_FAIL_FIRST:-0}" = "1" ] && [ "$count" -eq 1 ]; then exit 1; fi' \
        'trap "exit 0" INT TERM' \
        'while :; do sleep 1; done' \
        >"$DEPLOY_ROOT/$service/$service"
    chmod 0755 "$DEPLOY_ROOT/$service/$service"
    printf 'fixture: true\n' >"$DEPLOY_ROOT/$service/config.yaml"
}

test_status_protocol_and_lifecycle() {
    local output
    write_fake_service device-server
    output="$(DEPLOY_ROOT="$DEPLOY_ROOT" "$CONTROLLER" start thing-connect:device-server)"
    [[ "$output" == "thing-connect:device-server STARTING pid "* ]] || \
        fail "start output is not supervisorctl-compatible: $output"
    output="$(DEPLOY_ROOT="$DEPLOY_ROOT" "$CONTROLLER" status thing-connect:device-server)"
    [[ "$output" == "thing-connect:device-server STARTING pid "* ]] || \
        fail "status did not report STARTING for a non-listening child: $output"
    output="$(DEPLOY_ROOT="$DEPLOY_ROOT" "$CONTROLLER" stop thing-connect:device-server)"
    [ "$output" = "thing-connect:device-server STOPPED" ] || \
        fail "stop did not report STOPPED: $output"
    output="$(DEPLOY_ROOT="$DEPLOY_ROOT" "$CONTROLLER" status thing-connect:device-server)"
    [ "$output" = "thing-connect:device-server STOPPED" ] || \
        fail "stopped service still reported as running: $output"
}

test_runner_restarts_failed_child() {
    local elapsed=0 count=0
    rm -rf -- "$DEPLOY_ROOT/var/local-services"
    FAKE_FAIL_FIRST=1 DEPLOY_ROOT="$DEPLOY_ROOT" \
        "$CONTROLLER" start thing-connect:device-server >/dev/null
    while [ "$elapsed" -lt 8 ]; do
        if [ -f "$DEPLOY_ROOT/var/local-services/fake-start-count" ]; then
            count="$(<"$DEPLOY_ROOT/var/local-services/fake-start-count")"
            [ "$count" -ge 2 ] && break
        fi
        sleep 1
        elapsed=$((elapsed + 1))
    done
    [ "$count" -ge 2 ] || fail "failed child was not restarted"
    DEPLOY_ROOT="$DEPLOY_ROOT" "$CONTROLLER" status thing-connect:device-server |
        grep -q ' STARTING ' || fail "restarting child did not remain STARTING"
    DEPLOY_ROOT="$DEPLOY_ROOT" "$CONTROLLER" stop thing-connect:device-server >/dev/null
}

test_missing_pid_does_not_spawn_duplicate_runner() {
    local first_pid output runner_count
    local -a runners=()
    rm -rf -- "$DEPLOY_ROOT/var/local-services"
    write_fake_service device-server
    DEPLOY_ROOT="$DEPLOY_ROOT" "$CONTROLLER" start thing-connect:device-server >/dev/null
    first_pid="$(<"$DEPLOY_ROOT/var/local-services/device-server.pid")"
    rm -f -- "$DEPLOY_ROOT/var/local-services/device-server.pid"
    output="$(DEPLOY_ROOT="$DEPLOY_ROOT" "$CONTROLLER" start thing-connect:device-server)"
    mapfile -t runners < <(pgrep -f "^bash $CONTROLLER run device-server$" || true)
    runner_count="${#runners[@]}"
    for pid in "${runners[@]}"; do
        kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
    done
    sleep 1
    [ "$runner_count" -eq 1 ] || fail "missing PID file spawned $runner_count runners"
    [[ "$output" == *" pid $first_pid" ]] || fail "orphan runner was not adopted: $output"
}

test_crashing_child_is_not_reported_running() {
    local output
    rm -rf -- "$DEPLOY_ROOT/var/local-services"
    write_fake_service device-server
    FAKE_ALWAYS_FAIL=1 DEPLOY_ROOT="$DEPLOY_ROOT" \
        "$CONTROLLER" start thing-connect:device-server >/dev/null
    sleep 2
    output="$(DEPLOY_ROOT="$DEPLOY_ROOT" "$CONTROLLER" status thing-connect:device-server)"
    DEPLOY_ROOT="$DEPLOY_ROOT" "$CONTROLLER" stop thing-connect:device-server >/dev/null
    [[ "$output" != *" RUNNING "* ]] || fail "crashing service reported RUNNING: $output"
}

test_start_rejects_occupied_port_with_guidance() (
    DEPLOY_ROOT="$TEST_ROOT/occupied-port-deploy"
    source "$SOURCE_SCRIPT"
    mkdir -p "$DEPLOY_ROOT/call-server"
    printf '#!/usr/bin/env bash\n' >"$DEPLOY_ROOT/call-server/call-server"
    chmod +x "$DEPLOY_ROOT/call-server/call-server"
    printf 'fixture: true\n' >"$DEPLOY_ROOT/call-server/config.yaml"
    port_in_use() { return 0; }

    local output
    if output="$(start_one call-server 2>&1)"; then
        fail "occupied call-server port was accepted"
    fi
    [[ "$output" == *"端口 9005 已被占用"* ]] || fail "port conflict reason is missing: $output"
    [[ "$output" == *"处理建议"*"停止旧实例"* ]] || fail "port conflict guidance is missing: $output"
)

test_admin_receives_setup_listener() {
    local elapsed=0 args=""
    rm -rf -- "$DEPLOY_ROOT/var/local-services"
    write_fake_service admin-server
    SETUP_BIND=0.0.0.0 DEPLOY_ROOT="$DEPLOY_ROOT" \
        "$CONTROLLER" start thing-connect:admin-server >/dev/null
    while [ "$elapsed" -lt 5 ]; do
        if [ -f "$DEPLOY_ROOT/var/local-services/fake-args" ]; then
            args="$(<"$DEPLOY_ROOT/var/local-services/fake-args")"
            break
        fi
        sleep 1
        elapsed=$((elapsed + 1))
    done
    [[ "$args" == *"-setup-bind 0.0.0.0 -setup-port 9000"* ]] || \
        fail "Admin did not receive setup bind/port: $args"
    [[ "$args" == *"-supervisorctl $CONTROLLER"* ]] || \
        fail "Admin did not receive local controller path: $args"
    DEPLOY_ROOT="$DEPLOY_ROOT" "$CONTROLLER" stop thing-connect:admin-server >/dev/null
}

test_start_all_only_uses_existing_configs() (
    local capture="$TEST_ROOT/start-all.capture"
    DEPLOY_ROOT="$TEST_ROOT/start-all-deploy"
    source "$SOURCE_SCRIPT"
    start_one() { printf 'start=%s\n' "$1" >>"$capture"; }
    wait_ready() { printf 'ready=%s:%s\n' "$1" "$2" >>"$capture"; }
    config_exists() {
        case "$1" in
            device-server|user-server|ai-server) return 0 ;;
            *) return 1 ;;
        esac
    }

    start_all

    [ "$(<"$capture")" = $'start=admin-server\nready=admin-server:9000\nstart=device-server\nready=device-server:9001\nstart=user-server\nready=user-server:9002\nstart=ai-server\nready=ai-server:9004' ] || \
        fail "start-all did not honor configured optional services: $(<"$capture")"
)

test_start_all_stops_service_that_never_becomes_ready() (
    local capture="$TEST_ROOT/start-all-failure.capture"
    DEPLOY_ROOT="$TEST_ROOT/start-all-failure-deploy"
    source "$SOURCE_SCRIPT"
    start_one() { printf 'start=%s\n' "$1" >>"$capture"; }
    stop_one() { printf 'stop=%s\n' "$1" >>"$capture"; }
    wait_ready() { [ "$1" = "admin-server" ]; }
    config_exists() { [ "$1" = "device-server" ]; }

    if start_all; then
        fail "start-all accepted a service that failed readiness"
    fi
    grep -qx 'stop=device-server' "$capture"
)

test_unknown_service_is_rejected() {
    if DEPLOY_ROOT="$DEPLOY_ROOT" "$CONTROLLER" status thing-connect:unknown \
        >/dev/null 2>&1; then
        fail "unknown service was accepted"
    fi
}

prepare_controller
test_status_protocol_and_lifecycle
test_runner_restarts_failed_child
test_missing_pid_does_not_spawn_duplicate_runner
test_crashing_child_is_not_reported_running
test_start_rejects_occupied_port_with_guidance
test_admin_receives_setup_listener
test_start_all_only_uses_existing_configs
test_start_all_stops_service_that_never_becomes_ready
test_unknown_service_is_rejected
echo "PASS: service-local.sh"
