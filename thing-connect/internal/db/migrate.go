package db

import (
	"errors"
	"fmt"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

// IsIgnorableDDLError reports whether a DDL error means that an idempotent
// migration statement has already been applied. Permission errors are never
// ignored: production installations must provision the schema with a migration
// account before starting services with a restricted application account.
func IsIgnorableDDLError(err error) bool {
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) {
		return false
	}
	switch mysqlErr.Number {
	case 1060, 1061:
		return true
	default:
		return false
	}
}

// execIgnoreDup executes a DDL statement and ignores errors that IsIgnorableDDLError
// classifies as benign, so ALTER TABLE statements are idempotent on repeated Migrate
// calls.
func execIgnoreDup(db *sqlx.DB, stmt string) error {
	_, err := db.Exec(stmt)
	if err == nil || IsIgnorableDDLError(err) {
		return nil
	}
	return err
}

func coreMigrationStatements() []string {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id         BIGINT AUTO_INCREMENT PRIMARY KEY,
			email      VARCHAR(255) NOT NULL UNIQUE,
			password   VARCHAR(255) NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS device_pool (
			id         BIGINT AUTO_INCREMENT PRIMARY KEY,
			device_id  VARCHAR(64) NOT NULL UNIQUE,
			device_key VARCHAR(64) NOT NULL,
			status     TINYINT NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_status (status)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS voip_device_profile (
			id         BIGINT       NOT NULL AUTO_INCREMENT,
			device_id  VARCHAR(20)  NOT NULL,
			profile    VARCHAR(512) NOT NULL COMMENT 'JSON media params',
			updated_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			UNIQUE KEY uq_voip_device_id (device_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS voip_device_auth (
			id          BIGINT      NOT NULL AUTO_INCREMENT,
			device_id   VARCHAR(20) NOT NULL,
			wx_open_id  VARCHAR(64) NOT NULL,
			wx_app_id   VARCHAR(64) NOT NULL DEFAULT '',
			wx_model_id VARCHAR(64) NOT NULL DEFAULT '',
			remark      VARCHAR(64) NOT NULL DEFAULT '' COMMENT '备注名',
			authorized_device_name VARCHAR(64) NOT NULL DEFAULT '' COMMENT '最近一次微信授权时的设备名称快照',
			auth_status VARCHAR(16) NOT NULL DEFAULT 'active' COMMENT 'active/invalid',
			invalid_reason VARCHAR(64) NOT NULL DEFAULT '',
			invalid_at DATETIME DEFAULT NULL,
			last_verified_at DATETIME DEFAULT NULL,
			created_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			UNIQUE KEY uq_voip_device_auth (device_id, wx_open_id, wx_app_id),
			KEY idx_voip_identity_recent (wx_open_id, wx_app_id, created_at, id),
			KEY idx_voip_device_status (device_id, auth_status)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS voip_user_profile (
			id          BIGINT      NOT NULL AUTO_INCREMENT,
			wx_open_id  VARCHAR(64) NOT NULL,
			wx_app_id   VARCHAR(64) NOT NULL DEFAULT '',
			remark      VARCHAR(64) NOT NULL DEFAULT '' COMMENT 'OpenID 在所有设备联系人列表中的统一备注',
			updated_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			UNIQUE KEY uq_voip_user_profile (wx_open_id, wx_app_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS device_bind (
			id           BIGINT       NOT NULL AUTO_INCREMENT,
			device_id    VARCHAR(64)  NOT NULL,
			mac          VARCHAR(32)  NOT NULL DEFAULT '',
			chip_uid     VARCHAR(128) NOT NULL DEFAULT '',
			device_rand  VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '设备端维护的稳定唯一标识',
			assign       VARCHAR(16)  NOT NULL DEFAULT '' COMMENT 'dynamic=pool分配 preburn=出厂预烧',
			device_name  VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '当前绑定用户设置的设备名称，解绑清空',
			user_id      BIGINT       NOT NULL DEFAULT 0  COMMENT '0=无主/未绑定',
			last_user_id BIGINT       NOT NULL DEFAULT 0  COMMENT '最后一任主人',
			active_time  DATETIME     DEFAULT NULL,
			bind_time    DATETIME     DEFAULT NULL,
			unbind_time  DATETIME     DEFAULT NULL,
			created_at   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			UNIQUE KEY uq_device_id (device_id),
			KEY idx_user_id (user_id),
			KEY idx_mac     (mac)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS ai_user_role (
			id         BIGINT      NOT NULL AUTO_INCREMENT,
			user_id    BIGINT      NOT NULL COMMENT "用户ID",
			role_id    VARCHAR(64) NOT NULL COMMENT "探鸽云端角色ID",
			created_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			UNIQUE KEY uq_ai_user_role (user_id, role_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS ai_device_role (
			id         BIGINT      NOT NULL AUTO_INCREMENT,
			device_id  VARCHAR(64) NOT NULL,
			role_id    VARCHAR(64) NOT NULL COMMENT '探鸽云端角色ID',
			user_id    BIGINT      NOT NULL COMMENT '绑定操作人',
			created_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			UNIQUE KEY uq_device_id (device_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS device_bind_log (
			id          BIGINT       NOT NULL AUTO_INCREMENT,
			device_id   VARCHAR(64)  NOT NULL,
			user_id     BIGINT       NOT NULL,
			action      TINYINT      NOT NULL COMMENT '1=bind 2=unbind',
			mac         VARCHAR(32)  NOT NULL DEFAULT '',
			chip_uid    VARCHAR(128) NOT NULL DEFAULT '',
			device_rand VARCHAR(64)  NOT NULL DEFAULT '',
			assign       VARCHAR(16)  NOT NULL DEFAULT '' COMMENT 'dynamic=pool分配 preburn=出厂预烧',
			created_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			KEY idx_device (device_id),
			KEY idx_user   (user_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS call_contact (
			id              BIGINT       NOT NULL AUTO_INCREMENT,
			device_id_a     VARCHAR(64)  NOT NULL COMMENT '较小 device_id（字典序）',
			device_id_b     VARCHAR(64)  NOT NULL COMMENT '较大 device_id（字典序）',
			source          VARCHAR(8)   NOT NULL DEFAULT 'manual' COMMENT 'auto=同账号 / manual=跨账号',
			initiator       CHAR(1)      NOT NULL DEFAULT 'a',
			user_id_a       BIGINT       NOT NULL,
			user_id_b       BIGINT       NOT NULL,
			status          TINYINT      NOT NULL DEFAULT 1 COMMENT '0=pending 1=accepted 2=rejected 3=deleted',
			remark_a        VARCHAR(64)  NOT NULL DEFAULT '',
			remark_b        VARCHAR(64)  NOT NULL DEFAULT '',
			created_at      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			UNIQUE KEY uq_pair (device_id_a, device_id_b),
			KEY idx_call_a (device_id_a),
			KEY idx_call_b (device_id_b)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS ai_user_resource (
			id          BIGINT       NOT NULL AUTO_INCREMENT,
			user_id     BIGINT       NOT NULL,
			type        VARCHAR(32)  NOT NULL COMMENT 'mcp/device_plugin/kb/kb_file',
			resource_id VARCHAR(64)  NOT NULL COMMENT '探鸽云端资源ID',
			name        VARCHAR(128) NOT NULL DEFAULT '' COMMENT '展示名,创建时冗余自云端,改名时刷新',
			created_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			UNIQUE KEY uq_user_resource (user_id, type, resource_id),
			KEY idx_user_type (user_id, type)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS cleanup_outbox (
			id              BIGINT       NOT NULL AUTO_INCREMENT,
			device_id       VARCHAR(64)  NOT NULL,
			target          VARCHAR(32)  NOT NULL,
			attempts        INT          NOT NULL DEFAULT 0,
			next_attempt_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_error      VARCHAR(1024) NOT NULL DEFAULT '',
			created_at      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			UNIQUE KEY uq_cleanup_device_target (device_id, target),
			KEY idx_cleanup_due (next_attempt_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}
	// ALTER statements are not idempotent in MySQL 5.7. Duplicate column and
	// duplicate key errors are handled by the migration runner.
	stmts = append(stmts,
		// ALTER statements — append after CREATEs so the tables exist first.
		`ALTER TABLE users ADD COLUMN bind_quota INT NOT NULL DEFAULT 10 COMMENT '剩余可绑额度'`,
		`ALTER TABLE users ADD COLUMN status TINYINT NOT NULL DEFAULT 1 COMMENT '1=active 0=disabled'`,
		`ALTER TABLE users ADD COLUMN disabled_at DATETIME NULL`,
		`ALTER TABLE users ADD COLUMN auth_revision BIGINT NOT NULL DEFAULT 1 COMMENT '密码或账号状态变更时递增'`,
		`ALTER TABLE device_pool ADD KEY idx_alloc (status, id)`,
		`ALTER TABLE device_bind ADD COLUMN assign VARCHAR(16) NOT NULL DEFAULT '' COMMENT 'dynamic=pool分配 preburn=出厂预烧' AFTER device_rand`,
		`ALTER TABLE device_bind ADD COLUMN device_name VARCHAR(64) NOT NULL DEFAULT '' COMMENT '当前绑定用户设置的设备名称，解绑清空' AFTER assign`,
		`UPDATE device_bind SET device_name='' WHERE user_id=0`,
		`ALTER TABLE device_bind_log ADD COLUMN assign VARCHAR(16) NOT NULL DEFAULT '' COMMENT 'dynamic=pool分配 preburn=出厂预烧' AFTER device_rand`,
		`ALTER TABLE voip_device_auth ADD COLUMN remark VARCHAR(64) NOT NULL DEFAULT '' COMMENT '备注名'`,
		`ALTER TABLE voip_device_auth ADD COLUMN authorized_device_name VARCHAR(64) NOT NULL DEFAULT '' COMMENT '最近一次微信授权时的设备名称快照' AFTER remark`,
		`ALTER TABLE voip_device_auth ADD COLUMN auth_status VARCHAR(16) NOT NULL DEFAULT 'active' COMMENT 'active/invalid' AFTER authorized_device_name`,
		`ALTER TABLE voip_device_auth ADD COLUMN invalid_reason VARCHAR(64) NOT NULL DEFAULT '' AFTER auth_status`,
		`ALTER TABLE voip_device_auth ADD COLUMN invalid_at DATETIME DEFAULT NULL AFTER invalid_reason`,
		`ALTER TABLE voip_device_auth ADD COLUMN last_verified_at DATETIME DEFAULT NULL AFTER invalid_at`,
		`ALTER TABLE voip_device_auth ADD KEY idx_voip_identity_recent (wx_open_id, wx_app_id, created_at, id)`,
		`ALTER TABLE voip_device_auth ADD KEY idx_voip_device_status (device_id, auth_status)`,
		`UPDATE voip_device_auth
		    SET authorized_device_name=device_id
		  WHERE authorized_device_name=''`,

		// Introduce one canonical contact name per mini-program identity without
		// overwriting names that have already been written to the new table.
		`INSERT IGNORE INTO voip_user_profile (wx_open_id, wx_app_id, remark)
		 SELECT current_auth.wx_open_id, current_auth.wx_app_id, current_auth.remark
		   FROM voip_device_auth current_auth
		   LEFT JOIN voip_device_auth newer_auth
		     ON newer_auth.wx_open_id=current_auth.wx_open_id
		    AND newer_auth.wx_app_id=current_auth.wx_app_id
		    AND (newer_auth.created_at>current_auth.created_at
		      OR (newer_auth.created_at=current_auth.created_at AND newer_auth.id>current_auth.id))
		  WHERE newer_auth.id IS NULL`,
		`UPDATE voip_device_auth auth
		   JOIN voip_user_profile profile
		     ON profile.wx_open_id=auth.wx_open_id AND profile.wx_app_id=auth.wx_app_id
		    SET auth.remark=profile.remark`,

		// Per-user MAC uniqueness: a (mac, user_id) pair may map to only one
		// device_id. The generated column is NULL for MAC-less or unbound rows
		// (mac='' or user_id=0) so they don't occupy a uniqueness slot; an active
		// bind collapses to "mac:user_id" and the UNIQUE key rejects a second
		// device_id for the same (mac, user). Last-line guard against Case A
		// concurrent double-allocate, including cross-instance.
		`ALTER TABLE device_bind
		  ADD COLUMN mac_user_key VARCHAR(64)
		  AS (IF(mac='' OR user_id=0, NULL, CONCAT(mac, ':', user_id))) STORED,
		  ADD UNIQUE KEY uq_mac_user (mac_user_key)`,
	)
	return stmts
}

const (
	coreSchemaVersion = 1
)

// Migrate applies the schema owned by the five business services. It is safe
// for all business services to call this concurrently during rolling startup.
func Migrate(db *sqlx.DB) error {
	return runMigration(db, "core", coreSchemaVersion, coreMigrationStatements())
}

// MigrateAdmin applies the business schema followed by the tables owned by
// admin-server. Keeping this entry point separate prevents business-only
// deployments from creating Admin tables.
func MigrateAdmin(db *sqlx.DB) error {
	if err := Migrate(db); err != nil {
		return err
	}
	if err := runMigration(db, "admin", 1, adminSchemaStatements()); err != nil {
		return err
	}
	return runMigration(db, "admin", 2, adminMigrationV2Statements())
}

func runMigration(db *sqlx.DB, component string, version int, stmts []string) error {
	if err := ensureMigrationTable(db); err != nil {
		return fmt.Errorf("migrate %s: prepare schema_migrations: %w", component, err)
	}
	var applied int
	if err := db.Get(&applied, `SELECT COUNT(*) FROM schema_migrations WHERE component=? AND version=?`, component, version); err != nil {
		return fmt.Errorf("migrate %s: read version %d: %w", component, version, err)
	}
	if applied > 0 {
		return nil
	}
	for index, stmt := range stmts {
		if err := execIgnoreDup(db, stmt); err != nil {
			return fmt.Errorf("migrate %s version %d statement %d: %w", component, version, index+1, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations (component,version) VALUES (?,?)`, component, version); err != nil {
		var mysqlErr *mysql.MySQLError
		if !errors.As(err, &mysqlErr) || mysqlErr.Number != 1062 {
			return fmt.Errorf("migrate %s: record version %d: %w", component, version, err)
		}
	}
	return nil
}

func ensureMigrationTable(db *sqlx.DB) error {
	var exists int
	if err := db.Get(&exists, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='schema_migrations'`); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		component VARCHAR(64) NOT NULL,
		version BIGINT NOT NULL,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (component, version)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)
	return err
}
