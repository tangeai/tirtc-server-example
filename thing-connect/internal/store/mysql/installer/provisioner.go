package installer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"

	adminapp "thing-connect/internal/admin"
	baseconfig "thing-connect/internal/config"
	"thing-connect/internal/db"
	installapp "thing-connect/internal/installer"
	mysqlmigrate "thing-connect/internal/store/mysql/migrate"
)

var databaseNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

var managedTableNames = []string{
	"schema_migrations", "users", "device_pool", "device_bind", "device_bind_log",
	"voip_device_profile", "voip_device_auth", "voip_user_profile", "ai_user_role",
	"ai_device_role", "ai_user_resource", "call_contact", "cleanup_outbox",
	"admin_users", "admin_roles", "admin_user_roles", "admin_role_permissions",
	"admin_menus", "admin_role_menus", "admin_sessions", "admin_mfa_factors",
	"admin_mfa_recovery_codes", "admin_login_log", "admin_dict_types", "admin_dict_items",
	"admin_audit_log", "admin_jobs", "admin_job_items", "config_entries",
	"config_publish_outbox", "thingconnect_installation_state",
}

// All services use one runtime account. Admin and the required services also
// query optional-domain tables, so process selection cannot narrow this grant.
var requiredRuntimeDMLTables = append([]string(nil), managedTableNames[1:]...)

var runtimeReadOnlyTables = []string{"schema_migrations"}

var optionalRuntimeServices = map[string]bool{
	"voip-server": true,
	"ai-server":   true,
	"call-server": true,
}

type Provisioner struct{}

func New() *Provisioner { return &Provisioner{} }

// InspectDSN performs the same zero-write ownership and drift inspection used
// by the Web installer, while allowing a sealed ThingConnect instance for a
// deliberate daily migration.
func (p *Provisioner) InspectDSN(ctx context.Context, dsn string) (installapp.DatabaseAssessment, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return installapp.DatabaseAssessment{}, fmt.Errorf("解析 MySQL DSN 失败: %w", err)
	}
	name := cfg.DBName
	if !databaseNamePattern.MatchString(name) {
		return installapp.DatabaseAssessment{}, fmt.Errorf("%w: MySQL DSN 数据库名无效", installapp.ErrInvalidInput)
	}
	serverCfg := *cfg
	serverCfg.DBName = ""
	server, err := db.Open(baseconfig.DatabaseCfg{DSN: serverCfg.FormatDSN()})
	if err != nil {
		return installapp.DatabaseAssessment{}, err
	}
	defer server.Close()
	return inspect(ctx, server, name, true)
}

