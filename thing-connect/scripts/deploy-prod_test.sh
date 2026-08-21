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

test_yaml_section_headers_allow_valid_whitespace
test_running_admin_is_restarted_after_init
test_initialize_admin_is_noninteractive_and_refreshes_running_service
test_full_deploy_starts_admin_before_business_services
echo "PASS: deploy-prod.sh"
