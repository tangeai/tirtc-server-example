package tirtcapi

import (
	"context"
	"errors"
	"sync/atomic"
	"thing-connect/internal/ai"
)

type LifecycleAPI interface {
	DeleteRole(context.Context, string) error
	BatchCreateDeviceRoles(context.Context, BatchDeviceRolesRequest) error
	BatchDeleteDeviceRoles(context.Context, BatchDeviceRolesRequest) error
	BatchQueryDeviceRoles(context.Context, BatchDeviceRolesQueryRequest) ([]DeviceRoleBinding, error)
}
type roleCloud struct{ api LifecycleAPI }

func NewRoleCloud(api LifecycleAPI) ai.RoleCloud { return &roleCloud{api: api} }
func (c *roleCloud) Query(ctx context.Context, ids []string) ([]ai.CloudRoleBinding, error) {
	rows, err := c.api.BatchQueryDeviceRoles(ctx, BatchDeviceRolesQueryRequest{DeviceIDs: ids})
	if err != nil {
		return nil, err
	}
	out := make([]ai.CloudRoleBinding, 0, len(rows))
	for _, row := range rows {
		out = append(out, ai.CloudRoleBinding{DeviceID: row.DeviceID, RoleID: row.RoleID})
	}
	return out, nil
}
func (c *roleCloud) Bind(ctx context.Context, ids []string, role string) error {
	return c.api.BatchCreateDeviceRoles(ctx, BatchDeviceRolesRequest{DeviceIDs: ids, RoleID: role})
}
func (c *roleCloud) Unbind(ctx context.Context, ids []string, role string) error {
	return c.api.BatchDeleteDeviceRoles(ctx, BatchDeviceRolesRequest{DeviceIDs: ids, RoleID: role})
}
func (c *roleCloud) Delete(ctx context.Context, role string) error {
	err := c.api.DeleteRole(ctx, role)
	if errors.Is(err, ErrRoleNotFound) {
		return nil
	}
	return err
}

// RoleCloudProvider captures one immutable client per business operation while
// allowing the composition root to publish a new dynamic configuration.
type RoleCloudProvider struct{ current atomic.Pointer[roleCloud] }

func NewRoleCloudProvider(api LifecycleAPI) *RoleCloudProvider {
	p := &RoleCloudProvider{}
	p.SetAPI(api)
	return p
}
func (p *RoleCloudProvider) SetAPI(api LifecycleAPI) { p.current.Store(&roleCloud{api: api}) }
func (p *RoleCloudProvider) Snapshot() ai.RoleCloud  { return p.current.Load() }