func (p *Provisioner) RecordConfiguration(ctx context.Context, dsn, operationID, configDigest string) error {
	if strings.TrimSpace(dsn) == "" || strings.TrimSpace(operationID) == "" || len(configDigest) != 64 {
		return installapp.ErrSchemaDrift
	}
	target, err := db.Open(baseconfig.DatabaseCfg{DSN: dsn})
	if err != nil {
		return err
	}
	defer target.Close()
	result, err := target.ExecContext(ctx, `UPDATE thingconnect_installation_state
		SET stage='config_committed',status='installing',config_digest=?,last_error_code=''
		WHERE id=1 AND product=? AND operation_id=? AND status<>'installed'
		  AND (config_digest='' OR config_digest=?)`,
		configDigest, installapp.ProductName, operationID, configDigest)
	if err != nil {
		return fmt.Errorf("修复数据库配置提交状态失败: %w", err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr == nil && rows == 1 {
		return nil
	}
	var status, storedOperationID, storedDigest string
	if err := target.QueryRowxContext(ctx, `SELECT status,operation_id,config_digest
		FROM thingconnect_installation_state WHERE id=1 AND product=?`, installapp.ProductName).
		Scan(&status, &storedOperationID, &storedDigest); err != nil {
		return installapp.ErrSchemaDrift
	}
	if storedOperationID != operationID || storedDigest != configDigest || (status != "installing" && status != "installed") {
		return installapp.ErrSchemaDrift
	}
	return nil
}

// VerifyConfigurationIntent proves that the migration transaction committed
// the exact filesystem activation intent. It is deliberately read-only so a
// crash before claim.Record cannot be mistaken for permission to activate a
// prepared revision.
func (p *Provisioner) VerifyConfigurationIntent(ctx context.Context, dsn, operationID, configDigest string) error {
	if strings.TrimSpace(dsn) == "" || strings.TrimSpace(operationID) == "" || len(configDigest) != 64 {
		return installapp.ErrSchemaDrift
	}
	target, err := db.Open(baseconfig.DatabaseCfg{DSN: dsn})
	if err != nil {
		return err
	}
	defer target.Close()
	var count int
	if err := target.GetContext(ctx, &count, `SELECT COUNT(*) FROM thingconnect_installation_state
		WHERE id=1 AND product=? AND operation_id=? AND status='installing'
		  AND stage IN ('config_activation_pending','config_committed') AND config_digest=?`,
		installapp.ProductName, operationID, configDigest); err != nil {
		return fmt.Errorf("校验数据库配置激活意图失败: %w", err)
	}
	if count != 1 {
		return installapp.ErrSchemaDrift
	}
	return nil
}

func (p *Provisioner) Seal(ctx context.Context, dsn, operationID, configDigest string) error {
	if strings.TrimSpace(dsn) == "" {
		return fmt.Errorf("运行数据库连接未配置")
	}
	target, err := db.Open(baseconfig.DatabaseCfg{DSN: dsn})
	if err != nil {
		return err
	}
	defer target.Close()
	result, err := target.ExecContext(ctx, `UPDATE thingconnect_installation_state
		SET status='installed',stage='installed',config_digest=?,last_error_code=''
		WHERE id=1 AND product=? AND operation_id=? AND config_digest=?`,
		configDigest, installapp.ProductName, operationID, configDigest)
	if err != nil {
		return fmt.Errorf("锁定数据库安装状态失败: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取数据库安装状态更新结果失败: %w", err)
	}
	if rows == 1 {
		return nil
	}
	var status, storedOperationID, storedDigest string
	if err := target.QueryRowxContext(ctx, `SELECT status,operation_id,config_digest
		FROM thingconnect_installation_state WHERE id=1 AND product=?`, installapp.ProductName).
		Scan(&status, &storedOperationID, &storedDigest); err != nil {
		return installapp.ErrSchemaDrift
	}
	if status != "installed" || storedOperationID != operationID || storedDigest != configDigest {
		return installapp.ErrSchemaDrift
	}
	return nil
}

func (p *Provisioner) Inspect(ctx context.Context, input installapp.DatabaseInput) (installapp.DatabaseAssessment, error) {
	server, err := open(input, false)
	if err != nil {
		return installapp.DatabaseAssessment{}, err
	}
	defer server.Close()
	return inspect(ctx, server, input.Name, false)
}

func (p *Provisioner) Claim(ctx context.Context, input installapp.DatabaseInput, operationID, instanceID string) (installapp.DatabaseClaim, error) {
	server, err := open(input, false)
	if err != nil {
		return nil, err
	}
	conn, err := server.Connx(ctx)
	if err != nil {
		server.Close()
		return nil, fmt.Errorf("保留 MySQL 安装连接失败: %w", err)
	}
	digest := sha256.Sum256([]byte(input.Name))
	lockName := fmt.Sprintf("thingconnect:install:%x", digest[:16])
	var acquired int
	if err := conn.GetContext(ctx, &acquired, `SELECT GET_LOCK(?, 10)`, lockName); err != nil {
		conn.Close()
		server.Close()
		return nil, fmt.Errorf("获取 MySQL 安装锁失败: %w", err)
	}
	if acquired != 1 {
		conn.Close()
		server.Close()
		return nil, installapp.ErrInstallBusy
	}
	assessment, err := inspect(ctx, server, input.Name, false)
	if err != nil {
		release(conn, lockName)
		conn.Close()
		server.Close()
		return nil, err
	}
	return &claim{
		input: input, operationID: operationID, instanceID: instanceID,
		server: server, lockConn: conn, lockName: lockName, assessment: assessment,
	}, nil
}

type claim struct {
	input       installapp.DatabaseInput
	operationID string
	instanceID  string
	server      *sqlx.DB
	lockConn    *sqlx.Conn
	lockName    string
	target      *sqlx.DB
	assessment  installapp.DatabaseAssessment
}

func (c *claim) Assessment() installapp.DatabaseAssessment { return c.assessment }
func (c *claim) InstanceID() string                        { return c.instanceID }

func (c *claim) Prepare(ctx context.Context, firstAdmin installapp.FirstAdminInput, optionalServices []string) error {
	if err := c.checkLock(ctx); err != nil {
		return err
	}
	switch c.assessment.Class {
	case installapp.DatabaseAbsent:
		if _, err := c.lockConn.ExecContext(ctx, `CREATE DATABASE `+quoteIdentifier(c.input.Name)+` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci`); err != nil {
			return fmt.Errorf("创建数据库失败: %w", err)
		}
	case installapp.DatabaseEmpty, installapp.DatabaseManagedOlder, installapp.DatabaseManagedCurrent:
	default:
		return classificationError(c.assessment.Class)
	}
	target, err := open(c.input, true)
	if err != nil {
		return err
	}
	c.target = target
	if err := mysqlmigrate.EnsureInstallationState(ctx, target); err != nil {
		return fmt.Errorf("创建安装状态表失败: %w", err)
	}
	if _, err := target.ExecContext(ctx, `INSERT INTO thingconnect_installation_state
		(id,product,instance_id,operation_id,status,stage,generation)
		VALUES (1,?,?,?,?,?,1)
		ON DUPLICATE KEY UPDATE
			instance_id=IF(status='installed' OR config_digest<>'',instance_id,VALUES(instance_id)),
			operation_id=IF(status='installed' OR config_digest<>'',operation_id,VALUES(operation_id)),
			generation=IF(status='installed',generation,generation+1),
			stage=IF(status='installed',stage,VALUES(stage)),
			updated_at=NOW()`, installapp.ProductName, c.instanceID, c.operationID, "installing", "database_claimed"); err != nil {
		return fmt.Errorf("记录数据库安装所有权失败: %w", err)
	}
	var product, status, storedInstanceID, storedOperationID string
	if err := target.QueryRowxContext(ctx, `SELECT product,status,instance_id,operation_id FROM thingconnect_installation_state WHERE id=1`).Scan(&product, &status, &storedInstanceID, &storedOperationID); err != nil {
		return fmt.Errorf("读取数据库安装所有权失败: %w", err)
	}
	if product != installapp.ProductName {
		return installapp.ErrSchemaDrift
	}
	if status == "installed" {
		return installapp.ErrAlreadyInstalled
	}
	if storedOperationID != c.operationID {
		return fmt.Errorf("%w: 数据库已有配置提交后的安装任务，请导入原配置恢复", installapp.ErrAlreadyInstalled)
	}
	c.instanceID = storedInstanceID
	if err := c.checkLock(ctx); err != nil {
		return err
	}
	if err := mysqlmigrate.MigrateAdminContext(ctx, target); err != nil {
		return fmt.Errorf("执行数据库迁移失败: %w", err)
	}
	if err := c.verifyRuntimeDML(ctx, optionalServices); err != nil {
		return err
	}
	store := adminapp.NewStore(target)
	if err := store.SeedDefaults(ctx); err != nil {
		return fmt.Errorf("初始化后台权限失败: %w", err)
	}
	var adminCount int
	if err := target.GetContext(ctx, &adminCount, `SELECT COUNT(*) FROM admin_users`); err != nil {
		return fmt.Errorf("检查后台管理员失败: %w", err)
	}
	if adminCount == 0 {
		if strings.TrimSpace(firstAdmin.Email) == "" || strings.TrimSpace(firstAdmin.Password) == "" {
			return fmt.Errorf("%w: 首个管理员邮箱和密码不能为空", installapp.ErrInvalidInput)
		}
		hash, err := adminapp.HashAdminPassword(firstAdmin.Password)
		if err != nil {
			return fmt.Errorf("%w: %v", installapp.ErrInvalidInput, err)
		}
		nickName := strings.TrimSpace(firstAdmin.NickName)
		if nickName == "" {
			nickName = strings.SplitN(firstAdmin.Email, "@", 2)[0]
		}
		if _, err := store.BootstrapAdmin(ctx, firstAdmin.Email, nickName, hash); err != nil {
			return fmt.Errorf("创建首个管理员失败: %w", err)
		}
	}
	return c.Record(ctx, "admin_ready", "installing", "")
}

func (c *claim) verifyRuntimeDML(ctx context.Context, optionalServices []string) error {
	runtimeDB, err := openWithCredentials(c.input, c.input.RuntimeUser, c.input.RuntimePassword, true)
	if err != nil {
		return fmt.Errorf("运行账号连接检查失败: %w", err)
	}
	defer runtimeDB.Close()
	tx, err := runtimeDB.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("运行账号事务检查失败: %w", err)
	}
	defer tx.Rollback()
	for _, table := range runtimeReadOnlyTables {
		if _, err := tx.ExecContext(ctx, `SELECT 1 FROM `+quoteIdentifier(table)+` WHERE 1=0`); err != nil {
			return fmt.Errorf("运行账号缺少 %s 所需的 SELECT 权限: %w", table, err)
		}
	}
	tables, err := runtimeDMLTables(optionalServices)
	if err != nil {
		return err
	}
	for _, table := range tables {
		var columns []string
		if err := tx.SelectContext(ctx, &columns, `SELECT column_name FROM information_schema.columns
			WHERE table_schema=DATABASE() AND table_name=? AND extra NOT LIKE '%GENERATED%'
			ORDER BY ordinal_position`, table); err != nil || len(columns) == 0 {
			return fmt.Errorf("运行账号无法读取 %s 表结构，请检查数据库授权", table)
		}
		quoted := make([]string, len(columns))
		for index, column := range columns {
			quoted[index] = quoteIdentifier(column)
		}
		columnList := strings.Join(quoted, ",")
		quotedTable := quoteIdentifier(table)
		statements := []string{
			`SELECT ` + quoted[0] + ` FROM ` + quotedTable + ` WHERE 1=0`,
			`INSERT INTO ` + quotedTable + ` (` + columnList + `) SELECT ` + columnList + ` FROM ` + quotedTable + ` WHERE 1=0`,
			`UPDATE ` + quotedTable + ` SET ` + quoted[0] + `=` + quoted[0] + ` WHERE 1=0`,
			`DELETE FROM ` + quotedTable + ` WHERE 1=0`,
		}
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("运行账号缺少 %s 所需的 SELECT/INSERT/UPDATE/DELETE 权限: %w", table, err)
			}
		}
	}
	return tx.Rollback()
}

