package mqttc

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"thing-connect/internal/call"
)

func TestCallNotificationStableEnvelope(t *testing.T) {
	for _, kind := range []string{"call_incoming", "callee_answered", "call_reject", "room_cancel"} {
		t.Run(kind, func(t *testing.T) {
			b := &remarkCapture{}
			n := NewCallNotifier(b)
			e := call.Notification{ID: "room:event-1", RoomID: "room", Target: "device", Kind: kind, From: "caller", Payload: map[string]string{"room_id": "room"}, CreatedAt: time.Now().UnixMilli()}
			if err := n.PublishCallNotification(context.Background(), e); err != nil {
				t.Fatal(err)
			}
			topic := "device/sn_device/notify"
			if kind == "call_incoming" {
				topic = "device/sn_device/cmd"
			}
			if b.topic != topic || b.qos != 1 {
				t.Fatalf("topic/qos=%s/%d", b.topic, b.qos)
			}
			raw, err := json.Marshal(b.value)
			if err != nil {
				t.Fatal(err)
			}
			var envelope map[string]any
			if err = json.Unmarshal(raw, &envelope); err != nil {
				t.Fatal(err)
			}
			if len(envelope) != 6 || envelope["channel"] != "device" || envelope["type"] != kind || envelope["from"] != "caller" {
				t.Fatal(envelope)
			}
			if _, err := uuid.Parse(envelope["msg_id"].(string)); err != nil {
				t.Fatal(err)
			}
			if err := n.PublishCallNotification(context.Background(), e); err != nil {
				t.Fatal(err)
			}
			repeated, err := json.Marshal(b.value)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(raw, repeated) {
				t.Fatal("retry changed wire envelope")
			}
		})
	}
}
