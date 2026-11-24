package mqttc

import (
	"context"
	"time"

	"github.com/google/uuid"

	"thing-connect/internal/call"
)

type CallNotifier struct{ broker RemarkPublisher }

func NewCallNotifier(broker RemarkPublisher) *CallNotifier { return &CallNotifier{broker: broker} }
func (n *CallNotifier) PublishCallNotification(ctx context.Context, event call.Notification) error {
	topic := DeviceNotifyTopic("sn_" + event.Target)
	if event.Kind == "call_incoming" {
		topic = DeviceCmdTopic("sn_" + event.Target)
	}
	// All lifecycle notifications use QoS 1. The immutable msg_id survives retries.
	return n.broker.PublishContext(ctx, topic, 1, map[string]any{
		"msg_id": uuid.NewSHA1(uuid.NameSpaceOID, []byte("call:"+event.ID)).String(), "from": event.From, "type": event.Kind, "channel": "device",
		"payload": event.Payload, "ts": time.UnixMilli(event.CreatedAt).UTC().Format("2006-01-02T15:04:05.000Z"),
	})
}
