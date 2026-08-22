package installer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	adminapp "thing-connect/internal/admin"

	"github.com/google/uuid"
)

func (b *Bootstrap) Preview(ctx context.Context, draft Draft) (Plan, error) {
	if b.database == nil || b.probes == nil {
		return Plan{}, fmt.Errorf("installer dependencies are incomplete")
	}
	if err := validateDraft(draft); err != nil {
		return Plan{}, err
	}
	if err := b.probes.Probe(ctx, draft); err != nil {
		return Plan{}, err
	}
	assessment, err := b.database.Inspect(ctx, draft.Database)
	if err != nil {
		return Plan{}, previewDatabaseError(err)
	}
	if assessment.CreateAdmin {
		if err := adminapp.ValidateAdminPassword(draft.Admin.Password); err != nil {
			return Plan{}, fmt.Errorf("%w: %w", ErrInvalidInput, err)
		}
	}
	if (assessment.Class == DatabaseManagedOlder || assessment.Class == DatabaseManagedCurrent) &&
		!b.canResumeDatabase(draft.Database.Name, assessment) {
		return Plan{}, ErrExistingDatabase
	}
	plan := Plan{Database: assessment}
	switch assessment.Class {
	case DatabaseAbsent:
		plan.Actions = append(plan.Actions, "创建数据库", "初始化 ThingConnect 表")
	case DatabaseEmpty:
		plan.Actions = append(plan.Actions, "初始化空数据库中的 ThingConnect 表")
	case DatabaseManagedOlder:
		plan.Actions = append(plan.Actions, "恢复本机同一未完成安装任务，并执行其缺失迁移")
	case DatabaseManagedCurrent:
		plan.Actions = append(plan.Actions, "恢复本机同一未完成安装任务；不修改已有业务数据")
	default:
		return Plan{}, classificationProblem(assessment.Class)
	}
	if assessment.CreateAdmin {
		plan.Actions = append(plan.Actions, "创建首个超级管理员")
	} else {
		plan.Warnings = append(plan.Warnings, "数据库中已有管理员，安装器不会修改账号或密码")
	}
	plan.Actions = append(plan.Actions,
		"生成并原子发布 Admin 与五个业务服务的基础配置",
		"重启并验收 Admin；业务服务保持停止，待后台配置完成后由服务器脚本启动",
	)
	digest, err := planDigest(draft, assessment)
	if err != nil {
		return Plan{}, err
	}
	plan.Digest = digest
	return plan, nil
}

func previewDatabaseError(err error) error {
	switch {
	case errors.Is(err, ErrInvalidInput),
		errors.Is(err, ErrAlreadyInstalled),
		errors.Is(err, ErrUnknownDatabase),
		errors.Is(err, ErrExistingDatabase),
		errors.Is(err, ErrSchemaFuture),
		errors.Is(err, ErrSchemaDrift):
		return err
	default:
		return fmt.Errorf("%w: %w", ErrMySQLUnavailable, err)
	}
}

func (b *Bootstrap) canResumeDatabase(databaseName string, assessment DatabaseAssessment) bool {
	operationID := strings.TrimSpace(assessment.RecoveryOperationID)
	if operationID == "" {
		return false
	}
	state, err := b.loadJournal()
	if err != nil {
		return false
	}
	return state.OperationID == operationID && state.DatabaseName == databaseName
}

func (b *Bootstrap) Execute(ctx context.Context, request ExecuteRequest) (Snapshot, error) {
	mode, err := DetectMode(b.opts)
	if err != nil {
		return Snapshot{}, err
	}
	if mode == ModeInstalled {
		return Snapshot{}, ErrAlreadyInstalled
	}
	if existing, loadErr := b.loadJournal(); loadErr == nil && existing.ConfigDigest != "" {
		if b.bundles != nil {
			if receipt, active := b.bundles.Active(existing.OperationID); active && receipt.Digest == existing.ConfigDigest && request.Draft != nil {
				// Once activation occurred, accepting credentials again could regress
				// a durable operation. Normal Admin repairs the DB acknowledgement.
				return existing.Snapshot, ErrInstallBusy
			}
		}
		if request.Draft == nil {
			return b.ReconcileRuntime(ctx)
		}
	}
	if request.Draft == nil {
		return b.ReconcileRuntime(ctx)
	}
	plan, err := b.Preview(ctx, *request.Draft)
	if err != nil {
		return Snapshot{}, err
	}
	if request.PlanDigest == "" || request.PlanDigest != plan.Digest {
		return Snapshot{}, ErrPlanStale
	}
	b.mu.Lock()
	if b.running || b.closed {
		b.mu.Unlock()
		return Snapshot{}, ErrInstallBusy
	}
	operationID := uuid.NewString()
	instanceID := uuid.NewString()
	if existing, loadErr := b.loadJournal(); loadErr == nil && existing.OperationID != "" {
		operationID = existing.OperationID
		instanceID = existing.InstanceID
		if instanceID == "" {
			instanceID = uuid.NewString()
		}
		if existing.DatabaseName != "" && existing.DatabaseName != request.Draft.Database.Name && existing.Percent >= 20 {
			b.mu.Unlock()
			return Snapshot{}, fmt.Errorf("%w: 数据库已经被安装流程领取，不能更换", ErrPlanStale)
		}
	}
	state := journal{Snapshot: Snapshot{
		Mode: ModeRecovery, OperationID: operationID, Phase: "validating", Percent: 5,
		Message: "正在验证安装计划", NeedsToken: true, UpdatedAt: b.now().UTC(),
	}, DatabaseName: request.Draft.Database.Name, InstanceID: instanceID,
		EnabledServices: installOptionalServices()}
	if err := b.writeJournalUnlocked(state); err != nil {
		b.mu.Unlock()
		return Snapshot{}, err
	}
	b.running = true
	b.wg.Add(1)
	b.mu.Unlock()
	draft := *request.Draft
	go b.runInstall(b.ctx, draft, plan, state)
	return state.Snapshot, nil
}

