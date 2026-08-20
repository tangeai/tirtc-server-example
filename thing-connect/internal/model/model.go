package model

import "time"

type User struct {
	ID           int64      `db:"id"`
	Email        string     `db:"email"`
	Password     string     `db:"password"`
	BindQuota    int        `db:"bind_quota"`
	Status       int8       `db:"status"`
	DisabledAt   *time.Time `db:"disabled_at"`
	AuthRevision int64      `db:"auth_revision"`
	CreatedAt    time.Time  `db:"created_at"`
	UpdatedAt    time.Time  `db:"updated_at"`
}

// DevicePool — pre-generated device ID+KEY pool (renamed from GlobalDevicePool).
// status: 0=unallocated, 1=allocated
type DevicePool struct {
	ID        int64     `db:"id"`
	DeviceID  string    `db:"device_id"`
	DeviceKey string    `db:"device_key"`
	Status    int8      `db:"status"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// Fingerprint is the physical identity of a device.
// MAC is the only identity field. ChipUID/DeviceRand are legacy compatibility
// fields — accepted from old firmware report bodies but NOT used for identity,
// matching, or queries.
type Fingerprint struct {
	MAC        string
	ChipUID    string // legacy, ignored
	DeviceRand string // legacy, ignored
}

// IsEmpty returns true if the MAC (the only identity field) is blank.
func (f Fingerprint) IsEmpty() bool { return f.MAC == "" }

// DeviceBind is one row of device_bind (current state, PK=device_id).
type DeviceBind struct {
	ID         int64      `db:"id"`
	DeviceID   string     `db:"device_id"`
	MAC        string     `db:"mac"`
	ChipUID    string     `db:"chip_uid"`
	DeviceRand string     `db:"device_rand"`
	Assign     string     `db:"assign"`       // 'dynamic' = pool分配, 'preburn' = 出厂预烧
	DeviceName string     `db:"device_name"`  // current owner's display name; cleared on unbind
	UserID     int64      `db:"user_id"`      // 0 = unowned
	LastUserID int64      `db:"last_user_id"` // last owner, kept after unbind
	ActiveTime *time.Time `db:"active_time"`
	BindTime   *time.Time `db:"bind_time"`
	UnbindTime *time.Time `db:"unbind_time"`
	CreatedAt  time.Time  `db:"created_at"`
	UpdatedAt  time.Time  `db:"updated_at"`
	MacUserKey *string    `db:"mac_user_key"` // generated (read-only): nil for MAC-less/unbound rows, else "mac:user_id" — UNIQUE guard for per-user MAC uniqueness
}

// FP extracts the Fingerprint from a DeviceBind row.
func (b *DeviceBind) FP() Fingerprint {
	return Fingerprint{MAC: b.MAC, ChipUID: b.ChipUID, DeviceRand: b.DeviceRand}
}

// DeviceBindLog is one row of device_bind_log (append-only history).
type DeviceBindLog struct {
	ID         int64     `db:"id"`
	DeviceID   string    `db:"device_id"`
	UserID     int64     `db:"user_id"`
	Action     int8      `db:"action"` // 1=bind 2=unbind
	MAC        string    `db:"mac"`
	ChipUID    string    `db:"chip_uid"`
	DeviceRand string    `db:"device_rand"`
	Assign     string    `db:"assign"`
	CreatedAt  time.Time `db:"created_at"`
}

// UserDeviceRow is the query result for device list.
// Used by UserStore.GetDeviceList.
type UserDeviceRow struct {
	DeviceID    string  `db:"device_id"`
	DeviceName  string  `db:"device_name"`
	Status      int8    `db:"status"` // 1 always (device_bind only contains bound rows)
	MAC         string  `db:"mac"`
	BindTime    *string `db:"bind_time"`
	VoipProfile *string `db:"voip_profile"`
}

// DeviceRole is one row of ai_device_role — the device→role binding stored locally.
// Role details (name, prompt, etc.) live in the tange cloud and are fetched on demand.
type DeviceRole struct {
	ID        int64     `db:"id"`
	DeviceID  string    `db:"device_id"`
	RoleID    string    `db:"role_id"`
	UserID    int64     `db:"user_id"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// ResourceType* are the ai_user_resource.type values for cloud resources a user
// creates: MCP servers, device plugins, knowledge bases, and uploaded knowledge
// files.
const (
	ResourceTypeMCP          = "mcp"
	ResourceTypeDevicePlugin = "device_plugin"
	ResourceTypeKB           = "kb"
	ResourceTypeKBFile       = "kb_file"
)

// UserResource is one row of ai_user_resource — a local ownership record linking
// a user to a cloud resource they created. type is one of ResourceType*.
// Resource config lives in the tange cloud; name is cached locally for fast list
// rendering and refreshed when the cloud-side name changes.
type UserResource struct {
	ID         int64     `db:"id"`
	UserID     int64     `db:"user_id"`
	Type       string    `db:"type"`
	ResourceID string    `db:"resource_id"`
	Name       string    `db:"name"`
	CreatedAt  time.Time `db:"created_at"`
}

// ResourceRef is a lightweight {id, name} pair. It is the shape returned by
// user-resource list endpoints (served from local ai_user_resource, no cloud
// call) and the shape of configured default resources. Full details are fetched
// on demand from the cloud by id.
type ResourceRef struct {
	ID   string `json:"id" yaml:"id"`
	Name string `json:"name" yaml:"name"`
}
