package store

import (
	"context"
	"time"

	"thing-connect/internal/model"
)

// DeviceStore handles physical device identity persistence.
type DeviceStore interface {
	GetBindByDeviceID(ctx context.Context, deviceID string) (*model.DeviceBind, error)
	GetBindByFingerprint(ctx context.Context, mac string, userID int64) (*model.DeviceBind, error)
	GetDeviceKey(ctx context.Context, deviceID string) (*model.DevicePool, error)
	UpdateActiveTimeIfEmpty(ctx context.Context, deviceID string) error
}

// UserStore handles user account and quota persistence.
type UserStore interface {
	GetUserByEmail(ctx context.Context, email string) (*model.User, error)
	CreateUser(ctx context.Context, email, passwordHash string, bindQuota int) (int64, error)
	UpdatePassword(ctx context.Context, userID int64, passwordHash string) error
	GetQuota(ctx context.Context, userID int64) (int, error)
	GetDeviceList(ctx context.Context, userID int64) ([]model.UserDeviceRow, error)
	UpdateDeviceName(ctx context.Context, userID int64, deviceID, deviceName string) (bool, error)
}

// BindStore handles device-binding transactions.
// All Commit* methods execute a single DB transaction covering quota + device_bind + log.
type BindStore interface {
	// Read
	// GetBindByFingerprint prioritizes rows owned by userID, then rows this user
	// previously owned (last_user_id), then any unowned row — so a fingerprint
	// collision with another user's device never hides the caller's own device.
	GetBindByFingerprint(ctx context.Context, mac string, userID int64) (*model.DeviceBind, error)
	GetBindByDeviceID(ctx context.Context, deviceID string) (*model.DeviceBind, error)

	// Write — each runs in a single transaction
	CommitBindFromPool(ctx context.Context, fp model.Fingerprint, userID int64) (deviceID string, err error) // Case A
	CommitBindByDeviceID(ctx context.Context, deviceID string, fp model.Fingerprint, userID int64) error     // Case E
	CommitClaim(ctx context.Context, deviceID string, fp model.Fingerprint, userID int64) error              // Cases D+G
	TouchRebind(ctx context.Context, deviceID string, userID int64) error                                    // Case F
	CommitUnbind(ctx context.Context, deviceID string, userID int64) error                                   // Unbind

	GetDeviceKey(ctx context.Context, deviceID string) (*model.DevicePool, error)
	GetUserDevices(ctx context.Context, userID int64) ([]model.DeviceBind, error)
}

// UnbindCleanupStore atomically records cleanup events with a device unbind.
// The events are delivered by user-server's outbox to other services; this
// interface deliberately contains no knowledge of those services' tables.
type UnbindCleanupStore interface {
	CommitUnbindWithCleanup(ctx context.Context, deviceID string, userID int64, targets []string) error
}

