CREATE TABLE IF NOT EXISTS recognition_user_event_outbox (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY COMMENT '来源事件记录',
 device_id VARCHAR(64) NOT NULL COMMENT '设备标识',
 bind_id VARCHAR(128) NOT NULL COMMENT '原任务绑定代次',
 source_event_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '请求编号与事件类型',
 event_type VARCHAR(24) NOT NULL COMMENT '通知类型',
 occurred_at DATETIME(6) NOT NULL COMMENT '音频受理时间 UTC',
 registered_at DATETIME(6) NOT NULL COMMENT '首次登记时间 UTC',
 next_at DATETIME(6) NOT NULL COMMENT '下次可领取时间 UTC',
 status VARCHAR(32) NOT NULL DEFAULT 'pending' COMMENT '来源投递状态',
 attempts INT NOT NULL DEFAULT 0 COMMENT '已尝试次数',
 confirm_attempts INT NOT NULL DEFAULT 0 COMMENT '到期确认次数',
 lease VARCHAR(64) NOT NULL DEFAULT '' COMMENT '当前领取凭证',
 UNIQUE KEY uq_rec_user_event (device_id,bind_id,source_event_id),
 KEY idx_rec_user_queue (status,next_at,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='识别用户通知待发送事件';
