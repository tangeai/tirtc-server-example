#!/usr/bin/env bash
set -Eeuo pipefail
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TEST_ROOT"' EXIT
SCRIPTS="$(cd -- "$(dirname -- "$0")" && pwd)"
FIXTURE="$TEST_ROOT/source with spaces"
mkdir -p "$FIXTURE/thing-connect/scripts"
git -C "$FIXTURE" -c init.templateDir= init -q
cp "$SCRIPTS/"{deploy-local,install,deploy-prod,service-catalog}.sh "$FIXTURE/thing-connect/scripts/"
mkdir -p "$FIXTURE/thing-connect/internal/installer"
cp "$SCRIPTS/../internal/installer/service_catalog.tsv" "$FIXTURE/thing-connect/internal/installer/"
printf 'module fixture\n' >"$FIXTURE/thing-connect/go.mod"
printf 'bin/\nnode_modules/\nconfig.yaml\n' >"$FIXTURE/.gitignore"
cat >"$FIXTURE/thing-connect/build.sh" <<'BUILD'
#!/usr/bin/env bash
set -Eeuo pipefail
mkdir -p bin user-server/static ai-server/static admin/admin-web/dist
for service in admin-server device-server user-server ai-server call-server voip-server; do
    printf '#!/usr/bin/env bash\nexit 0\n' >"bin/$service"
    chmod +x "bin/$service"
done
git rev-parse HEAD >bin/.release-commit
BUILD
chmod +x "$FIXTURE/thing-connect/build.sh"
git -C "$FIXTURE" add --all
git -C "$FIXTURE" -c user.name=test -c user.email=test@localhost -c commit.gpgSign=false commit -qm fixture
printf 'local change\n' >>"$FIXTURE/thing-connect/go.mod"
printf 'untracked feature\n' >"$FIXTURE/thing-connect/new-feature.go"
printf 'private secret\n' >"$FIXTURE/thing-connect/config.yaml"
BEFORE="$(git -C "$FIXTURE" status --porcelain)"

run() { env SOURCE_ROOT="$FIXTURE" DEPLOY_ROOT="$TEST_ROOT/deploy with spaces" "$SCRIPTS/deploy-local.sh" "$@"; }
run help >"$TEST_ROOT/help"
rg -q -- '--archive-existing' "$TEST_ROOT/help"
if rg -q 'DATABASE_BACKUP_FILE|DATABASE_BACKUP_RESTORE_VERIFIED|恢复演练确认' "$TEST_ROOT/help"; then
    echo 'FAIL: local update help must not require the production backup gate' >&2
    exit 1
fi
OVERRIDE_LINE="$(grep -n '^    validate_database_backup()' "$SCRIPTS/deploy-local.sh" | head -1 | cut -d: -f1)"
DEPLOY_LINE="$(grep -n '^    full_deploy$' "$SCRIPTS/deploy-local.sh" | head -1 | cut -d: -f1)"
[ -n "$OVERRIDE_LINE" ] && [ -n "$DEPLOY_LINE" ] && [ "$OVERRIDE_LINE" -lt "$DEPLOY_LINE" ]
echo 'PASS: local update bypasses only the production backup restore gate'
run prepare >"$TEST_ROOT/prepare.log" 2>&1
SNAPSHOT="$(find "$TEST_ROOT/deploy with spaces.sources" -mindepth 1 -maxdepth 1 -type d)"
[ -f "$SNAPSHOT/repository/thing-connect/new-feature.go" ]
[ ! -f "$SNAPSHOT/repository/thing-connect/config.yaml" ]
rg -q 'local change' "$SNAPSHOT/repository/thing-connect/go.mod"
[ "$(git -C "$FIXTURE" status --porcelain)" = "$BEFORE" ]
[ "$(stat -c %a "$SNAPSHOT")" = 700 ]
sha256sum -c "$SNAPSHOT/source.sha256" >/dev/null
(cd "$SNAPSHOT/repository/thing-connect" && sha256sum -c "$SNAPSHOT/artifacts.sha256" >/dev/null)
echo 'PASS: snapshot includes local changes, excludes credentials, preserves source and validates digests'

if run nonsense >/dev/null 2>&1; then exit 1; fi
if run prepare --archive-existing >/dev/null 2>&1; then exit 1; fi
if DEPLOY_ROOT=/opt SOURCE_ROOT="$FIXTURE" "$SCRIPTS/deploy-local.sh" prepare >/dev/null 2>&1; then exit 1; fi
echo 'PASS: invalid command, option and root rejected'

mkdir -p "$TEST_ROOT/deploy with spaces/var/installer"
printf 'keep\n' >"$TEST_ROOT/deploy with spaces/var/installer/installed.json"
if run install >"$TEST_ROOT/install.log" 2>&1; then exit 1; fi
rg -q 'keep' "$TEST_ROOT/deploy with spaces/var/installer/installed.json"
echo 'PASS: installed instance cannot be overwritten by install'

