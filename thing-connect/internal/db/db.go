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

func configureMySQLPool(db mysqlPool) {
	db.SetMaxOpenConns(50)
	db.SetMaxIdleConns(10)
	// Retire idle sockets before the production MySQL wait_timeout baseline
	// (five minutes), so the driver does not receive server-closed connections.
	db.SetConnMaxIdleTime(4 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)
}

func Open(cfg config.DatabaseCfg) (*sqlx.DB, error) {
	if cfg.DSN == "" {
		return nil, fmt.Errorf("db: DSN not configured")
	}
	db, err := sqlx.Open("mysql", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("db: open: %w", err)
	}
	configureMySQLPool(db)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return db, nil
}