// CacheStore handles Redis operations for verification codes and online status.
type CacheStore interface {
	// IncrReportAttempt counts Report calls for physHash within window, starting
	// the window (setting its TTL) on the first call. Returns the count after
	// incrementing.
	IncrReportAttempt(ctx context.Context, physHash string, window time.Duration) (int64, error)
	SetReportReplay(ctx context.Context, physHash string, val []byte, ttl time.Duration) error
	GetReportReplay(ctx context.Context, physHash string) ([]byte, error)
	SetVerifyRecord(ctx context.Context, physHash string, val []byte, ttl time.Duration) (bool, error)
	GetVerifyRecord(ctx context.Context, physHash string) ([]byte, error)
	// Device verification codes need a reverse lookup because the user scans
	// the six-digit code without knowing the physical fingerprint. Reservation
	// is collision-safe: reserved=false means another device already owns code.
	ReserveDeviceCode(ctx context.Context, code, physHash string, ttl time.Duration) (reserved bool, err error)
	GetDeviceCodeLookup(ctx context.Context, code string) (string, error)
	DelDeviceCodeLookup(ctx context.Context, code string) error

	// Email verification codes are keyed by email, not by the six-digit code.
	// This keeps registration completely separate from device binding and lets
	// different users receive the same random code without interfering.
	SetEmailCode(ctx context.Context, email, code string, ttl time.Duration) error
	GetEmailCode(ctx context.Context, email string) (string, error)
	// ConsumeEmailCode atomically verifies and deletes an email code. It returns
	// false when the code is missing, expired, or does not match.
	ConsumeEmailCode(ctx context.Context, email, code string) (bool, error)
	DelEmailCode(ctx context.Context, email string) error
	// IncrRateLimitAttempt increments an isolated rate-limit scope such as an
	// email address or client IP and expires the counter with the given window.
	IncrRateLimitAttempt(ctx context.Context, scope string, window time.Duration) (int64, error)
	IsDeviceOnline(ctx context.Context, deviceID string) (bool, error)

	// IsInCall reports whether deviceID currently holds a call-server room lock
	// (room:lock:{device_id}) — i.e. it's mid-call with another device. call-server
	// owns that key; this just reads it so other services (e.g. user-server's
	// live-preview token issuance) can surface "device busy in a call" to callers.
	IsInCall(ctx context.Context, deviceID string) (bool, error)
	DelVerifyAndCode(ctx context.Context, physHash, code string) error

	SetNonce(ctx context.Context, nonce string, ttl time.Duration) (bool, error)
	SetPendingBind(ctx context.Context, deviceID, tempClientID string, ttl time.Duration) error
	GetPendingBind(ctx context.Context, deviceID string) (string, error)
	DelPendingBind(ctx context.Context, deviceID string) error

	// AddIPFingerprint adds physHash to the IP's fingerprint set and returns
	// (added=true, count) if the fingerprint is new, or (added=false, count)
	// if it already existed. count is the total unique fingerprints from this IP.
	AddIPFingerprint(ctx context.Context, ip, physHash string, window time.Duration) (added bool, count int64, err error)

	// IncrGlobalPending increments the global pending verification code counter.
	IncrGlobalPending(ctx context.Context) (int64, error)

	// DecrGlobalPending decrements the global pending verification code counter.
	DecrGlobalPending(ctx context.Context) error

	// ReconcileGlobalPending counts active verify:* keys and resets the
	// global:pending_codes counter to the actual count. Used by the periodic
	// GC to correct drift caused by expired verification codes whose
	// DelVerifyAndCode was never called.
	ReconcileGlobalPending(ctx context.Context) error

	// SetReportFingerprint stores the baseline physHash for a deviceID that
	// has proven its identity via signature. Used by the signed-report path
	// for fingerprint consistency checks and by BindByDeviceID for liveness.
	SetReportFingerprint(ctx context.Context, deviceID, physHash string, ttl time.Duration) error

	// GetReportFingerprint returns the stored baseline physHash for deviceID,
	// or an empty string if no record exists.
	GetReportFingerprint(ctx context.Context, deviceID string) (string, error)

	// DelReportFingerprint removes the fingerprint baseline record for deviceID.
	DelReportFingerprint(ctx context.Context, deviceID string) error
}

// RoleBindingStore handles device→role binding persistence.
// Role details live in the tange cloud; local MySQL stores only the binding.
type RoleBindingStore interface {
	// GetDeviceRole returns the bound role_id for a device. Returns "" if not bound.
	GetDeviceRole(ctx context.Context, deviceID string) (string, error)
	// SetDeviceRole upserts the binding. A device can only bind to one role.
	SetDeviceRole(ctx context.Context, deviceID, roleID string, userID int64) error
	// DeleteDeviceRole removes the binding.
	DeleteDeviceRole(ctx context.Context, deviceID string) error
	// UserOwnsDevice reports whether deviceID is currently bound to userID.
	UserOwnsDevice(ctx context.Context, deviceID string, userID int64) (bool, error)
}

// UserRoleStore tracks which roles a user has created (local index).
// Role details are fetched from the tange cloud by role_id.
type UserRoleStore interface {
	// ListUserRoleIDs returns all role IDs created by a user.
	ListUserRoleIDs(ctx context.Context, userID int64) ([]string, error)
	// AddUserRole records a role creation.
	AddUserRole(ctx context.Context, userID int64, roleID string) error
	// RemoveUserRole removes the record (when role is deleted).
	RemoveUserRole(ctx context.Context, userID int64, roleID string) error
	// ExistsUserRole reports whether roleID belongs to userID.
	ExistsUserRole(ctx context.Context, userID int64, roleID string) (bool, error)
}

// UserResourceStore tracks cloud resources (MCP / device plugin / knowledge
// base / uploaded knowledge file) created by a user. Strictly private: every
// method is scoped by userID, so a user can only see and mutate their own
// resources. Resource config lives in the tange cloud; name is cached locally
// for list rendering.
type UserResourceStore interface {
	// Add records a resource creation. A duplicate (user_id,type,resource_id) is
	// silently ignored and does NOT overwrite the stored name.
	Add(ctx context.Context, userID int64, typ, resourceID, name string) error
	// Remove deletes the ownership record. No-op if the row is not found.
	Remove(ctx context.Context, userID int64, typ, resourceID string) error
	// List returns all of a user's resources of the given type, newest first.
	List(ctx context.Context, userID int64, typ string) ([]model.UserResource, error)
	// Count returns how many resources of a type a user owns (for quota checks).
	Count(ctx context.Context, userID int64, typ string) (int, error)
	// UpdateName refreshes the cached name after a cloud-side rename. Scoped by
	// userID so only the owner can rename. No-op if the row is not found.
	UpdateName(ctx context.Context, userID int64, typ, resourceID, name string) error
	// Exists reports whether userID owns a resource of typ with the given id.
	// Used for ownership checks on update/delete/get-detail.
	Exists(ctx context.Context, userID int64, typ, resourceID string) (bool, error)
}
