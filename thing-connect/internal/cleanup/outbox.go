package cleanup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"thing-connect/internal/backoff"
	"time"
)

const (
	batchSize       = 20
	deliveryTimeout = 5 * time.Second
	// leaseDuration covers a full batch of sequential deliveries so the last
	// task in a batch is still leased when it is acknowledged.
	leaseDuration = 2 * time.Minute
	// leaseMargin stops acknowledging before the database lease can expire;
	// later tasks become due again and are redelivered, which targets tolerate.
	leaseMargin = 10 * time.Second

	// queueTimeout bounds every queue call; a half-open database connection
	// would otherwise block the delivery loop forever.
	queueTimeout = 10 * time.Second

	backlogUnhealthyAge = 15 * time.Minute
	backlogSampleStale  = time.Minute
)

// Target is one idempotent service-to-service cleanup endpoint.
type Target struct {
	Name        string
	URL         string
	InternalKey string
}

// Task is one claimed cleanup notification. Lease is the opaque claim
// snapshot; acknowledging with a stale snapshot must not touch the row.
type Task struct {
	ID       int64
	DeviceID string
	Target   string
	Attempts int
	Lease    string
}

// Backlog is a sample of all pending tasks. OldestAge measures from the first
// enqueue, so re-enqueueing a pending task keeps its waiting age.
type Backlog struct {
	Pending   int64
	OldestAge time.Duration
}

// Queue is the durable task store behind the outbox. Unbind transactions
// insert tasks; several user-server instances may drain it concurrently.
type Queue interface {
	// Claim leases up to limit due tasks that no other claimer holds. The
	// lease time comes from the database clock.
	Claim(ctx context.Context, limit int, lease time.Duration) ([]Task, error)
	// Complete deletes the task only if it still carries task.Lease. It
	// reports false when the task was re-enqueued or re-claimed meanwhile.
	Complete(ctx context.Context, task Task) (bool, error)
	// Retry records the failure and schedules the next attempt only if the
	// task still carries task.Lease.
	Retry(ctx context.Context, task Task, delay time.Duration, lastError string) (bool, error)
	Backlog(ctx context.Context) (Backlog, error)
}

// Outbox delivers persisted cleanup notifications so transient failures,
// process restarts and concurrent instances cannot silently lose them.
type Outbox struct {
	queue   Queue
	client  *http.Client
	targets map[string]Target
	now     func() time.Time

	pending    atomic.Int64
	oldestMS   atomic.Int64
	measuredMS atomic.Int64
}

func NewOutbox(queue Queue, targets []Target) (*Outbox, error) {
	if queue == nil {
		return nil, errors.New("cleanup outbox: queue is required")
	}
	m := make(map[string]Target, len(targets))
	for _, target := range targets {
		if target.Name != "" && target.URL != "" {
			m[target.Name] = target
		}
	}
	return &Outbox{queue: queue, client: &http.Client{Timeout: deliveryTimeout}, targets: m, now: time.Now}, nil
}

// TargetNames returns the configured, de-duplicated delivery targets. It is
// used when an unbind transaction writes its outbox records before dispatch.
func TargetNames(targets []Target) []string {
	seen := make(map[string]struct{}, len(targets))
	names := make([]string, 0, len(targets))
	for _, target := range targets {
		if target.Name == "" || target.URL == "" {
			continue
		}
		if _, exists := seen[target.Name]; exists {
			continue
		}
		seen[target.Name] = struct{}{}
		names = append(names, target.Name)
	}
	return names
}

// Run delivers queued tasks until ctx is cancelled.
func (o *Outbox) Run(ctx context.Context) {
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			o.sampleBacklog(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	defer func() { <-sampled }()
	o.process(ctx)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			o.process(ctx)
		}
	}
}

