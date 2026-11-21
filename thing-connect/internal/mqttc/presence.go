package mqttc

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"thing-connect/internal/config"
	"thing-connect/internal/presence"
)

type PresenceObserver interface {
	Observe(presence.Observation)
	// SubscriptionGap reports that events published while the subscription
	// was down may be missing.
	SubscriptionGap()
}

// PresenceSource is created by NewPresence, which only device-server uses.
// Command clients use New and explicitly unsubscribe from the former presence
// subscriptions.
type PresenceSource interface {
	Ping(context.Context) error
	Close()
}

func NewPresence(cfg config.MQTTCfg, observer PresenceObserver) (PresenceSource, error) {
	return newBroker(cfg, nil, func(_ mqtt.Client, msg mqtt.Message) {
		if o, ok := presenceObservation(msg.Topic(), msg.Payload(), msg.Retained(), time.Now()); ok {
			observer.Observe(o)
		}
	}, observer.SubscriptionGap)
}

func presenceObservation(topic string, payload []byte, retained bool, now time.Time) (presence.Observation, bool) {
	o := presence.Observation{At: now}
	if retained {
		return o, false
	}
	parts := strings.Split(topic, "/")
	if len(parts) == 3 && parts[0] == "device" && parts[2] == "up" {
		var body struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(payload, &body) != nil || body.Type != "heartbeat" {
			return o, false
		}
		o.ClientID = parts[1]
	} else if len(parts) == 6 && parts[0] == "$SYS" && parts[1] == "brokers" && parts[3] == "clients" {
		if parts[5] != "connected" && parts[5] != "disconnected" {
			return o, false
		}
		o.ClientID = parts[4]
		if parts[5] == "connected" {
			var body struct {
				IP          string `json:"ipaddress"`
				ConnectedAt int64  `json:"connected_at"`
			}
			_ = json.Unmarshal(payload, &body)
			return presence.Connected(o.ClientID, body.IP, time.UnixMilli(body.ConnectedAt), now)
		}
		o.Offline = true
	} else {
		return o, false
	}
	return o, presence.ValidClientID(o.ClientID)
}
