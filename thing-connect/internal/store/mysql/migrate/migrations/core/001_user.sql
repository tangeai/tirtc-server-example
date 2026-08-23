CREATE TABLE IF NOT EXISTS users (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    email VARCHAR(255) NOT NULL UNIQUE,
    password VARCHAR(255) NOT NULL,
    bind_quota INT NOT NULL DEFAULT 10 COMMENT '剩余可绑额度',
    status TINYINT NOT NULL DEFAULT 1 COMMENT '1=active 0=disabled',
    disabled_at DATETIME NULL,
    auth_revision BIGINT NOT NULL DEFAULT 1 COMMENT '密码或账号状态变更时递增',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    KEY idx_users_created (created_at, id),
    KEY idx_users_status_created (status, created_at, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
