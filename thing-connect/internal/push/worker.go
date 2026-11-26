package push

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

var ErrQueueEmpty = errors.New("通知队列暂无可领取任务")

type Delivery struct {
	AppID     string
	Attempts  int
	ID        int64
	Lease     string
	Open      string
	Device    string
	Kind      string
	Template  string
	Reference string
	Time      string
}
type Result string

const (
	Accepted     Result = "accepted"
	Unknown      Result = "unknown"
	Failed       Result = "failed"
	Retry        Result = "pending"
	Unsubscribed Result = "unsubscribed"
)

type DeliveryQueue interface {
	ClaimPush(context.Context) (*Delivery, error)
	FinishPush(context.Context, Delivery, Result) error
	MaintainPush(context.Context) error
}
type Channel interface {
	Send(context.Context, Delivery) Result
}

func RunDelivery(ctx context.Context, q DeliveryQueue, ch Channel, metrics *DeliveryMetrics) {
	for ctx.Err() == nil {
		d, err := q.ClaimPush(ctx)
		if err == nil && d != nil {
			send, cancel := context.WithTimeout(ctx, 15*time.Second)
			result := ch.Send(send, *d)
			cancel()
			metrics.Observe(result)
			err = finishDelivery(ctx, q, *d, result)
		}
		if err != nil && !errors.Is(err, ErrQueueEmpty) && ctx.Err() == nil {
			slog.Warn("微信投递队列暂时不可用")
		}
		if err != nil || d == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
}

// Retry the database acknowledgement under the original lease. The channel
// has already returned and must never be called again by this operation.
func finishDelivery(ctx context.Context, q DeliveryQueue, d Delivery, result Result) error {
	ack, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for {
		err := q.FinishPush(ack, d, result)
		if err == nil {
			return nil
		}
		select {
		case <-ack.Done():
			return err
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func RunMaintenance(ctx context.Context, q DeliveryQueue) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := q.MaintainPush(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("微信队列维护暂时不可用")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
