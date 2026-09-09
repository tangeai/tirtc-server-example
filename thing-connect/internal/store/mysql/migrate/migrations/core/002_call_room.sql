CREATE TABLE IF NOT EXISTS call_rooms (
 room_id VARCHAR(64) NOT NULL COMMENT '永不复用的业务房间标识',
 room_code CHAR(6) NOT NULL COMMENT '允许前导零的展示房间号',
 owner_user_id BIGINT NOT NULL COMMENT '创建账号',
 password_verifier VARBINARY(32) DEFAULT NULL COMMENT '带服务端密钥的密码校验值',
 status VARCHAR(24) NOT NULL COMMENT 'waiting_join active empty_grace closed',
 empty_deadline DATETIME(6) DEFAULT NULL COMMENT '连续空房截止时间',
 closed_at DATETIME(6) DEFAULT NULL COMMENT '关闭时间',
 created_at DATETIME(6) NOT NULL COMMENT '创建时间',
 participant_limit SMALLINT NOT NULL COMMENT '有效连接容量上限',
 PRIMARY KEY (room_id),
 KEY idx_intercom_due (status, empty_deadline)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='多人对讲房间';

CREATE TABLE IF NOT EXISTS call_room_codes (
 room_code CHAR(6) NOT NULL COMMENT '房间号唯一占用',
 room_id VARCHAR(64) NOT NULL COMMENT '最近使用该号码的房间',
 reusable_at DATETIME(6) DEFAULT NULL COMMENT '为空表示占用，否则为冷却截止时间',
 PRIMARY KEY (room_code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='多人对讲房间号及冷却登记';

CREATE TABLE IF NOT EXISTS call_assignments (
 device_id VARCHAR(64) NOT NULL COMMENT '目标设备',
 owner_user_id BIGINT NOT NULL DEFAULT 0 COMMENT '关系所属账号',
 room_id VARCHAR(64) NOT NULL DEFAULT '' COMMENT '期望房间',
 desired_state VARCHAR(16) NOT NULL DEFAULT 'left' COMMENT 'joined left',
 assignment_version BIGINT NOT NULL DEFAULT 0 COMMENT '关系单调版本',
 state VARCHAR(24) NOT NULL DEFAULT 'left' COMMENT '设备执行状态',
 session_id VARCHAR(64) NOT NULL DEFAULT '' COMMENT '最近一次连接代次',
 password_failures INT NOT NULL DEFAULT 0 COMMENT '连续密码错误次数',
 locked_until DATETIME(6) DEFAULT NULL COMMENT '密码重试锁定截止',
 PRIMARY KEY (device_id),
 KEY idx_intercom_assignment_room (room_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='设备多人对讲期望状态';

CREATE TABLE IF NOT EXISTS call_leases (
 device_id VARCHAR(64) NOT NULL COMMENT '同一设备唯一租约',
 room_id VARCHAR(64) NOT NULL COMMENT '连接房间',
 session_id VARCHAR(64) NOT NULL COMMENT '连接代次',
 assignment_version BIGINT NOT NULL COMMENT '连接对应关系版本',
 state VARCHAR(16) NOT NULL COMMENT 'connecting joined ended',
 expires_at DATETIME(6) NOT NULL COMMENT '有效期限或实际结束时间',
 PRIMARY KEY (device_id),
 KEY idx_intercom_lease_room (room_id, expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='多人对讲在线租约及容量预占';

CREATE TABLE IF NOT EXISTS call_requests (
 request_key VARCHAR(200) NOT NULL COMMENT '账号设备操作及客户端幂等键',
 fingerprint CHAR(64) NOT NULL COMMENT '带密钥的请求摘要',
 response JSON NOT NULL COMMENT '首次提交的无凭证状态响应',
 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '幂等记录创建时间',
 PRIMARY KEY (request_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='多人对讲控制操作幂等记录';

CREATE TABLE IF NOT EXISTS call_outbox (
 event_id CHAR(32) NOT NULL COMMENT '稳定通知去重标识',
 device_id VARCHAR(64) NOT NULL COMMENT '通知目标',
 assignment_version BIGINT NOT NULL COMMENT '触发同步的关系版本',
 attempts INT NOT NULL DEFAULT 0 COMMENT '通知尝试次数',
 next_attempt_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '下一次投递时间',
 PRIMARY KEY (event_id),
 KEY idx_call_outbox_due (next_attempt_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='多人对讲事务通知队列';
