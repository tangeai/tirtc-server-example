#!/usr/bin/env bash

set -Eeuo pipefail

TEST_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TEST_ROOT"' EXIT

DEPLOY_ROOT="$TEST_ROOT"
source "$(cd "$(dirname "$0")" && pwd)/deploy-prod.sh"
DATABASE_BACKUP_FILE="$TEST_ROOT/database-backup.sql"
DATABASE_BACKUP_RESTORE_VERIFIED=1
printf 'verified test backup\n' >"$DATABASE_BACKUP_FILE"

assert_eq() {
    local want="$1" got="$2" message="$3"
    if [ "$got" != "$want" ]; then
        echo "FAIL: $message: got '$got', want '$want'" >&2
        exit 1
    fi
}

test_http_admin_cookie_is_allowed_by_default() {
    assert_eq "1" "$ALLOW_INSECURE_ADMIN_COOKIE" \
        "default HTTP deployment must allow a non-Secure Admin cookie"
}

test_migration_refuses_missing_or_unverified_database_backup() (
    SKIP_MIGRATIONS=0
    DATABASE_BACKUP_FILE=""
    DATABASE_BACKUP_RESTORE_VERIFIED=0
    if validate_database_backup >/dev/null 2>&1; then
        echo "FAIL: migration accepted an unverified database backup" >&2
        exit 1
    fi
    DATABASE_BACKUP_RESTORE_VERIFIED=1
    DATABASE_BACKUP_FILE="$TEST_ROOT/missing-backup.sql"
    if validate_database_backup >/dev/null 2>&1; then
        echo "FAIL: migration accepted a missing database backup" >&2
        exit 1
    fi
)

test_current_database_skips_backup_gate() (
    local capture="$TEST_ROOT/current-migration.capture" result=0 output
    BUILD_DIR="$TEST_ROOT/current-migration-build"
    MIGRATION_CONFIG="$TEST_ROOT/current-migration.yaml"
    DATABASE_BACKUP_FILE=""
    DATABASE_BACKUP_RESTORE_VERIFIED=0
    SKIP_MIGRATIONS=0
    mkdir -p "$BUILD_DIR/bin"
    : >"$MIGRATION_CONFIG"
    cat >"$BUILD_DIR/bin/admin-server" <<'BASH'
#!/usr/bin/env bash
printf '%s\n' "$@" >"$MIGRATION_CAPTURE"
if [[ " $* " == *" -migration-check-only "* ]]; then
    printf 'migration_result=unchanged database schema already current\n'
    exit 0
fi
printf 'unexpected migration execution\n' >&2
exit 97
BASH
    chmod +x "$BUILD_DIR/bin/admin-server"
    MIGRATION_CAPTURE="$capture"
    export MIGRATION_CAPTURE

    output="$(run_migrations 2>&1)" || result=$?

    assert_eq "0" "$result" "current database must not require a migration backup"
    grep -qx -- '-migration-check-only' "$capture"
    [[ "$output" == *"数据库已是当前版本；跳过数据库迁移和备份门槛"* ]] || {
        echo "FAIL: current database did not report the skipped migration backup" >&2
        printf '%s\n' "$output" >&2
        exit 1
    }
)

test_pending_database_still_requires_verified_backup() (
    local capture="$TEST_ROOT/pending-migration.capture" result=0
    BUILD_DIR="$TEST_ROOT/pending-migration-build"
    MIGRATION_CONFIG="$TEST_ROOT/pending-migration.yaml"
    DATABASE_BACKUP_FILE=""
    DATABASE_BACKUP_RESTORE_VERIFIED=0
    SKIP_MIGRATIONS=0
    mkdir -p "$BUILD_DIR/bin"
    : >"$MIGRATION_CONFIG"
    cat >"$BUILD_DIR/bin/admin-server" <<'BASH'
#!/usr/bin/env bash
printf '%s\n' "$@" >>"$MIGRATION_CAPTURE"
if [[ " $* " == *" -migration-check-only "* ]]; then
    printf 'migration_result=pending database migrations required\n'
    exit 0
fi
printf 'migration_result=changed database migrations applied\n'
BASH
    chmod +x "$BUILD_DIR/bin/admin-server"
    MIGRATION_CAPTURE="$capture"
    export MIGRATION_CAPTURE

    run_migrations >/dev/null 2>&1 || result=$?

    assert_eq "1" "$result" "pending migration must require a verified backup"
    grep -qx -- '-migration-check-only' "$capture"
    if grep -qx -- '-migrate-only' "$capture"; then
        echo "FAIL: pending migration ran DDL without a verified backup" >&2
        exit 1
    fi
)

