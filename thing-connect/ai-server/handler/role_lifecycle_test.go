package handler

import (
	"context"
	"thing-connect/internal/ai"
)

// Adapter for the existing handler fixtures. Lifecycle behavior is exercised
// through ai.Roles; SQL atomicity has separate MySQL integration coverage.
type fixtureRoleLifecycle struct {
	h        *AgentHandler
	deleting map[string]bool
}

func (f *fixtureRoleLifecycle) WithAccount(ctx context.Context, _ int64, fn func(context.Context) error) error {
	return fn(ctx)
}
func (f *fixtureRoleLifecycle) CheckRole(ctx context.Context, u int64, r string, allow bool) error {
	ok, e := f.h.userRoleStore.ExistsUserRole(ctx, u, r)
	if e != nil {
		return e
	}
	if !ok {
		return ai.ErrRoleForbidden
	}
	if f.deleting[r] && !allow {
		return ai.ErrRoleDeleting
	}
	return nil
}
func (f *fixtureRoleLifecycle) Devices(context.Context, int64) ([]string, error) { return nil, nil }
func (f *fixtureRoleLifecycle) Usage(_ context.Context, r string) ([]ai.RoleDevice, error) {
	out := []ai.RoleDevice{}
	if m, ok := f.h.roleStore.(*mockRoleBindingStore); ok {
		for d, role := range m.bindings {
			if role == r {
				out = append(out, ai.RoleDevice{DeviceID: d})
			}
		}
	}
	return out, nil
}
func (f *fixtureRoleLifecycle) MarkDeleting(_ context.Context, _ int64, r string) error {
	f.deleting[r] = true
	return nil
}
func (f *fixtureRoleLifecycle) RemoveRole(ctx context.Context, u int64, r string) error {
	return f.h.userRoleStore.RemoveUserRole(ctx, u, r)
}
func (f *fixtureRoleLifecycle) SetLocal(ctx context.Context, u int64, d, r string) error {
	if r == "" {
		return f.h.roleStore.DeleteDeviceRole(ctx, d)
	}
	if m, ok := f.h.roleStore.(*mockRoleBindingStore); ok && m.bindings == nil {
		m.bindings = map[string]string{}
	}
	return f.h.roleStore.SetDeviceRole(ctx, d, r, u)
}
func (f *fixtureRoleLifecycle) WithDevices(ctx context.Context, _ []string, fn func(context.Context) error) error {
	return fn(ctx)
}
func (f *fixtureRoleLifecycle) PrepareCloud(_ context.Context, u int64, ids []string, r string, remove bool) ([]ai.CloudTarget, error) {
	out := []ai.CloudTarget{}
	for _, id := range ids {
		out = append(out, ai.CloudTarget{DeviceID: id, UserID: u, RoleID: r, Remove: remove})
	}
	return out, nil
}
func (f *fixtureRoleLifecycle) CloudTarget(ctx context.Context, id string, cleanup bool) (ai.CloudTarget, error) {
	if cleanup {
		if err := f.h.roleStore.DeleteDeviceRole(ctx, id); err != nil {
			return ai.CloudTarget{}, err
		}
	}
	return ai.CloudTarget{DeviceID: id, Remove: true}, nil
}
func (f *fixtureRoleLifecycle) CompleteCloud(context.Context, ai.CloudTarget) (bool, error) {
	return true, nil
}
func (f *fixtureRoleLifecycle) DueCloud(context.Context) ([]string, error)     { return nil, nil }
func (f *fixtureRoleLifecycle) DelayCloud(context.Context, string, bool) error { return nil }
