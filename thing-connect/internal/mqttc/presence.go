package mqttc

import (
	"context"
	"encoding/json"
	"net/netip"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"thing-connect/internal/config"
	"thing-connect/internal/presence"
)

type PresenceObserver interface{ Observe(presence.Observation) }

// NewPresence is only used by device-server. Command clients use New and
// explicitly unsubscribe from the former presence subscriptions.
type PresenceSource interface {
	Ping(context.Context) error
	Close()
}

func NewPresence(cfg config.MQTTCfg, observer PresenceObserver) (PresenceSource, error) {
	return newBroker(cfg, nil, func(_ mqtt.Client, msg mqtt.Message) {
		if o, ok := presenceObservation(msg.Topic(), msg.Payload(), msg.Retained(), time.Now()); ok {
			observer.Observe(o)
		}
	})
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
		o.Offline = parts[5] == "disconnected"
		if !o.Offline && strings.HasPrefix(o.ClientID, "sn_") {
			var body struct {
				IP          string `json:"ipaddress"`
				ConnectedAt int64  `json:"connected_at"`
			}
			if json.Unmarshal(payload, &body) == nil && body.ConnectedAt > 0 && body.ConnectedAt <= now.Add(time.Minute).UnixMilli() {
				if ip, err := netip.ParseAddr(body.IP); err == nil {
					o.IP = ip.WithZone("").Unmap().String()
				}
				o.ConnectedAt = time.UnixMilli(body.ConnectedAt).UTC()
			}
		}
	} else {
		return o, false
	}
	return o, (strings.HasPrefix(o.ClientID, "sn_") && len(o.ClientID) > 3 && len(o.ClientID) <= 67) || (strings.HasPrefix(o.ClientID, "tmp_") && len(o.ClientID) > 4 && len(o.ClientID) <= 68)
}