func runtimeDMLTables(optionalServices []string) ([]string, error) {
	seen := make(map[string]bool, len(optionalServices))
	for _, service := range optionalServices {
		if !optionalRuntimeServices[service] || seen[service] {
			return nil, fmt.Errorf("%w: 可选服务列表无效", installapp.ErrInvalidInput)
		}
		seen[service] = true
	}
	return append([]string(nil), requiredRuntimeDMLTables...), nil
}

func (c *claim) Record(ctx context.Context, stage, status, configDigest string) error {
	if c.target == nil {
		target, err := open(c.input, true)
		if err != nil {
			return err
		}
		c.target = target
	}
	if err := c.checkLock(ctx); err != nil {
		return err
	}
	var existingDigest string
	if err := c.target.GetContext(ctx, &existingDigest, `SELECT config_digest FROM thingconnect_installation_state
		WHERE id=1 AND product=? AND operation_id=?`, installapp.ProductName, c.operationID); err != nil {
		return installapp.ErrSchemaDrift
	}
	if existingDigest != "" {
		if configDigest != "" && configDigest != existingDigest {
			return installapp.ErrSchemaDrift
		}
		configDigest = existingDigest
	}
	result, err := c.target.ExecContext(ctx, `UPDATE thingconnect_installation_state
		SET stage=?,status=?,config_digest=?,last_error_code='' WHERE id=1 AND product=? AND operation_id=?`,
		stage, status, configDigest, installapp.ProductName, c.operationID)
	if err != nil {
		return fmt.Errorf("更新数据库安装状态失败: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取数据库安装状态更新结果失败: %w", err)
	}
	if rows == 1 {
		return nil
	}
	var storedStage, storedStatus, storedDigest string
	if err := c.target.QueryRowxContext(ctx, `SELECT stage,status,config_digest
		FROM thingconnect_installation_state WHERE id=1 AND product=? AND operation_id=?`,
		installapp.ProductName, c.operationID).Scan(&storedStage, &storedStatus, &storedDigest); err != nil {
		return installapp.ErrSchemaDrift
	}
	if storedStage != stage || storedStatus != status || storedDigest != configDigest {
		return installapp.ErrSchemaDrift
	}
	return nil
}

