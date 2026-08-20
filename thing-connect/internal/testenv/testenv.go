package testenv

import (
	"fmt"
	"os"
	"strings"
	"testing"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"

	"thing-connect/internal/cache"
	"thing-connect/internal/config"
	"thing-connect/internal/db"
)

// LoadConfigOrSkip loads integration-test config from an explicit env var,
// then caller-provided repository paths. CI treats a missing config as a
// failure so integration coverage cannot silently disappear.
func LoadConfigOrSkip(t testing.TB, candidates ...string) *config.Config {
	t.Helper()
	path, err := findConfigPath(candidates...)
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("integration test config unavailable in CI: %v", err)
		}
		t.Skipf("integration test config unavailable: %v", err)
	}
	t.Logf("using integration test config: %s", path)
	return config.Load(path)
}

// OpenDBOrSkip opens MySQL for integration tests, skipping when the dependency
// is unavailable on the current machine.
func OpenDBOrSkip(t testing.TB, cfg *config.Config) *sqlx.DB {
	t.Helper()
	if err := validateTestDatabaseDSN(cfg.Database.DSN); err != nil {
		t.Fatalf("refusing unsafe integration-test database: %v", err)
	}
	sqlDB, err := db.Open(cfg.Database)
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("integration test database unavailable in CI: %v", err)
		}
		t.Skipf("integration test database unavailable: %v", err)
	}
	return sqlDB
}

// validateTestDatabaseDSN prevents destructive integration-test setup from
// ever running against a development or production schema. Integration tests
// intentionally truncate and rewrite shared fixture tables, so a dedicated
// schema whose name ends in _test is mandatory.
func validateTestDatabaseDSN(dsn string) error {
	cfg, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		return fmt.Errorf("invalid DSN: %w", err)
	}
	database := strings.TrimSpace(cfg.DBName)
	if database == "" {
		return fmt.Errorf("DSN must select a dedicated database ending in _test")
	}
	if !strings.HasSuffix(strings.ToLower(database), "_test") {
		return fmt.Errorf("database %q does not end in _test", database)
	}
	return nil
}

// OpenRedisOrSkip opens Redis for integration tests, skipping when the
// dependency is unavailable on the current machine.
func OpenRedisOrSkip(t testing.TB, cfg *config.Config) *redis.Client {
	t.Helper()
	rdb, err := cache.New(cfg.Redis)
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("integration test redis unavailable in CI: %v", err)
		}
		t.Skipf("integration test redis unavailable: %v", err)
	}
	return rdb
}

func findConfigPath(candidates ...string) (string, error) {
	paths := make([]string, 0, len(candidates)+1)
	if envPath := os.Getenv("THING_CONNECT_TEST_CONFIG"); envPath != "" {
		paths = append(paths, envPath)
	}
	paths = append(paths, candidates...)

	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("checked THING_CONNECT_TEST_CONFIG and %v", candidates)
}
