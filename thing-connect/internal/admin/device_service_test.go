package admin

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fakeDeviceCommandStore struct {
	mutation ForceUnbindMutation
	err      error
	calls    int
}

func (f *fakeDeviceCommandStore) ForceUnbind(_ context.Context, mutation ForceUnbindMutation) error {
	f.calls++
	f.mutation = mutation
	return f.err
}

func TestDeviceServiceForceUnbindBuildsAtomicMutation(t *testing.T) {
	commands := &fakeDeviceCommandStore{}
	service := NewDeviceService(commands)
	result, err := service.ForceUnbind(context.Background(), ForceUnbindInput{
		DeviceID:       " device-1 ",
		ExpectedUserID: 42,
		Reason:         " owner requested ",
		Actor:          AccessIdentity{UserID: 7, Roles: []string{"operator", "auditor"}},
		RequestID:      "request-1",
		Method:         "POST",
		Path:           "/v1/admin/devices/device-1/force-unbind",
		ClientIP:       "127.0.0.1",
		UserAgent:      "test-agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	if commands.calls != 1 {
		t.Fatalf("store calls=%d, want 1", commands.calls)
	}
	if result.DeviceID != "device-1" || !result.Unbound || !reflect.DeepEqual(result.CleanupTargets, []string{"ai", "voip", "call"}) {
		t.Fatalf("unexpected result: %+v", result)
	}
	got := commands.mutation
	if got.DeviceID != "device-1" || got.ExpectedUserID != 42 || !reflect.DeepEqual(got.CleanupTargets, result.CleanupTargets) {
		t.Fatalf("unexpected mutation: %+v", got)
	}
	if got.Audit.Action != "device.unbind" || got.Audit.ResourceID != "device-1" || got.Audit.AdminUserID != 7 || got.Audit.RoleCodes != "operator,auditor" || got.Audit.Reason != "owner requested" {
		t.Fatalf("unexpected audit: %+v", got.Audit)
	}
	if got.Audit.Before != `{"user_id":42}` || got.Audit.After != `{"user_id":0}` {
		t.Fatalf("unexpected audit values: before=%s after=%s", got.Audit.Before, got.Audit.After)
	}
}

func TestDeviceServiceForceUnbindRejectsInvalidInputAndPropagatesPortError(t *testing.T) {
	commands := &fakeDeviceCommandStore{}
	service := NewDeviceService(commands)
	if _, err := service.ForceUnbind(context.Background(), ForceUnbindInput{}); !errors.Is(err, ErrInvalidDeviceCommand) {
		t.Fatalf("invalid input error=%v", err)
	}
	if commands.calls != 0 {
		t.Fatal("invalid input reached persistence port")
	}

	commands.err = ErrConflict
	_, err := service.ForceUnbind(context.Background(), ForceUnbindInput{
		DeviceID: "device-1", ExpectedUserID: 42, Reason: "test", Actor: AccessIdentity{UserID: 7},
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("port error=%v, want ErrConflict", err)
	}
}
