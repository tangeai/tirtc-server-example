CREATE TABLE IF NOT EXISTS voip_device_profile (
    id BIGINT NOT NULL AUTO_INCREMENT,
    device_id VARCHAR(20) NOT NULL,
    profile VARCHAR(512) NOT NULL COMMENT 'JSON media params',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_voip_device_id (device_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS voip_device_auth (
    id BIGINT NOT NULL AUTO_INCREMENT,
    device_id VARCHAR(20) NOT NULL,
    wx_open_id VARCHAR(64) NOT NULL,
    wx_app_id VARCHAR(64) NOT NULL DEFAULT '',
    wx_model_id VARCHAR(64) NOT NULL DEFAULT '',
    remark VARCHAR(64) NOT NULL DEFAULT '' COMMENT '备注名',
    authorized_device_name VARCHAR(64) NOT NULL DEFAULT '' COMMENT '最近一次微信授权时的设备名称快照',
    auth_status VARCHAR(16) NOT NULL DEFAULT 'active' COMMENT 'active/invalid',
    invalid_reason VARCHAR(64) NOT NULL DEFAULT '',
    invalid_at DATETIME DEFAULT NULL,
    last_verified_at DATETIME DEFAULT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_voip_device_auth (device_id, wx_open_id, wx_app_id),
    KEY idx_voip_identity_recent (wx_open_id, wx_app_id, created_at, id),
    KEY idx_voip_device_status (device_id, auth_status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS voip_user_profile (
    id BIGINT NOT NULL AUTO_INCREMENT,
    wx_open_id VARCHAR(64) NOT NULL,
    wx_app_id VARCHAR(64) NOT NULL DEFAULT '',
    remark VARCHAR(64) NOT NULL DEFAULT '' COMMENT 'OpenID 在所有设备联系人列表中的统一备注',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_voip_user_profile (wx_open_id, wx_app_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

ALTER TABLE voip_device_auth ADD COLUMN remark VARCHAR(64) NOT NULL DEFAULT '' COMMENT '备注名';
ALTER TABLE voip_device_auth ADD COLUMN authorized_device_name VARCHAR(64) NOT NULL DEFAULT '' COMMENT '最近一次微信授权时的设备名称快照' AFTER remark;
ALTER TABLE voip_device_auth ADD COLUMN auth_status VARCHAR(16) NOT NULL DEFAULT 'active' COMMENT 'active/invalid' AFTER authorized_device_name;
ALTER TABLE voip_device_auth ADD COLUMN invalid_reason VARCHAR(64) NOT NULL DEFAULT '' AFTER auth_status;
ALTER TABLE voip_device_auth ADD COLUMN invalid_at DATETIME DEFAULT NULL AFTER invalid_reason;
ALTER TABLE voip_device_auth ADD COLUMN last_verified_at DATETIME DEFAULT NULL AFTER invalid_at;
ALTER TABLE voip_device_auth ADD KEY idx_voip_identity_recent (wx_open_id, wx_app_id, created_at, id);
ALTER TABLE voip_device_auth ADD KEY idx_voip_device_status (device_id, auth_status);
UPDATE voip_device_auth SET authorized_device_name=device_id WHERE authorized_device_name='';

INSERT IGNORE INTO voip_user_profile (wx_open_id, wx_app_id, remark)
SELECT current_auth.wx_open_id, current_auth.wx_app_id, current_auth.remark
FROM voip_device_auth current_auth
LEFT JOIN voip_device_auth newer_auth
  ON newer_auth.wx_open_id=current_auth.wx_open_id
 AND newer_auth.wx_app_id=current_auth.wx_app_id
 AND (newer_auth.created_at>current_auth.created_at
   OR (newer_auth.created_at=current_auth.created_at AND newer_auth.id>current_auth.id))
WHERE newer_auth.id IS NULL;

UPDATE voip_device_auth auth
JOIN voip_user_profile profile
  ON profile.wx_open_id=auth.wx_open_id AND profile.wx_app_id=auth.wx_app_id
SET auth.remark=profile.remark;
