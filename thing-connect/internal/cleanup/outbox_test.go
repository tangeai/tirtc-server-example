package cleanup

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type cleanupTransport func(*http.Request) (*http.Response, error)

func (f cleanupTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type fakeQueue struct {
	mu         sync.Mutex
	claimed    []Task
	completed  []int64
	retried    []int64
	stale      map[int64]bool
	backlog    Backlog
	backlogErr error
	onComplete func()
	deadlines  []bool
	targets    [][]string
}

func (q *fakeQueue) recordDeadline(ctx context.Context) {
	deadline, ok := ctx.Deadline()
	q.deadlines = append(q.deadlines, ok && time.Until(deadline) <= queueTimeout)
}

func (q *fakeQueue) Claim(ctx context.Context, targets []string, _ int, _ time.Duration) ([]Task, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.recordDeadline(ctx)
	q.targets = append(q.targets, targets)
	tasks := q.claimed
	q.claimed = nil
	return tasks, nil
}

func (q *fakeQueue) Complete(ctx context.Context, task Task) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.recordDeadline(ctx)
	q.completed = append(q.completed, task.ID)
	if q.onComplete != nil {
		q.onComplete()
	}
	return !q.stale[task.ID], nil
}

func (q *fakeQueue) Retry(ctx context.Context, task Task, _ time.Duration, _ string) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.recordDeadline(ctx)
	q.retried = append(q.retried, task.ID)
	return !q.stale[task.ID], nil
}

func (q *fakeQueue) Backlog(ctx context.Context, targets []string) (Backlog, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.recordDeadline(ctx)
	q.targets = append(q.targets, targets)
	return q.backlog, q.backlogErr
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newTestOutbox(t *testing.T, queue *fakeQueue, respond func(*http.Request) (int, string)) (*Outbox, *fakeClock) {
	t.Helper()
	o, err := NewOutbox(queue, []Target{
		{Name: "voip", URL: "http://voip.invalid", InternalKey: "test-key"},
		{Name: "ai", URL: "http://ai.invalid", InternalKey: "test-key"},
	})
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Unix(1_800_000_000, 0)}
	o.now = clock.Now
	o.client.Transport = cleanupTransport(func(r *http.Request) (*http.Response, error) {
		status, body := respond(r)
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	return o, clock
}

func TestNewOutboxRejectsMissingQueue(t *testing.T) {
	if _, err := NewOutbox(nil, nil); err == nil {
		t.Fatal("NewOutbox(nil) should fail at startup")
	}
}

func TestDeliveryRequiresBusinessSuccess(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		success    bool
	}{
		{"voip success", `{"code":0}`, 200, true},
		{"other service success", `{"code":200}`, 200, true},
		{"business failure", `{"code":50000}`, 200, false},
		{"missing code", `{}`, 200, false},
		{"invalid response", `not json`, 200, false},
		{"http failure", `{"code":0}`, 503, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, _ := newTestOutbox(t, &fakeQueue{}, func(r *http.Request) (int, string) {
				if r.Header.Get("X-Internal-Key") != "test-key" {
					t.Fatal("missing credential")
				}
				return tc.status, tc.body
			})
			err := o.deliver(context.Background(), Task{DeviceID: "device", Target: "voip"})
			if (err == nil) != tc.success {
				t.Fatalf("success=%v err=%v", tc.success, err)
			}
		})
	}
}

func TestProcessCompletesSuccessAndRetriesFailure(t *testing.T) {
	queue := &fakeQueue{claimed: []Task{{ID: 1, Target: "voip"}, {ID: 2, Target: "ai"}}}
	o, _ := newTestOutbox(t, queue, func(r *http.Request) (int, string) {
		if r.URL.Host == "ai.invalid" {
			return 503, ""
		}
		return 200, `{"code":0}`
	})
	o.process(context.Background())
	if len(queue.completed) != 1 || queue.completed[0] != 1 {
		t.Fatalf("completed=%v, want [1]", queue.completed)
	}
	if len(queue.retried) != 1 || queue.retried[0] != 2 {
		t.Fatalf("retried=%v, want [2]", queue.retried)
	}
}

// A half-open database connection must not block the delivery loop forever.
func TestProcessBoundsEveryQueueCall(t *testing.T) {
	queue := &fakeQueue{claimed: []Task{{ID: 1, Target: "voip"}, {ID: 2, Target: "ai"}}}
	o, _ := newTestOutbox(t, queue, func(r *http.Request) (int, string) {
		if r.URL.Host == "ai.invalid" {
			return 503, ""
		}
		return 200, `{"code":0}`
	})
	o.sampleBacklog(context.Background())
	o.process(context.Background())
	if len(queue.deadlines) != 4 {
		t.Fatalf("queue calls=%d, want backlog, claim, complete and retry", len(queue.deadlines))
	}
	for i, bounded := range queue.deadlines {
		if !bounded {
			t.Fatalf("queue call %d has no deadline within %s", i, queueTimeout)
		}
	}
}