test_failed_migration_check_never_runs_ddl() (
    local capture="$TEST_ROOT/failed-migration-check.capture" result=0
    BUILD_DIR="$TEST_ROOT/failed-migration-check-build"
    MIGRATION_CONFIG="$TEST_ROOT/failed-migration-check.yaml"
    DATABASE_BACKUP_FILE=""
    DATABASE_BACKUP_RESTORE_VERIFIED=0
    SKIP_MIGRATIONS=0
    mkdir -p "$BUILD_DIR/bin"
    : >"$MIGRATION_CONFIG"
    cat >"$BUILD_DIR/bin/admin-server" <<'BASH'
#!/usr/bin/env bash
printf '%s\n' "$@" >>"$MIGRATION_CAPTURE"
if [[ " $* " == *" -migration-check-only "* ]]; then
    printf 'migration preflight failed\n' >&2
    exit 23
fi
printf 'migration_result=changed database migrations applied\n'
BASH
    chmod +x "$BUILD_DIR/bin/admin-server"
    MIGRATION_CAPTURE="$capture"
    export MIGRATION_CAPTURE

    run_migrations >/dev/null 2>&1 || result=$?

    assert_eq "23" "$result" "migration check failure status must be preserved"
    grep -qx -- '-migration-check-only' "$capture"
    if grep -qx -- '-migrate-only' "$capture"; then
        echo "FAIL: failed migration check still attempted DDL" >&2
        exit 1
    fi
)

test_yaml_section_headers_allow_valid_whitespace() {
    local cfg="$TEST_ROOT/whitespace.yaml"
    cat >"$cfg" <<'YAML'
internal:  
  key: "shared-key"
admin:  # valid YAML comment
  server_url: "http://127.0.0.1:9000"
YAML

    assert_eq "shared-key" "$(yaml_section_value "$cfg" internal key)" \
        "section parser must accept trailing spaces"
    assert_eq "http://127.0.0.1:9000" "$(yaml_section_value "$cfg" admin server_url)" \
        "section parser must accept an inline comment"
    yaml_has_section_key "$cfg" internal key || {
        echo "FAIL: section-key detection must accept trailing spaces" >&2
        exit 1
    }
}

test_dynamic_mqtt_is_not_required_in_base_yaml() (
    local root="$TEST_ROOT/dynamic-mqtt-config"
    local shared_jwt="shared-business-jwt-0123456789abcdef"
    local internal_key="shared-internal-key-0123456789abcdef"
    local config_key="MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
    local output result=0
    DEPLOY_ROOT="$root"
    mkdir -p "$root/admin-server" "$root/user-server"
    cat >"$root/admin-server/config.yaml" <<YAML
server:
  http_port: 9000
database:
  dsn: "runtime:secret@tcp(mysql.internal:3306)/thing_connect"
redis:
  addr: "redis.internal:6379"
  password: "redis-secret"
  db: 0
internal:
  key: "$internal_key"
admin:
  jwt_secret: "admin-jwt-secret-0123456789abcdef"
  cookie_secure: true
security:
  config_encryption_key: "$config_key"
YAML
    cat >"$root/user-server/config.yaml" <<YAML
server:
  http_port: 9002
database:
  dsn: "runtime:secret@tcp(mysql.internal:3306)/thing_connect"
redis:
  addr: "redis.internal:6379"
  password: "redis-secret"
  db: 0
jwt_secret: "$shared_jwt"
internal:
  key: "$internal_key"
admin:
  server_url: "http://127.0.0.1:9000"
ai:
  server_url: "http://127.0.0.1:9004"
voip:
  server_url: "http://127.0.0.1:9003"
call:
  server_url: "http://127.0.0.1:9005"
YAML
    resolve_active_services() {
        ACTIVE_SERVICES=(admin-server user-server)
        ACTIVE_BUSINESS_SERVICES=(user-server)
        ACTIVE_STOP_ORDER=(user-server admin-server)
    }
    strict_validate_config_syntax() { :; }

    output="$(validate_configs 2>&1)" || result=$?
    if [ "$result" -ne 0 ]; then
        echo "FAIL: dynamic MQTT configuration was incorrectly required in base YAML" >&2
        printf '%s\n' "$output" >&2
        exit 1
    fi
    [[ "$output" != *"mqtt.broker"* ]] || {
        echo "FAIL: base YAML validation still reported legacy mqtt.broker" >&2
        exit 1
    }
)

test_missing_supervisor_selects_manual_service_control() (
    local root="$TEST_ROOT/manual-service-control"
    DEPLOY_ROOT="$root"
    SUPERVISORCTL="$root/bin/supervisorctl-not-installed"
    LOCAL_SERVICECTL="$root/service-local.sh"
    SERVICE_MANAGER="auto"
    ACTIVE_SERVICES=(admin-server user-server)
    mkdir -p "$root"
    cat >"$LOCAL_SERVICECTL" <<'BASH'
#!/usr/bin/env bash
printf 'thing-connect:admin-server STOPPED\n'
printf 'thing-connect:user-server STOPPED\n'
BASH
    chmod +x "$LOCAL_SERVICECTL"

    validate_service_manager

    assert_eq "manual" "$ACTIVE_SERVICE_MANAGER" \
        "missing Supervisor must select manual service-local control"
)

test_partial_supervisor_inventory_is_rejected() (
    local root="$TEST_ROOT/partial-supervisor"
    local output result=0
    DEPLOY_ROOT="$root"
    SUPERVISORCTL="true"
    LOCAL_SERVICECTL="$root/service-local.sh"
    SERVICE_MANAGER="auto"
    ACTIVE_SERVICES=(admin-server user-server)
    supervisor_state() {
        if [ "$1" = "admin-server" ]; then
            printf 'RUNNING\n'
        fi
        return 0
    }

    output="$(validate_service_manager 2>&1)" || result=$?

    assert_eq "1" "$result" "partial Supervisor inventory must be rejected"
    [[ "$output" == *"只配置了 1/2 个已安装服务"* ]] || {
        echo "FAIL: partial Supervisor inventory was not explained" >&2
        exit 1
    }
)

