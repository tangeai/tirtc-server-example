CREATE TABLE IF NOT EXISTS user_connection (
 user_id BIGINT NOT NULL PRIMARY KEY COMMENT '业务用户 ID',
 registration_ip VARCHAR(45) NOT NULL DEFAULT '' COMMENT '注册请求来源 IP',
 last_login_ip VARCHAR(45) NOT NULL DEFAULT '' COMMENT '最近成功登录来源 IP',
 last_login_at DATETIME(6) NULL COMMENT '最近成功登录时间 UTC'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='业务用户注册与登录来源';
CREATE TABLE IF NOT EXISTS device_connection (
 device_id VARCHAR(64) NOT NULL PRIMARY KEY COMMENT '正式设备 ID',
 last_online_ip VARCHAR(45) NOT NULL DEFAULT '' COMMENT 'Broker 观测的最近上线 IP',
 last_online_at DATETIME(3) NOT NULL COMMENT '最近 MQTT 连接时间 UTC'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='设备最近 MQTT 连接来源';
