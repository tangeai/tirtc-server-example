package recognition

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"thing-connect/internal/push"
)

type permittedPost struct{ UserEventQueue }

func (permittedPost) MayPost(context.Context, UserEvent) (bool, error) { return true, nil }

func TestPublisherSeparatesCreateAndLookup(t *testing.T) {
	var post, get atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Key") != "test" || r.URL.Path != "/v1/push/internal/events" {
			t.Error("wrong authenticated endpoint")
		}
		if r.Method == http.MethodGet {
			get.Add(1)
			if r.URL.Query().Get("source_event_id") != "sample:alarm" {
				t.Error("missing lookup identity")
			}
			fmt.Fprint(w, `{"code":200,"data":{"outcome":"queued"}}`)
			return
		}
		post.Add(1)
		w.WriteHeader(429)
		fmt.Fprint(w, `{"code":42921,"data":{"outcome":"skipped_overload"}}`)
	}))
	defer srv.Close()
	p := Publisher{Queue: permittedPost{}, BaseURL: srv.URL, InternalKey: "test"}
	now := time.Now()
	v := UserEvent{Event: push.Event{Device: "d", Bind: "1:1", ID: "sample:alarm", Registered: now, Occurred: now, Expires: now.Add(time.Minute)}}
	if got := p.send(context.Background(), v); got != "skipped_overload" {
		t.Fatal(got)
	}
	v.Event.Expires = now.Add(-time.Second)
	v.Checking = true
	if got := p.send(context.Background(), v); got != "accepted" {
		t.Fatal(got)
	}
	v.Event.Registered = now.Add(-25 * time.Hour)
	if got := p.send(context.Background(), v); got != "confirmation_unknown" {
		t.Fatal(got)
	}
	if post.Load() != 1 || get.Load() != 1 {
		t.Fatal(post.Load(), get.Load())
	}
}

type failedLocalAck struct {
	permittedPost
	calls int
}

func (q *failedLocalAck) FinishUserEvents(_ context.Context, batch []UserEvent) error {
	q.calls++
	if len(batch) != 1 || batch[0].Lease != "original" || batch[0].Outcome != "pending" {
		return fmt.Errorf("unexpected batch")
	}
	if q.calls == 1 {
		return fmt.Errorf("temporary local failure")
	}
	return nil
}
func TestPublisherRetriesLocalAckWithoutAnotherPOST(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"code":50300}`)
	}))
	defer srv.Close()
	q := &failedLocalAck{}
	p := Publisher{Queue: q, BaseURL: srv.URL}
	v := UserEvent{Lease: "original", Event: push.Event{Expires: time.Now().Add(time.Minute)}}
	v.Outcome = p.send(context.Background(), v)
	if err := p.finish(context.Background(), []UserEvent{v}); err != nil || calls != 1 || q.calls != 2 {
		t.Fatal(calls, q.calls, err)
	}
}