func (b *Bootstrap) runInstall(ctx context.Context, draft Draft, expected Plan, state journal) {
	defer func() {
		b.mu.Lock()
		b.running = false
		b.mu.Unlock()
		b.wg.Done()
	}()
	lockFile, err := acquireFileLock(b.opts.DeployLockPath())
	if err != nil {
		b.fail(state, "INSTALL_BUSY", "另一个安装或发布任务正在运行", true, err)
		return
	}
	defer releaseFileLock(lockFile)
	b.progress(&state, "dependencies_verified", 15, "依赖连接检查通过")
	claim, err := b.database.Claim(ctx, draft.Database, state.OperationID, state.InstanceID)
	if err != nil {
		b.fail(state, problemCode(err), safeMessage(err), true, err)
		return
	}
	defer func() { _ = claim.Close() }()
	if !sameAssessment(expected.Database, claim.Assessment()) {
		b.fail(state, "PLAN_STALE", "数据库状态已经变化，请重新预检", true, ErrPlanStale)
		return
	}
	state.InstanceID = claim.InstanceID()
	b.progress(&state, "database_claimed", 25, "已取得数据库安装所有权")
	if err := claim.Prepare(ctx, draft.Admin, installOptionalServices()); err != nil {
		b.fail(state, problemCode(err), safeMessage(err), true, err)
		return
	}
	b.progress(&state, "admin_ready", 55, "数据库结构和首个管理员已就绪")
	if b.bundles == nil {
		b.fail(state, "BUNDLE_PUBLISH_FAILED", "配置发布组件不可用", true, errors.New("bundle publisher is nil"))
		return
	}
	receipt, err := b.bundles.Prepare(ctx, draft, state.OperationID)
	if err != nil {
		b.fail(state, "BUNDLE_PUBLISH_FAILED", "启动配置发布失败，请检查磁盘和目录权限", true, err)
		return
	}
	if !sameStrings(state.EnabledServices, receipt.OptionalServices) {
		// The immutable manifest is authoritative once a revision exists. Keep
		// the journal aligned even though this attempt supplied a stale choice.
		state.EnabledServices = append([]string(nil), receipt.OptionalServices...)
		b.fail(state, "PLAN_STALE", "已存在配置 revision 的服务选择与当前计划不一致，请按原选择重试", true, ErrPlanStale)
		return
	}
	state.ConfigDigest = receipt.Digest
	state.Phase = "config_prepared"
	state.Percent = 60
	state.Message = "配置 revision 已落盘，正在提交数据库激活意图"
	state.UpdatedAt = b.now().UTC()
	if err := b.writeJournal(state); err != nil {
		b.fail(state, "BUNDLE_PUBLISH_FAILED", "配置 revision 已落盘，但恢复状态写入失败", true, err)
		return
	}
	// This row is the transactional outbox/activation intent. The active
	// filesystem pointer may only change after this durable DB commit.
	if err := claim.Record(ctx, "config_activation_pending", "installing", receipt.Digest); err != nil {
		b.fail(state, problemCode(err), safeMessage(err), true, err)
		return
	}
	b.progress(&state, "config_activation_pending", 64, "数据库激活意图已提交，正在原子切换配置")
	if err := b.bundles.Activate(ctx, state.OperationID, receipt.Digest); err != nil {
		b.fail(state, "BUNDLE_PUBLISH_FAILED", "配置激活失败，可安全重试当前安装任务", true, err)
		return
	}
	state.Phase = "config_activated"
	state.Percent = 68
	state.Message = "配置已激活，正在确认数据库安装状态"
	state.UpdatedAt = b.now().UTC()
	if err := b.writeJournal(state); err != nil {
		b.fail(state, "BUNDLE_PUBLISH_FAILED", "配置已激活，但恢复状态写入失败；正在切换到正常 Admin 修复", true, err)
		b.requestRestart()
		return
	}
	if err := claim.Record(ctx, "config_committed", "installing", receipt.Digest); err != nil {
		b.fail(state, problemCode(err), safeMessage(err), true, err)
		b.requestRestart()
		return
	}
	b.progress(&state, "awaiting_admin_restart", 70, "配置已安全提交，正在切换到正常 Admin")
	b.requestRestart()
}