func (c *claim) Close() error {
	if c.target != nil {
		_ = c.target.Close()
	}
	release(c.lockConn, c.lockName)
	_ = c.lockConn.Close()
	return c.server.Close()
}

func (c *claim) checkLock(ctx context.Context) error {
	var owner, current sql.NullInt64
	if err := c.lockConn.GetContext(ctx, &owner, `SELECT IS_USED_LOCK(?)`, c.lockName); err != nil || !owner.Valid {
		return fmt.Errorf("MySQL 安装锁连接已丢失")
	}
	if err := c.lockConn.GetContext(ctx, &current, `SELECT CONNECTION_ID()`); err != nil || !current.Valid || current.Int64 != owner.Int64 {
		return fmt.Errorf("MySQL 安装锁所有权已丢失")
	}
	return nil
}

func release(conn *sqlx.Conn, name string) {
	if conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var ignored any
	_ = conn.GetContext(ctx, &ignored, `SELECT RELEASE_LOCK(?)`, name)
}

func inspect(ctx context.Context, server *sqlx.DB, databaseName string, allowInstalled bool) (installapp.DatabaseAssessment, error) {
	if !databaseNamePattern.MatchString(databaseName) {
		return installapp.DatabaseAssessment{}, fmt.Errorf("%w: 数据库名只能包含字母、数字和下划线，长度 1-64", installapp.ErrInvalidInput)
	}
	assessment := installapp.DatabaseAssessment{Versions: map[string]int{}}
	var exists int
	if err := server.GetContext(ctx, &exists, `SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name=?`, databaseName); err != nil {
		return assessment, fmt.Errorf("读取数据库信息失败: %w", err)
	}
	if exists == 0 {
		assessment.Class = installapp.DatabaseAbsent
		assessment.CreateAdmin = true
		assessment.Description = "数据库不存在，将创建并初始化"
		return assessment, nil
	}
	var tables []string
	if err := server.SelectContext(ctx, &tables, `SELECT table_name FROM information_schema.tables WHERE table_schema=? AND table_type='BASE TABLE' ORDER BY table_name`, databaseName); err != nil {
		return assessment, fmt.Errorf("读取数据库表清单失败: %w", err)
	}
	assessment.TableCount = len(tables)
	if len(tables) == 0 {
		assessment.Class = installapp.DatabaseEmpty
		assessment.CreateAdmin = true
		assessment.Description = "数据库存在且为空，将初始化表结构"
		return assessment, nil
	}
	tableSet := make(map[string]bool, len(tables))
	for _, table := range tables {
		tableSet[table] = true
	}
	hasMarker := tableSet["thingconnect_installation_state"]
	installStatus := ""
	if hasMarker {
		var product string
		query := `SELECT product,status FROM ` + quoteIdentifier(databaseName) + `.thingconnect_installation_state WHERE id=1`
		if err := server.QueryRowxContext(ctx, query).Scan(&product, &installStatus); err != nil || product != installapp.ProductName {
			assessment.Class = installapp.DatabaseDrift
			assessment.Description = "安装状态标识无效"
			return assessment, installapp.ErrSchemaDrift
		}
		if installStatus == "installed" && !allowInstalled {
			assessment.Description = "数据库属于已安装实例，请导入该实例的原配置和共享密钥"
			return assessment, installapp.ErrAlreadyInstalled
		}
	}
	if !tableSet["schema_migrations"] {
		if hasMarker {
			if installStatus == "installed" || len(tables) != 1 || (installStatus != "installing" && installStatus != "migration_only") {
				assessment.Class = installapp.DatabaseDrift
				assessment.Description = "安装标识存在但迁移账本或业务表丢失"
				return assessment, installapp.ErrSchemaDrift
			}
			assessment.Class = installapp.DatabaseManagedOlder
			assessment.CreateAdmin = true
			assessment.Description = "已识别未完成的 ThingConnect 安装，将从已持久化步骤继续"
			return assessment, nil
		}
		assessment.Class = installapp.DatabaseUnknownNonEmpty
		assessment.Description = "非空数据库没有 ThingConnect 迁移记录"
		return assessment, installapp.ErrUnknownDatabase
	}
	type versionRow struct {
		Component string `db:"component"`
		Version   int    `db:"version"`
	}
	var rows []versionRow
	versionsQuery := `SELECT component,version FROM ` + quoteIdentifier(databaseName) + `.schema_migrations ORDER BY component,version`
	if err := server.SelectContext(ctx, &rows, versionsQuery); err != nil {
		return assessment, fmt.Errorf("读取迁移版本失败: %w", err)
	}
	current := mysqlmigrate.CurrentMigrationVersions()
	seenVersions := make(map[string]map[int]bool, len(current))
	for _, row := range rows {
		if row.Version < 1 {
			assessment.Class = installapp.DatabaseDrift
			assessment.Description = "迁移记录版本无效"
			return assessment, installapp.ErrSchemaDrift
		}
		ceiling, ok := current[row.Component]
		if !ok || row.Version > ceiling {
			assessment.Class = installapp.DatabaseFuture
			assessment.Description = "数据库版本高于当前程序"
			return assessment, installapp.ErrSchemaFuture
		}
		if seenVersions[row.Component] == nil {
			seenVersions[row.Component] = map[int]bool{}
		}
		seenVersions[row.Component][row.Version] = true
		if row.Version > assessment.Versions[row.Component] {
			assessment.Versions[row.Component] = row.Version
		}
	}
	for component, maximum := range assessment.Versions {
		for version := 1; version <= maximum; version++ {
			if !seenVersions[component][version] {
				assessment.Class = installapp.DatabaseDrift
				assessment.Description = "迁移记录存在断层"
				return assessment, installapp.ErrSchemaDrift
			}
		}
	}
	if (len(rows) == 0 || assessment.Versions["core"] == 0) && !hasMarker {
		assessment.Class = installapp.DatabaseDrift
		assessment.Description = "迁移记录不完整"
		return assessment, installapp.ErrSchemaDrift
	}
	if err := validateLegacyFingerprint(tableSet, assessment.Versions, hasMarker); err != nil {
		assessment.Class = installapp.DatabaseDrift
		assessment.Description = "表结构与迁移记录不一致"
		return assessment, err
	}
	if err := validateCompleteVersionedShape(ctx, server, databaseName, assessment.Versions, hasMarker); err != nil {
		assessment.Class = installapp.DatabaseDrift
		assessment.Description = "完整表结构与迁移记录不一致"
		return assessment, err
	}
	adminCount := 0
	if tableSet["admin_users"] {
		adminQuery := `SELECT COUNT(*) FROM ` + quoteIdentifier(databaseName) + `.admin_users`
		if err := server.GetContext(ctx, &adminCount, adminQuery); err != nil {
			return assessment, fmt.Errorf("读取管理员数量失败: %w", err)
		}
	}
	assessment.CreateAdmin = adminCount == 0
	upToDate := true
	for component, version := range current {
		if assessment.Versions[component] < version {
			upToDate = false
		}
	}
	if upToDate {
		assessment.Class = installapp.DatabaseManagedCurrent
		assessment.Description = "已识别当前版本 ThingConnect 数据库，不修改已有数据"
	} else {
		assessment.Class = installapp.DatabaseManagedOlder
		assessment.Description = "已识别旧版 ThingConnect 数据库，只执行缺失迁移"
	}
	return assessment, nil
}

