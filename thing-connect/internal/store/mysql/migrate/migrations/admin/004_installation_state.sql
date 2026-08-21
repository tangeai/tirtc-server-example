CREATE TABLE IF NOT EXISTS thingconnect_installation_state (
    id TINYINT NOT NULL,
    product VARCHAR(64) NOT NULL,
    instance_id CHAR(36) NOT NULL,
    operation_id CHAR(36) NOT NULL,
    status VARCHAR(32) NOT NULL,
    stage VARCHAR(64) NOT NULL,
    generation BIGINT NOT NULL DEFAULT 1,
    config_digest CHAR(64) NOT NULL DEFAULT '',
    last_error_code VARCHAR(64) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_install_instance (instance_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT IGNORE INTO thingconnect_installation_state
    (id, product, instance_id, operation_id, status, stage, generation)
VALUES
    (1, 'thingconnect', UUID(), UUID(), 'migration_only', 'migration_claimed', 1);
