#!/usr/bin/env bash

set -Eeuo pipefail

TEST_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TEST_ROOT"' EXIT

DEPLOY_ROOT="$TEST_ROOT/deploy"
REPO_PATH="$TEST_ROOT/repository"
BUILD_DIR="$REPO_PATH/thing-connect"
source "$(cd "$(dirname "$0")" && pwd)/install.sh"

assert_eq() {
    local want="$1" got="$2" message="$3"
    if [ "$got" != "$want" ]; then
        echo "FAIL: $message: got '$got', want '$want'" >&2
        exit 1
    fi
}

make_release_fixture() {
    local service
    mkdir -p "$BUILD_DIR/bin" "$BUILD_DIR/user-server/static" \
        "$BUILD_DIR/ai-server/static" "$BUILD_DIR/admin/admin-web/dist"
    for service in "${ALL_SERVICES[@]}"; do
        printf '#!/usr/bin/env bash\n' >"$BUILD_DIR/bin/$service"
        chmod +x "$BUILD_DIR/bin/$service"
    done
    printf 'user\n' >"$BUILD_DIR/user-server/static/index.html"
    printf 'ai\n' >"$BUILD_DIR/ai-server/static/index.html"
    printf 'admin\n' >"$BUILD_DIR/admin/admin-web/dist/index.html"
}

test_install_help_is_single_purpose() {
    local output
    output="$(usage)"
    [[ "$output" == *"仅用于空服务器的首次安装"* ]]
    [[ "$output" == *"deploy-prod.sh"* ]]
    [[ "$output" != *"日常更新："* ]]
}

test_installed_deployment_is_never_reopened() (
    DEPLOY_ROOT="$TEST_ROOT/already-installed"
    mkdir -p "$DEPLOY_ROOT/var/installer"
    : >"$DEPLOY_ROOT/var/installer/installed.json"
    if validate_empty_deployment >/dev/null 2>&1; then
        echo "FAIL: installed deployment was accepted as empty" >&2
        exit 1
    fi
)

test_broken_installed_marker_fails_closed() (
    DEPLOY_ROOT="$TEST_ROOT/broken-installed-marker"
    mkdir -p "$DEPLOY_ROOT/var/installer"
    ln -s missing-installed-state "$DEPLOY_ROOT/var/installer/installed.json"
    if validate_empty_deployment >/dev/null 2>&1; then
        echo "FAIL: broken installed marker reopened first-run setup" >&2
        exit 1
    fi
)

test_active_bundle_is_sent_to_recovery_instead_of_reinstall() (
    DEPLOY_ROOT="$TEST_ROOT/active-bundle"
    mkdir -p "$DEPLOY_ROOT/config-releases/revision"
    ln -s config-releases/revision "$DEPLOY_ROOT/config-current"
    local output
    if output="$(validate_empty_deployment 2>&1)"; then
        echo "FAIL: active bundle was accepted as a fresh deployment" >&2
        exit 1
    fi
    [[ "$output" == *"启动 Admin 让安装器自动恢复"* ]]
)

test_first_publish_never_creates_example_configs() (
    DEPLOY_ROOT="$TEST_ROOT/published"
    BUILD_DIR="$TEST_ROOT/release-fixture"
    make_release_fixture
    publish_release
    local service
    for service in "${ALL_SERVICES[@]}"; do
        [ -x "$DEPLOY_ROOT/$service/$service" ]
        [ ! -e "$DEPLOY_ROOT/$service/config.yaml" ]
    done
    grep -qx 'user' "$DEPLOY_ROOT/user-server/static/index.html"
    grep -qx 'ai' "$DEPLOY_ROOT/ai-server/static/index.html"
    grep -qx 'admin' "$DEPLOY_ROOT/admin-server/static/index.html"
)

test_source_is_cloned_by_installer() (
    local origin="$TEST_ROOT/source-origin"
    DEPLOY_ROOT="$TEST_ROOT/source-clone-deploy"
    REPO_PATH="$DEPLOY_ROOT/tirtc-server-example"
    BUILD_DIR="$REPO_PATH/thing-connect"
    mkdir -p "$origin/thing-connect"
    git -C "$origin" init -q -b main
    printf 'module example.invalid/fixture\n\ngo 1.21\n' >"$origin/thing-connect/go.mod"
    printf '#!/usr/bin/env bash\nexit 0\n' >"$origin/thing-connect/build.sh"
    chmod +x "$origin/thing-connect/build.sh"
    git -C "$origin" add thing-connect
    git -C "$origin" -c user.name=installer-test -c user.email=installer@example.invalid \
        commit -q -m fixture
    REPO_URL="$origin"

    pull_source >/dev/null

    [ -d "$REPO_PATH/.git" ]
    [ -x "$BUILD_DIR/build.sh" ]
    [ -z "$(git -C "$REPO_PATH" status --porcelain)" ]
)

