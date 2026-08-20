package db

import (
	"testing"
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
	configureMySQLPool(pool)

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
