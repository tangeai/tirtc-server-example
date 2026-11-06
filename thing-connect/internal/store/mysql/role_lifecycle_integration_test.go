package mysql_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"thing-connect/internal/ai"
	mysqlstore "thing-connect/internal/store/mysql"
	"thing-connect/internal/store/mysql/migrate"
	"thing-connect/internal/testenv"
)

type lifecycleCloud struct {
	bindings []ai.CloudRoleBinding
	deleteFn func() error
	bindErr  error
	deleted  int
}

func (c *lifecycleCloud) Query(context.Context, []string) ([]ai.CloudRoleBinding, error) {
	return c.bindings, nil
}
func (c *lifecycleCloud) Bind(context.Context, []string, string) error { return c.bindErr }
func (c *lifecycleCloud) Unbind(context.Context, []string, string) error {
	c.bindings = nil
	return nil
}
func (c *lifecycleCloud) Delete(context.Context, string) error {
	c.deleted++
	if c.deleteFn != nil {
		return c.deleteFn()
	}
	return nil
}

func TestRoleLifecycleProtectsOfflineCloudAndConcurrentBindings(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../../tests/testdata/config.yaml")
	db := testenv.OpenDBOrSkip(t, cfg)
	defer db.Close()
	if err := migrate.Migrate(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	stamp := time.Now().UnixNano()
	user := stamp%1000000000 + 1000000000
	device := fmt.Sprintf("R%018d", stamp%1000000000000000000)
	role := fmt.Sprintf("role-%d", stamp)
	_, err := db.Exec(`INSERT INTO device_bind(device_id,mac,user_id,last_user_id,bind_time) VALUES(?,?,?,?,NOW())`, device, fmt.Sprintf("role-%d", stamp), user, user)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM device_bind WHERE device_id=?`, device)
	_, err = db.Exec(`INSERT INTO ai_user_role(user_id,role_id) VALUES(?,?)`, user, role)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM ai_user_role WHERE role_id=?`, role)
	defer db.Exec(`DELETE FROM ai_device_role WHERE device_id=?`, device)
	defer db.Exec(`DELETE FROM ai_cloud_role_ref WHERE device_id=?`, device)
	defer db.Exec(`DELETE FROM ai_cloud_role_target WHERE device_id=?`, device)
	store := mysqlstore.NewRoleLifecycleStore(db)
	// Losing the session that owns GET_LOCK must not move writes to another connection.
	err = store.WithAccount(ctx, user, func(locked context.Context) error {
		var connectionID int64
		if e := db.Get(&connectionID, `SELECT IS_USED_LOCK(?)`, fmt.Sprintf("tc:ai-role:%d", user)); e != nil {
			return e
		}
		if _, e := db.Exec(fmt.Sprintf("KILL CONNECTION %d", connectionID)); e != nil {
			return e
		}
		return store.MarkDeleting(locked, user, role)
	})
	if err == nil {
		t.Fatal("lost lock connection allowed a protected write")
	}
	var deleting bool
	if err = db.Get(&deleting, `SELECT deleting FROM ai_user_role WHERE role_id=?`, role); err != nil || deleting {
		t.Fatalf("lost-lock write persisted: deleting=%v err=%v", deleting, err)
	}

	cloud := &lifecycleCloud{}
	roles := ai.NewRoles(store, func() ai.RoleCloud { return cloud })
	if err = roles.SetLocal(ctx, user, device, role); err != nil {
		t.Fatal(err)
	}
	if err = roles.Delete(ctx, user, role); !errors.Is(err, ai.ErrRoleBusy) {
		t.Fatalf("offline local binding deletion: %v", err)
	}
	if cloud.deleted != 0 {
		t.Fatal("used role reached cloud delete")
	}
	if err = roles.SetLocal(ctx, user, device, ""); err != nil {
		t.Fatal(err)
	}
	cloud.bindings = []ai.CloudRoleBinding{{DeviceID: device, RoleID: role}}
	if err = roles.Delete(ctx, user, role); !errors.Is(err, ai.ErrRoleBusy) {
		t.Fatalf("legacy cloud binding deletion: %v", err)
	}
	cloud.bindings = nil
	cloud.bindErr = errors.New("timeout")
	if err = roles.SetCloud(ctx, user, []string{device}, role, false); !errors.Is(err, ai.ErrRoleUpstream) {
		t.Fatal(err)
	}
	if err = roles.Delete(ctx, user, role); !errors.Is(err, ai.ErrRoleBusy) {
		t.Fatalf("uncertain cloud binding deletion: %v", err)
	}
	if err = roles.SetCloud(ctx, user, []string{device}, role, true); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	cloud.deleteFn = func() error { close(entered); <-release; return errors.New("delete timeout") }
	deleted := make(chan error, 1)
	go func() { deleted <- roles.Delete(ctx, user, role) }()
	<-entered
	bound := make(chan error, 1)
	go func() { bound <- roles.SetLocal(ctx, user, device, role) }()
	select {
	case err = <-bound:
		t.Fatalf("binding bypassed delete lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err = <-deleted; !errors.Is(err, ai.ErrRoleUpstream) {
		t.Fatal(err)
	}
	if err = <-bound; !errors.Is(err, ai.ErrRoleDeleting) {
		t.Fatalf("binding must reject durable deletion marker: %v", err)
	}
	cloud.deleteFn = nil
	if err = roles.Delete(ctx, user, role); err != nil {
		t.Fatalf("delete retry: %v", err)
	}
	if err = roles.SetLocal(ctx, user, device, role); !errors.Is(err, ai.ErrRoleForbidden) {
		t.Fatalf("deleted role binding: %v", err)
	}
}
