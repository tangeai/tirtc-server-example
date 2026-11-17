package ai

import (
	"context"
	"testing"
	"time"
)

type reconcileStore struct {
	RoleLifecycleStore
	targets   map[string]CloudTarget
	claimed   []string
	settled   map[string]bool
	unbounded int
	onWork    func()
}

func (s *reconcileStore) WithDevices(ctx context.Context, _ []string, fn func(context.Context) error) error {
	return fn(ctx)
}
func (s *reconcileStore) CloudTarget(_ context.Context, device string, _ bool) (CloudTarget, error) {
	if s.onWork != nil {
		s.onWork()
	}
	return s.targets[device], nil
}
func (s *reconcileStore) CompleteCloud(_ context.Context, target CloudTarget, settled bool) (bool, error) {
	s.settled[target.DeviceID] = settled
	return true, nil
}
func (s *reconcileStore) DelayCloud(context.Context, string, bool) error { return nil }
func (s *reconcileStore) ClaimCloud(ctx context.Context, _ int, _ time.Duration) ([]string, error) {
	if d, ok := ctx.Deadline(); !ok || time.Until(d) > reconcileClaimTimeout {
		s.unbounded++
	}
	return s.claimed, nil
}

type reconcileCloud struct {
	RoleCloud
	bindings []CloudRoleBinding
	mutated  []string
}

func (c *reconcileCloud) Query(_ context.Context, ids []string) ([]CloudRoleBinding, error) {
	var out []CloudRoleBinding
	for _, b := range c.bindings {
		if b.DeviceID == ids[0] {
			out = append(out, b)
		}
	}
	return out, nil
}
func (c *reconcileCloud) Bind(_ context.Context, ids []string, _ string) error {
	c.mutated = append(c.mutated, ids...)
	return nil
}
func (c *reconcileCloud) Unbind(_ context.Context, ids []string, _ string) error {
	c.mutated = append(c.mutated, ids...)
	return nil
}

func TestReconcileSettlesOnlyTargetsConfirmedWithoutMutation(t *testing.T) {
	store := &reconcileStore{
		targets: map[string]CloudTarget{
			"bound":   {DeviceID: "bound", RoleID: "r"},
			"missing": {DeviceID: "missing", RoleID: "r"},
			"clean":   {DeviceID: "clean", Remove: true},
			"stale":   {DeviceID: "stale", Remove: true},
		},
		claimed: []string{"bound", "missing", "clean", "stale"},
		settled: map[string]bool{},
	}
	cloud := &reconcileCloud{bindings: []CloudRoleBinding{{DeviceID: "bound", RoleID: "r"}, {DeviceID: "stale", RoleID: "old"}}}
	roles := NewRoles(store, func() RoleCloud { return cloud })
	roles.reconcileDue(context.Background())
	want := map[string]bool{"bound": true, "missing": false, "clean": true, "stale": false}
	for device, settled := range want {
		got, ok := store.settled[device]
		if !ok || got != settled {
			t.Errorf("%s settled=%v (completed=%v), want %v", device, got, ok, settled)
		}
	}
	if store.unbounded != 0 {
		t.Fatal("claim without a deadline")
	}
}

func TestReconcileDueStopsBeforeLeaseRunsOut(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	store := &reconcileStore{
		targets: map[string]CloudTarget{"a": {DeviceID: "a", Remove: true}, "b": {DeviceID: "b", Remove: true}},
		claimed: []string{"a", "b"},
		settled: map[string]bool{},
		onWork:  func() { now = now.Add(reconcileLease) },
	}
	roles := NewRoles(store, func() RoleCloud { return &reconcileCloud{} })
	roles.now = func() time.Time { return now }
	roles.reconcileDue(context.Background())
	if _, ok := store.settled["b"]; ok || len(store.settled) != 1 {
		t.Fatalf("completed %v; want only the device started within the lease", store.settled)
	}
}
