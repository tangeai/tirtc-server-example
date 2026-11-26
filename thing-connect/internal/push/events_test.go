package push

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestMalformedDisplayTimeKeepsEventValid(t *testing.T) {
	var e Event
	err := json.Unmarshal([]byte(`{"source_event_id":"x:crying","device_id":"d","bind_id":"1:2","event_type":"crying","registered_at":"2026-11-26T10:00:00Z","occurred_at":"bad"}`), &e)
	if err != nil || e.Validate() != nil || !e.Occurred.IsZero() {
		t.Fatalf("%+v %v", e, err)
	}
	e.Kind = "quarrel"
	if e.Validate() == nil {
		t.Fatal("changed type accepted")
	}
}

type ackQueue struct {
	DeliveryQueue
	attempts int
}

func (q *ackQueue) FinishPush(_ context.Context, d Delivery, result Result) error {
	if d.Lease != "original" || result != Accepted {
		return errors.New("acknowledgement changed")
	}
	q.attempts++
	if q.attempts == 1 {
		return errors.New("temporary write failure")
	}
	return nil
}

func TestRetryAcknowledgementAfterChannelAccepted(t *testing.T) {
	q := new(ackQueue)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := finishDelivery(ctx, q, Delivery{Lease: "original"}, Accepted); err != nil || q.attempts != 2 {
		t.Fatal(err, q.attempts)
	}
}