func validateCompleteVersionedShape(
	ctx context.Context,
	server *sqlx.DB,
	databaseName string,
	versions map[string]int,
	hasMarker bool,
) error {
	expected, err := mysqlmigrate.SchemaShapeForVersions(versions, hasMarker)
	if err != nil {
		return fmt.Errorf("读取版本化数据库结构契约失败: %w", err)
	}
	var tableCount int
	for table := range expected {
		if err := server.GetContext(ctx, &tableCount, `SELECT COUNT(*) FROM information_schema.tables
			WHERE table_schema=? AND table_name=? AND table_type='BASE TABLE' AND engine='InnoDB'`, databaseName, table); err != nil {
			return fmt.Errorf("读取表结构失败: %w", err)
		}
		if tableCount != 1 {
			return installapp.ErrSchemaDrift
		}
	}
	type columnRow struct {
		TableName            string         `db:"table_name"`
		ColumnName           string         `db:"column_name"`
		ColumnType           string         `db:"column_type"`
		IsNullable           string         `db:"is_nullable"`
		ColumnDefault        sql.NullString `db:"column_default"`
		Extra                string         `db:"extra"`
		GenerationExpression string         `db:"generation_expression"`
	}
	var columns []columnRow
	if err := server.SelectContext(ctx, &columns, `SELECT table_name,column_name,column_type,is_nullable,column_default,extra,generation_expression
		FROM information_schema.columns WHERE table_schema=?`, databaseName); err != nil {
		return fmt.Errorf("读取完整字段结构失败: %w", err)
	}
	actualColumns := make(map[string]map[string]mysqlmigrate.ColumnShape)
	for _, column := range columns {
		if actualColumns[column.TableName] == nil {
			actualColumns[column.TableName] = map[string]mysqlmigrate.ColumnShape{}
		}
		actualColumns[column.TableName][column.ColumnName] = mysqlmigrate.InformationSchemaColumnShape(
			column.ColumnType, column.IsNullable, column.ColumnDefault.Valid, column.ColumnDefault.String,
			column.Extra, column.GenerationExpression,
		)
	}
	type indexRow struct {
		TableName  string         `db:"table_name"`
		IndexName  string         `db:"index_name"`
		NonUnique  int            `db:"non_unique"`
		SeqInIndex int            `db:"seq_in_index"`
		ColumnName sql.NullString `db:"column_name"`
		SubPart    sql.NullInt64  `db:"sub_part"`
		Collation  sql.NullString `db:"collation"`
		IndexType  string         `db:"index_type"`
		IsVisible  string         `db:"is_visible"`
	}
	var indexes []indexRow
	if err := server.SelectContext(ctx, &indexes, `SELECT table_name,index_name,non_unique,seq_in_index,column_name,sub_part,collation,index_type,is_visible
		FROM information_schema.statistics WHERE table_schema=? ORDER BY table_name,index_name,seq_in_index`, databaseName); err != nil {
		return fmt.Errorf("读取完整索引结构失败: %w", err)
	}
	actualIndexes := make(map[string]map[string]mysqlmigrate.IndexShape)
	for _, index := range indexes {
		if !index.ColumnName.Valid {
			return installapp.ErrSchemaDrift
		}
		if actualIndexes[index.TableName] == nil {
			actualIndexes[index.TableName] = map[string]mysqlmigrate.IndexShape{}
		}
		shape := actualIndexes[index.TableName][index.IndexName]
		shape.Unique = index.NonUnique == 0
		shape.Type = strings.ToUpper(index.IndexType)
		shape.Visible = strings.EqualFold(index.IsVisible, "YES")
		shape.Parts = append(shape.Parts, mysqlmigrate.IndexPart{
			Column: index.ColumnName.String, Prefix: index.SubPart.Int64,
			Desc: index.Collation.Valid && strings.EqualFold(index.Collation.String, "D"),
		})
		actualIndexes[index.TableName][index.IndexName] = shape
	}
	for table, shape := range expected {
		for column, columnShape := range shape.Columns {
			if !reflect.DeepEqual(actualColumns[table][column], columnShape) {
				return installapp.ErrSchemaDrift
			}
		}
		for index, indexShape := range shape.Indexes {
			if !reflect.DeepEqual(actualIndexes[table][index], indexShape) {
				return installapp.ErrSchemaDrift
			}
		}
	}
	return nil
}

