package adminmysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
	"thing-connect/internal/admin"
)

type ResourceReadStore struct{ db *sqlx.DB }

func NewResourceReadStore(db *sqlx.DB) *ResourceReadStore { return &ResourceReadStore{db: db} }

const userSelect = `SELECT u.id,u.email,COALESCE(uc.registration_ip,'') registration_ip,COALESCE(uc.last_login_ip,'') last_login_ip,uc.last_login_at,u.bind_quota,u.status,u.disabled_at,u.auth_revision,u.created_at,u.updated_at,(SELECT COUNT(*) FROM device_bind d WHERE d.user_id=u.id) device_count FROM users u LEFT JOIN user_connection uc ON uc.user_id=u.id`
const managedDeviceSelect = `SELECT COALESCE(dc.last_online_ip,'') last_online_ip,dc.last_online_at,d.id,d.device_id,d.mac,d.assign,d.device_name,d.user_id,COALESCE(u.email,'') user_email,d.last_user_id,d.active_time,d.bind_time,d.unbind_time,d.created_at,d.updated_at FROM device_bind d LEFT JOIN users u ON u.id=d.user_id LEFT JOIN device_connection dc ON dc.device_id=d.device_id`

const managedDeviceDetailSelect = `SELECT COALESCE(dc.last_online_ip,'') last_online_ip,dc.last_online_at,d.id,d.device_id,d.mac,d.assign,d.device_name,d.user_id,COALESCE(u.email,'') user_email,d.last_user_id,d.active_time,d.bind_time,d.unbind_time,d.created_at,d.updated_at,p.profile FROM device_bind d LEFT JOIN users u ON u.id=d.user_id LEFT JOIN device_profile p ON p.device_id=d.device_id LEFT JOIN device_connection dc ON dc.device_id=d.device_id`

func resourceOrder(q admin.ResourceQuery, devices bool) (string, error) {
	column := "u.created_at"
	tie := "u.id"
	if devices {
		tie = "d.id"
		switch q.SortBy {
		case "id":
			column = "d.id"
		case "active_time":
			column = "d.active_time"
		case "bind_time":
			column = "d.bind_time"
		default:
			return "", admin.ErrInvalidResourceQuery
		}
	} else if q.SortBy != "created_at" {
		return "", admin.ErrInvalidResourceQuery
	}
	direction := "ASC"
	if q.Desc {
		direction = "DESC"
	}
	result := " ORDER BY " + column + " " + direction
	if column != tie {
		result += "," + tie + " " + direction
	}
	return result, nil
}
func (s *ResourceReadStore) Users(ctx context.Context, q admin.ResourceQuery) ([]admin.ManagedUser, int, error) {
	where := " WHERE 1=1"
	args := []any{}
	if q.Keyword != "" {
		where += " AND (u.email LIKE ? OR CAST(u.id AS CHAR)=?)"
		args = append(args, "%"+q.Keyword+"%", q.Keyword)
	}
	if q.Status == "0" || q.Status == "1" {
		where += " AND u.status=?"
		args = append(args, q.Status)
	}
	if q.From != nil {
		where += " AND u.created_at>=?"
		args = append(args, *q.From)
	}
	if q.Until != nil {
		where += " AND u.created_at<?"
		args = append(args, *q.Until)
	}
	order, err := resourceOrder(q, false)
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.db.GetContext(ctx, &total, `SELECT COUNT(*) FROM users u`+where, args...); err != nil {
		return nil, 0, err
	}
	var rows []admin.ManagedUser
	args = append(args, q.PageSize, (q.Page-1)*q.PageSize)
	err = s.db.SelectContext(ctx, &rows, userSelect+where+order+` LIMIT ? OFFSET ?`, args...)
	return rows, total, err
}
func (s *ResourceReadStore) User(ctx context.Context, id int64) (admin.UserDetail, error) {
	var result admin.UserDetail
	if err := s.db.GetContext(ctx, &result.User, userSelect+` WHERE u.id=?`, id); err != nil {
		return result, resourceReadError(err)
	}
	if err := s.db.SelectContext(ctx, &result.Devices, managedDeviceSelect+` WHERE d.user_id=? ORDER BY d.id DESC`, id); err != nil {
		return result, err
	}
	if err := s.db.SelectContext(ctx, &result.BindLogs, `SELECT id,device_id,action,mac,assign,created_at FROM device_bind_log WHERE user_id=? ORDER BY id DESC LIMIT 100`, id); err != nil {
		return result, err
	}
	if err := s.db.GetContext(ctx, &result.AI, `SELECT (SELECT COUNT(*) FROM ai_user_role WHERE user_id=?) role_count,(SELECT COUNT(*) FROM ai_user_resource WHERE user_id=?) resource_count`, id, id); err != nil {
		return result, err
	}
	return result, nil
}
func (s *ResourceReadStore) Devices(ctx context.Context, q admin.ResourceQuery) ([]admin.ManagedDevice, int, error) {
	where := " WHERE 1=1"
	args := []any{}
	if q.Keyword != "" {
		where += " AND (d.device_id LIKE ? OR d.mac LIKE ? OR u.email LIKE ?)"
		args = append(args, "%"+q.Keyword+"%", "%"+q.Keyword+"%", "%"+q.Keyword+"%")
	}
	if q.Bound == "true" {
		where += " AND d.user_id>0"
	} else if q.Bound == "false" {
		where += " AND d.user_id=0"
	}
	order, err := resourceOrder(q, true)
	if err != nil {
		return nil, 0, err
	}
	var total int
	from := ` FROM device_bind d`
	if q.Keyword != "" {
		from += ` LEFT JOIN users u ON u.id=d.user_id`
	}
	if err := s.db.GetContext(ctx, &total, `SELECT COUNT(*)`+from+where, args...); err != nil {
		return nil, 0, err
	}
	var rows []admin.ManagedDevice
	args = append(args, q.PageSize, (q.Page-1)*q.PageSize)
	err = s.db.SelectContext(ctx, &rows, managedDeviceSelect+where+order+` LIMIT ? OFFSET ?`, args...)
	return rows, total, err
}
func (s *ResourceReadStore) Device(ctx context.Context, id string) (admin.ManagedDevice, error) {
	var row admin.ManagedDevice
	err := s.db.GetContext(ctx, &row, managedDeviceDetailSelect+` WHERE d.device_id=?`, strings.TrimSpace(id))
	return row, resourceReadError(err)
}
func resourceReadError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return admin.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("admin resource read: %w", err)
	}
	return nil
}