test_non_git_source_is_never_overwritten() (
    DEPLOY_ROOT="$TEST_ROOT/non-git-deploy"
    REPO_PATH="$DEPLOY_ROOT/tirtc-server-example"
    BUILD_DIR="$REPO_PATH/thing-connect"
    mkdir -p "$REPO_PATH"
    printf 'keep\n' >"$REPO_PATH/operator-file"

    if pull_source >/dev/null 2>&1; then
        echo "FAIL: non-Git source path was accepted" >&2
        exit 1
    fi
    grep -qx keep "$REPO_PATH/operator-file"
)

test_dirty_source_is_never_updated() (
    DEPLOY_ROOT="$TEST_ROOT/source-clone-deploy"
    REPO_PATH="$DEPLOY_ROOT/tirtc-server-example"
    BUILD_DIR="$REPO_PATH/thing-connect"
    printf '\noperator change\n' >>"$BUILD_DIR/go.mod"

    if pull_source >/dev/null 2>&1; then
        echo "FAIL: dirty Git source was updated" >&2
        exit 1
    fi
    grep -qx 'operator change' "$BUILD_DIR/go.mod"
)

test_operational_scripts_are_published_atomically() (
    local source_scripts
    source_scripts="$(cd "$(dirname "$0")" && pwd)"
    DEPLOY_ROOT="$TEST_ROOT/published-entries"
    BUILD_DIR="$TEST_ROOT/entry-build"
    LOCAL_CONTROLLER="$DEPLOY_ROOT/service-local.sh"
    mkdir -p "$DEPLOY_ROOT" "$BUILD_DIR/scripts"
    cp "$source_scripts/install.sh" "$BUILD_DIR/scripts/install.sh"
    cp "$source_scripts/deploy-prod.sh" "$BUILD_DIR/scripts/deploy-prod.sh"
    cp "$source_scripts/service-local.sh" "$BUILD_DIR/scripts/service-local.sh"

    publish_install_script
    publish_deploy_script
    publish_local_controller

    [ -x "$DEPLOY_ROOT/install.sh" ]
    [ -x "$DEPLOY_ROOT/deploy-prod.sh" ]
    [ -x "$DEPLOY_ROOT/service-local.sh" ]
    bash -n "$DEPLOY_ROOT/install.sh"
    bash -n "$DEPLOY_ROOT/deploy-prod.sh"
    bash -n "$DEPLOY_ROOT/service-local.sh"
)

test_failed_setup_start_stops_admin_runner() (
    local capture="$TEST_ROOT/setup-failure.capture"
    LOCAL_CONTROLLER="$TEST_ROOT/fake-local-controller"
    export LOCAL_CAPTURE="$capture"
    printf '%s\n' \
        '#!/usr/bin/env bash' \
        'printf "%s\n" "$*" >>"$LOCAL_CAPTURE"' \
        'if [ "$1" = status ]; then printf "thing-connect:admin-server STOPPED\n"; fi' \
        >"$LOCAL_CONTROLLER"
    chmod +x "$LOCAL_CONTROLLER"
    wait_for_admin() { return 1; }

    if start_setup_server >/dev/null 2>&1; then
        echo "FAIL: failed setup liveness was treated as success" >&2
        exit 1
    fi
    tail -1 "$capture" | grep -qx 'stop thing-connect:admin-server'
)

test_install_flow_stays_short_and_publishes_daily_entry() (
    local capture="$TEST_ROOT/install-flow.capture"
    require_commands() { printf 'commands\n' >>"$capture"; }
    validate_empty_deployment() { printf 'empty\n' >>"$capture"; }
    pull_source() { printf 'pull\n' >>"$capture"; }
    build_release() { printf 'build\n' >>"$capture"; }
    publish_release() { printf 'publish\n' >>"$capture"; }
    publish_deploy_script() { printf 'deploy-entry\n' >>"$capture"; }
    publish_install_script() { printf 'install-entry\n' >>"$capture"; }
    publish_local_controller() { printf 'local-entry\n' >>"$capture"; }
    prepare_setup() { printf 'token\n'; printf 'prepare\n' >>"$capture"; }
    start_setup_server() { printf 'start-setup\n' >>"$capture"; }

    run_install >/dev/null

    assert_eq $'commands\nempty\npull\nbuild\npublish\ndeploy-entry\ninstall-entry\nlocal-entry\nprepare\nstart-setup' \
        "$(<"$capture")" "first install flow"
)

test_install_help_is_single_purpose
test_installed_deployment_is_never_reopened
test_broken_installed_marker_fails_closed
test_active_bundle_is_sent_to_recovery_instead_of_reinstall
test_first_publish_never_creates_example_configs
test_source_is_cloned_by_installer
test_non_git_source_is_never_overwritten
test_dirty_source_is_never_updated
test_operational_scripts_are_published_atomically
test_failed_setup_start_stops_admin_runner
test_install_flow_stays_short_and_publishes_daily_entry
echo "PASS: install.sh"