func validateLegacyFingerprint(tables map[string]bool, versions map[string]int, hasMarker bool) error {
	required := []string{"schema_migrations"}
	if versions["core"] > 0 {
		required = append(required,
			"users", "device_pool", "device_bind", "device_bind_log",
			"voip_device_profile", "voip_device_auth", "voip_user_profile",
			"ai_user_role", "ai_device_role", "ai_user_resource",
			"call_contact", "cleanup_outbox",
		)
	}
	if versions["admin"] > 0 {
		required = append(required,
			"admin_users", "admin_roles", "admin_user_roles", "admin_role_permissions",
			"admin_menus", "admin_role_menus", "admin_sessions", "admin_mfa_factors",
			"admin_mfa_recovery_codes", "admin_login_log", "admin_dict_types",
			"admin_dict_items", "admin_audit_log", "admin_jobs", "admin_job_items",
			"config_entries", "config_publish_outbox",
		)
	}
	if versions["admin"] >= 4 {
		required = append(required, "thingconnect_installation_state")
	}
	for _, table := range required {
		if !tables[table] {
			return installapp.ErrSchemaDrift
		}
	}
	known := map[string]bool{
		"schema_migrations": true, "users": true, "device_pool": true, "device_bind": true,
		"device_bind_log": true, "call_contact": true, "voip_device_profile": true,
		"voip_device_auth": true, "voip_user_profile": true, "ai_user_role": true,
		"ai_device_role": true, "ai_user_resource": true, "cleanup_outbox": true,
		"admin_users": true, "admin_roles": true, "admin_user_roles": true,
		"admin_role_permissions": true, "admin_menus": true, "admin_role_menus": true,
		"admin_sessions": true, "admin_mfa_factors": true, "admin_mfa_recovery_codes": true,
		"admin_login_log": true, "admin_dict_types": true, "admin_dict_items": true,
		"admin_audit_log": true, "admin_jobs": true, "admin_job_items": true,
		"config_entries": true, "config_publish_outbox": true,
		"thingconnect_installation_state": true,
	}
	unknown := make([]string, 0)
	for table := range tables {
		if !known[table] {
			unknown = append(unknown, table)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return installapp.ErrSchemaDrift
	}
	return nil
}

func open(input installapp.DatabaseInput, includeDatabase bool) (*sqlx.DB, error) {
	if err := validateInput(input); err != nil {
		return nil, err
	}
	return openWithCredentials(input, input.MigrationUser, input.MigrationPassword, includeDatabase)
}

func openWithCredentials(input installapp.DatabaseInput, user, password string, includeDatabase bool) (*sqlx.DB, error) {
	cfg := mysql.NewConfig()
	cfg.User = strings.TrimSpace(user)
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(strings.TrimSpace(input.Host), strconv.Itoa(input.Port))
	if includeDatabase {
		cfg.DBName = input.Name
	}
	cfg.ParseTime = true
	cfg.Loc = time.Local
	cfg.Params = map[string]string{"charset": "utf8mb4"}
	if input.TLS != "" && input.TLS != "false" {
		cfg.TLSConfig = input.TLS
	}
	return db.Open(baseconfig.DatabaseCfg{DSN: cfg.FormatDSN()})
}

func validateInput(input installapp.DatabaseInput) error {
	if strings.TrimSpace(input.Host) == "" || strings.ContainsAny(input.Host, "/?#@") {
		return fmt.Errorf("%w: MySQL 主机无效", installapp.ErrInvalidInput)
	}
	if input.Port < 1 || input.Port > 65535 {
		return fmt.Errorf("%w: MySQL 端口无效", installapp.ErrInvalidInput)
	}
	if !databaseNamePattern.MatchString(input.Name) {
		return fmt.Errorf("%w: 数据库名只能包含字母、数字和下划线，长度 1-64", installapp.ErrInvalidInput)
	}
	if strings.TrimSpace(input.MigrationUser) == "" {
		return fmt.Errorf("%w: MySQL 安装账号不能为空", installapp.ErrInvalidInput)
	}
	switch input.TLS {
	case "", "false", "true", "skip-verify", "preferred":
	default:
		return fmt.Errorf("%w: MySQL TLS 模式无效", installapp.ErrInvalidInput)
	}
	return nil
}

func quoteIdentifier(value string) string { return "`" + value + "`" }

func classificationError(class installapp.DatabaseClass) error {
	switch class {
	case installapp.DatabaseUnknownNonEmpty:
		return installapp.ErrUnknownDatabase
	case installapp.DatabaseFuture:
		return installapp.ErrSchemaFuture
	default:
		return installapp.ErrSchemaDrift
	}
}
