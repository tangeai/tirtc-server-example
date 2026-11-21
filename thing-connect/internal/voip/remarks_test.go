package voip

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type remarkFixture struct {
	saved     string
	user      int64
	saveLease time.Duration
	event     RemarkEvent
	claimable []RemarkEvent
	pending   bool
	fail      bool
	delay     time.Duration
	completed int
	retried   int
	unbounded int
	onNotify  func()
}

func (f *remarkFixture) bounded(ctx context.Context) {
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > remarkStoreTimeout {
		f.unbounded++
	}
}

func (f *remarkFixture) Save(_ context.Context, d, p, v string, u int64, lease time.Duration) (RemarkEvent, error) {
	f.saved = v
	f.user = u
	f.saveLease = lease
	f.pending = true
	f.event = RemarkEvent{ID: 1, DeviceID: d, PeerID: p, Lease: "lease-1"}
	return f.event, nil
}
func (f *remarkFixture) Claim(ctx context.Context, _ int, _ time.Duration) ([]RemarkEvent, error) {
	f.bounded(ctx)
	events := f.claimable
	f.claimable = nil
	return events, nil
}
func (f *remarkFixture) Complete(ctx context.Context, _ RemarkEvent) (bool, error) {
	f.bounded(ctx)
	f.completed++
	f.pending = false
	return true, nil
}
func (f *remarkFixture) Retry(ctx context.Context, _ RemarkEvent, d time.Duration) (bool, error) {
	f.bounded(ctx)
	f.retried++
	f.delay = d
	return true, nil
}
func (f *remarkFixture) RemarkChanged(context.Context, RemarkEvent) error {
	if f.onNotify != nil {
		f.onNotify()
	}
	if f.fail {
		return errors.New("offline")
	}
	return nil
}

func TestRemarkNotificationFailureRetainsDurableEvent(t *testing.T) {
	f := &remarkFixture{fail: true}
	s := NewRemarks(f, f)
	ctx := context.Background()
	if err := s.Set(ctx, "device", "peer", "  爸爸  ", 42); err != nil {
		t.Fatal(err)
	}
	if f.saved != "爸爸" || f.user != 42 || !f.pending || f.delay != time.Second || f.saveLease != remarkSaveLease {
		t.Fatalf("bad saved state: %+v", f)
	}
	f.fail = false
	f.claimable = []RemarkEvent{f.event}
	s.drain(ctx)
	if f.pending {
		t.Fatal("successful retry did not complete event")
	}
	if f.unbounded != 0 {
		t.Fatalf("%d store calls without a deadline", f.unbounded)
	}
	if err := s.Set(ctx, "device", "peer", strings.Repeat("名", 65), 42); !errors.Is(err, ErrRemarkInvalid) {
		t.Fatal(err)
	}
}

func TestRemarkDeliveryPastLeaseLeavesEventForRedelivery(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	f := &remarkFixture{}
	s := NewRemarks(f, f)
	s.now = func() time.Time { return now }
	f.onNotify = func() { now = now.Add(remarkSaveLease) }
	if err := s.Set(context.Background(), "device", "peer", "妈妈", 42); err != nil {
		t.Fatal(err)
	}
	if f.completed != 0 || f.retried != 0 || !f.pending {
		t.Fatalf("completed=%d retried=%d pending=%v, want event left for redelivery", f.completed, f.retried, f.pending)
	}
}
