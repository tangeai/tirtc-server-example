package testenv

import (
	"fmt"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"

	"thing-connect/internal/cache"
	"thing-connect/internal/config"
	"thing-connect/internal/db"
)

const deployedConfigPath = "/data/mqtt-demo.tange-ai.com/user-server/config.yaml"

// LoadConfigOrSkip loads integration-test config from an explicit env var,
// then caller-provided candidates, then the deployed local environment.
func LoadConfigOrSkip(t testing.TB, candidates ...string) *config.Config {
	t.Helper()
	path, err := findConfigPath(candidates...)
	if err != nil {
		t.Skipf("integration test config unavailable: %v", err)
	}
	t.Logf("using integration test config: %s", path)
	return config.Load(path)
}

// OpenDBOrSkip opens MySQL for integration tests, skipping when the dependency
// is unavailable on the current machine.
func OpenDBOrSkip(t testing.TB, cfg *config.Config) *sqlx.DB {
	t.Helper()
	sqlDB, err := db.Open(cfg.Database)
	if err != nil {
		t.Skipf("integration test database unavailable: %v", err)
	}
	return sqlDB
}

// OpenRedisOrSkip opens Redis for integration tests, skipping when the
// dependency is unavailable on the current machine.
func OpenRedisOrSkip(t testing.TB, cfg *config.Config) *redis.Client {
	t.Helper()
	rdb, err := cache.New(cfg.Redis)
	if err != nil {
		t.Skipf("integration test redis unavailable: %v", err)
	}
	return rdb
}

func findConfigPath(candidates ...string) (string, error) {
	paths := make([]string, 0, len(candidates)+2)
	if envPath := os.Getenv("THING_CONNECT_TEST_CONFIG"); envPath != "" {
		paths = append(paths, envPath)
	}
	paths = append(paths, candidates...)
	paths = append(paths, deployedConfigPath)

	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("checked THING_CONNECT_TEST_CONFIG, %v, and %s", candidates, deployedConfigPath)
}