test_explicit_manual_mode_rejects_existing_supervisor_services() (
    local root="$TEST_ROOT/manual-with-supervisor"
    local output result=0
    DEPLOY_ROOT="$root"
    SUPERVISORCTL="true"
    LOCAL_SERVICECTL="$root/service-local.sh"
    SERVICE_MANAGER="manual"
    ACTIVE_SERVICES=(admin-server user-server)
    supervisor_state() {
        if [ "$1" = "admin-server" ]; then
            printf 'STOPPED\n'
        fi
        return 0
    }

    output="$(validate_service_manager 2>&1)" || result=$?

    assert_eq "1" "$result" "manual mode must reject existing Supervisor services"
    [[ "$output" == *"不能忽略 Supervisor 中已有的 1 个 ThingConnect 服务"* ]] || {
        echo "FAIL: explicit manual mode did not explain the Supervisor conflict" >&2
        exit 1
    }
)

test_manual_update_requires_local_services_to_be_stopped() (
    local root="$TEST_ROOT/manual-running-services"
    local output result=0
    DEPLOY_ROOT="$root"
    LOCAL_SERVICECTL="$root/service-local.sh"
    mkdir -p "$root"
    cat >"$LOCAL_SERVICECTL" <<'BASH'
#!/usr/bin/env bash
printf 'thing-connect:admin-server RUNNING pid 123\n'
printf 'thing-connect:user-server STOPPED\n'
BASH
    chmod +x "$LOCAL_SERVICECTL"

    output="$(validate_manual_services_stopped 2>&1)" || result=$?

    assert_eq "1" "$result" "manual update must reject running local services"
    [[ "$output" == *"$LOCAL_SERVICECTL stop-all"* ]] || {
        echo "FAIL: manual update did not provide the exact stop-all command" >&2
        exit 1
    }
)

test_manual_update_publishes_without_automatic_restart() (
    local capture="$TEST_ROOT/manual-update.capture"
    : >"$capture"
    validate_deploy_root() { :; }
    validate_service_manager() { ACTIVE_SERVICE_MANAGER="manual"; }
    validate_manual_services_stopped() { printf 'manual-checked\n' >>"$capture"; }
    validate_backup_retention() { :; }
    validate_release_options() { :; }
    pull_code() { :; }
    validate_paths() { :; }
    load_deployment_service_catalog() { :; }
    resolve_active_services() {
        ACTIVE_SERVICES=(admin-server user-server)
        ACTIVE_BUSINESS_SERVICES=(user-server)
        ACTIVE_STOP_ORDER=(user-server admin-server)
    }
    run_batch() { :; }
    validate_build_release() { :; }
    validate_configs() { :; }
    backup_release() { BACKUP_DIR="$TEST_ROOT/manual-update-backup"; }
    run_migrations() { :; }
    deploy_one() { printf 'deployed=%s\n' "$1" >>"$capture"; }
    restart_one() { printf 'restarted=%s\n' "$1" >>"$capture"; }
    mark_backup_successful() { :; }
    publish_service_catalog() { :; }
    publish_local_service_script() { :; }
    publish_deploy_script() { :; }
    publish_release_state() { printf 'release-state-published\n' >>"$capture"; }
    prune_successful_backups() { :; }
    git() { printf 'test-commit\n'; }

    full_deploy

    grep -qx 'manual-checked' "$capture"
    grep -qx 'deployed=admin-server' "$capture"
    grep -qx 'deployed=user-server' "$capture"
    grep -qx 'release-state-published' "$capture"
    if grep -q '^restarted=' "$capture"; then
        echo "FAIL: manual update restarted services automatically" >&2
        exit 1
    fi
)

test_same_release_commit_skips_the_entire_update() (
    local current_commit="1111111111111111111111111111111111111111"
    local output result=0
    DEPLOY_ROOT="$TEST_ROOT/same-release"
    REPO_PATH="$TEST_ROOT/same-release-source"
    FORCE_UPDATE=0
    ACTIVE_SERVICES=(admin-server user-server)
    mkdir -p "$DEPLOY_ROOT"
    printf '%s\n%s\n' "$current_commit" "${ACTIVE_SERVICES[*]}" >"$DEPLOY_ROOT/.release-state"
    validate_deploy_root() { :; }
    resolve_active_services() { ACTIVE_SERVICES=(admin-server user-server); }
    prepare_update_service_control() {
        echo "FAIL: same release checked service manager or required stopped services" >&2
        return 92
    }
    validate_backup_retention() { :; }
    validate_release_options() { :; }
    pull_code() { :; }
    validate_paths() {
        echo "FAIL: same release continued into update preparation" >&2
        return 91
    }
    git() {
        if [[ " $* " == *" --short "* ]]; then
            printf '1111111\n'
        else
            printf '%s\n' "$current_commit"
        fi
    }

    output="$(full_deploy 2>&1)" || result=$?

    assert_eq "0" "$result" "same release commit must skip the update"
    [[ "$output" == *"当前已发布版本与待发布版本一致（1111111），无需更新"* ]] || {
        echo "FAIL: same release did not report the no-op decision" >&2
        printf '%s\n' "$output" >&2
        exit 1
    }
)

