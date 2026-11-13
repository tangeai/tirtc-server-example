package mqttc

import (
	"context"
	"errors"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"sync/atomic"
	"testing"
	"time"
)

type recoveryClient struct {
	mqtt.Client
	open  atomic.Bool
	calls atomic.Int64
	fail  bool
}

func (c *recoveryClient) IsConnectionOpen() bool { return c.open.Load() }
func (c *recoveryClient) Disconnect(uint)        { c.open.Store(false) }
func (c *recoveryClient) operation() mqtt.Token {
	n := c.calls.Add(1)
	if n == 1 || c.fail {
		return subscriptionToken{err: errors.New("temporary subscription failure")}
	}
	return subscriptionToken{}
}
func (c *recoveryClient) SubscribeMultiple(map[string]byte, mqtt.MessageHandler) mqtt.Token {
	return c.operation()
}
func (c *recoveryClient) Unsubscribe(...string) mqtt.Token { return c.operation() }
func recoveryBroker(client *recoveryClient, source bool) *Broker {
	ctx, cancel := context.WithCancel(context.Background())
	b := &Broker{client: client, cancel: cancel, recoveryDone: make(chan struct{}), reconnect: make(chan struct{}, 1), initialized: make(chan struct{})}
	if source {
		b.presenceHandler = func(mqtt.Client, mqtt.Message) {}
	}
	go b.recoverSubscriptions(ctx)
	return b
}
func TestSubscriptionRecoveryWithoutTransportDisconnect(t *testing.T) {
	for _, source := range []bool{false, true} {
		c := &recoveryClient{}
		c.open.Store(true)
		b := recoveryBroker(c, source)
		b.connectionChanged()
		select {
		case <-b.initialized:
		case <-time.After(3 * time.Second):
			b.Close()
			t.Fatal("subscription never recovered")
		}
		if c.calls.Load() < 2 || b.Ping(context.Background()) != nil {
			t.Fatal("retry did not restore readiness")
		}
		b.Close()
		if b.Ping(context.Background()) == nil {
			t.Fatal("closed broker ready")
		}
	}
}
func TestSubscriptionRecoveryStopsDuringBackoff(t *testing.T) {
	c := &recoveryClient{fail: true}
	c.open.Store(true)
	b := recoveryBroker(c, true)
	b.connectionChanged()
	deadline := time.Now().Add(time.Second)
	for c.calls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no subscription attempt")
		}
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	b.Close()
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("close waited for retry timer")
	}
}

func TestOldConnectionReadinessCannotAuthorizeNewConnection(t *testing.T) {
	c := &recoveryClient{}
	c.open.Store(true)
	b := &Broker{client: c}
	b.ready.Store(true)
	b.readyGeneration.Store(1)
	b.generation.Store(2)
	if b.Ping(context.Background()) == nil {
		t.Fatal("late subscription completion marked a different connection ready")
	}
}