if run update >"$TEST_ROOT/update.log" 2>&1; then exit 1; fi
rg -q 'config-current/admin-server/config.yaml' "$TEST_ROOT/update.log"
echo 'PASS: update defaults to the installed Admin database configuration'

(
    exec 8>"$TEST_ROOT/deploy with spaces/deploy.lock"
    flock -n 8
    if run prepare >"$TEST_ROOT/lock.log" 2>&1; then exit 1; fi
    rg -q '另一个安装或发布' "$TEST_ROOT/lock.log"
)
echo 'PASS: deployment lock prevents concurrent mutation'

ln -s /etc/passwd "$FIXTURE/thing-connect/external-link"
if run prepare >"$TEST_ROOT/symlink.log" 2>&1; then exit 1; fi
rg -q '符号链接' "$TEST_ROOT/symlink.log"
rm "$FIXTURE/thing-connect/external-link"
echo 'PASS: external symlinks cannot enter source snapshot'

cat >"$FIXTURE/thing-connect/build.sh" <<'BUILD'
#!/usr/bin/env bash
exit 42
BUILD
mkdir -p "$TEST_ROOT/deploy with spaces/call-server"
printf 'old executable\n' >"$TEST_ROOT/deploy with spaces/call-server/call-server"
if run prepare >"$TEST_ROOT/build-failure.log" 2>&1; then exit 1; fi
rg -q '构建失败' "$TEST_ROOT/build-failure.log"
rg -q 'old executable' "$TEST_ROOT/deploy with spaces/call-server/call-server"
echo 'PASS: failed build preserves deployed files'
# Exercise the directory cutover without launching fixture services.
ARCHIVE_ROOT="$TEST_ROOT/archive target"
mkdir -p "$ARCHIVE_ROOT" "$TEST_ROOT/archive-snapshot/repository/thing-connect/scripts"
printf 'old config\n' >"$ARCHIVE_ROOT/config-preserved"
cp "$SCRIPTS/"{install,deploy-local}.sh "$TEST_ROOT/archive-snapshot/repository/thing-connect/scripts/"
cat >>"$TEST_ROOT/archive-snapshot/repository/thing-connect/scripts/install.sh" <<'STUB'
run_install() { printf 'published\n' >"$DEPLOY_ROOT/published"; }
STUB
export ARCHIVE_ROOT TEST_ROOT FIXTURE SCRIPTS
bash -c '
    source "$SCRIPTS/deploy-local.sh"
    LOCAL_SOURCE_ROOT="$FIXTURE"
    LOCAL_DEPLOY_ROOT="$ARCHIVE_ROOT"
    local_require_stopped() { :; }
    local_prepare() { LOCAL_SNAPSHOT="$TEST_ROOT/archive-snapshot"; }
    local_main install --archive-existing
' >"$TEST_ROOT/archive.log" 2>&1
ARCHIVE="$(find "$TEST_ROOT" -maxdepth 1 -type d -name 'archive target.backup-*')"
rg -q 'old config' "$ARCHIVE/config-preserved"
[ -f "$ARCHIVE_ROOT/published" ]
[ "$(stat -c %i "$ARCHIVE/deploy.lock")" = "$(stat -c %i "$ARCHIVE_ROOT/deploy.lock")" ]
[ -f "$ARCHIVE_ROOT/local-release-path" ]
echo 'PASS: archive preserves old files and lock while publishing new installation'

# Failed migration preflight must not publish files or touch old configuration.
printf 'placeholder\n' >"$TEST_ROOT/migration.yaml"
mkdir -p "$TEST_ROOT/preflight-snapshot/repository/thing-connect/bin"
printf '#!/usr/bin/env bash\nexit 49\n' >"$TEST_ROOT/preflight-snapshot/repository/thing-connect/bin/admin-server"
chmod +x "$TEST_ROOT/preflight-snapshot/repository/thing-connect/bin/admin-server"
if MIGRATION_CONFIG="$TEST_ROOT/migration.yaml" bash -c '
    source "$SCRIPTS/deploy-local.sh"
    LOCAL_SOURCE_ROOT="$FIXTURE"
    LOCAL_DEPLOY_ROOT="$ARCHIVE_ROOT"
    local_prepare() { LOCAL_SNAPSHOT="$TEST_ROOT/preflight-snapshot"; }
    local_main update
' >"$TEST_ROOT/preflight.log" 2>&1; then exit 1; fi
rg -q '数据库预检失败' "$TEST_ROOT/preflight.log"
rg -q 'published' "$ARCHIVE_ROOT/published"
echo 'PASS: migration incompatibility leaves deployed files intact'
echo 'PASS: deploy-local.sh'
