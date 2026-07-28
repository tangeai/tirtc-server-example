package db

import (
	"fmt"

	"github.com/jmoiron/sqlx"

	// MySQL driver
	_ "github.com/go-sql-driver/mysql"
	"thing-connect/internal/config"
)

func Open(cfg config.DatabaseCfg) (*sqlx.DB, error) {
	if cfg.DSN == "" {
		return nil, fmt.Errorf("db: DSN not configured")
	}
	db, err := sqlx.Open("mysql", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("db: open: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	db.SetMaxOpenConns(50)
	db.SetMaxIdleConns(10)
	return db, nil
}
