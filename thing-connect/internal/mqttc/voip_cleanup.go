package mqttc

import (
	"context"
	"encoding/json"
	"thing-connect/internal/voip"
	"time"
)

type voipAuthNotifier struct{ broker *Broker }

func NewVoipAuthNotifier(broker *Broker) voip.AuthNotifier { return &voipAuthNotifier{broker: broker} }

func (n *voipAuthNotifier) AuthChanged(ctx context.Context, deviceID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"type": "callers_update", "channel": "wx", "payload": map[string]any{}})
	if err != nil {
		return err
	}
	token := n.broker.client.Publish("device/sn_"+deviceID+"/notify", 1, false, body)
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ErrPublishTimeout
	case <-token.Done():
		return token.Error()
	}
}
