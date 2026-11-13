package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"thing-connect/internal/deviceprofile"
	"time"
)

var ErrInvalidResourceQuery = errors.New("查询参数无效")

type ResourceFilter struct {
	Page, PageSize                                                    int
	Keyword, Status, Bound, CreatedFrom, CreatedTo, SortBy, SortOrder string
}
type ResourceQuery struct {
	Page, PageSize                 int
	Keyword, Status, Bound, SortBy string
	Desc                           bool
	From, Until                    *time.Time
}
type ResourcePage[T any] struct {
	Items    []T `json:"items"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
	Total    int `json:"total"`
}
type ResourceBindLog struct {
	ID        int64     `db:"id" json:"id"`
	DeviceID  string    `db:"device_id" json:"device_id"`
	Action    int8      `db:"action" json:"action"`
	MAC       string    `db:"mac" json:"mac"`
	Assign    string    `db:"assign" json:"assign"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}
type UserAISummary struct {
	RoleCount     int `db:"role_count" json:"role_count"`
	ResourceCount int `db:"resource_count" json:"resource_count"`
}
type UserDetail struct {
	User     ManagedUser       `json:"user"`
	Devices  []ManagedDevice   `json:"devices"`
	BindLogs []ResourceBindLog `json:"bind_logs"`
	AI       UserAISummary     `json:"ai"`
}

// ResourceReadStore owns a complete read, including pagination counts and
// related detail rows. SQL errors must not turn into partial success responses.
type ResourceReadStore interface {
	Users(context.Context, ResourceQuery) ([]ManagedUser, int, error)
	User(context.Context, int64) (UserDetail, error)
	Devices(context.Context, ResourceQuery) ([]ManagedDevice, int, error)
	Device(context.Context, string) (ManagedDevice, error)
}
type DevicePresence struct {
	Online     bool
	LastSeenAt *time.Time
}
type ResourcePresenceReader interface {
	Read(context.Context, []string) (map[string]DevicePresence, error)
}
type ResourceQueries interface {
	Users(context.Context, ResourceFilter) (ResourcePage[ManagedUser], error)
	User(context.Context, int64) (UserDetail, error)
	Devices(context.Context, ResourceFilter) (ResourcePage[ManagedDevice], error)
	Device(context.Context, string) (ManagedDevice, error)
}
type ResourceService struct {
	store    ResourceReadStore
	presence ResourcePresenceReader
}

func NewResourceService(store ResourceReadStore, presence ResourcePresenceReader) *ResourceService {
	return &ResourceService{store: store, presence: presence}
}
func normalizeResourceFilter(f ResourceFilter, devices bool) (ResourceQuery, error) {
	q := ResourceQuery{Page: f.Page, PageSize: f.PageSize, Keyword: strings.TrimSpace(f.Keyword), Status: strings.TrimSpace(f.Status), Bound: strings.TrimSpace(f.Bound), SortBy: strings.TrimSpace(f.SortBy), Desc: true}
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 {
		q.PageSize = 20
	}
	if q.PageSize > 100 {
		q.PageSize = 100
	}
	order := strings.ToLower(strings.TrimSpace(f.SortOrder))
	if order != "" && order != "desc" && order != "asc" {
		return q, fmt.Errorf("%w：排序方向必须是 asc 或 desc", ErrInvalidResourceQuery)
	}
	q.Desc = order != "asc"
	if devices {
		if q.SortBy == "" {
			q.SortBy = "id"
		}
		if q.SortBy != "id" && q.SortBy != "active_time" && q.SortBy != "bind_time" {
			return q, fmt.Errorf("%w：不支持的设备排序字段", ErrInvalidResourceQuery)
		}
	} else {
		if q.SortBy == "" {
			q.SortBy = "created_at"
		}
		if q.SortBy != "created_at" {
			return q, fmt.Errorf("%w：不支持的用户排序字段", ErrInvalidResourceQuery)
		}
		for i, v := range []string{f.CreatedFrom, f.CreatedTo} {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			parsed, err := time.Parse("2006-01-02", v)
			if err != nil {
				return q, fmt.Errorf("%w：日期必须使用 YYYY-MM-DD", ErrInvalidResourceQuery)
			}
			if i == 0 {
				q.From = &parsed
			} else {
				until := parsed.AddDate(0, 0, 1)
				q.Until = &until
			}
		}
	}
	return q, nil
}
func (s *ResourceService) Users(ctx context.Context, f ResourceFilter) (ResourcePage[ManagedUser], error) {
	q, err := normalizeResourceFilter(f, false)
	if err != nil {
		return ResourcePage[ManagedUser]{}, err
	}
	rows, total, err := s.store.Users(ctx, q)
	return ResourcePage[ManagedUser]{Items: rows, Page: q.Page, PageSize: q.PageSize, Total: total}, err
}
func (s *ResourceService) User(ctx context.Context, id int64) (UserDetail, error) {
	if id <= 0 {
		return UserDetail{}, ErrInvalidResourceQuery
	}
	result, err := s.store.User(ctx, id)
	if err != nil {
		return UserDetail{}, err
	}
	s.enrich(ctx, result.Devices)
	return result, nil
}
func (s *ResourceService) Devices(ctx context.Context, f ResourceFilter) (ResourcePage[ManagedDevice], error) {
	q, err := normalizeResourceFilter(f, true)
	if err != nil {
		return ResourcePage[ManagedDevice]{}, err
	}
	rows, total, err := s.store.Devices(ctx, q)
	if err != nil {
		return ResourcePage[ManagedDevice]{}, err
	}
	s.enrich(ctx, rows)
	return ResourcePage[ManagedDevice]{Items: rows, Page: q.Page, PageSize: q.PageSize, Total: total}, nil
}
func (s *ResourceService) Device(ctx context.Context, id string) (ManagedDevice, error) {
	if strings.TrimSpace(id) == "" {
		return ManagedDevice{}, ErrInvalidResourceQuery
	}
	row, err := s.store.Device(ctx, id)
	if err != nil {
		return ManagedDevice{}, err
	}
	rows := []ManagedDevice{row}
	s.enrich(ctx, rows)
	return rows[0], nil
}
func (s *ResourceService) enrich(ctx context.Context, rows []ManagedDevice) {
	ids := make([]string, len(rows))
	for i := range rows {
		ids[i] = rows[i].DeviceID
		rows[i].projectProfileMetadata()
	}
	if len(rows) == 0 || s.presence == nil {
		return
	}
	values, err := s.presence.Read(ctx, ids)
	if err != nil {
		return
	}
	for i := range rows {
		v := values[rows[i].DeviceID]
		rows[i].PresenceKnown = true
		rows[i].Online = v.Online
		rows[i].LastSeenAt = v.LastSeenAt
	}
}

