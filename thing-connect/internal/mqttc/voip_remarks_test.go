package mqttc

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"thing-connect/internal/voip"
	"time"
)

type remarkCapture struct {
	topic string
	qos   byte
	value any
}

func (b *remarkCapture) Publish(topic string, qos byte, value any) error {
	b.topic = topic
	b.qos = qos
	b.value = value
	return nil
}
func TestRemarkNotificationPreservesDeviceEnvelope(t *testing.T) {
	b := &remarkCapture{}
	n := NewRemarkNotifier(b)
	event := voip.RemarkEvent{ID: 42, DeviceID: "device", PeerID: "openid"}
	if err := n.RemarkChanged(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if b.topic != "device/sn_device/notify" || b.qos != 1 {
		t.Fatalf("topic/qos changed: %s/%d", b.topic, b.qos)
	}
	raw, err := json.Marshal(b.value)
	if err != nil {
		t.Fatal(err)
	}
	var msg map[string]any
	if err = json.Unmarshal(raw, &msg); err != nil {
		t.Fatal(err)
	}
	if len(msg) != 6 || msg["type"] != "callers_update" || msg["channel"] != "device" || msg["from"] != "" {
		t.Fatalf("legacy envelope: %s", raw)
	}
	id, ok := msg["msg_id"].(string)
	if !ok || !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(id) {
		t.Fatalf("message UUID: %v", msg["msg_id"])
	}
	if _, err = time.Parse(time.RFC3339Nano, msg["ts"].(string)); err != nil {
		t.Fatal(err)
	}
	payload := msg["payload"].(map[string]any)
	if len(payload) != 3 || payload["action"] != "remark" || payload["contact_type"] != "voip" || payload["peer_id"] != "openid" {
		t.Fatalf("legacy payload: %v", payload)
	}
	if err = n.RemarkChanged(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if b.value.(map[string]any)["msg_id"] != id {
		t.Fatal("retry changed event identity")
	}
}
