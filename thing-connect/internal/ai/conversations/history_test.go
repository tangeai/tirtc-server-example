package conversations

import (
	"context"
	"errors"
	"testing"
	"time"
)

type ownerStub struct {
	owned bool
	err   error
}

func (o *ownerStub) UserOwnsDevice(context.Context, string, int64) (bool, error) {
	return o.owned, o.err
}

type cloudStub struct {
	scope    string
	rows     []Entry
	calls    int
	err      error
	listHook func()
}

func (c *cloudStub) Scope() string { return c.scope }
func (c *cloudStub) ListConversations(context.Context, string, int, int) ([]Entry, error) {
	c.calls++
	if c.listHook != nil {
		c.listHook()
	}
	return c.rows, c.err
}
func (c *cloudStub) ConversationDetail(context.Context, string) ([]Message, error) {
	c.calls++
	return nil, c.err
}
func (c *cloudStub) DeleteConversation(context.Context, string) error { c.calls++; return c.err }
func fixture() (*History, *ownerStub, *cloudStub) {
	o := &ownerStub{owned: true}
	c := &cloudStub{scope: "app-cn", rows: []Entry{{ID: "record", DeviceID: "device"}}}
	return New(o, func() Cloud { return c }, "test-secret"), o, c
}
func TestHistoryAccess(t *testing.T) {
	h, o, c := fixture()
	ctx := context.Background()
	page, err := h.List(ctx, 1, "device", 1, 1)
	if err != nil || !page.HasMore || len(page.Items) != 1 {
		t.Fatalf("list: %+v %v", page, err)
	}
	ref := page.Items[0].Reference
	if messages, err := h.Detail(ctx, 1, "device", "record", ref); err != nil || messages == nil {
		t.Fatalf("detail: %v %v", messages, err)
	}
	for _, tc := range []struct {
		name            string
		user            int64
		device, id, ref string
	}{
		{"another user", 2, "device", "record", ref}, {"another device", 1, "other", "record", ref}, {"another record", 1, "device", "secret", ref}, {"forged reference", 1, "device", "record", ref + "a"}, {"missing reference", 1, "device", "record", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := c.calls
			_, err := h.Detail(ctx, tc.user, tc.device, tc.id, tc.ref)
			if !errors.Is(err, ErrForbidden) || c.calls != before {
				t.Fatalf("unauthorized upstream call: %v", err)
			}
			if err = h.Delete(ctx, tc.user, tc.device, tc.id, tc.ref); !errors.Is(err, ErrForbidden) || c.calls != before {
				t.Fatalf("unauthorized delete: %v", err)
			}
		})
	}
	o.owned = false
	before := c.calls
	if _, err = h.Detail(ctx, 1, "device", "record", ref); !errors.Is(err, ErrForbidden) || c.calls != before {
		t.Fatalf("unbound: %v", err)
	}
	o.owned = true
	c.scope = "other-app"
	if _, err = h.Detail(ctx, 1, "device", "record", ref); !errors.Is(err, ErrForbidden) {
		t.Fatalf("scope change: %v", err)
	}
	c.scope = "app-cn"
	h.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if _, err = h.Detail(ctx, 1, "device", "record", ref); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expired: %v", err)
	}
}
func TestHistoryListFailsClosed(t *testing.T) {
	h, o, c := fixture()
	ctx := context.Background()
	o.owned = false
	if _, err := h.List(ctx, 1, "device", 1, 10); !errors.Is(err, ErrForbidden) || c.calls != 0 {
		t.Fatalf("ownership: %v", err)
	}
	o.owned = true
	c.rows[0].DeviceID = "other"
	if _, err := h.List(ctx, 1, "device", 1, 10); !errors.Is(err, ErrUpstream) {
		t.Fatalf("wrong device: %v", err)
	}
	c.rows[0].DeviceID = "device"
	c.listHook = func() { o.owned = false }
	if _, err := h.List(ctx, 1, "device", 1, 10); !errors.Is(err, ErrForbidden) {
		t.Fatalf("late unbind: %v", err)
	}
	for _, p := range []int{0, -1, 100001} {
		if _, err := h.List(ctx, 1, "device", p, 10); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
}
func TestHistoryDeleteRetryAndFailures(t *testing.T) {
	h, o, c := fixture()
	ctx := context.Background()
	p, _ := h.List(ctx, 1, "device", 1, 10)
	ref := p.Items[0].Reference
	c.err = ErrNotFound
	if err := h.Delete(ctx, 1, "device", "record", ref); err != nil {
		t.Fatal(err)
	}
	c.err = context.DeadlineExceeded
	if err := h.Delete(ctx, 1, "device", "record", ref); !errors.Is(err, ErrUpstream) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	o.err = errors.New("db offline")
	before := c.calls
	if _, err := h.Detail(ctx, 1, "device", "record", ref); !errors.Is(err, o.err) || c.calls != before {
		t.Fatal(err)
	}
}
