CREATE TABLE IF NOT EXISTS push_event (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY COMMENT '记录编号',
 device_id VARCHAR(64) NOT NULL COMMENT '设备编号',
 bind_id VARCHAR(128) NOT NULL COMMENT '设备绑定代次',
 source_event_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '来源事件编号',
 registered_at DATETIME(6) NOT NULL COMMENT '来源首次登记时间',
 received_at DATETIME(6) NOT NULL COMMENT '通知首次受理时间',
 outcome VARCHAR(32) NOT NULL COMMENT '准入结果',
 UNIQUE KEY uq_push_event (device_id,bind_id,source_event_id),
 KEY idx_push_retention (received_at,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='用户通知事件与准入结果';

CREATE TABLE IF NOT EXISTS push_window (
 user_id BIGINT NOT NULL COMMENT '当前账号',
 device_id VARCHAR(64) NOT NULL COMMENT '设备编号',
 bind_id VARCHAR(128) NOT NULL COMMENT '设备绑定代次',
 event_type VARCHAR(24) NOT NULL COMMENT '设备事件类型',
 interval_seconds INT NULL COMMENT '同类提醒间隔秒数',
 last_admitted DATETIME(6) NULL COMMENT '最近准入时间',
 PRIMARY KEY (user_id,device_id,bind_id,event_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='事件间隔偏好及最近准入时间';

CREATE TABLE IF NOT EXISTS push_recipient (
 user_id BIGINT NOT NULL PRIMARY KEY COMMENT '当前账号',
 app_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '小程序编号',
 open_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '微信接收身份',
 generation VARCHAR(36) NOT NULL COMMENT '接收身份关联代次',
 UNIQUE KEY uq_push_recipient (app_id,open_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='用户当前接收微信身份';

CREATE TABLE IF NOT EXISTS push_subscription (
 generation VARCHAR(36) NOT NULL COMMENT '接收身份关联代次',
 template_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '订阅模板标识',
 grant_id VARCHAR(36) NOT NULL COMMENT '本次授权会话编号',
 PRIMARY KEY (generation,template_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='当前身份已同意的长期订阅';

CREATE TABLE IF NOT EXISTS push_session (
 id VARCHAR(36) NOT NULL PRIMARY KEY COMMENT '记录编号',
 user_id BIGINT NOT NULL COMMENT '当前账号',
 app_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '小程序编号',
 open_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '微信接收身份',
 template_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '订阅模板标识',
 old_generation VARCHAR(36) NOT NULL COMMENT '原接收身份代次',
 other_generation VARCHAR(36) NOT NULL COMMENT '另一账号原关联代次',
 generation VARCHAR(36) NOT NULL DEFAULT '' COMMENT '接收身份关联代次',
 decision VARCHAR(16) NOT NULL DEFAULT '' COMMENT '订阅操作结果',
 expires_at DATETIME(6) NOT NULL COMMENT '到期时间',
 KEY idx_push_session_expiry (expires_at,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='订阅操作及身份替换确认';

CREATE TABLE IF NOT EXISTS push_delivery (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY COMMENT '记录编号',
 event_id BIGINT NOT NULL COMMENT '通知事件编号',
 user_id BIGINT NOT NULL COMMENT '当前账号',
 device_id VARCHAR(64) NOT NULL COMMENT '设备编号',
 device_name VARCHAR(64) NOT NULL COMMENT '受理时设备名称',
 bind_id VARCHAR(128) NOT NULL COMMENT '设备绑定代次',
 event_type VARCHAR(24) NOT NULL COMMENT '设备事件类型',
 generation VARCHAR(36) NOT NULL COMMENT '接收身份关联代次',
 template_id VARCHAR(128) NOT NULL COMMENT '订阅模板标识',
 grant_id VARCHAR(36) NOT NULL COMMENT '首次预检的授权会话编号',
 occurred_at DATETIME(6) NOT NULL COMMENT '报警展示时间',
 reference_id VARCHAR(36) NOT NULL COMMENT '通知点击引用',
 started_at DATETIME(6) NOT NULL COMMENT '投递准入时间',
 status VARCHAR(32) NOT NULL COMMENT '投递状态',
 attempts INT NOT NULL DEFAULT 0 COMMENT '发送尝试数',
 lease VARCHAR(36) NOT NULL DEFAULT '' COMMENT '工作领取凭证',
 next_at DATETIME(6) NOT NULL COMMENT '下次处理时间',
 finished_at DATETIME(6) NULL COMMENT '终态时间',
 UNIQUE KEY uq_push_delivery_event (event_id),
 UNIQUE KEY uq_push_delivery_reference (reference_id),
 KEY idx_push_delivery_queue (status,next_at,id),
 KEY idx_push_delivery_expiry (status,started_at,id),
 KEY idx_push_delivery_recipient (generation,status),
 KEY idx_push_delivery_cleanup (finished_at,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='微信订阅通知投递任务';
