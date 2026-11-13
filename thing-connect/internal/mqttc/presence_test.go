package mqttc

import (
	"errors"
	"fmt"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type subscriptionToken struct {
	mqtt.Token
	err     error
	timeout bool
}

func (t subscriptionToken) WaitTimeout(time.Duration) bool { return !t.timeout }
func (t subscriptionToken) Error() error                   { return t.err }

type subscriptionClient struct {
	mqtt.Client
	subscribed, unsubscribed []string
	token                    mqtt.Token
}

func (c *subscriptionClient) SubscribeMultiple(topics map[string]byte, _ mqtt.MessageHandler) mqtt.Token {
	for k := range topics {
		c.subscribed = append(c.subscribed, k)
	}
	return c.token
}
func (c *subscriptionClient) Unsubscribe(topics ...string) mqtt.Token {
	c.unsubscribed = append(c.unsubscribed, topics...)
	return c.token
}
func TestPresenceSubscriptionOwnership(t *testing.T) {
	for _, source := range []bool{false, true} {
		client := &subscriptionClient{token: subscriptionToken{}}
		b := &Broker{}
		if source {
			b.presenceHandler = func(mqtt.Client, mqtt.Message) {}
		}
		if err := b.configureSubscriptions(client); err != nil {
			t.Fatal(err)
		}
		if source && (len(client.subscribed) != 3 || len(client.unsubscribed) != 0) {
			t.Fatal("source subscriptions missing")
		}
		if !source && (len(client.unsubscribed) != 3 || len(client.subscribed) != 0) {
			t.Fatal("publisher retained legacy subscriptions")
		}
	}
	for _, token := range []mqtt.Token{subscriptionToken{timeout: true}, subscriptionToken{err: errors.New("rejected")}} {
		if (&Broker{}).configureSubscriptions(&subscriptionClient{token: token}) == nil {
			t.Fatal("failure hidden")
		}
	}
}
func TestPresenceParsing(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		topic, payload          string
		retained, want, offline bool
	}{
		{"device/sn_a/up", `{"type":"heartbeat"}`, false, true, false},
		{"device/sn_a/up", `{"type":"heartbeat"}`, true, false, false},
		{"device/sn_a/up", `broken`, false, false, false},
		{"device/sn_a/ack", `{"ack":true}`, false, false, false},
		{"$SYS/brokers/node/clients/tmp_a/connected", `{}`, false, true, false},
		{"$SYS/brokers/node/clients/sn_a/disconnected", `{}`, false, true, true},
		{"$SYS/brokers/node/clients/service/connected", `{}`, false, false, false},
	} {
		got, ok := presenceObservation(tc.topic, []byte(tc.payload), tc.retained, now)
		if ok != tc.want || ok && (got.Offline != tc.offline || got.At != now) {
			t.Fatalf("%s: %+v accepted=%v", tc.topic, got, ok)
		}
	}
}

func TestConnectedIPUsesBrokerPayloadOnly(t *testing.T) {
	now := time.Now()
	o, ok := presenceObservation("$SYS/brokers/node/clients/sn_a/connected", []byte(`{"ipaddress":"2001:db8::1","connected_at":1700000000123}`), false, now)
	if !ok || o.IP != "2001:db8::1" || o.ConnectedAt.UnixMilli() != 1700000000123 {
		t.Fatalf("connection metadata=%+v", o)
	}
	o, ok = presenceObservation("device/sn_a/up", []byte(`{"type":"heartbeat","ipaddress":"192.0.2.9"}`), false, now)
	if !ok || o.IP != "" {
		t.Fatal("accepted device supplied IP")
	}
	o, ok = presenceObservation("$SYS/brokers/node/clients/sn_a/connected", []byte(`{"ipaddress":"not an address"}`), false, now)
	if !ok || o.IP != "" {
		t.Fatal("invalid IP persisted")
	}
}

func TestUnreliableConnectionTimeCannotSupplyIP(t *testing.T) {
	now := time.Now()
	for _, stamp := range []string{"", `,"connected_at":0`, `,"connected_at":-1`, fmt.Sprintf(`,"connected_at":%d`, now.Add(2*time.Minute).UnixMilli())} {
		o, ok := presenceObservation("$SYS/brokers/node/clients/sn_a/connected", []byte(`{"ipaddress":"192.0.2.1"`+stamp+`}`), false, now)
		if !ok || o.At != now {
			t.Fatal("lost receipt-time online observation")
		}
		if o.IP != "" || !o.ConnectedAt.IsZero() {
			t.Fatal("unreliable event could overwrite latest IP")
		}
	}
}
