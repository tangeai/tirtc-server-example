CREATE TABLE IF NOT EXISTS admin_users (
    id BIGINT NOT NULL AUTO_INCREMENT, email VARCHAR(255) NOT NULL, password VARCHAR(255) NOT NULL,
    nick_name VARCHAR(64) NOT NULL DEFAULT '', status TINYINT NOT NULL DEFAULT 1,
    auth_revision BIGINT NOT NULL DEFAULT 1, must_change_password TINYINT NOT NULL DEFAULT 0,
    password_updated_at DATETIME NULL, last_login_ip VARCHAR(45) NOT NULL DEFAULT '', last_login_at DATETIME NULL,
    remark VARCHAR(256) NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id), UNIQUE KEY uq_admin_email (email), KEY idx_admin_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

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
    started_at DATETIME NULL,
    finished_at DATETIME NULL, last_error VARCHAR(1024) NOT NULL DEFAULT '',
    created_by BIGINT NOT NULL DEFAULT 0, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id), KEY idx_admin_job_due (status, next_attempt_at),
    KEY idx_admin_job_creator_time (created_by, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS admin_job_items (
    id BIGINT NOT NULL AUTO_INCREMENT, job_id BIGINT NOT NULL, row_no INT NOT NULL, status TINYINT NOT NULL DEFAULT 0,
    resource_id VARCHAR(128) NOT NULL DEFAULT '', error_code VARCHAR(64) NOT NULL DEFAULT '', error_message VARCHAR(512) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id), UNIQUE KEY uq_admin_job_row (job_id, row_no), KEY idx_admin_job_item_status (job_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS config_entries (
    id BIGINT NOT NULL AUTO_INCREMENT, namespace VARCHAR(64) NOT NULL, config_key VARCHAR(128) NOT NULL,
    scope_type VARCHAR(16) NOT NULL DEFAULT 'global', scope_id VARCHAR(128) NOT NULL DEFAULT '', value TEXT NOT NULL,
    status TINYINT NOT NULL DEFAULT 1, revision BIGINT NOT NULL DEFAULT 1,
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