func (b *Bootstrap) requestRestart() {
	select {
	case b.restart <- struct{}{}:
	default:
	}
}

// ReconcileRuntime repairs durable configuration state after Admin has loaded
// the active runtime configuration. It seals the Admin-only installation but
// never starts a business service; host-side process management owns that step.
func (b *Bootstrap) ReconcileRuntime(ctx context.Context) (Snapshot, error) {
	state, found, err := b.reconcileRuntimeState(ctx)
	if err != nil {
		return state.Snapshot, err
	}
	if !found {
		return Snapshot{Mode: ModeNormal, Message: "Admin 已就绪；业务服务由部署环境独立管理"}, nil
	}
	return state.Snapshot, nil
}

func (b *Bootstrap) reconcileRuntimeState(ctx context.Context) (journal, bool, error) {
	state, err := b.loadJournal()
	if err != nil {
		if os.IsNotExist(err) {
			return journal{}, false, nil
		}
		return journal{}, false, err
	}
	if state.OperationID == "" {
		return state, true, fmt.Errorf("%w: 安装任务标识缺失", ErrSchemaDrift)
	}
	if b.bundles == nil {
		return state, true, fmt.Errorf("%w: 配置发布组件不可用", ErrSchemaDrift)
	}
	receipt, active := b.bundles.Active(state.OperationID)
	if !active {
		var prepared bool
		receipt, prepared = b.bundles.Prepared(state.OperationID)
		if !prepared {
			return state, true, fmt.Errorf("%w: 找不到完整的配置 revision", ErrPlanStale)
		}
	}
	if state.ConfigDigest == "" || state.ConfigDigest != receipt.Digest {
		return state, true, fmt.Errorf("%w: 配置摘要与安装任务不一致", ErrSchemaDrift)
	}
	if !sameStrings(state.EnabledServices, receipt.OptionalServices) {
		return state, true, fmt.Errorf("%w: 服务选择与配置 revision 不一致", ErrSchemaDrift)
	}
	state.ConfigDigest = receipt.Digest
	if state.Phase == "installed" {
		state.Mode = ModeInstalled
		state.NeedsToken = false
		state.CanResume = false
		return state, true, nil
	}
	if b.database == nil {
		return state, true, fmt.Errorf("%w: 运行数据库连接不可用", ErrSchemaDrift)
	}
	runtimeDSN := strings.TrimSpace(b.opts.RuntimeDatabaseDSN)
	if runtimeDSN == "" {
		runtimeDSN = strings.TrimSpace(receipt.RuntimeDatabaseDSN)
	}
	if runtimeDSN == "" {
		return state, true, fmt.Errorf("%w: 配置 revision 缺少运行数据库连接", ErrSchemaDrift)
	}
	if !active {
		if err := b.database.VerifyConfigurationIntent(ctx, runtimeDSN, state.OperationID, state.ConfigDigest); err != nil {
			return state, true, err
		}
		if err := b.bundles.Activate(ctx, state.OperationID, state.ConfigDigest); err != nil {
			return state, true, fmt.Errorf("重放配置激活意图失败: %w", err)
		}
		state.Phase = "config_activated"
		state.Percent = 68
		state.Message = "数据库激活意图已验证，配置 revision 已恢复激活"
		state.UpdatedAt = b.now().UTC()
		if err := b.writeJournal(state); err != nil {
			return state, true, err
		}
	}
	if err := b.database.RecordConfiguration(ctx, runtimeDSN, state.OperationID, state.ConfigDigest); err != nil {
		return state, true, err
	}
	if strings.TrimSpace(b.opts.RuntimeDatabaseDSN) == "" {
		state.Phase = "awaiting_admin_restart"
		state.Percent = 70
		state.Message = "配置与数据库安装状态已对账，正在切换到正常 Admin"
		state.CanResume = false
		state.UpdatedAt = b.now().UTC()
		if err := b.writeJournal(state); err != nil {
			return state, true, err
		}
		b.requestRestart()
		return state, true, nil
	}
	state.Phase = "sealing"
	state.Percent = 90
	state.Message = "Admin 已就绪，正在关闭首次安装入口"
	state.CanResume = false
	state.UpdatedAt = b.now().UTC()
	if err := b.writeJournal(state); err != nil {
		return state, true, err
	}
	if err := b.sealInstallation(ctx, &state); err != nil {
		return state, true, err
	}
	return state, true, nil
}

