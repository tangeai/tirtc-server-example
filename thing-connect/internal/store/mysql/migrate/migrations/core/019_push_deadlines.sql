ALTER TABLE recognition_user_event_outbox ADD COLUMN expires_at DATETIME(6) NULL COMMENT '来源发送截止 UTC';
ALTER TABLE recognition_user_event_outbox ADD COLUMN max_attempts INT NOT NULL DEFAULT 3 COMMENT '业务投递预算';
ALTER TABLE recognition_user_event_outbox ADD COLUMN policy_revision BIGINT NOT NULL DEFAULT 0 COMMENT '来源策略版本';
ALTER TABLE recognition_user_event_outbox ADD COLUMN generation BIGINT NOT NULL DEFAULT 0 COMMENT '来源关闭代次';
ALTER TABLE recognition_user_event_outbox ADD COLUMN reserved BOOLEAN NOT NULL DEFAULT FALSE COMMENT '本次领取预占业务次数';
ALTER TABLE recognition_user_event_outbox ADD KEY idx_source_deadline (status,expires_at,id);
ALTER TABLE push_delivery ADD COLUMN expires_at DATETIME(6) NULL COMMENT '微信发送截止 UTC';
ALTER TABLE push_delivery ADD COLUMN max_attempts INT NOT NULL DEFAULT 3 COMMENT '微信调用预算';
ALTER TABLE push_delivery ADD KEY idx_delivery_deadline (status,expires_at,id);

CREATE TABLE IF NOT EXISTS recognition_push_target (
 target VARCHAR(64) NOT NULL PRIMARY KEY COMMENT '逻辑推送目标',
 generation BIGINT NOT NULL DEFAULT 0 COMMENT '生产代次',
 revision BIGINT NOT NULL DEFAULT 0 COMMENT '策略版本',
 enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT '允许生产',
 paused BOOLEAN NOT NULL DEFAULT FALSE COMMENT '鉴权暂停',
 auth_epoch BIGINT NOT NULL DEFAULT 0 COMMENT '鉴权拒绝代次',
 probe_epoch BIGINT NOT NULL DEFAULT 0 COMMENT '探测取得的代次',
 probe_lease VARCHAR(64) NOT NULL DEFAULT '' COMMENT '探测凭证',
 probe_at DATETIME(6) NULL COMMENT '下次探测时间',
 failures INT NOT NULL DEFAULT 0 COMMENT '连续探测失败次数'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='识别推送目标状态';

CREATE TABLE IF NOT EXISTS push_channel_limit (
 app_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY COMMENT '微信应用',
 available_at DATETIME(6) NULL COMMENT '速率预算下一可用时间',
 next_at DATETIME(6) NOT NULL COMMENT '下次允许调用时间'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='微信渠道共享限速';

UPDATE recognition_user_event_outbox SET expires_at=occurred_at+INTERVAL 120 SECOND WHERE expires_at IS NULL;
UPDATE push_delivery SET expires_at=occurred_at+INTERVAL 120 SECOND WHERE expires_at IS NULL;

ALTER TABLE recognition_user_event_outbox ADD COLUMN bucket INT NOT NULL DEFAULT 0 COMMENT '容量分片';
ALTER TABLE recognition_user_event_outbox ADD KEY idx_source_bucket (bucket,status,id);

ALTER TABLE push_delivery ADD COLUMN bucket INT NOT NULL DEFAULT 0 COMMENT '容量分片';
ALTER TABLE push_delivery ADD KEY idx_delivery_bucket (bucket,status,id);

CREATE TABLE IF NOT EXISTS push_queue_bucket (
 queue_name VARCHAR(16) NOT NULL COMMENT '队列名称',
 bucket INT NOT NULL COMMENT '分片编号',
 PRIMARY KEY (queue_name,bucket)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='事务内准入容量锁';
