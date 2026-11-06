ALTER TABLE ai_user_role ADD COLUMN deleting TINYINT(1) NOT NULL DEFAULT 0 COMMENT '角色删除中标记，未完成时禁止设备应用';
CREATE TABLE IF NOT EXISTS ai_cloud_role_ref (
    device_id VARCHAR(64) NOT NULL COMMENT '云端关联或待确认关联的设备ID',
    role_id VARCHAR(64) NOT NULL COMMENT '被引用的角色ID',
    user_id BIGINT NOT NULL COMMENT '发起关联的用户ID',
    PRIMARY KEY (device_id, role_id),
    KEY idx_ai_cloud_role (role_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='云端角色关联的持久化删除保护';
