CREATE TABLE IF NOT EXISTS call_contact (
    id BIGINT NOT NULL AUTO_INCREMENT,
    device_id_a VARCHAR(64) NOT NULL COMMENT '较小 device_id（字典序）',
    device_id_b VARCHAR(64) NOT NULL COMMENT '较大 device_id（字典序）',
    source VARCHAR(8) NOT NULL DEFAULT 'manual' COMMENT 'auto=同账号 / manual=跨账号',
    initiator CHAR(1) NOT NULL DEFAULT 'a',
    user_id_a BIGINT NOT NULL,
    user_id_b BIGINT NOT NULL,
    status TINYINT NOT NULL DEFAULT 1 COMMENT '0=pending 1=accepted 2=rejected 3=deleted',
    remark_a VARCHAR(64) NOT NULL DEFAULT '',
    remark_b VARCHAR(64) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_pair (device_id_a, device_id_b),
    KEY idx_call_a (device_id_a),
    KEY idx_call_b (device_id_b)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
