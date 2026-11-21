ALTER TABLE voip_remark_outbox ADD COLUMN pending_since TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '首次入队时间，旧记录从迁移时开始计龄';
ALTER TABLE recognition_outbox ADD COLUMN pending_since TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '首次入队时间，旧记录从迁移时开始计龄';
ALTER TABLE ai_cloud_role_target ADD COLUMN pending_since TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '当前连续待处理周期起点，替换未完成目标时保留';
-- Legacy first attempts used the session clock instead of UTC. Initial
-- notifications have no scheduled-delay contract; preserve active leases.
UPDATE recognition_outbox SET next_at=LEAST(next_at,UTC_TIMESTAMP(6)) WHERE delivered=FALSE AND attempts=0;
