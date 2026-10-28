ALTER TABLE device_bind ADD COLUMN binding_revision BIGINT NOT NULL DEFAULT 1 COMMENT '绑定生命周期版本，解绑递增以隔离旧任务';
CREATE TABLE IF NOT EXISTS recognition_scheme (
 id VARCHAR(64) NOT NULL PRIMARY KEY COMMENT '记录主键',
 revision BIGINT NOT NULL DEFAULT 0 COMMENT '编辑版本号',
 version BIGINT NOT NULL DEFAULT 0 COMMENT '发布版本号',
 enabled BOOLEAN NOT NULL DEFAULT FALSE COMMENT '是否可供用户选择',
 draft JSON NOT NULL COMMENT '识别类型草稿 JSON',
 updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '更新时间'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI识别类型草稿';
CREATE TABLE IF NOT EXISTS recognition_scheme_version (
 scheme_id VARCHAR(64) NOT NULL COMMENT '类型标识',
 version BIGINT NOT NULL COMMENT '发布版本号',
 snapshot JSON NOT NULL COMMENT '不可变配置或任务状态快照',
 PRIMARY KEY(scheme_id,version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='不可变识别类型版本';
CREATE TABLE IF NOT EXISTS recognition_device (
 device_id VARCHAR(64) NOT NULL PRIMARY KEY COMMENT '设备标识',
 bind_id VARCHAR(128) NOT NULL COMMENT '绑定记录主键与生命周期版本',
 policy JSON NOT NULL COMMENT '用户选择及管理员限额'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='设备音频识别开关顺序和限额';
CREATE TABLE IF NOT EXISTS recognition_task (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY COMMENT '记录主键',
 device_id VARCHAR(64) NOT NULL COMMENT '设备标识',
 request_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '设备幂等请求号',
 bind_id VARCHAR(128) NOT NULL COMMENT '绑定记录主键与生命周期版本',
 fingerprint CHAR(64) NOT NULL COMMENT '音频内容 SHA256',
 config_revision BIGINT NOT NULL COMMENT '受理时设备配置版本',
 month CHAR(7) NOT NULL COMMENT 'UTC自然月',
 units INT NOT NULL COMMENT '受理预占类型次数',
 status VARCHAR(24) NOT NULL COMMENT '执行状态',
 snapshot JSON NOT NULL COMMENT '不可变配置或任务状态快照',
 media MEDIUMBLOB NULL COMMENT '待分析原始音频',
 lease VARCHAR(64) NOT NULL DEFAULT '' COMMENT '工作领取凭证',
 lease_until DATETIME(6) NULL COMMENT '工作租约截止时间',
 created_at DATETIME(6) NOT NULL COMMENT '创建时间 UTC',
 UNIQUE KEY uq_recognition_request(device_id,request_id),
 KEY idx_recognition_queue(status,id),
 KEY idx_recognition_quota(device_id,month),
 KEY idx_recognition_owner(bind_id,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='异步识别父任务和原始音频';
CREATE TABLE IF NOT EXISTS recognition_audio (
 task_id BIGINT NOT NULL COMMENT '父任务主键',
 item_id VARCHAR(64) NOT NULL COMMENT '任务内子项标识',
 data MEDIUMBLOB NOT NULL COMMENT '合成WAV完整音频',
 PRIMARY KEY(task_id,item_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='完整合成语音，通过鉴权接口下载';
CREATE TABLE IF NOT EXISTS recognition_call (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY COMMENT '记录主键',
 task_id BIGINT NOT NULL COMMENT '父任务主键',
 item_id VARCHAR(64) NOT NULL COMMENT '任务内子项标识',
 stage VARCHAR(16) NOT NULL COMMENT 'analysis或tts',
 status VARCHAR(24) NOT NULL COMMENT '执行状态',
 usage_json JSON NULL COMMENT '提供商上报用量，缺失为NULL',
 started_at DATETIME(6) NOT NULL COMMENT '调用开始时间',
 finished_at DATETIME(6) NULL COMMENT '调用结束时间',
 UNIQUE KEY uq_recognition_call(task_id,item_id,stage)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='每次分析或语音合成的实际调用账本';
CREATE TABLE IF NOT EXISTS recognition_outbox (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY COMMENT '记录主键',
 device_id VARCHAR(64) NOT NULL COMMENT '设备标识',
 bind_id VARCHAR(128) NOT NULL COMMENT '绑定记录主键与生命周期版本',
 payload JSON NOT NULL COMMENT '固定通知信封',
 attempts INT NOT NULL DEFAULT 0 COMMENT '已投递次数',
 next_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '下次投递时间',
 delivered BOOLEAN NOT NULL DEFAULT FALSE COMMENT 'Broker是否已接收',
 KEY idx_recognition_delivery(delivered,next_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='持久化结果和配置通知，有限重试后查询补偿';
