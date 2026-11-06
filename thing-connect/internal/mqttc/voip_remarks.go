package mqttc

import (
	"context"
	"crypto/sha256"
	"fmt"
	"thing-connect/internal/voip"
	"time"
)

type RemarkPublisher interface{ Publish(string, byte, any) error }
type remarkNotifier struct{ broker RemarkPublisher }

func NewRemarkNotifier(broker RemarkPublisher) voip.RemarkNotifier {
	return &remarkNotifier{broker: broker}
}
func (n *remarkNotifier) RemarkChanged(ctx context.Context, event voip.RemarkEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Stable event ID makes retries identifiable; consumers refresh their list,
	// so duplicate and out-of-order invalidations cannot restore old names.
	id := sha256.Sum256([]byte(fmt.Sprintf("voip-remark:%s:%s:%d", event.DeviceID, event.PeerID, event.ID)))
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	msgID := fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:16])
	return n.broker.Publish(DeviceNotifyTopic("sn_"+event.DeviceID), 1, map[string]any{
		"msg_id": msgID, "from": "", "type": "callers_update", "channel": "device",
		"ts":      time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"payload": map[string]any{"action": "remark", "contact_type": "voip", "peer_id": event.PeerID},
	})
}
