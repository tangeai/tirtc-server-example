// Package ai owns AI role lifecycle and plugin naming shared by AI services.
package ai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"
)

var ErrCleanupSuperseded = errors.New("设备已进入新的绑定周期")

// ErrDeviceUnknown reports a device that was never provisioned. No cloud
// target exists for it, so nothing is persisted or retried.
var ErrDeviceUnknown = errors.New("设备不存在")

var ErrRoleForbidden = errors.New("角色或设备不属于当前用户")
var ErrRoleBusy = errors.New("角色正在使用或有未完成操作，请先解除设备关联")
var ErrRoleDeleting = errors.New("角色删除尚未完成，请重试删除后再选择其他角色")
var ErrRoleUpstream = errors.New("AI 云服务暂不可用，请稍后重试")

type RoleDevice struct {
	DeviceID string `json:"device_id" db:"device_id"`
	Name     string `json:"device_name" db:"device_name"`
}
type RoleInUse struct{ Devices []RoleDevice }

func (e *RoleInUse) Error() string {
	return "该角色正在被设备使用，请先切换这些设备的角色或恢复默认角色"
}
func (e *RoleInUse) Unwrap() error { return ErrRoleBusy }

type CloudRoleBinding struct {
	DeviceID string
	RoleID   string
}
type RoleCloud interface {
	Query(context.Context, []string) ([]CloudRoleBinding, error)
	Bind(context.Context, []string, string) error
	Unbind(context.Context, []string, string) error
	Delete(context.Context, string) error
}

// CloudTarget is the cloud role a device should have at one binding revision.
type CloudTarget struct {
	DeviceID string `db:"device_id"`
	UserID   int64  `db:"user_id"`
	Revision int64  `db:"binding_revision"`
	RoleID   string `db:"role_id"`
	Remove   bool   `db:"remove_role"`
}
type RoleOperations interface {
	Delete(context.Context, int64, string) error
	SetLocal(context.Context, int64, string, string) error
	SetCloud(context.Context, int64, []string, string, bool) error
	CleanupDevice(context.Context, string) error
}

// RoleLifecycleStore serializes all user-facing mutations across processes.
// Remote work runs outside SQL transactions. Durable deletion markers and
// conservative cloud reservations survive timeouts and process interruption.
type RoleLifecycleStore interface {
	WithAccount(context.Context, int64, func(context.Context) error) error
	CheckRole(context.Context, int64, string, bool) error
	Devices(context.Context, int64) ([]string, error)
	Usage(context.Context, string) ([]RoleDevice, error)
	MarkDeleting(context.Context, int64, string) error
	RemoveRole(context.Context, int64, string) error
	SetLocal(context.Context, int64, string, string) error
	WithDevices(context.Context, []string, func(context.Context) error) error
	PrepareCloud(context.Context, int64, []string, string, bool) ([]CloudTarget, error)
	CloudTarget(context.Context, string, bool) (CloudTarget, error)
	// CompleteCloud records the target as applied only while both device ownership
	// and the persisted target still match the snapshot. A settled target was
	// confirmed without a remote mutation and is no longer rechecked; otherwise
	// one recheck catches late remote requests.
	CompleteCloud(ctx context.Context, target CloudTarget, settled bool) (bool, error)
	// ClaimCloud leases up to limit due targets that no other worker holds.
	ClaimCloud(ctx context.Context, limit int, lease time.Duration) ([]string, error)
	// DelayCloud backs off only the failed snapshot, never a replacement target.
	DelayCloud(context.Context, CloudTarget) error
}

const (
	reconcileBatchSize    = 20
	reconcileTimeout      = 20 * time.Second
	reconcileLease        = reconcileBatchSize*reconcileTimeout + 30*time.Second
	reconcileLeaseMargin  = 10 * time.Second
	reconcileClaimTimeout = 10 * time.Second
)

type Roles struct {
	store RoleLifecycleStore
	cloud func() RoleCloud
	now   func() time.Time
}

