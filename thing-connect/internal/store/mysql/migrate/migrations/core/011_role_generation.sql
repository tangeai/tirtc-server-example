ALTER TABLE ai_device_role ADD COLUMN binding_revision BIGINT NOT NULL DEFAULT 0 COMMENT '设备归属代次';
UPDATE ai_device_role r JOIN device_bind d ON d.device_id=r.device_id SET r.binding_revision=d.binding_revision WHERE r.binding_revision=0;
CREATE TABLE IF NOT EXISTS ai_cloud_role_target (
 device_id VARCHAR(64) NOT NULL PRIMARY KEY COMMENT '设备 ID',
 user_id BIGINT NOT NULL COMMENT '目标所属用户',
 binding_revision BIGINT NOT NULL COMMENT '设备归属代次',
 role_id VARCHAR(64) NOT NULL DEFAULT '' COMMENT '目标角色 ID',
 remove_role TINYINT(1) NOT NULL DEFAULT 0 COMMENT '是否清除云端角色',
 attempts INT NOT NULL DEFAULT 0 COMMENT '对账重试次数',
 next_attempt_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '下次对账时间',
 KEY idx_ai_cloud_role_target_due(next_attempt_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='设备代次下的云端角色目标及持久化对账队列';
