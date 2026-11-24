package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"thing-connect/internal/bindlimit"
	"thing-connect/internal/model"
)

type memoryBindAttempts struct {
	mu     sync.Mutex
	counts map[int64]int
	locked map[int64]bool
}

func (m *memoryBindAttempts) Reserve(_ context.Context, userID int64, p bindlimit.Policy) (time.Duration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.locked[userID] {
		return p.Cooldown, nil
	}
	m.counts[userID]++
	if m.counts[userID] > p.MaxFailures {
		m.locked[userID], m.counts[userID] = true, 0
		return p.Cooldown, nil
	}
	return 0, nil
}

func (m *memoryBindAttempts) Release(_ context.Context, userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counts[userID]--
	return nil
}

func (m *memoryBindAttempts) Fail(_ context.Context, userID int64, p bindlimit.Policy) (time.Duration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.counts[userID] >= p.MaxFailures {
		m.locked[userID], m.counts[userID] = true, 0
		return p.Cooldown, nil
	}
	return 0, nil
}

func TestBindCountsOnlyWrongCodesAndLocksPerUser(t *testing.T) {
	store := &memoryBindAttempts{counts: map[int64]int{}, locked: map[int64]bool{}}
	limiter, err := bindlimit.New(store, bindlimit.Policy{Window: 10 * time.Minute, MaxFailures: 3, Cooldown: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	bs := &fakeBindStore{
		bindByFP: &model.DeviceBind{DeviceID: "dev-001", UserID: 1},
		devKey:   &model.DevicePool{DeviceID: "dev-001", DeviceKey: "key-001"},
	}
	svc := NewBindService(bs, newFakeCache3("123456", "AA:BB", "", "", true), nil, limiter, DefaultServiceConfig())
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := svc.Bind(ctx, 1, "123456"); err != nil {
			t.Fatalf("valid code %d: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := svc.Bind(ctx, 1, "000000"); !errors.Is(err, ErrCodeExpired) {
			t.Fatalf("wrong code %d: %v", i, err)
		}
	}
	_, err = svc.Bind(ctx, 1, "000000")
	var locked *bindlimit.LockedError
	if !errors.As(err, &locked) || locked.RetryAfter != time.Hour || err.Error() != "绑定失败次数过多，请 60 分钟后重试" {
		t.Fatalf("third failure: %v", err)
	}
	if _, err := svc.Bind(ctx, 1, "123456"); !errors.Is(err, bindlimit.ErrLocked) {
		t.Fatalf("valid code during cooldown: %v", err)
	}
	if _, err := svc.Bind(ctx, 2, "000000"); !errors.Is(err, ErrCodeExpired) {
		t.Fatalf("other user affected: %v", err)
	}
}

func TestBindByDeviceIDSharesFailureBudgetWithCodeBinding(t *testing.T) {
	store := &memoryBindAttempts{counts: map[int64]int{}, locked: map[int64]bool{}}
	limiter, err := bindlimit.New(store, bindlimit.Policy{Window: 10 * time.Minute, MaxFailures: 3, Cooldown: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	pool := &model.DevicePool{DeviceID: "dev-001", DeviceKey: "key-001"}
	byCode := NewBindService(&fakeBindStore{devKey: pool}, newFakeCache3("123456", "AA:BB", "", "", true), nil, limiter, DefaultServiceConfig())
	missing := NewBindService(&fakeBindStore{}, newFakeCache3("", "", "", "", true), nil, limiter, DefaultServiceConfig())
	offline := NewBindService(&fakeBindStore{devKey: pool}, newFakeCache3("", "", "", "", false), nil, limiter, DefaultServiceConfig())
	own := NewBindService(&fakeBindStore{devKey: pool, bindByID: &model.DeviceBind{DeviceID: "dev-001", UserID: 1}}, newFakeCache3("", "", "", "", true), nil, limiter, DefaultServiceConfig())
	ctx := context.Background()

	if _, err := byCode.Bind(ctx, 1, "000000"); !errors.Is(err, ErrCodeExpired) {
		t.Fatalf("wrong code: %v", err)
	}
	if err := missing.BindByDeviceID(ctx, 1, "TIRZ00000002", model.Fingerprint{}); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("unknown device: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := offline.BindByDeviceID(ctx, 1, "dev-001", model.Fingerprint{}); !errors.Is(err, ErrDeviceMQTTOffline) {
			t.Fatalf("dropped temporary connection %d: %v", i, err)
		}
	}
	if err := own.BindByDeviceID(ctx, 1, "dev-001", model.Fingerprint{}); err != nil {
		t.Fatalf("own device rebind: %v", err)
	}
	err = missing.BindByDeviceID(ctx, 1, "TIRZ00000003", model.Fingerprint{})
	var locked *bindlimit.LockedError
	if !errors.As(err, &locked) || err.Error() != "绑定失败次数过多，请 60 分钟后重试" {
		t.Fatalf("third probe: %v", err)
	}
	if err := own.BindByDeviceID(ctx, 1, "dev-001", model.Fingerprint{}); !errors.Is(err, bindlimit.ErrLocked) {
		t.Fatalf("bind by id during cooldown: %v", err)
	}
	if _, err := byCode.Bind(ctx, 1, "123456"); !errors.Is(err, bindlimit.ErrLocked) {
		t.Fatalf("code bind during cooldown: %v", err)
	}
	if err := missing.BindByDeviceID(ctx, 2, "TIRZ00000002", model.Fingerprint{}); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("other user affected: %v", err)
	}
}
