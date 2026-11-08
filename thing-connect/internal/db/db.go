package db

import (
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	// MySQL driver
	_ "github.com/go-sql-driver/mysql"
	"thing-connect/internal/config"
)

type mysqlPool interface {
	SetMaxOpenConns(int)
	SetMaxIdleConns(int)
	SetConnMaxIdleTime(time.Duration)
	SetConnMaxLifetime(time.Duration)
}

func configureMySQLPool(db mysqlPool, maxOpen, maxIdle int) {
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	// Retire idle sockets before the production MySQL wait_timeout baseline
	// (five minutes), so the driver does not receive server-closed connections.
	db.SetConnMaxIdleTime(4 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)
}

func Open(cfg config.DatabaseCfg) (*sqlx.DB, error) {
	if cfg.DSN == "" {
		return nil, fmt.Errorf("db: DSN not configured")
	}
	maxOpen, maxIdle, err := cfg.ConnectionLimits()
	if err != nil {
		return nil, err
	}
	db, err := sqlx.Open("mysql", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("db: open: %w", err)
	}
	configureMySQLPool(db, maxOpen, maxIdle)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return db, nil
}
