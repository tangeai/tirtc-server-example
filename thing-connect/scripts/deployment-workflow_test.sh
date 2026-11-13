#!/usr/bin/env bash
set -Eeuo pipefail
SCRIPTS="$(cd "$(dirname "$0")" && pwd)"
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TEST_ROOT"' EXIT
failed=0
check() { if ( "$@" ); then echo "PASS: $1"; else echo "FAIL: $1"; failed=1; fi; }

test_old_toolchain_rejected() (
    source "$SCRIPTS/install.sh"
    go() { echo 'go1.18.0'; }
    node() { echo '12.0.0'; }
    npm() { echo '6.0.0'; }
    if require_commands >"$TEST_ROOT/tools.log" 2>&1; then return 1; fi
    grep -q 'Go' "$TEST_ROOT/tools.log" || return 1
    grep -q 'Node' "$TEST_ROOT/tools.log" || return 1
    grep -q 'npm' "$TEST_ROOT/tools.log"
)

test_resume_does_not_pull_build_or_overwrite() (
    DEPLOY_ROOT="$TEST_ROOT/resume"
    source "$SCRIPTS/install.sh"
    mkdir -p "$DEPLOY_ROOT/var/installer" "$DEPLOY_ROOT/admin-server"
    printf 'saved-state' >"$DEPLOY_ROOT/var/installer/state.json"
    touch "$DEPLOY_ROOT/var/installer/first-run.allowed"
    printf '#!/bin/sh\nexit 0\n' >"$DEPLOY_ROOT/admin-server/admin-server"
    chmod +x "$DEPLOY_ROOT/admin-server/admin-server"
    LOCAL_CONTROLLER="$DEPLOY_ROOT/service-local.sh"
    cp "$DEPLOY_ROOT/admin-server/admin-server" "$LOCAL_CONTROLLER"
    require_commands() { return 91; }
    pull_source() { return 92; }
    build_release() { return 93; }
    prepare_setup() { echo 'token-fixture'; }
    start_setup_server() { echo 'resumed' >"$TEST_ROOT/resumed"; }
    run_install >"$TEST_ROOT/resume.log" 2>&1 || return 1
    test -f "$TEST_ROOT/resumed" || return 1
    test "$(cat "$DEPLOY_ROOT/var/installer/state.json")" = saved-state
)

test_runtime_config_can_check_current_schema() (
    DEPLOY_ROOT="$TEST_ROOT/migration"
    BUILD_DIR="$TEST_ROOT/migration-build"
    source "$SCRIPTS/deploy-prod.sh"
    mkdir -p "$DEPLOY_ROOT/config-current/admin-server" "$BUILD_DIR/bin"
    touch "$DEPLOY_ROOT/config-current/admin-server/config.yaml"
    cat >"$BUILD_DIR/bin/admin-server" <<'SH'
#!/bin/sh
case " $* " in *' -migration-check-only '*) echo 'migration_result=unchanged';; *) exit 91;; esac
SH
    chmod +x "$BUILD_DIR/bin/admin-server"
    MIGRATION_CONFIG="" DATABASE_BACKUP_FILE="" DATABASE_BACKUP_RESTORE_VERIFIED=0
    run_migrations
)

test_enabled_service_list() (
    source "$SCRIPTS/service-catalog.sh"
    load_service_catalog "$SCRIPTS/../internal/installer/service_catalog.tsv"
    DEPLOY_ROOT="$TEST_ROOT/enabled"; mkdir -p "$DEPLOY_ROOT"
    printf 'admin-server\ndevice-server\nuser-server\n' >"$DEPLOY_ROOT/enabled-services"
    declare -F service_is_enabled >/dev/null || return 1
    declare -F validate_enabled_services >/dev/null || return 1
    service_is_enabled user-server || return 1
    if service_is_enabled voip-server; then return 1; fi
    printf 'not-a-service\n' >>"$DEPLOY_ROOT/enabled-services"
    if validate_enabled_services; then return 1; fi
)


