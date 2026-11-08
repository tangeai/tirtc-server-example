package db

import (
	"testing"
	"thing-connect/internal/config"
	"time"
)

type recordingPool struct {
	maxOpenConns int
	maxIdleConns int
	maxIdleTime  time.Duration
	maxLifetime  time.Duration
}

func (p *recordingPool) SetMaxOpenConns(n int)              { p.maxOpenConns = n }
func (p *recordingPool) SetMaxIdleConns(n int)              { p.maxIdleConns = n }
func (p *recordingPool) SetConnMaxIdleTime(d time.Duration) { p.maxIdleTime = d }
func (p *recordingPool) SetConnMaxLifetime(d time.Duration) { p.maxLifetime = d }

func TestConfigureMySQLPoolExpiresIdleConnectionsBeforeServer(t *testing.T) {
	pool := &recordingPool{}
	configureMySQLPool(pool, 50, 10)

	if pool.maxOpenConns != 50 {
		t.Fatalf("max open connections = %d, want 50", pool.maxOpenConns)
	}
	if pool.maxIdleConns != 10 {
		t.Fatalf("max idle connections = %d, want 10", pool.maxIdleConns)
	}
	if pool.maxIdleTime != 4*time.Minute {
		t.Fatalf("max idle time = %s, want 4m", pool.maxIdleTime)
	}
	if pool.maxLifetime != 30*time.Minute {
		t.Fatalf("max lifetime = %s, want 30m", pool.maxLifetime)
	}
}

func TestOpenRejectsInvalidPoolBeforeConnecting(t *testing.T) {
	idle := 3
	pool, err := Open(config.DatabaseCfg{DSN: "not-a-valid-dsn", MaxOpenConns: 2, MaxIdleConns: &idle})
	if pool != nil || err == nil || err.Error() != "database.max_idle_conns must be between 0 and max_open_conns" {
		t.Fatalf("expected pool validation before driver open, got %v", err)
	}
}

func TestConfigureCustomMySQLPool(t *testing.T) {
	for _, idle := range []int{0, 2} {
		pool := &recordingPool{}
		configureMySQLPool(pool, 10, idle)
		if pool.maxOpenConns != 10 || pool.maxIdleConns != idle {
			t.Fatalf("custom pool limits not applied: %+v", pool)
		}
	}
}
