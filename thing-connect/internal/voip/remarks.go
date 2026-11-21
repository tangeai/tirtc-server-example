package voip

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"thing-connect/internal/backoff"
	"time"
	"unicode/utf8"
)

var ErrContactMissing = errors.New("联系人不存在")
var ErrRemarkInvalid = errors.New("备注不能超过 64 个字符")

const (
	remarkDeliveryTimeout = 6 * time.Second
	remarkBatchSize       = 20
	// remarkSaveLease reserves a freshly saved event for the inline delivery
	// of the instance that wrote it; other instances pick it up only after it
	// expires.
	remarkSaveLease = 30 * time.Second
	// remarkClaimLease covers a batch of sequential deliveries.
	remarkClaimLease   = remarkBatchSize*remarkDeliveryTimeout + 30*time.Second
	remarkLeaseMargin  = 10 * time.Second
	remarkStoreTimeout = 10 * time.Second
)

// RemarkEvent is one durable cache invalidation for a device contact. Lease
// is the claim snapshot; acknowledging with a stale one leaves the row alone.
type RemarkEvent struct {
	ID       int64  `db:"id"`
	DeviceID string `db:"device_id"`
	PeerID   string `db:"peer_id"`
	Attempts int    `db:"attempts"`
	Lease    string `db:"lease"`
}

// RemarkStore persists remarks and their notification outbox. Several
// call-server instances may drain the outbox concurrently.
type RemarkStore interface {
	// Save verifies the device and active authorization under lock and atomically
	// writes the independent name and a durable cache invalidation event leased
	// to the caller for lease.
	Save(ctx context.Context, deviceID, peerID, remark string, userID int64, lease time.Duration) (RemarkEvent, error)
	// Claim leases up to limit due events that no other claimer holds.
	Claim(ctx context.Context, limit int, lease time.Duration) ([]RemarkEvent, error)
	// Complete and Retry apply only while the event still carries its Lease.
	Complete(ctx context.Context, event RemarkEvent) (bool, error)
	Retry(ctx context.Context, event RemarkEvent, delay time.Duration) (bool, error)
}

type RemarkNotifier interface {
	RemarkChanged(context.Context, RemarkEvent) error
}

type Remarks struct {
	store    RemarkStore
	notifier RemarkNotifier
	now      func() time.Time
}

func NewRemarks(store RemarkStore, notifier RemarkNotifier) *Remarks {
	return &Remarks{store: store, notifier: notifier, now: time.Now}
}

func (s *Remarks) Set(ctx context.Context, deviceID, peerID, value string, userID int64) error {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) > 64 {
		return ErrRemarkInvalid
	}
	deadline := s.now().Add(remarkSaveLease - remarkLeaseMargin)
	event, err := s.store.Save(ctx, deviceID, peerID, value, userID, remarkSaveLease)
	if err != nil {
		return err
	}
	// Delivery failure leaves the event durable; the successful write is not
	// reported as failed, which would encourage clients to overwrite newer edits.
	s.deliver(ctx, event, deadline)
	return nil
}

// deliver notifies the device and acknowledges the event unless the lease
// ran out meanwhile, in which case the event is redelivered after it expires.
func (s *Remarks) deliver(ctx context.Context, event RemarkEvent, deadline time.Time) {
	notifyCtx, cancel := context.WithTimeout(ctx, remarkDeliveryTimeout)
	notifyErr := s.notifier.RemarkChanged(notifyCtx, event)
	cancel()
	if !s.now().Before(deadline) {
		slog.WarnContext(ctx, "contact notification lease expired, event will be redelivered", "id", event.ID)
		return
	}
	storeCtx, cancel := context.WithTimeout(ctx, remarkStoreTimeout)
	defer cancel()
	if notifyErr != nil {
		attempt := min(event.Attempts, 6)
		if _, e := s.store.Retry(storeCtx, event, max(time.Second, backoff.Jitter(time.Duration(1<<max(0, attempt))*time.Second))); e != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "contact notification retry failed", "err", e)
		}
		return
	}
	if _, err := s.store.Complete(storeCtx, event); err != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "contact notification completion failed", "err", err)
	}
}

func (s *Remarks) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		s.drain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Remarks) drain(ctx context.Context) {
	deadline := s.now().Add(remarkClaimLease - remarkLeaseMargin)
	claimCtx, cancel := context.WithTimeout(ctx, remarkStoreTimeout)
	events, err := s.store.Claim(claimCtx, remarkBatchSize, remarkClaimLease)
	cancel()
	if err != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "contact notifications unavailable", "err", err)
	}
	for _, event := range events {
		if ctx.Err() != nil || !s.now().Before(deadline) {
			return
		}
		s.deliver(ctx, event, deadline)
	}
}