func NewRoles(store RoleLifecycleStore, cloud func() RoleCloud) *Roles {
	return &Roles{store: store, cloud: cloud, now: time.Now}
}
func (s *Roles) Delete(ctx context.Context, user int64, role string) error {
	cloud := s.cloud()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return s.store.WithAccount(ctx, user, func(ctx context.Context) error {
		if err := s.store.CheckRole(ctx, user, role, true); err != nil {
			return err
		}
		used, err := s.store.Usage(ctx, role)
		if err != nil {
			return err
		}
		if len(used) > 0 {
			return &RoleInUse{Devices: used}
		}
		devices, err := s.store.Devices(ctx, user)
		if err != nil {
			return err
		}
		// Include legacy cloud-only associations, not only the local token mapping.
		for i := 0; i < len(devices); i += 100 {
			end := i + 100
			if end > len(devices) {
				end = len(devices)
			}
			bindings, e := cloud.Query(ctx, devices[i:end])
			if e != nil {
				return fmt.Errorf("%w: %w", ErrRoleUpstream, e)
			}
			for _, b := range bindings {
				if b.RoleID == role {
					used = append(used, RoleDevice{DeviceID: b.DeviceID})
				}
			}
		}
		if len(used) > 0 {
			return &RoleInUse{Devices: used}
		}
		if err := s.store.MarkDeleting(ctx, user, role); err != nil {
			return err
		}
		if err := cloud.Delete(ctx, role); err != nil {
			return fmt.Errorf("%w: %w", ErrRoleUpstream, err)
		}
		return s.store.RemoveRole(ctx, user, role)
	})
}
func (s *Roles) SetLocal(ctx context.Context, user int64, device, role string) error {
	return s.store.WithAccount(ctx, user, func(ctx context.Context) error {
		if role != "" {
			if err := s.store.CheckRole(ctx, user, role, false); err != nil {
				return err
			}
		}
		return s.store.WithDevices(ctx, []string{device}, func(ctx context.Context) error { return s.store.SetLocal(ctx, user, device, role) })
	})
}
func (s *Roles) SetCloud(ctx context.Context, user int64, devices []string, role string, remove bool) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	devices = append([]string(nil), devices...)
	sort.Strings(devices)
	cloud := s.cloud()
	return s.store.WithAccount(ctx, user, func(ctx context.Context) error {
		return s.store.WithDevices(ctx, devices, func(ctx context.Context) error {
			if !remove {
				if err := s.store.CheckRole(ctx, user, role, false); err != nil {
					return err
				}
			}
			targets, err := s.store.PrepareCloud(ctx, user, devices, role, remove)
			if err != nil {
				return err
			}
			if remove {
				err = cloud.Unbind(ctx, devices, role)
			} else {
				err = cloud.Bind(ctx, devices, role)
			}
			if err != nil {
				return fmt.Errorf("%w: %w", ErrRoleUpstream, err)
			}
			stale := false
			for _, target := range targets {
				current, e := s.store.CompleteCloud(ctx, target, false)
				if e != nil {
					return e
				}
				if !current {
					stale = true
					if e = s.reconcileLocked(ctx, target.DeviceID, cloud, false); e != nil {
						return e
					}
				}
			}
			if stale {
				return ErrRoleForbidden
			}
			return nil
		})
	})
}

// CleanupDevice shares the same device lock as user mutations and reconciliation.
// The durable target remains after success: a remote request can finish even
// after its HTTP caller timed out, so absence of a single response is not proof
// that the cloud state will never change again.
func (s *Roles) CleanupDevice(ctx context.Context, device string) error {
	return s.store.WithDevices(ctx, []string{device}, func(ctx context.Context) error {
		return s.reconcileLocked(ctx, device, s.cloud(), true)
	})
}
func (s *Roles) reconcileLocked(ctx context.Context, device string, cloud RoleCloud, cleanup bool) error {
	target, err := s.store.CloudTarget(ctx, device, cleanup)
	if errors.Is(err, ErrCleanupSuperseded) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.reconcileTarget(ctx, target, cloud)
}

func (s *Roles) reconcileTarget(ctx context.Context, target CloudTarget, cloud RoleCloud) error {
	device := target.DeviceID
	rows, err := cloud.Query(ctx, []string{device})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRoleUpstream, err)
	}
	found := false
	for _, row := range rows {
		if row.DeviceID == device && (target.RoleID == "" || row.RoleID == target.RoleID) {
			found = true
		}
	}
	mutated := target.Remove == found
	if target.Remove && found {
		err = cloud.Unbind(ctx, []string{device}, target.RoleID)
	}
	if !target.Remove && !found {
		err = cloud.Bind(ctx, []string{device}, target.RoleID)
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRoleUpstream, err)
	}
	current, err := s.store.CompleteCloud(ctx, target, !mutated)
	if err != nil {
		return err
	}
	if !current {
		return ErrRoleForbidden
	} // next pass resolves the newer generation
	return nil
}
func (s *Roles) Reconcile(ctx context.Context, device string) error {
	return s.store.WithDevices(ctx, []string{device}, func(ctx context.Context) error {
		target, err := s.store.CloudTarget(ctx, device, false)
		if errors.Is(err, ErrCleanupSuperseded) {
			return nil
		}
		if err != nil {
			return err // No snapshot was read; leave the lease for recovery.
		}
		if err = s.reconcileTarget(ctx, target, s.cloud()); err != nil {
			return errors.Join(err, s.store.DelayCloud(ctx, target))
		}
		return nil
	})
}

// reconcileDue works through one claimed batch. Device locks keep concurrent
// workers correct; the lease only avoids duplicate cloud calls, so a pass that
// could outlive it stops and leaves the rest to the next claimer.
func (s *Roles) reconcileDue(ctx context.Context) {
	deadline := s.now().Add(reconcileLease - reconcileLeaseMargin - reconcileTimeout)
	claimCtx, cancel := context.WithTimeout(ctx, reconcileClaimTimeout)
	ids, err := s.store.ClaimCloud(claimCtx, reconcileBatchSize, reconcileLease)
	cancel()
	if err != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "role reconciliation queue unavailable", "err", err)
	}
	for _, id := range ids {
		if ctx.Err() != nil || !s.now().Before(deadline) {
			return
		}
		op, cancel := context.WithTimeout(ctx, reconcileTimeout)
		err = s.Reconcile(op, id)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "role reconciliation pending", "err", err)
		}
	}
}

func (s *Roles) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		s.reconcileDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