test_release_state_records_commit_and_installed_services() (
    local current_commit="2222222222222222222222222222222222222222"
    DEPLOY_ROOT="$TEST_ROOT/release-marker"
    REPO_PATH="$TEST_ROOT/release-marker-source"
    ACTIVE_SERVICES=(admin-server device-server user-server)
    mkdir -p "$DEPLOY_ROOT"
    git() {
        if [[ " $* " == *" --short "* ]]; then
            printf '2222222\n'
        else
            printf '%s\n' "$current_commit"
        fi
    }

    publish_release_state >/dev/null

    assert_eq "$current_commit
${ACTIVE_SERVICES[*]}" "$(<"$DEPLOY_ROOT/.release-state")" \
        "release state must identify the commit and installed service set"
    [ ! -e "$DEPLOY_ROOT/.release-state.new" ]
)

test_force_update_and_service_changes_bypass_same_commit_marker() (
    local current_commit="3333333333333333333333333333333333333333"
    DEPLOY_ROOT="$TEST_ROOT/release-marker-bypass"
    REPO_PATH="$TEST_ROOT/release-marker-bypass-source"
    ACTIVE_SERVICES=(admin-server user-server)
    mkdir -p "$DEPLOY_ROOT"
    git() { printf '%s\n' "$current_commit"; }

    printf '%s\n%s\n' "$current_commit" "${ACTIVE_SERVICES[*]}" >"$DEPLOY_ROOT/.release-state"
    FORCE_UPDATE=1
    if release_state_is_current; then
        echo "FAIL: FORCE_UPDATE=1 was ignored" >&2
        exit 1
    fi

    FORCE_UPDATE=0
    printf '%s\n%s\n' "$current_commit" "admin-server" >"$DEPLOY_ROOT/.release-state"
    if release_state_is_current; then
        echo "FAIL: newly installed service was hidden by the commit marker" >&2
        exit 1
    fi
)

test_missing_or_invalid_release_state_continues_update() (
    local current_commit="4444444444444444444444444444444444444444"
    local capture="$TEST_ROOT/invalid-release-state.capture"
    local result=0 state
    DEPLOY_ROOT="$TEST_ROOT/invalid-release-state"
    REPO_PATH="$TEST_ROOT/invalid-release-state-source"
    FORCE_UPDATE=0
    mkdir -p "$DEPLOY_ROOT"
    validate_deploy_root() { :; }
    resolve_active_services() { ACTIVE_SERVICES=(admin-server user-server); }
    validate_backup_retention() { :; }
    validate_release_options() { :; }
    pull_code() { :; }
    validate_paths() {
        printf 'update-preparation-reached\n' >"$capture"
        return 73
    }
    git() { printf '%s\n' "$current_commit"; }

    for state in missing invalid; do
        rm -f -- "$DEPLOY_ROOT/.release-state"
        if [ "$state" = "invalid" ]; then
            printf 'not-a-commit\nadmin-server user-server\n' >"$DEPLOY_ROOT/.release-state"
        fi
        rm -f -- "$capture"
        result=0
        full_deploy >/dev/null 2>&1 || result=$?
        [ "$result" -ne 0 ] || {
            echo "FAIL: $state release state unexpectedly completed the update" >&2
            exit 1
        }
        grep -qx 'update-preparation-reached' "$capture" || {
            echo "FAIL: $state release state skipped update preparation" >&2
            exit 1
        }
    done
)

test_failed_same_commit_repair_invalidates_previous_release_state() (
    local current_commit="5555555555555555555555555555555555555555"
    local capture="$TEST_ROOT/incomplete-release.capture"
    local failure_mode output result
    : >"$capture"
    DEPLOY_ROOT="$TEST_ROOT/incomplete-release"
    REPO_PATH="$TEST_ROOT/incomplete-release-source"
    FORCE_UPDATE=1
    ACTIVE_SERVICES=(admin-server)
    mkdir -p "$DEPLOY_ROOT"
    validate_deploy_root() { :; }
    resolve_active_services() {
        ACTIVE_SERVICES=(admin-server)
        ACTIVE_BUSINESS_SERVICES=()
        ACTIVE_STOP_ORDER=(admin-server)
    }
    validate_backup_retention() { :; }
    validate_release_options() { :; }
    pull_code() { :; }
    validate_paths() { :; }
    load_deployment_service_catalog() { :; }
    prepare_update_service_control() { ACTIVE_SERVICE_MANAGER="supervisor"; }
    run_batch() { :; }
    validate_build_release() { :; }
    validate_configs() { :; }
    backup_release() { BACKUP_DIR="$TEST_ROOT/incomplete-release-backup"; }
    run_migrations() { :; }
    deploy_one() { :; }
    restart_one() { :; }
    mark_backup_successful() { :; }
    publish_service_catalog() { [ "$failure_mode" != "auxiliary" ]; }
    publish_local_service_script() { :; }
    publish_deploy_script() { :; }
    publish_release_state() {
        [ "$failure_mode" != "state" ] || return 62
        printf 'release-state-published\n' >>"$capture"
    }
    prune_successful_backups() { :; }
    git() {
        if [[ " $* " == *" --short "* ]]; then
            printf '5555555\n'
        else
            printf '%s\n' "$current_commit"
        fi
    }

    for failure_mode in auxiliary state; do
        : >"$capture"
        printf '%s\n%s\n' "$current_commit" "admin-server" >"$DEPLOY_ROOT/.release-state"
        result=0
        output="$(full_deploy 2>&1)" || result=$?

        assert_eq "1" "$result" "$failure_mode failure during same-commit repair must fail visibly"
        FORCE_UPDATE=0
        if release_state_is_current; then
            echo "FAIL: $failure_mode failure left the previous same-commit release state current" >&2
            exit 1
        fi
        FORCE_UPDATE=1
        [[ "$output" == *"业务服务已发布，但"* ]] || {
            echo "FAIL: $failure_mode failure did not explain its partial-success state" >&2
            printf '%s\n' "$output" >&2
            exit 1
        }
    done
)

