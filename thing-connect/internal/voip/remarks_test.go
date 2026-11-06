package voip

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type remarkFixture struct {
	saved   string
	user    int64
	event   RemarkEvent
	pending bool
	fail    bool
	delay   time.Duration
}

func (f *remarkFixture) Save(_ context.Context, d, p, v string, u int64) (RemarkEvent, error) {
	f.saved = v
	f.user = u
	f.pending = true
	f.event = RemarkEvent{ID: 1, DeviceID: d, PeerID: p}
	return f.event, nil
}
func (f *remarkFixture) Pending(context.Context) ([]RemarkEvent, error) {
	return []RemarkEvent{f.event}, nil
}
func (f *remarkFixture) Complete(context.Context, int64) error { f.pending = false; return nil }
func (f *remarkFixture) Retry(_ context.Context, _ int64, d time.Duration) error {
	f.delay = d
	return nil
}
func (f *remarkFixture) RemarkChanged(context.Context, RemarkEvent) error {
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
	if f.saved != "爸爸" || f.user != 42 || !f.pending || f.delay != time.Second {
		t.Fatalf("bad saved state: %+v", f)
	}
	f.fail = false
	s.deliver(ctx, f.event)
	if f.pending {
		t.Fatal("successful retry did not complete event")
	}
	if err := s.Set(ctx, "device", "peer", strings.Repeat("名", 65), 42); !errors.Is(err, ErrRemarkInvalid) {
		t.Fatal(err)
	}
}
