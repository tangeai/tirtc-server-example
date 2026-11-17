package mqttc

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/eclipse/paho.mqtt.golang/packets"
	"thing-connect/internal/config"
)

const ackFixtureTopic = "device/sn_ack/ack"

// ackFixture is a minimal MQTT 3.1.1 broker. It records ACK-topic
// subscription changes and command publishes, and sends a device ACK only
// when the test calls sendACK.
type ackFixture struct {
	t        *testing.T
	listener net.Listener
	writeMu  sync.Mutex
	conn     net.Conn
	connOnce chan struct{}
	events   chan string
}

func newACKFixture(t *testing.T) *ackFixture {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &ackFixture{t: t, listener: listener, connOnce: make(chan struct{}), events: make(chan string, 64)}
	t.Cleanup(func() { _ = listener.Close() })
	go f.serve()
	return f
}

func (f *ackFixture) write(p packets.ControlPacket) {
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	_ = p.Write(f.conn)
}

func (f *ackFixture) serve() {
	conn, err := f.listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	f.conn = conn
	close(f.connOnce)
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	for {
		p, err := packets.ReadPacket(conn)
		if err != nil {
			return
		}
		switch msg := p.(type) {
		case *packets.ConnectPacket:
			f.write(packets.NewControlPacket(packets.Connack))
		case *packets.SubscribePacket:
			for _, topic := range msg.Topics {
				if topic == ackFixtureTopic {
					f.events <- "subscribe"
				}
			}
			ack := packets.NewControlPacket(packets.Suback).(*packets.SubackPacket)
			ack.MessageID = msg.MessageID
			for range msg.Topics {
				ack.ReturnCodes = append(ack.ReturnCodes, 1)
			}
			f.write(ack)
		case *packets.UnsubscribePacket:
			for _, topic := range msg.Topics {
				if topic == ackFixtureTopic {
					f.events <- "unsubscribe"
				}
			}
			ack := packets.NewControlPacket(packets.Unsuback).(*packets.UnsubackPacket)
			ack.MessageID = msg.MessageID
			f.write(ack)
		case *packets.PublishPacket:
			if msg.Qos == 1 {
				ack := packets.NewControlPacket(packets.Puback).(*packets.PubackPacket)
				ack.MessageID = msg.MessageID
				f.write(ack)
			}
			f.events <- "publish"
		case *packets.PingreqPacket:
			f.write(packets.NewControlPacket(packets.Pingresp))
		case *packets.DisconnectPacket:
			return
		}
	}
}

func (f *ackFixture) sendACK() {
	<-f.connOnce
	p := packets.NewControlPacket(packets.Publish).(*packets.PublishPacket)
	p.TopicName = ackFixtureTopic
	p.Payload = []byte(`{"ack":true}`)
	f.write(p)
}

func (f *ackFixture) expect(want string) {
	f.t.Helper()
	select {
	case got := <-f.events:
		if got != want {
			f.t.Fatalf("event=%s, want %s", got, want)
		}
	case <-time.After(3 * time.Second):
		f.t.Fatalf("missing %s", want)
	}
}

func (f *ackFixture) expectNone(d time.Duration) {
	f.t.Helper()
	select {
	case got := <-f.events:
		f.t.Fatalf("unexpected %s", got)
	case <-time.After(d):
	}
}

func (f *ackFixture) broker() *Broker {
	f.t.Helper()
	b, err := New(config.MQTTCfg{Broker: "tcp://" + f.listener.Addr().String(), ClientID: "ack-test"}, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(b.Close)
	return b
}

func TestConcurrentACKWaitersShareOneDeviceACK(t *testing.T) {
	f := newACKFixture(t)
	b := f.broker()
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			results <- b.PublishAndWaitACK("device/sn_ack/cmd", ackFixtureTopic, map[string]string{"type": "test"}, 3*time.Second)
		}()
	}
	published := 0
	for published < 2 {
		select {
		case event := <-f.events:
			if event == "publish" {
				published++
			} else if event != "subscribe" {
				t.Fatalf("unexpected %s before both commands were sent", event)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("commands not published")
		}
	}
	f.sendACK()
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatalf("waiter %d: %v", i, err)
		}
	}
	f.expect("unsubscribe")
}

func TestACKWaiterTimeoutKeepsSubscriptionForRemainingWaiter(t *testing.T) {
	f := newACKFixture(t)
	b := f.broker()

	long := make(chan error, 1)
	go func() {
		long <- b.PublishAndWaitACK("device/sn_ack/cmd", ackFixtureTopic, map[string]string{"type": "long"}, 5*time.Second)
	}()
	f.expect("subscribe")
	f.expect("publish")

	if err := b.PublishAndWaitACK("device/sn_ack/cmd", ackFixtureTopic, map[string]string{"type": "short"}, 200*time.Millisecond); !errors.Is(err, ErrACKTimeout) {
		t.Fatalf("short waiter err=%v, want ErrACKTimeout", err)
	}
	f.expect("subscribe")
	f.expect("publish")
	f.expectNone(300 * time.Millisecond)

	f.sendACK()
	if err := <-long; err != nil {
		t.Fatalf("remaining waiter: %v", err)
	}
	f.expect("unsubscribe")

	b.mu.Lock()
	remaining := len(b.ackWaiters)
	b.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("ack waiter topics left=%d, want 0", remaining)
	}
}
