CREATE TABLE IF NOT EXISTS device_pool (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    device_id VARCHAR(64) NOT NULL UNIQUE,
    device_key VARCHAR(64) NOT NULL,
    status TINYINT NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_status (status),
    INDEX idx_alloc (status, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS device_bind (
    id BIGINT NOT NULL AUTO_INCREMENT,
    device_id VARCHAR(64) NOT NULL,
    mac VARCHAR(32) NOT NULL DEFAULT '',
    chip_uid VARCHAR(128) NOT NULL DEFAULT '',
    device_rand VARCHAR(64) NOT NULL DEFAULT '' COMMENT '设备端维护的稳定唯一标识',
    assign VARCHAR(16) NOT NULL DEFAULT '' COMMENT 'dynamic=pool分配 preburn=出厂预烧',
    device_name VARCHAR(64) NOT NULL DEFAULT '' COMMENT '当前绑定用户设置的设备名称，解绑清空',
    user_id BIGINT NOT NULL DEFAULT 0 COMMENT '0=无主/未绑定',
    last_user_id BIGINT NOT NULL DEFAULT 0 COMMENT '最后一任主人',
    active_time DATETIME DEFAULT NULL,
    bind_time DATETIME DEFAULT NULL,
    unbind_time DATETIME DEFAULT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    mac_user_key VARCHAR(64)
        AS (IF(mac='' OR user_id=0, NULL, CONCAT(mac, ':', user_id))) STORED,
    PRIMARY KEY (id),
    UNIQUE KEY uq_device_id (device_id),
    KEY idx_user_id (user_id),
    KEY idx_mac (mac),
    KEY idx_device_active_time (active_time, id),
    KEY idx_device_bind_time (bind_time, id),
    UNIQUE KEY uq_mac_user (mac_user_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS device_bind_log (
    id BIGINT NOT NULL AUTO_INCREMENT,
    device_id VARCHAR(64) NOT NULL,
    user_id BIGINT NOT NULL,
    action TINYINT NOT NULL COMMENT '1=bind 2=unbind',
    mac VARCHAR(32) NOT NULL DEFAULT '',
    chip_uid VARCHAR(128) NOT NULL DEFAULT '',
    device_rand VARCHAR(64) NOT NULL DEFAULT '',
    assign VARCHAR(16) NOT NULL DEFAULT '' COMMENT 'dynamic=pool分配 preburn=出厂预烧',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    KEY idx_device (device_id),
    KEY idx_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS cleanup_outbox (
    id BIGINT NOT NULL AUTO_INCREMENT,
    device_id VARCHAR(64) NOT NULL,
    target VARCHAR(32) NOT NULL,
    attempts INT NOT NULL DEFAULT 0,
    next_attempt_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_error VARCHAR(1024) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_cleanup_device_target (device_id, target),
    KEY idx_cleanup_due (next_attempt_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