test_publish_deploy_script_replaces_root_entry_atomically() (
    BUILD_DIR="$TEST_ROOT/build"
    mkdir -p "$BUILD_DIR/scripts"
    cat >"$BUILD_DIR/scripts/deploy-prod.sh" <<'BASH'
#!/usr/bin/env bash
echo current
BASH
    printf '%s\n' '#!/usr/bin/env bash' 'echo old' >"$DEPLOY_ROOT/deploy-prod.sh"

    publish_deploy_script

    cmp -s "$BUILD_DIR/scripts/deploy-prod.sh" "$DEPLOY_ROOT/deploy-prod.sh"
    [ -x "$DEPLOY_ROOT/deploy-prod.sh" ]
    [ ! -e "$DEPLOY_ROOT/.deploy-prod.sh.new" ]
)

test_publish_local_service_script_replaces_root_entry_atomically() (
    BUILD_DIR="$TEST_ROOT/local-service-build"
    mkdir -p "$BUILD_DIR/scripts"
    cat >"$BUILD_DIR/scripts/service-local.sh" <<'BASH'
#!/usr/bin/env bash
echo current-local-service
BASH
    printf '%s\n' '#!/usr/bin/env bash' 'echo old-local-service' >"$DEPLOY_ROOT/service-local.sh"

    publish_local_service_script

    cmp -s "$BUILD_DIR/scripts/service-local.sh" "$DEPLOY_ROOT/service-local.sh"
    [ -x "$DEPLOY_ROOT/service-local.sh" ]
    [ ! -e "$DEPLOY_ROOT/.service-local.sh.new" ]
)

test_publish_deploy_script_keeps_previous_entry_on_invalid_source() (
    BUILD_DIR="$TEST_ROOT/invalid-build"
    mkdir -p "$BUILD_DIR/scripts"
    printf '%s\n' '#!/usr/bin/env bash' 'if' >"$BUILD_DIR/scripts/deploy-prod.sh"
    printf '%s\n' '#!/usr/bin/env bash' 'echo stable' >"$DEPLOY_ROOT/deploy-prod.sh"

    if publish_deploy_script; then
        echo "FAIL: invalid deployment script was published" >&2
        exit 1
    fi
    grep -qx 'echo stable' "$DEPLOY_ROOT/deploy-prod.sh"
)

test_running_admin_is_restarted_after_init() (
    local capture="$TEST_ROOT/admin-restart.capture"
    SUPERVISORCTL="true"
    supervisor_state() { printf 'RUNNING\n'; }
    restart_one() { printf '%s\n' "$1" >"$capture"; }

    restart_admin_after_init

    grep -qx 'admin-server' "$capture"
)

test_initialize_admin_is_noninteractive_and_refreshes_running_service() (
    local capture="$TEST_ROOT/admin-init.capture"
    mkdir -p "$TEST_ROOT/admin-server"
    cat >"$TEST_ROOT/admin-server/admin-server" <<'BASH'
#!/usr/bin/env bash
printf 'password_env=%s\n' "${ADMIN_INIT_PASSWORD:+set}" >"$ADMIN_INIT_CAPTURE"
printf 'arg=%s\n' "$@" >>"$ADMIN_INIT_CAPTURE"
BASH
    chmod +x "$TEST_ROOT/admin-server/admin-server"
    : >"$TEST_ROOT/admin-server/config.yaml"

    ADMIN_INIT_CAPTURE="$capture"
    export ADMIN_INIT_CAPTURE
    ADMIN_INIT_EMAIL="admin@example.com"
    ADMIN_INIT_NICK_NAME="管理员"
    ADMIN_INIT_PASSWORD="Diagnostic-password-123"
    validate_paths() { :; }
    validate_release_options() { :; }
    validate_configs() { :; }
    run_migrations() { :; }
    restart_admin_after_init() { printf 'restarted\n' >>"$capture"; }

    initialize_first_admin

    grep -qx 'password_env=set' "$capture"
    grep -qx 'arg=-init-admin' "$capture"
    grep -qx 'arg=-init-email' "$capture"
    grep -qx 'arg=admin@example.com' "$capture"
    grep -qx 'restarted' "$capture"
    if grep -q 'Diagnostic-password-123' "$capture"; then
        echo "FAIL: administrator password leaked into command arguments" >&2
        exit 1
    fi
)