type ManagedUser struct {
	RegistrationIP string     `db:"registration_ip" json:"registration_ip"`
	LastLoginIP    string     `db:"last_login_ip" json:"last_login_ip"`
	LastLoginAt    *time.Time `db:"last_login_at" json:"last_login_at,omitempty"`
	ID             int64      `db:"id" json:"id"`
	Email          string     `db:"email" json:"email"`
	BindQuota      int        `db:"bind_quota" json:"bind_quota"`
	Status         int8       `db:"status" json:"status"`
	DisabledAt     *time.Time `db:"disabled_at" json:"disabled_at,omitempty"`
	AuthRevision   int64      `db:"auth_revision" json:"auth_revision"`
	DeviceCount    int        `db:"device_count" json:"device_count"`
	CreatedAt      time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at" json:"updated_at"`
}

type ManagedDevice struct {
	LastOnlineIP    string                                `db:"last_online_ip" json:"last_online_ip"`
	LastOnlineAt    *time.Time                            `db:"last_online_at" json:"last_online_at,omitempty"`
	ID              int64                                 `db:"id" json:"id"`
	DeviceID        string                                `db:"device_id" json:"device_id"`
	MAC             string                                `db:"mac" json:"mac"`
	Assign          string                                `db:"assign" json:"assign"`
	DeviceName      string                                `db:"device_name" json:"device_name"`
	UserID          int64                                 `db:"user_id" json:"user_id"`
	UserEmail       string                                `db:"user_email" json:"user_email"`
	LastUserID      int64                                 `db:"last_user_id" json:"last_user_id"`
	ActiveTime      *time.Time                            `db:"active_time" json:"active_time,omitempty"`
	BindTime        *time.Time                            `db:"bind_time" json:"bind_time,omitempty"`
	UnbindTime      *time.Time                            `db:"unbind_time" json:"unbind_time,omitempty"`
	CreatedAt       time.Time                             `db:"created_at" json:"created_at"`
	UpdatedAt       time.Time                             `db:"updated_at" json:"updated_at"`
	Online          bool                                  `db:"-" json:"online"`
	PresenceKnown   bool                                  `db:"-" json:"presence_known"`
	LastSeenAt      *time.Time                            `db:"-" json:"last_seen_at,omitempty"`
	Profile         *string                               `db:"profile" json:"-"`
	Profiles        map[string]map[string]json.RawMessage `db:"-" json:"profiles,omitempty"`
	Hardware        *deviceprofile.Hardware               `db:"-" json:"hardware,omitempty"`
	FirmwareVersion *string                               `db:"-" json:"firmware_version,omitempty"`
}

func (d *ManagedDevice) projectProfileMetadata() {
	metadata := deviceprofile.PublicMetadata(d.Profile)
	d.Hardware = metadata.Hardware
	d.FirmwareVersion = metadata.FirmwareVersion
	d.Profiles = deviceprofile.Public(d.Profile)
}
