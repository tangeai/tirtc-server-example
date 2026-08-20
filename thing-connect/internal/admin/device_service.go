package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidDeviceCommand = errors.New("admin: invalid device command")

var forceUnbindCleanupTargets = []string{"ai", "voip", "call"}

// ForceUnbindMutation is the complete atomic persistence request owned by the
// Admin device module. Its adapter must commit the device mutation, cleanup
// outbox entries and audit event in one transaction.
type ForceUnbindMutation struct {
	DeviceID       string
	ExpectedUserID int64
	CleanupTargets []string
	Audit          AuditEvent
}

// DeviceCommandStore is the persistence port used by administrative device
// commands. The MySQL adapter owns SQL and transaction details.
type DeviceCommandStore interface {
	ForceUnbind(ctx context.Context, mutation ForceUnbindMutation) error
}

type ForceUnbindInput struct {
	DeviceID       string
	ExpectedUserID int64
	Reason         string
	Actor          AccessIdentity
	RequestID      string
	Method         string
	Path           string
	ClientIP       string
	UserAgent      string
}

type ForceUnbindResult struct {
	DeviceID       string   `json:"device_id"`
	Unbound        bool     `json:"unbound"`
	CleanupTargets []string `json:"cleanup_targets"`
}

// DeviceService centralizes the force-unbind invariant and audit payload so
// the HTTP adapter only parses and renders the request.
type DeviceService struct {
	commands DeviceCommandStore
}

func NewDeviceService(commands DeviceCommandStore) *DeviceService {
	return &DeviceService{commands: commands}
}

func (s *DeviceService) ForceUnbind(ctx context.Context, input ForceUnbindInput) (ForceUnbindResult, error) {
	deviceID := strings.TrimSpace(input.DeviceID)
	reason := strings.TrimSpace(input.Reason)
	if s == nil || s.commands == nil || deviceID == "" || input.ExpectedUserID <= 0 || input.Actor.UserID <= 0 || reason == "" {
		return ForceUnbindResult{}, ErrInvalidDeviceCommand
	}
	targets := append([]string(nil), forceUnbindCleanupTargets...)
	mutation := ForceUnbindMutation{
		DeviceID:       deviceID,
		ExpectedUserID: input.ExpectedUserID,
		CleanupTargets: targets,
		Audit: AuditEvent{
			AdminUserID: input.Actor.UserID,
			RoleCodes:   strings.Join(input.Actor.Roles, ","),
			RequestID:   input.RequestID,
			Method:      input.Method,
			Path:        input.Path,
			HTTPStatus:  200,
			Action:      "device.unbind",
			Resource:    "device",
			ResourceID:  deviceID,
			Reason:      reason,
			Before:      fmt.Sprintf(`{"user_id":%d}`, input.ExpectedUserID),
			After:       `{"user_id":0}`,
			ClientIP:    input.ClientIP,
			UserAgent:   input.UserAgent,
			Success:     true,
		},
	}
	if err := s.commands.ForceUnbind(ctx, mutation); err != nil {
		return ForceUnbindResult{}, err
	}
	return ForceUnbindResult{DeviceID: deviceID, Unbound: true, CleanupTargets: targets}, nil
}