func TestProcessStopsAcknowledgingWhenLeaseRunsOut(t *testing.T) {
	queue := &fakeQueue{claimed: []Task{{ID: 1, Target: "voip"}, {ID: 2, Target: "voip"}}}
	var o *Outbox
	var clock *fakeClock
	o, clock = newTestOutbox(t, queue, func(*http.Request) (int, string) {
		clock.Advance(leaseDuration)
		return 200, `{"code":0}`
	})
	o.process(context.Background())
	if len(queue.completed) != 0 || len(queue.retried) != 0 {
		t.Fatalf("completed=%v retried=%v, want no acknowledgement after the lease ran out", queue.completed, queue.retried)
	}
}

func TestProcessSkipsRemainingTasksAfterLeaseDeadline(t *testing.T) {
	queue := &fakeQueue{claimed: []Task{{ID: 1, Target: "voip"}, {ID: 2, Target: "voip"}}}
	delivered := 0
	o, clock := newTestOutbox(t, queue, func(*http.Request) (int, string) {
		delivered++
		return 200, `{"code":0}`
	})
	queue.onComplete = func() { clock.Advance(leaseDuration - leaseMargin) }
	o.process(context.Background())
	if delivered != 1 || len(queue.completed) != 1 || queue.completed[0] != 1 {
		t.Fatalf("delivered=%d completed=%v, want only task 1", delivered, queue.completed)
	}
}

func TestProcessToleratesTaskChangedDuringDelivery(t *testing.T) {
	queue := &fakeQueue{claimed: []Task{{ID: 1, Target: "voip"}}, stale: map[int64]bool{1: true}}
	o, _ := newTestOutbox(t, queue, func(*http.Request) (int, string) { return 200, `{"code":0}` })
	o.process(context.Background())
	if len(queue.completed) != 1 {
		t.Fatalf("completed=%v, want one conditional attempt", queue.completed)
	}
}

func TestHealthTracksBacklogAgeAndRecovery(t *testing.T) {
	queue := &fakeQueue{}
	o, clock := newTestOutbox(t, queue, func(*http.Request) (int, string) { return 200, `{"code":0}` })
	if o.Health(context.Background()) == nil {
		t.Fatal("health before the first sample should fail")
	}

	queue.backlog = Backlog{Pending: 3, OldestAge: backlogUnhealthyAge + time.Second}
	o.sampleBacklog(context.Background())
	o.process(context.Background())
	if o.Health(context.Background()) == nil {
		t.Fatal("an over-age backlog should be unhealthy even when deliveries succeed")
	}
	metrics := o.Metrics()
	if metrics["cleanup_pending"] != 3 || metrics["cleanup_oldest_pending_age_ms"] != (backlogUnhealthyAge+time.Second).Milliseconds() || metrics["cleanup_backlog_measured_at_ms"] != clock.Now().UnixMilli() {
		t.Fatalf("metrics=%v", metrics)
	}

	queue.backlog = Backlog{Pending: 1, OldestAge: time.Minute}
	o.sampleBacklog(context.Background())
	o.process(context.Background())
	if err := o.Health(context.Background()); err != nil {
		t.Fatalf("drained backlog should recover: %v", err)
	}

	queue.backlog = Backlog{}
	o.sampleBacklog(context.Background())
	o.process(context.Background())
	if got := o.Metrics()["cleanup_oldest_pending_age_ms"]; got != 0 {
		t.Fatalf("empty backlog age=%d, want 0", got)
	}

	queue.backlogErr = errors.New("database unavailable")
	clock.Advance(backlogSampleStale + time.Second)
	o.process(context.Background())
	if o.Health(context.Background()) == nil {
		t.Fatal("a stale backlog sample should be unhealthy")
	}
}

// A task for a target removed from configuration cannot be delivered; it must
// not hold the configured backlog unhealthy forever, yet stay observable.
func TestUnconfiguredTargetTasksAreReportedSeparately(t *testing.T) {
	queue := &fakeQueue{backlog: Backlog{Pending: 1, OldestAge: time.Minute, Unknown: 2}}
	o, _ := newTestOutbox(t, queue, func(*http.Request) (int, string) { return 200, `{"code":0}` })
	o.sampleBacklog(context.Background())
	o.process(context.Background())
	for _, targets := range queue.targets {
		if strings.Join(targets, ",") != "ai,voip" {
			t.Fatalf("queue scoped to %v, want the configured targets", targets)
		}
	}
	if len(queue.targets) != 2 {
		t.Fatalf("scoped queue calls=%d, want backlog and claim", len(queue.targets))
	}
	if err := o.Health(context.Background()); err != nil {
		t.Fatalf("unconfigured-target tasks degraded health: %v", err)
	}
	if metrics := o.Metrics(); metrics["cleanup_unknown_target"] != 2 || metrics["cleanup_pending"] != 1 {
		t.Fatalf("metrics=%v", metrics)
	}
	queue.backlog = Backlog{}
	o.sampleBacklog(context.Background())
	if got := o.Metrics()["cleanup_unknown_target"]; got != 0 {
		t.Fatalf("cleanup_unknown_target=%d after removal, want 0", got)
	}
}
