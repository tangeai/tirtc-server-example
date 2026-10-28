CREATE TABLE IF NOT EXISTS ai_recognition_policy (
 device_id VARCHAR(64) NOT NULL COMMENT '设备标识',
 enabled BOOLEAN NOT NULL DEFAULT FALSE COMMENT '是否启用媒体识别',
 mode VARCHAR(32) NOT NULL DEFAULT 'image_scene' COMMENT 'audio_alarm异常声音 image_scene场景 image_encyclopedia百科',
 provider VARCHAR(16) NOT NULL DEFAULT 'qwen' COMMENT 'qwen千问 deepseek',
 monthly_limit BIGINT NOT NULL DEFAULT 100 COMMENT '每自然月请求额度，0禁止调用，未确认请求占用额度',
 revision BIGINT NOT NULL DEFAULT 0 COMMENT '配置乐观锁版本',
 PRIMARY KEY (device_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='设备媒体识别策略';
CREATE TABLE IF NOT EXISTS ai_recognition_request (
 device_id VARCHAR(64) NOT NULL COMMENT '设备标识',
 request_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '客户端幂等请求编号',
 user_id BIGINT NOT NULL COMMENT '提交时设备所属用户',
 fingerprint CHAR(64) NOT NULL COMMENT '上传媒体内容SHA256摘要',
 mode VARCHAR(32) NOT NULL COMMENT '本次识别类型',
 provider VARCHAR(16) NOT NULL COMMENT '本次模型提供商',
 model VARCHAR(128) NOT NULL COMMENT '本次模型名称',
 status VARCHAR(16) NOT NULL DEFAULT 'pending' COMMENT 'pending处理中 succeeded成功 unconfirmed结果未确认',
 result_json TEXT NULL COMMENT '结构化识别结果，不存储原始媒体',
 input_tokens BIGINT NOT NULL DEFAULT 0 COMMENT '模型报告的输入token数',
 output_tokens BIGINT NOT NULL DEFAULT 0 COMMENT '模型报告的输出token数',
 created_at DATETIME(6) NOT NULL COMMENT '提交时间，UTC',
 PRIMARY KEY (device_id,request_id),
 KEY idx_recognition_month (device_id,created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='媒体识别幂等记录和用量，未确认请求不自动重发';