test_full_deploy_starts_admin_before_business_services() {
    assert_eq "admin-server device-server user-server voip-server ai-server call-server" "$(select_all)" \
        "full deploy must make Admin available before business service startup"
}

test_supervisor_boot_order_starts_admin_first() {
    local config
    config="$(cd "$(dirname "$0")/.." && pwd)/deploy/supervisor/thing-connect.supervisor.conf"
    grep -A1 '^\[program:admin-server\]$' "$config" | grep -qx 'priority=10'
    grep -A1 '^\[program:device-server\]$' "$config" | grep -qx 'priority=20'
    grep -A1 '^\[program:user-server\]$' "$config" | grep -qx 'priority=20'
    grep -A1 '^\[program:voip-server\]$' "$config" | grep -qx 'priority=30'
    grep -A1 '^\[program:ai-server\]$' "$config" | grep -qx 'priority=30'
    grep -A1 '^\[program:call-server\]$' "$config" | grep -qx 'priority=30'
}

test_activated_bundle_is_resolved_and_not_overwritten() (
    local root="$TEST_ROOT/bundle-deploy"
    DEPLOY_ROOT="$root"
    BUILD_DIR="$root/source"
    mkdir -p "$BUILD_DIR/bin" "$BUILD_DIR/device-server" \
        "$DEPLOY_ROOT/config-current/device-server"
    printf '#!/usr/bin/env bash\n' >"$BUILD_DIR/bin/device-server"
    printf 'example: true\n' >"$BUILD_DIR/device-server/config.yaml.example"
    printf 'jwt_secret: bundled\n' >"$DEPLOY_ROOT/config-current/device-server/config.yaml"

    assert_eq "$DEPLOY_ROOT/config-current/device-server/config.yaml" \
        "$(service_config_path device-server)" \
        "activated bundle must resolve as the service config"
    deploy_one device-server
    if [ -e "$DEPLOY_ROOT/device-server/config.yaml" ]; then
        echo "FAIL: deploy created a direct config over the activated bundle" >&2
        exit 1
    fi
)

test_file_publish_never_creates_configuration() (
    local root="$TEST_ROOT/no-config-publish"
    DEPLOY_ROOT="$root"
    BUILD_DIR="$root/source"
    mkdir -p "$BUILD_DIR/bin"
    printf '#!/usr/bin/env bash\n' >"$BUILD_DIR/bin/device-server"

    deploy_one device-server

    [ ! -e "$DEPLOY_ROOT/device-server/config.yaml" ] || {
        echo "FAIL: daily file publish created a configuration" >&2
        exit 1
    }
)

test_deploy_uses_catalog_static_directory() (
    local root="$TEST_ROOT/catalog-static"
    DEPLOY_ROOT="$root"
    BUILD_DIR="$root/source"
    SERVICE_STATIC_DIR[metrics-server]="web/metrics-dist"
    mkdir -p "$BUILD_DIR/bin" "$BUILD_DIR/web/metrics-dist"
    printf '#!/usr/bin/env bash\n' >"$BUILD_DIR/bin/metrics-server"
    printf 'metrics-ui\n' >"$BUILD_DIR/web/metrics-dist/index.html"

    deploy_one metrics-server

    grep -qx 'metrics-ui' "$DEPLOY_ROOT/metrics-server/static/index.html"
)

test_deploy_requires_complete_current_build() (
    BUILD_DIR="$TEST_ROOT/build-guard"
    mkdir -p "$BUILD_DIR/bin"
    git() {
        if [[ "$*" == *"rev-parse HEAD"* ]]; then
            printf 'expected-commit\n'
        fi
    }
    for service in "${ALL_SERVICES[@]}"; do
        printf '#!/usr/bin/env bash\n' >"$BUILD_DIR/bin/$service"
        chmod +x "$BUILD_DIR/bin/$service"
    done
    printf 'stale-commit\n' >"$BUILD_DIR/bin/.release-commit"
    if validate_build_release "${ALL_SERVICES[@]}" >/dev/null 2>&1; then
        echo "FAIL: stale build marker was accepted" >&2
        exit 1
    fi
    printf 'expected-commit\n' >"$BUILD_DIR/bin/.release-commit"
    validate_build_release "${ALL_SERVICES[@]}"
)

test_deploy_lock_is_scoped_to_one_mutating_command() (
    local capture="$TEST_ROOT/deploy-lock.capture"
    full_deploy() {
        if flock -n "$DEPLOY_ROOT/deploy.lock" true; then
            echo "FAIL: update action did not hold the deployment lock" >&2
            return 1
        fi
        printf 'held\n' >"$capture"
    }

    run_named_command update
    assert_eq "held" "$(<"$capture")" "update command must execute under the deployment lock"
    flock -n "$DEPLOY_ROOT/deploy.lock" true || {
        echo "FAIL: deployment lock remained held after the command" >&2
        exit 1
    }
)

