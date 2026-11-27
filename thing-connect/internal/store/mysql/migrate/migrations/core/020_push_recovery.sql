ALTER TABLE push_session ADD COLUMN session_seq BIGINT NOT NULL AUTO_INCREMENT COMMENT '订阅会话创建顺序', ADD UNIQUE KEY uq_push_session_seq (session_seq);
ALTER TABLE push_subscription ADD COLUMN decision_seq BIGINT NOT NULL DEFAULT 0 COMMENT '最后生效会话序号';
ALTER TABLE push_subscription ADD COLUMN granted BOOLEAN NOT NULL DEFAULT TRUE COMMENT '当前同意订阅';
ALTER TABLE push_channel_limit ADD COLUMN cooldown_seconds INT NOT NULL DEFAULT 0 COMMENT '连续限流退避';
ALTER TABLE push_channel_limit ADD COLUMN probe_lease VARCHAR(64) NOT NULL DEFAULT '' COMMENT '恢复探测凭证';
ALTER TABLE push_channel_limit ADD COLUMN recovered_at DATETIME(6) NULL COMMENT '渐进恢复开始时间';
ALTER TABLE recognition_user_event_outbox ADD KEY idx_source_retention (registered_at,id);
-- Active grants survive upgrade. Reopen unfinished pre-migration sessions in the client.
UPDATE push_session SET expires_at=UTC_TIMESTAMP(6) WHERE decision='' AND expires_at>UTC_TIMESTAMP(6);
