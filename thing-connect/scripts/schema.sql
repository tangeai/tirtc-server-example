-- thing-connect 数据库建表语句
--
-- 权威来源：internal/db/migrate.go（服务启动时自动执行）
-- 本文件供全新安装直接导入：mysql -u root -p < scripts/schema.sql
--
-- 使用说明：
--   如果你手上已经有旧数据库，不要直接导入此文件——高版本 migrate.go 会自动补到最新。
--   此文件用于全新环境建库，每个 CREATE TABLE 都是表的最终形态，不走 ALTER。

CREATE TABLE IF NOT EXISTS users (
    id         BIGINT AUTO_INCREMENT PRIMARY KEY,
    email      VARCHAR(255) NOT NULL UNIQUE,
    password   VARCHAR(255) NOT NULL,
    bind_quota INT          NOT NULL DEFAULT 10 COMMENT '剩余可绑额度',
    created_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
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
    PRIMARY KEY (id),
    UNIQUE KEY uq_device_id (device_id),
    KEY idx_user_id (user_id),
    KEY idx_mac     (mac)
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
    KEY idx_device_a (device_id_a),
    KEY idx_device_b (device_id_b)
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

-- 旧数据升级：同一 OpenID + AppID 采用最近一条授权记录的备注，并同步到所有设备。
INSERT IGNORE INTO voip_user_profile (wx_open_id, wx_app_id, remark)
SELECT current_auth.wx_open_id, current_auth.wx_app_id, current_auth.remark
FROM voip_device_auth current_auth
LEFT JOIN voip_device_auth newer_auth
  ON newer_auth.wx_open_id = current_auth.wx_open_id
 AND newer_auth.wx_app_id = current_auth.wx_app_id
 AND (
      newer_auth.created_at > current_auth.created_at
   OR (newer_auth.created_at = current_auth.created_at AND newer_auth.id > current_auth.id)
 )
WHERE newer_auth.id IS NULL;

UPDATE voip_device_auth auth
JOIN voip_user_profile profile
  ON profile.wx_open_id = auth.wx_open_id
 AND profile.wx_app_id = auth.wx_app_id
SET auth.remark = profile.remark;

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
