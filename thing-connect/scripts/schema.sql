-- thing-connect 数据库建表语句
--
-- 权威来源：internal/store/mysql/migrate/migrations/（仅安装/显式迁移执行）
-- 本文件是不被 install.sh / deploy-prod.sh 直接执行的当前结构基线，供 DBA 审阅、
-- mysql 客户端或外部数据库平台初始化空库和迁移一致性测试使用。
--
-- 使用说明：
--   默认安装不要手工导入本文件；使用 Web 安装器或 deploy-prod.sh migrate。
--   旧数据库不得导入本文件覆盖，必须执行版本化迁移。
--   完整字段释义同时见 core/001_zzz_schema_comments.sql 和
--   admin/001_schema_comments.sql；两者与建表文件共同构成版本 1。
--   使用 mysql 客户端直接导入时，先 cd 到 thing-connect 仓库目录，使文件末尾的
--   SOURCE 相对路径可以加载字段 COMMENT；不支持 SOURCE 的平台需按顺序执行这两个文件。

CREATE TABLE IF NOT EXISTS schema_migrations (
    component  VARCHAR(64) NOT NULL,
    version    BIGINT      NOT NULL,
    applied_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (component, version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS thingconnect_installation_state (
    id TINYINT NOT NULL,
    product VARCHAR(64) NOT NULL,
    instance_id CHAR(36) NOT NULL,
    operation_id CHAR(36) NOT NULL,
    status VARCHAR(32) NOT NULL,
    stage VARCHAR(64) NOT NULL,
    generation BIGINT NOT NULL DEFAULT 1,
    config_digest CHAR(64) NOT NULL DEFAULT '',
    last_error_code VARCHAR(64) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_install_instance (instance_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT IGNORE INTO thingconnect_installation_state
    (id, product, instance_id, operation_id, status, stage, generation)
VALUES
    (1, 'thingconnect', UUID(), UUID(), 'migration_only', 'migration_claimed', 1);

CREATE TABLE IF NOT EXISTS users (
    id         BIGINT AUTO_INCREMENT PRIMARY KEY,
    email      VARCHAR(255) NOT NULL UNIQUE,
    password   VARCHAR(255) NOT NULL,
    bind_quota INT          NOT NULL DEFAULT 10 COMMENT '剩余可绑额度',
    status     TINYINT      NOT NULL DEFAULT 1 COMMENT '1=active 0=disabled',
    disabled_at DATETIME    NULL,
    auth_revision BIGINT    NOT NULL DEFAULT 1 COMMENT '密码或账号状态变更时递增',
    created_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    KEY idx_users_created (created_at, id),
    KEY idx_users_status_created (status, created_at, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS device_pool (
    id         BIGINT AUTO_INCREMENT PRIMARY KEY,
    device_id  VARCHAR(64) NOT NULL UNIQUE,
    device_key VARCHAR(64) NOT NULL,
    status     TINYINT     NOT NULL DEFAULT 0 COMMENT '0=未分配 1=已分配',
    created_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_status (status),
    INDEX idx_alloc  (status, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS device_bind (
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
    mac_user_key VARCHAR(64) AS (IF(mac='' OR user_id=0, NULL, CONCAT(mac, ':', user_id))) STORED,
    PRIMARY KEY (id),
    UNIQUE KEY uq_device_id (device_id),
    KEY idx_user_id (user_id),
    KEY idx_mac     (mac),
    KEY idx_device_active_time (active_time, id),
    KEY idx_device_bind_time (bind_time, id),
    UNIQUE KEY uq_mac_user (mac_user_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS device_bind_log (
    id          BIGINT       NOT NULL AUTO_INCREMENT,
    device_id   VARCHAR(64)  NOT NULL,
    user_id     BIGINT       NOT NULL,
    action      TINYINT      NOT NULL COMMENT '1=bind 2=unbind',
    mac         VARCHAR(32)  NOT NULL DEFAULT '',
    chip_uid    VARCHAR(128) NOT NULL DEFAULT '',
    device_rand VARCHAR(64)  NOT NULL DEFAULT '',
    assign      VARCHAR(16)  NOT NULL DEFAULT '' COMMENT 'dynamic=pool分配 preburn=出厂预烧',
    created_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    KEY idx_device (device_id),
    KEY idx_user   (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS call_contact (
    id          BIGINT      NOT NULL AUTO_INCREMENT,
    device_id_a VARCHAR(64) NOT NULL COMMENT '较小 device_id（字典序）',
    device_id_b VARCHAR(64) NOT NULL COMMENT '较大 device_id（字典序）',
    source      VARCHAR(8)  NOT NULL DEFAULT 'manual' COMMENT 'auto=同账号 / manual=跨账号',
    initiator   CHAR(1)     NOT NULL DEFAULT 'a',
    user_id_a   BIGINT      NOT NULL,
    user_id_b   BIGINT      NOT NULL,
    status      TINYINT     NOT NULL DEFAULT 1 COMMENT '0=pending 1=accepted 2=rejected 3=deleted',
    remark_a    VARCHAR(64) NOT NULL DEFAULT '',
    remark_b    VARCHAR(64) NOT NULL DEFAULT '',
    created_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_pair (device_id_a, device_id_b),
    KEY idx_call_a (device_id_a),
    KEY idx_call_b (device_id_b)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS voip_device_profile (
    id         BIGINT       NOT NULL AUTO_INCREMENT,
    device_id  VARCHAR(20)  NOT NULL,
    profile    VARCHAR(512) NOT NULL COMMENT 'JSON media params',
    updated_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_voip_device_id (device_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS voip_device_auth (
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
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS voip_user_profile (
    id         BIGINT       NOT NULL AUTO_INCREMENT,
    wx_open_id VARCHAR(64)  NOT NULL,
    wx_app_id  VARCHAR(64)  NOT NULL DEFAULT '',
    remark     VARCHAR(64)  NOT NULL DEFAULT '' COMMENT 'OpenID 在所有设备联系人列表中的统一备注',
    updated_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_voip_user_profile (wx_open_id, wx_app_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS ai_user_role (
    id         BIGINT      NOT NULL AUTO_INCREMENT,
    user_id    BIGINT      NOT NULL COMMENT '用户ID',
    role_id    VARCHAR(64) NOT NULL COMMENT '探鸽云端角色ID',
    created_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_ai_user_role (user_id, role_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS ai_device_role (
    id         BIGINT      NOT NULL AUTO_INCREMENT,
    device_id  VARCHAR(64) NOT NULL,
    role_id    VARCHAR(64) NOT NULL COMMENT '探鸽云端角色ID',
    user_id    BIGINT      NOT NULL COMMENT '绑定操作人',
    created_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_device_id (device_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS ai_user_resource (
    id          BIGINT       NOT NULL AUTO_INCREMENT,
    user_id     BIGINT       NOT NULL,
    type        VARCHAR(32)  NOT NULL COMMENT 'mcp/device_plugin/kb/kb_file',
    resource_id VARCHAR(64)  NOT NULL COMMENT '探鸽云端资源ID',
    name        VARCHAR(128) NOT NULL DEFAULT '' COMMENT '展示名,创建时冗余自云端,改名时刷新',
    created_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_user_resource (user_id, type, resource_id),
    KEY idx_user_type (user_id, type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS cleanup_outbox (
    id              BIGINT        NOT NULL AUTO_INCREMENT,
    device_id       VARCHAR(64)   NOT NULL,
    target          VARCHAR(32)   NOT NULL,
    attempts        INT           NOT NULL DEFAULT 0,
    next_attempt_at DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_error      VARCHAR(1024) NOT NULL DEFAULT '',
    created_at      DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_cleanup_device_target (device_id, target),
    KEY idx_cleanup_due (next_attempt_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS admin_users (
    id BIGINT NOT NULL AUTO_INCREMENT COMMENT '管理员主键',
    email VARCHAR(255) NOT NULL COMMENT '管理员登录邮箱，系统内唯一',
    password VARCHAR(255) NOT NULL COMMENT '密码哈希，不存储明文',
    nick_name VARCHAR(64) NOT NULL DEFAULT '' COMMENT '管理员显示名称',
    status TINYINT NOT NULL DEFAULT 1 COMMENT '账号状态：1=启用，0=禁用',
    auth_revision BIGINT NOT NULL DEFAULT 1 COMMENT '认证版本；安全属性变化时递增并撤销旧会话',
    must_change_password TINYINT NOT NULL DEFAULT 0 COMMENT '是否要求下次登录修改密码：1=是，0=否',
    password_updated_at DATETIME NULL COMMENT '最近一次修改密码时间',
    last_login_ip VARCHAR(45) NOT NULL DEFAULT '' COMMENT '最近一次成功登录客户端 IP',
    last_login_at DATETIME NULL COMMENT '最近一次成功登录时间',
    remark VARCHAR(256) NOT NULL DEFAULT '' COMMENT '管理员备注',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '最后更新时间',
    PRIMARY KEY (id),
    UNIQUE KEY uq_admin_email (email),
    KEY idx_admin_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='后台管理员账号';

CREATE TABLE IF NOT EXISTS admin_roles (
    id BIGINT NOT NULL AUTO_INCREMENT, code VARCHAR(64) NOT NULL, name VARCHAR(64) NOT NULL,
    parent_id BIGINT NOT NULL DEFAULT 0, sort_no INT NOT NULL DEFAULT 0, status TINYINT NOT NULL DEFAULT 1,
    remark VARCHAR(256) NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id), UNIQUE KEY uq_admin_role_code (code), KEY idx_admin_role_parent (parent_id), KEY idx_admin_role_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS admin_user_roles (
    admin_user_id BIGINT NOT NULL, role_id BIGINT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (admin_user_id, role_id), KEY idx_admin_user_role_role (role_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS admin_role_permissions (
    role_id BIGINT NOT NULL, permission_code VARCHAR(64) NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (role_id, permission_code), KEY idx_admin_role_permission_code (permission_code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS admin_menus (
    id BIGINT NOT NULL AUTO_INCREMENT, parent_id BIGINT NOT NULL DEFAULT 0, menu_code VARCHAR(64) NOT NULL,
    name VARCHAR(64) NOT NULL, icon VARCHAR(64) NOT NULL DEFAULT '', path VARCHAR(128) NOT NULL DEFAULT '',
    permission_code VARCHAR(64) NOT NULL DEFAULT '', menu_type TINYINT NOT NULL DEFAULT 2,
    sort_no INT NOT NULL DEFAULT 0, visible TINYINT NOT NULL DEFAULT 1, status TINYINT NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id), UNIQUE KEY uq_admin_menu_code (menu_code), KEY idx_admin_menu_parent_sort (parent_id, sort_no), KEY idx_admin_menu_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS admin_role_menus (
    role_id BIGINT NOT NULL, menu_id BIGINT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (role_id, menu_id), KEY idx_admin_role_menu_menu (menu_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS admin_sessions (
    id BIGINT NOT NULL AUTO_INCREMENT, admin_user_id BIGINT NOT NULL, family_id CHAR(36) NOT NULL,
    token_hash CHAR(64) NOT NULL, replaced_by_id BIGINT NOT NULL DEFAULT 0, expires_at DATETIME NOT NULL,
    revoked_at DATETIME NULL, revoked_reason VARCHAR(128) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id), UNIQUE KEY uq_admin_session_token (token_hash),
    KEY idx_admin_session_user_expiry (admin_user_id, expires_at), KEY idx_admin_session_family (family_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS admin_mfa_factors (
    id BIGINT NOT NULL AUTO_INCREMENT, admin_user_id BIGINT NOT NULL, factor_type VARCHAR(16) NOT NULL DEFAULT 'totp',
    secret_enc TEXT NOT NULL, status TINYINT NOT NULL DEFAULT 0, last_used_step BIGINT NOT NULL DEFAULT 0,
    confirmed_at DATETIME NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id), UNIQUE KEY uq_admin_mfa_user_type (admin_user_id, factor_type), KEY idx_admin_mfa_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS admin_mfa_recovery_codes (
    id BIGINT NOT NULL AUTO_INCREMENT, admin_user_id BIGINT NOT NULL, code_hash CHAR(64) NOT NULL,
    used_at DATETIME NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id), UNIQUE KEY uq_admin_mfa_recovery_hash (code_hash), KEY idx_admin_mfa_recovery_user (admin_user_id, used_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS admin_login_log (
    id BIGINT NOT NULL AUTO_INCREMENT, admin_user_id BIGINT NOT NULL DEFAULT 0, email VARCHAR(255) NOT NULL DEFAULT '',
    client_ip VARCHAR(45) NOT NULL DEFAULT '', user_agent VARCHAR(512) NOT NULL DEFAULT '', status TINYINT NOT NULL DEFAULT 1,
    message VARCHAR(512) NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id), KEY idx_admin_login_user_time (admin_user_id, created_at),
    KEY idx_admin_login_email_time (email, created_at), KEY idx_admin_login_status_time (status, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS admin_dict_types (
    id BIGINT NOT NULL AUTO_INCREMENT, code VARCHAR(64) NOT NULL, name VARCHAR(64) NOT NULL,
    status TINYINT NOT NULL DEFAULT 1, remark VARCHAR(256) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id), UNIQUE KEY uq_admin_dict_type_code (code), KEY idx_admin_dict_type_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS admin_dict_items (
    id BIGINT NOT NULL AUTO_INCREMENT, dict_type_code VARCHAR(64) NOT NULL, label VARCHAR(128) NOT NULL,
    value VARCHAR(128) NOT NULL, sort_no INT NOT NULL DEFAULT 0, is_default TINYINT NOT NULL DEFAULT 0,
    status TINYINT NOT NULL DEFAULT 1, extra VARCHAR(1024) NOT NULL DEFAULT '', remark VARCHAR(256) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id), UNIQUE KEY uq_admin_dict_item_value (dict_type_code, value), KEY idx_admin_dict_item_list (dict_type_code, status, sort_no)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS admin_audit_log (
    id BIGINT NOT NULL AUTO_INCREMENT, admin_user_id BIGINT NOT NULL DEFAULT 0, role_codes VARCHAR(1024) NOT NULL DEFAULT '',
    request_id VARCHAR(64) NOT NULL, method VARCHAR(8) NOT NULL DEFAULT '', path VARCHAR(255) NOT NULL DEFAULT '',
    http_status INT NOT NULL DEFAULT 0, latency_ms BIGINT NOT NULL DEFAULT 0, action VARCHAR(64) NOT NULL,
    resource_type VARCHAR(64) NOT NULL, resource_id VARCHAR(128) NOT NULL DEFAULT '', reason VARCHAR(512) NOT NULL DEFAULT '',
    before_value TEXT NULL, after_value TEXT NULL, client_ip VARCHAR(45) NOT NULL DEFAULT '', user_agent VARCHAR(512) NOT NULL DEFAULT '',
    success TINYINT NOT NULL DEFAULT 1, error_message VARCHAR(512) NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id), KEY idx_audit_resource_time (resource_type, resource_id, created_at),
    KEY idx_audit_admin_time (admin_user_id, created_at), KEY idx_audit_request (request_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS admin_jobs (
    id BIGINT NOT NULL AUTO_INCREMENT, job_type VARCHAR(64) NOT NULL, status TINYINT NOT NULL DEFAULT 0,
    source_name VARCHAR(255) NOT NULL DEFAULT '', input_file VARCHAR(512) NOT NULL DEFAULT '', result_file VARCHAR(512) NOT NULL DEFAULT '',
    total_count INT NOT NULL DEFAULT 0, succeeded_count INT NOT NULL DEFAULT 0, failed_count INT NOT NULL DEFAULT 0,
    attempts INT NOT NULL DEFAULT 0, next_attempt_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at DATETIME NULL, worker_id VARCHAR(64) NOT NULL DEFAULT '', lease_until DATETIME NULL,
    finished_at DATETIME NULL, last_error VARCHAR(1024) NOT NULL DEFAULT '',
    created_by BIGINT NOT NULL DEFAULT 0, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id), KEY idx_admin_job_due (status, next_attempt_at), KEY idx_admin_job_lease (status, lease_until),
    KEY idx_admin_job_creator_time (created_by, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS admin_job_items (
    id BIGINT NOT NULL AUTO_INCREMENT, job_id BIGINT NOT NULL, row_no INT NOT NULL, status TINYINT NOT NULL DEFAULT 0,
    resource_id VARCHAR(128) NOT NULL DEFAULT '', error_code VARCHAR(64) NOT NULL DEFAULT '', error_message VARCHAR(512) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id), UNIQUE KEY uq_admin_job_row (job_id, row_no), KEY idx_admin_job_item_status (job_id, status),
    KEY idx_admin_job_item_resource (resource_id, status, job_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS config_entries (
    id BIGINT NOT NULL AUTO_INCREMENT, namespace VARCHAR(64) NOT NULL, config_key VARCHAR(128) NOT NULL,
    scope_type VARCHAR(16) NOT NULL DEFAULT 'global', scope_id VARCHAR(128) NOT NULL DEFAULT '', value TEXT NOT NULL,
    secret_value TEXT NULL, status TINYINT NOT NULL DEFAULT 1, revision BIGINT NOT NULL DEFAULT 1,
    updated_by BIGINT NOT NULL DEFAULT 0, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id), UNIQUE KEY uq_config_scope (namespace, config_key, scope_type, scope_id), KEY idx_config_namespace (namespace, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS config_publish_outbox (
    id BIGINT NOT NULL AUTO_INCREMENT, config_entry_id BIGINT NOT NULL, revision BIGINT NOT NULL,
    event_type VARCHAR(32) NOT NULL DEFAULT 'config.updated', attempts INT NOT NULL DEFAULT 0,
    next_attempt_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, delivered_at DATETIME NULL,
    last_error VARCHAR(1024) NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id), UNIQUE KEY uq_config_publish_revision (config_entry_id, revision), KEY idx_config_publish_due (delivered_at, next_attempt_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- COMMENT 的单一事实源与建表文件共同构成版本 1，避免复制数百条字段释义。
SOURCE internal/store/mysql/migrate/migrations/core/001_zzz_schema_comments.sql
SOURCE internal/store/mysql/migrate/migrations/admin/001_schema_comments.sql

INSERT IGNORE INTO schema_migrations (component, version) VALUES
    ('core', 1),
    ('admin', 1);
