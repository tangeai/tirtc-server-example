#!/usr/bin/env bash

set -Eeuo pipefail

TEST_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TEST_ROOT"' EXIT

DEPLOY_ROOT="$TEST_ROOT"
source "$(cd "$(dirname "$0")" && pwd)/deploy-prod.sh"

assert_eq() {
    local want="$1" got="$2" message="$3"
    if [ "$got" != "$want" ]; then
        echo "FAIL: $message: got '$got', want '$want'" >&2
        exit 1
    fi
}

test_yaml_section_headers_allow_valid_whitespace() {
    local cfg="$TEST_ROOT/whitespace.yaml"
    cat >"$cfg" <<'YAML'
internal:  
  key: "shared-key"
admin:  # valid YAML comment
  server_url: "http://127.0.0.1:9010"
YAML

    assert_eq "shared-key" "$(yaml_section_value "$cfg" internal key)" \
        "section parser must accept trailing spaces"
    assert_eq "http://127.0.0.1:9010" "$(yaml_section_value "$cfg" admin server_url)" \
        "section parser must accept an inline comment"
    yaml_has_section_key "$cfg" internal key || {
        echo "FAIL: section-key detection must accept trailing spaces" >&2
        exit 1
    }
}

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
    ADMIN_INIT_PASSWORD="diagnostic-password-123"
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
    if grep -q 'diagnostic-password-123' "$capture"; then
        echo "FAIL: administrator password leaked into command arguments" >&2
        exit 1
    fi
)

test_full_deploy_starts_admin_before_business_services() {
    assert_eq "admin-server device-server user-server voip-server ai-server call-server" "$(select_all)" \
        "full deploy must make Admin available before business service startup"
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

test_menu_orders_lifecycle_actions_first() {
    local output
    output="$(menu <<<'invalid')"
    [[ "$output" == *$'1) 首次 Web 安装（仅空部署）\n2) 日常更新部署'* ]] || {
        echo "FAIL: first install and daily update are not the first menu actions" >&2
        exit 1
    }
    [[ "$output" == *$'11) 重启已安装服务\n12) 查看全部服务状态\n0) 退出'* ]] || {
        echo "FAIL: operations and exit are not in a coherent menu order" >&2
        exit 1
    }
}

test_status_reports_every_service_even_when_one_is_missing() (
    local capture="$TEST_ROOT/status.capture" result=0
    SUPERVISORCTL="true"
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

test_yaml_section_headers_allow_valid_whitespace
test_running_admin_is_restarted_after_init
test_initialize_admin_is_noninteractive_and_refreshes_running_service
test_full_deploy_starts_admin_before_business_services
test_activated_bundle_is_resolved_and_not_overwritten
test_deploy_requires_complete_current_build
test_deploy_lock_is_scoped_to_one_mutating_command
test_menu_orders_lifecycle_actions_first
test_optional_services_are_not_treated_as_installed_without_config
test_status_reports_every_service_even_when_one_is_missing
test_migration_requires_runtime_target_match_and_reports_schema_change
test_failed_migration_marker_is_treated_as_schema_change
test_interrupted_migration_with_change_marker_quiesces_services
test_standalone_migration_failure_quiesces_services
test_schema_change_failure_never_auto_starts_old_binaries
test_daily_update_backs_up_files_before_database_migration
echo "PASS: deploy-prod.sh"