func (o *Outbox) process(ctx context.Context) {
	deadline := o.now().Add(leaseDuration - leaseMargin)
	claimCtx, cancel := context.WithTimeout(ctx, queueTimeout)
	tasks, err := o.queue.Claim(claimCtx, batchSize, leaseDuration)
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("cleanup outbox claim failed", "err", err)
		}
		return
	}
	for _, current := range tasks {
		if !o.now().Before(deadline) {
			slog.Warn("cleanup outbox lease nearly expired, leaving remaining tasks for redelivery", "remaining_from_id", current.ID)
			return
		}
		deliverErr := o.deliver(ctx, current)
		if !o.now().Before(deadline) {
			slog.Warn("cleanup outbox lease expired during delivery, task will be redelivered", "id", current.ID)
			return
		}
		ackCtx, cancel := context.WithTimeout(ctx, queueTimeout)
		if deliverErr != nil {
			delay := retryDelay(current.Attempts + 1)
			applied, err := o.queue.Retry(ackCtx, current, delay, truncateError(deliverErr))
			cancel()
			if err != nil && ctx.Err() == nil {
				slog.Warn("cleanup outbox retry update failed", "id", current.ID, "err", err)
			} else if err == nil && !applied {
				slog.Info("cleanup outbox task changed during delivery, keeping newer task", "id", current.ID)
			}
			continue
		}
		applied, err := o.queue.Complete(ackCtx, current)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.Warn("cleanup outbox delete failed", "id", current.ID, "err", err)
		} else if err == nil && !applied {
			slog.Info("cleanup outbox task changed during delivery, keeping newer task", "id", current.ID)
		}
	}
}

func (o *Outbox) sampleBacklog(ctx context.Context) {
	sampleCtx, cancel := context.WithTimeout(ctx, queueTimeout)
	defer cancel()
	backlog, err := o.queue.Backlog(sampleCtx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("cleanup outbox backlog sample failed", "err", err)
		}
		return
	}
	now := o.now()
	o.pending.Store(backlog.Pending)
	if backlog.Pending > 0 {
		o.oldestMS.Store(now.Add(-backlog.OldestAge).UnixMilli())
	} else {
		o.oldestMS.Store(0)
	}
	o.measuredMS.Store(now.UnixMilli())
}

// Metrics is a non-blocking snapshot for the service status reporter.
func (o *Outbox) Metrics() map[string]int64 {
	age := int64(0)
	if oldest := o.oldestMS.Load(); oldest > 0 {
		age = max(0, o.now().UnixMilli()-oldest)
	}
	return map[string]int64{
		"cleanup_pending":                o.pending.Load(),
		"cleanup_oldest_pending_age_ms":  age,
		"cleanup_backlog_measured_at_ms": o.measuredMS.Load(),
	}
}

// Health fails while the oldest pending task has waited longer than
// backlogUnhealthyAge, or while the backlog cannot be sampled. It recovers
// once a fresh sample shows the old tasks drained.
func (o *Outbox) Health(context.Context) error {
	now := o.now().UnixMilli()
	measured := o.measuredMS.Load()
	if measured == 0 || now-measured > backlogSampleStale.Milliseconds() {
		return errors.New("cleanup outbox backlog sample is stale")
	}
	if oldest := o.oldestMS.Load(); oldest > 0 && now-oldest > backlogUnhealthyAge.Milliseconds() {
		return fmt.Errorf("cleanup outbox oldest task waited over %s", backlogUnhealthyAge)
	}
	return nil
}

func (o *Outbox) deliver(ctx context.Context, current Task) error {
	target, ok := o.targets[current.Target]
	if !ok {
		return fmt.Errorf("unknown cleanup target %q", current.Target)
	}
	body, _ := json.Marshal(map[string]string{"device_id": current.DeviceID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Key", target.InternalKey)
	resp, err := o.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s returned HTTP %d", current.Target, resp.StatusCode)
	}
	// VoIP reports business failures with HTTP 200. A transport success alone
	// must not acknowledge and delete a durable cleanup task.
	var result struct {
		Code *int `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&result); err != nil {
		return fmt.Errorf("%s returned invalid cleanup response: %w", current.Target, err)
	}
	if result.Code == nil || (*result.Code != 0 && *result.Code != 200) {
		return fmt.Errorf("%s rejected cleanup", current.Target)
	}
	return nil
}

func retryDelay(attempt int) time.Duration {
	if attempt > 6 {
		attempt = 6
	}
	return backoff.Jitter(time.Duration(1<<max(0, attempt)) * 5 * time.Second)
}

func truncateError(err error) string {
	msg := err.Error()
	if len(msg) > 1000 {
		return msg[:1000]
	}
	return msg
}