test_menu_starts_with_maintenance_update() {
    local output
    output="$(menu <<<'invalid')"
    [[ "$output" == *$'1) 维护发布更新（Supervisor 自动重启；无 Supervisor 时手动启停）\n2) 仅执行数据库迁移'* ]] || {
        echo "FAIL: maintenance update and migration are not the first menu actions" >&2
        exit 1
    }
    [[ "$output" == *$'4) 查看全部服务状态\n5) 重启并检查已安装服务\n6) 启动并检查已安装服务\n7) 停止已安装服务\n0) 退出'* ]] || {
        echo "FAIL: operations and exit are not in a coherent menu order" >&2
        exit 1
    }
    [[ "$output" != *"仅发布文件"* ]] || {
        echo "FAIL: unsafe partial publish command was exposed in the daily menu" >&2
        exit 1
    }
}

test_first_install_is_not_a_deploy_command() {
    local result=0 output
    output="$(run_named_command first-install 2>&1)" || result=$?
    assert_eq "2" "$result" "daily deploy script must reject first-install"
    [[ "$output" == *"未知命令: first-install"* ]] || {
        echo "FAIL: removed first-install command was not reported clearly" >&2
        exit 1
    }
}

test_pull_never_clones_missing_daily_source() (
    local result=0 output
    REPO_PATH="$TEST_ROOT/missing-source"
    output="$(pull_code 2>&1)" || result=$?
    assert_eq "1" "$result" "daily pull must fail when its managed source is missing"
    [[ "$output" == *"运行 install.sh 完成首次安装"* ]] || {
        echo "FAIL: missing daily source did not point to install.sh" >&2
        exit 1
    }
    [ ! -e "$REPO_PATH" ]
)

test_status_reports_every_service_even_when_one_is_missing() (
    local capture="$TEST_ROOT/status.capture" result=0
    SUPERVISORCTL="true"
    resolve_active_services() {
        ACTIVE_SERVICES=("${ALL_SERVICES[@]}")
        ACTIVE_BUSINESS_SERVICES=("${BUSINESS_SERVICES[@]}")
    }
    validate_service_manager() { ACTIVE_SERVICE_MANAGER="supervisor"; }
    status_one() {
        printf '%s\n' "$1" >>"$capture"
        [ "$1" != "user-server" ]
    }
    status_action || result=$?
    assert_eq "1" "$result" "status must return failure when a service is missing"
    assert_eq "$(select_all)" "$(tr '\n' ' ' <"$capture" | sed 's/ $//')" \
        "status must still inspect every service"
)

test_optional_services_are_not_treated_as_installed_without_config() (
    local root="$TEST_ROOT/optional-services"
    DEPLOY_ROOT="$root"
    mkdir -p "$root/admin-server" "$root/device-server" "$root/user-server"
    : >"$root/admin-server/config.yaml"
    : >"$root/device-server/config.yaml"
    : >"$root/user-server/config.yaml"

    resolve_active_services

    assert_eq "admin-server device-server user-server" "${ACTIVE_SERVICES[*]}" \
        "unconfigured optional services must not be deployed or started"
    assert_eq "device-server user-server admin-server" "${ACTIVE_STOP_ORDER[*]}" \
        "only installed services participate in the normal stop order"
)

test_migration_requires_runtime_target_match_and_reports_schema_change() (
    local capture="$TEST_ROOT/migration.capture"
    BUILD_DIR="$TEST_ROOT/migration-build"
    MIGRATION_CONFIG="$TEST_ROOT/migration-config.yaml"
    mkdir -p "$BUILD_DIR/bin"
    : >"$MIGRATION_CONFIG"
    cat >"$BUILD_DIR/bin/admin-server" <<'BASH'
#!/usr/bin/env bash
printf '%s\n' "$@" >"$MIGRATION_CAPTURE"
if [[ " $* " == *" -migration-check-only "* ]]; then
    printf 'migration_result=pending database migrations required\n'
    exit 0
fi
printf 'migration_result=changed database migrations applied\n'
BASH
    chmod +x "$BUILD_DIR/bin/admin-server"
    MIGRATION_CAPTURE="$capture"
    export MIGRATION_CAPTURE

    run_migrations

    assert_eq "1" "$MIGRATION_CHANGED" "changed migration must disable automatic binary rollback"
    grep -qx -- '-deploy-root' "$capture"
    grep -qx -- '-require-runtime-target' "$capture"
)

test_failed_migration_marker_is_treated_as_schema_change() (
    local result=0
    MIGRATION_CONFIG="$TEST_ROOT/migration-failure.yaml"
    mkdir -p "$BUILD_DIR/bin"
    : >"$MIGRATION_CONFIG"
    cat >"$BUILD_DIR/bin/admin-server" <<'BASH'
#!/usr/bin/env bash
if [[ " $* " == *" -migration-check-only "* ]]; then
    printf 'migration_result=pending database migrations required\n'
    exit 0
fi
printf 'migration_result=change_possible pending migrations detected\n'
printf 'migrate: injected DDL failure\n' >&2
exit 23
BASH
    chmod +x "$BUILD_DIR/bin/admin-server"

    run_migrations || result=$?

    assert_eq "23" "$result" "migration exit status must be preserved"
    assert_eq "1" "$MIGRATION_CHANGED" "failed migration after the pre-DDL marker may have changed schema"
)

