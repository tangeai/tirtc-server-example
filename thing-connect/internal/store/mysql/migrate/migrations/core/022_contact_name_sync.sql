ALTER TABLE voip_remark_outbox ADD COLUMN contact_type VARCHAR(16) NOT NULL DEFAULT 'voip' COMMENT '联系人类型 device 或 voip';
ALTER TABLE voip_remark_outbox MODIFY COLUMN device_id VARCHAR(64) NOT NULL COMMENT '需刷新联系人缓存的设备ID';
