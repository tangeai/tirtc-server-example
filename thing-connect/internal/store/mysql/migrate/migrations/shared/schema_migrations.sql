CREATE TABLE IF NOT EXISTS schema_migrations (
    component VARCHAR(64) NOT NULL,
    version BIGINT NOT NULL,
    applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (component, version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
