ALTER TABLE push_subscription ADD COLUMN bind_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '授权绑定代次';
ALTER TABLE push_subscription ADD COLUMN model_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '微信设备型号';
ALTER TABLE push_session ADD COLUMN device_id VARCHAR(64) NOT NULL DEFAULT '' COMMENT '授权设备编号';
ALTER TABLE push_session ADD COLUMN bind_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT '授权绑定代次';
ALTER TABLE push_session ADD COLUMN model_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '微信设备型号';
ALTER TABLE push_subscription DROP PRIMARY KEY, ADD PRIMARY KEY (generation,template_id,bind_id);
