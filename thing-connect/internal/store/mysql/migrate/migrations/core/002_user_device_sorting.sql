ALTER TABLE users ADD KEY idx_users_created (created_at, id);
ALTER TABLE users ADD KEY idx_users_status_created (status, created_at, id);
ALTER TABLE device_bind ADD KEY idx_device_active_time (active_time, id);
ALTER TABLE device_bind ADD KEY idx_device_bind_time (bind_time, id);
