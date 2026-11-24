CREATE TABLE IF NOT EXISTS admin_reset_mail_outbox (
    id BIGINT NOT NULL AUTO_INCREMENT COMMENT '管理员邮件命令编号，与操作审计关联',
    user_id BIGINT NOT NULL COMMENT '目标用户编号',
    email VARCHAR(255) NOT NULL COMMENT '接受命令时的目标邮箱',
    attempts INT NOT NULL DEFAULT 0 COMMENT '累计领取次数，作为租约确认快照',
    next_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '下次领取或当前租约到期时间',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '首次等待时间',
    PRIMARY KEY (id),
    KEY idx_reset_mail_due (next_at,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='管理员密码重置邮件待发送命令';