test_local_readiness_retries_without_supervisor() (
    source "$SCRIPTS/deploy-prod.sh"
    ACTIVE_SERVICE_MANAGER=local HEALTH_WAIT_SECONDS=3
    service_health_url() { echo http://fixture/health/ready; }
    supervisor_state() { echo 'unexpected supervisor call' >>"$TEST_ROOT/unexpected"; return 99; }
    curl() { if [ -f "$TEST_ROOT/ready" ]; then return 0; fi; touch "$TEST_ROOT/ready"; return 1; }
    sleep() { :; }
    wait_for_readiness user-server || return 1
    [ ! -e "$TEST_ROOT/unexpected" ]
)

test_update_prepares_before_downtime() (
    source "$SCRIPTS/deploy-prod.sh"
    DEPLOY_ROOT="$TEST_ROOT/ordering"; mkdir -p "$DEPLOY_ROOT"
    local events="$TEST_ROOT/events" scenario result
    event() { echo "$*" >>"$events"; }
    validate_deploy_root() { :; }; validate_release_options() { :; }
    resolve_active_services() { ACTIVE_SERVICES=(admin-server user-server); ACTIVE_STOP_ORDER=(user-server admin-server); }
    pull_code() { :; }; load_deployment_service_catalog() { :; }
    release_state_is_current() { return 1; }; invalidate_release_state() { :; }
    validate_backup_retention() { :; }; validate_paths() { :; }
    prepare_update_service_control() { ACTIVE_SERVICE_MANAGER=local; }
    run_batch() { event build; [ "$scenario" != build-failure ]; }
    validate_build_release() { :; }; prepare_service_configs() { :; }; validate_configs() { :; }
    backup_release() { BACKUP_DIR="$DEPLOY_ROOT/backup"; }
    inspect_migrations() { event inspect; [ "$scenario" != migration-preflight-failure ]; }
    mark_backup_failed() { :; }
    preflight_business_services() { event preflight; [ "$scenario" != business-preflight-failure ]; }
    stop_one() { event "stop $1"; }
    validate_manual_services_stopped() { :; }
    run_migrations() { event migrate; }
    deploy_one() { event "publish $1"; }
    start_one() { event "ready $1"; }
    mark_backup_successful() { event success; }
    publish_service_catalog() { :; }; publish_local_service_script() { :; }
    publish_deploy_script() { :; }; publish_release_state() { :; }; prune_successful_backups() { :; }
    git() { echo fixture; }
    for scenario in build-failure migration-preflight-failure business-preflight-failure success; do
        : >"$events"; result=0
        full_deploy >/dev/null 2>&1 || result=$?
        if [ "$scenario" != success ]; then
            [ "$result" != 0 ] || return 1
            if grep -Eq 'stop|publish|ready|success|^migrate$' "$events"; then return 1; fi
        else
            [ "$result" = 0 ] || return 1
            printf '%s\n' build inspect preflight 'stop user-server' 'stop admin-server' migrate 'publish admin-server' 'publish user-server' 'ready admin-server' preflight 'ready user-server' success >"$TEST_ROOT/expected"
            diff -u "$TEST_ROOT/expected" "$events" || return 1
        fi
    done
)

test_pending_migration_requires_explicit_credentials() (
    DEPLOY_ROOT="$TEST_ROOT/pending" BUILD_DIR="$TEST_ROOT/pending-build"
    source "$SCRIPTS/deploy-prod.sh"
    mkdir -p "$DEPLOY_ROOT/config-current/admin-server" "$BUILD_DIR/bin"
    touch "$DEPLOY_ROOT/config-current/admin-server/config.yaml"
    printf '#!/bin/sh\necho migration_result=pending\n' >"$BUILD_DIR/bin/admin-server"
    chmod +x "$BUILD_DIR/bin/admin-server"
    MIGRATION_CONFIG=""
    if inspect_migrations >"$TEST_ROOT/pending.log" 2>&1; then return 1; fi
    grep -q 'DDL' "$TEST_ROOT/pending.log"
)

test_optional_service_enable_disable() (
    DEPLOY_ROOT="$TEST_ROOT/optional"
    source "$SCRIPTS/service-local.sh"
    initialize_enabled_services || return 1
    stop_one() { echo "$1" >"$TEST_ROOT/stopped-optional"; }
    set_service_enabled ai-server 1 || return 1
    service_is_enabled ai-server || return 1
    set_service_enabled ai-server 0 || return 1
    if service_is_enabled ai-server; then return 1; fi
    [ "$(cat "$TEST_ROOT/stopped-optional")" = ai-server ] || return 1
    if set_service_enabled user-server 0; then return 1; fi
    service_is_enabled user-server || return 1
    local calls="$TEST_ROOT/enabled-starts"
    start_one() { echo "$1" >>"$calls"; }
    wait_ready() { return 0; }
    config_exists() { return 0; }
    preflight_service() { [ "$1" != ai-server ]; }
    start_all || return 1
    printf '%s\n' admin-server device-server user-server >"$TEST_ROOT/expected-starts"
    diff -u "$TEST_ROOT/expected-starts" "$calls"
)

check test_optional_service_enable_disable
check test_local_readiness_retries_without_supervisor
check test_update_prepares_before_downtime
check test_pending_migration_requires_explicit_credentials
check test_old_toolchain_rejected
check test_resume_does_not_pull_build_or_overwrite
check test_runtime_config_can_check_current_schema
check test_enabled_service_list
exit "$failed"