func (b *Bootstrap) sealInstallation(ctx context.Context, state *journal) error {
	if b.database == nil {
		return errors.New("database provisioner is nil")
	}
	if err := b.database.Seal(ctx, b.opts.RuntimeDatabaseDSN, state.OperationID, state.ConfigDigest); err != nil {
		return err
	}
	installed := map[string]any{
		"product": ProductName, "instance_id": state.InstanceID,
		"operation_id": state.OperationID, "config_digest": state.ConfigDigest,
		"business_services": businessServiceNames(), "installed_at": b.now().UTC(),
	}
	if err := writeAtomicJSON(b.opts.InstalledPath(), installed, 0o400); err != nil {
		return err
	}
	state.Mode = ModeInstalled
	state.Phase = "installed"
	state.Percent = 100
	state.Message = "Admin 安装完成；请登录后台配置业务服务后在服务器执行启动命令"
	state.Retryable = false
	state.CanResume = false
	state.NeedsToken = false
	state.Problem = nil
	state.UpdatedAt = b.now().UTC()
	if err := b.writeJournal(*state); err != nil {
		log.Printf("installer final journal update failed: operation_id=%s err=%v", state.OperationID, err)
	}
	_ = os.Remove(b.opts.TokenHashPath())
	_ = os.Remove(b.opts.AllowPath())
	return syncDir(b.opts.StateDir())
}

func (b *Bootstrap) progress(state *journal, phase string, percent int, message string) {
	state.Phase = phase
	state.Percent = percent
	state.Message = message
	state.Retryable = false
	state.CanResume = false
	state.Problem = nil
	state.UpdatedAt = b.now().UTC()
	_ = b.writeJournal(*state)
}

func (b *Bootstrap) fail(state journal, code, message string, retryable bool, cause error) {
	log.Printf("installer operation failed: operation_id=%s phase=%s code=%s err=%v", state.OperationID, state.Phase, code, cause)
	problem := Explain(cause)
	if problem.Code == "INSTALL_FAILED" {
		problem.Code = code
		problem.Message = message
	}
	state.Mode = ModeRecovery
	state.Message = problem.Message
	state.Retryable = retryable
	state.CanResume = retryable && state.ConfigDigest != ""
	state.NeedsToken = true
	state.Problem = &problem
	state.UpdatedAt = b.now().UTC()
	_ = b.writeJournal(state)
}

func (b *Bootstrap) writeJournal(state journal) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.writeJournalUnlocked(state)
}

func (b *Bootstrap) writeJournalUnlocked(state journal) error {
	if err := os.MkdirAll(b.opts.StateDir(), 0o700); err != nil {
		return err
	}
	return writeAtomicJSON(b.opts.JournalPath(), state, 0o600)
}

func writeAtomicJSON(path string, value any, mode os.FileMode) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	temp := path + ".tmp"
	_ = os.Remove(temp)
	if err := writeSynced(temp, raw, mode); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func planDigest(draft Draft, assessment DatabaseAssessment) (string, error) {
	// MQTT and historical optional-service selection are intentionally outside
	// first-run installation. Keeping them out of the digest lets older clients
	// submit their legacy fields without changing the Admin-only install plan.
	draft.MQTT = MQTTInput{}
	draft.OptionalServices = nil
	payload := struct {
		Draft      Draft
		Assessment DatabaseAssessment
		Versions   map[string]int
	}{Draft: draft, Assessment: assessment, Versions: map[string]int{"format": 1}}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sameAssessment(left, right DatabaseAssessment) bool {
	if left.Class != right.Class || left.TableCount != right.TableCount || left.CreateAdmin != right.CreateAdmin ||
		left.RecoveryOperationID != right.RecoveryOperationID || len(left.Versions) != len(right.Versions) {
		return false
	}
	for component, version := range left.Versions {
		if right.Versions[component] != version {
			return false
		}
	}
	return true
}

func classificationProblem(class DatabaseClass) error {
	switch class {
	case DatabaseUnknownNonEmpty:
		return ErrUnknownDatabase
	case DatabaseFuture:
		return ErrSchemaFuture
	default:
		return ErrSchemaDrift
	}
}

func problemCode(err error) string {
	return Explain(err).Code
}

func safeMessage(err error) string {
	return Explain(err).Message
}

func acquireFileLock(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func releaseFileLock(file *os.File) {
	if file == nil {
		return
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}
