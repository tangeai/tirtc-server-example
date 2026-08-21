ALTER TABLE admin_jobs ADD COLUMN worker_id VARCHAR(64) NOT NULL DEFAULT '' AFTER started_at;
ALTER TABLE admin_jobs ADD COLUMN lease_until DATETIME NULL AFTER worker_id;
ALTER TABLE admin_jobs ADD KEY idx_admin_job_lease (status, lease_until);
ALTER TABLE admin_job_items ADD KEY idx_admin_job_item_resource (resource_id, status, job_id);
UPDATE admin_jobs SET status=0,worker_id='',lease_until=NULL,next_attempt_at=NOW(),last_error='服务升级后自动重新排队' WHERE status=1;