test_interrupted_migration_with_change_marker_quiesces_services() (
	local capture="$TEST_ROOT/migration-interrupt.capture" result=0
	BUILD_DIR="$TEST_ROOT/migration-interrupt-build"
	MIGRATION_CONFIG="$TEST_ROOT/migration-interrupt.yaml"
	mkdir -p "$BUILD_DIR/bin"
	: >"$MIGRATION_CONFIG"
	cat >"$BUILD_DIR/bin/admin-server" <<'BASH'
#!/usr/bin/env bash
if [[ " $* " == *" -migration-check-only "* ]]; then
	printf 'migration_result=pending database migrations required\n'
	exit 0
fi
printf 'migration_result=change_possible pending migrations detected\n'
kill -TERM "$PPID"
sleep 5
BASH
	chmod +x "$BUILD_DIR/bin/admin-server"
	stop_one() { printf 'stopped=%s\n' "$1" >>"$capture"; }
	set +e
	( run_migrations )
	result=$?
	set -e

	assert_eq "143" "$result" "interrupted migration status must be preserved"
	for service in "${STOP_ORDER[@]}"; do
		grep -qx "stopped=$service" "$capture"
	done
)

test_standalone_migration_failure_quiesces_services() (
    local capture="$TEST_ROOT/migrate-quiesce.capture" result=0
    validate_paths() { return 0; }
    validate_release_options() { return 0; }
    run_migrations() { MIGRATION_CHANGED=1; return 17; }
    quiesce_after_schema_change() { printf 'quiesced\n' >"$capture"; }

    migrate_action || result=$?

    assert_eq "17" "$result" "standalone migration failure status must be preserved"
    grep -qx 'quiesced' "$capture"
)

test_schema_change_failure_never_auto_starts_old_binaries() (
    local capture="$TEST_ROOT/schema-rollback.capture" result=0
    MIGRATION_CHANGED=1
    mark_backup_failed() { printf 'marked\n' >>"$capture"; }
    rollback_release() { printf 'rolled-back\n' >>"$capture"; }
	stop_one() { printf 'stopped=%s\n' "$1" >>"$capture"; }

    rollback_active_release 7 || result=$?

    assert_eq "7" "$result" "release failure status must be preserved"
    grep -qx 'marked' "$capture"
	for service in "${STOP_ORDER[@]}"; do
		grep -qx "stopped=$service" "$capture"
	done
    if grep -q 'rolled-back' "$capture"; then
        echo "FAIL: old binaries were automatically restored after a schema change" >&2
        exit 1
    fi
)

test_daily_update_backs_up_files_before_database_migration() {
    local function_body backup_line migration_line
    function_body="$(declare -f full_deploy)"
    backup_line="$(grep -n 'backup_release' <<<"$function_body" | head -1 | cut -d: -f1)"
    migration_line="$(grep -n 'run_migrations' <<<"$function_body" | head -1 | cut -d: -f1)"
    [ -n "$backup_line" ] && [ -n "$migration_line" ] && [ "$backup_line" -lt "$migration_line" ] || {
        echo "FAIL: daily update must complete file backup before database migration" >&2
        exit 1
    }
}

test_http_admin_cookie_is_allowed_by_default
test_migration_refuses_missing_or_unverified_database_backup
test_current_database_skips_backup_gate
test_pending_database_still_requires_verified_backup
test_failed_migration_check_never_runs_ddl
test_yaml_section_headers_allow_valid_whitespace
test_dynamic_mqtt_is_not_required_in_base_yaml
test_missing_supervisor_selects_manual_service_control
test_partial_supervisor_inventory_is_rejected
test_explicit_manual_mode_rejects_existing_supervisor_services
test_manual_update_requires_local_services_to_be_stopped
test_manual_update_publishes_without_automatic_restart
test_same_release_commit_skips_the_entire_update
test_release_state_records_commit_and_installed_services
test_force_update_and_service_changes_bypass_same_commit_marker
test_missing_or_invalid_release_state_continues_update
test_failed_same_commit_repair_invalidates_previous_release_state
test_publish_deploy_script_replaces_root_entry_atomically
test_publish_local_service_script_replaces_root_entry_atomically
test_publish_deploy_script_keeps_previous_entry_on_invalid_source
test_running_admin_is_restarted_after_init
test_initialize_admin_is_noninteractive_and_refreshes_running_service
test_full_deploy_starts_admin_before_business_services
test_supervisor_boot_order_starts_admin_first
test_activated_bundle_is_resolved_and_not_overwritten
test_file_publish_never_creates_configuration
test_deploy_uses_catalog_static_directory
test_deploy_requires_complete_current_build
test_deploy_lock_is_scoped_to_one_mutating_command
test_menu_starts_with_maintenance_update
test_first_install_is_not_a_deploy_command
test_pull_never_clones_missing_daily_source
test_optional_services_are_not_treated_as_installed_without_config
test_status_reports_every_service_even_when_one_is_missing
test_migration_requires_runtime_target_match_and_reports_schema_change
test_failed_migration_marker_is_treated_as_schema_change
test_interrupted_migration_with_change_marker_quiesces_services
test_standalone_migration_failure_quiesces_services
test_schema_change_failure_never_auto_starts_old_binaries
test_daily_update_backs_up_files_before_database_migration
echo "PASS: deploy-prod.sh"
