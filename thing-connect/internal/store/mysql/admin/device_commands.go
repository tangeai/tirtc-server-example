// Package adminmysql contains MySQL adapters for ports owned by the Admin
// business modules.
package adminmysql

import (
	"context"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"

	adminapp "thing-connect/internal/admin"
	"thing-connect/internal/store"
	mysqlstore "thing-connect/internal/store/mysql"
)

type DeviceCommandStore struct {
	db *sqlx.DB
}

func NewDeviceCommandStore(db *sqlx.DB) *DeviceCommandStore {
	return &DeviceCommandStore{db: db}
}

func (s *DeviceCommandStore) ForceUnbind(ctx context.Context, mutation adminapp.ForceUnbindMutation) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("admin device force-unbind begin: %w", err)
	}
	defer tx.Rollback()
	if err := mysqlstore.ApplyUnbindTx(ctx, tx, mutation.DeviceID, mutation.ExpectedUserID, mutation.CleanupTargets); err != nil {
		switch {
		case errors.Is(err, store.ErrDeviceNotFound):
			return adminapp.ErrNotFound
		case errors.Is(err, store.ErrSlotConflict):
			return adminapp.ErrConflict
		default:
			return fmt.Errorf("admin device force-unbind mutation: %w", err)
		}
	}
	if err := insertAuditTx(ctx, tx, mutation.Audit); err != nil {
		return fmt.Errorf("admin device force-unbind audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("admin device force-unbind commit: %w", err)
	}
	return nil
}

func insertAuditTx(ctx context.Context, tx *sqlx.Tx, event adminapp.AuditEvent) error {
	success := 0
	if event.Success {
		success = 1
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO admin_audit_log
		(admin_user_id,role_codes,request_id,method,path,http_status,latency_ms,action,resource_type,resource_id,reason,before_value,after_value,client_ip,user_agent,success,error_message)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		event.AdminUserID, event.RoleCodes, event.RequestID, event.Method, event.Path,
		event.HTTPStatus, event.LatencyMS, event.Action, event.Resource, event.ResourceID,
		event.Reason, nullableText(event.Before), nullableText(event.After), event.ClientIP,
		event.UserAgent, success, event.Error)
	return err
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
