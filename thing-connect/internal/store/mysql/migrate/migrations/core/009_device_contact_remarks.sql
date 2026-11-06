ALTER TABLE voip_device_auth ADD COLUMN device_remark VARCHAR(64) NOT NULL DEFAULT '' COMMENT '当前设备对该微信联系人的独立备注，空值表示未设置';
CREATE TABLE IF NOT EXISTS voip_remark_outbox (
    id BIGINT NOT NULL AUTO_INCREMENT COMMENT '通知事件ID',
    device_id VARCHAR(20) NOT NULL COMMENT '需刷新联系人缓存的设备ID',
    peer_id VARCHAR(64) NOT NULL COMMENT '微信联系人OpenID',
    attempts INT NOT NULL DEFAULT 0 COMMENT '已失败次数，最多记录7次',
    next_attempt_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '下次投递时间',
    PRIMARY KEY (id),
    KEY idx_voip_remark_due (next_attempt_at, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='设备独立备注的缓存刷新通知';
