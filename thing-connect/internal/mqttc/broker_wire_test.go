package mqttc

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/eclipse/paho.mqtt.golang/packets"
	"thing-connect/internal/config"
)

// Exercise the real Paho client over MQTT 3.1.1 wire packets. The fixture
// starts with a persistent session, as on an upgrade from the old consumer.
func TestPublisherClearsPersistentSubscriptionsAndStillReceivesACK(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverErr := make(chan error, 1)
	unsubscribed := make(chan []string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		for {
			p, err := packets.ReadPacket(conn)
			if err != nil {
				serverErr <- err
				return
			}
			var reply packets.ControlPacket
			switch msg := p.(type) {
			case *packets.ConnectPacket:
				ack := packets.NewControlPacket(packets.Connack).(*packets.ConnackPacket)
				ack.SessionPresent = true
				reply = ack
			case *packets.UnsubscribePacket:
				select {
				case unsubscribed <- msg.Topics:
				default:
				}
				ack := packets.NewControlPacket(packets.Unsuback).(*packets.UnsubackPacket)
				ack.MessageID = msg.MessageID
				reply = ack
			case *packets.SubscribePacket:
				ack := packets.NewControlPacket(packets.Suback).(*packets.SubackPacket)
				ack.MessageID = msg.MessageID
				ack.ReturnCodes = []byte{1}
				reply = ack
			case *packets.PublishPacket:
				if msg.Qos == 1 {
					ack := packets.NewControlPacket(packets.Puback).(*packets.PubackPacket)
					ack.MessageID = msg.MessageID
					if err := ack.Write(conn); err != nil {
						serverErr <- err
						return
					}
				}
				ack := packets.NewControlPacket(packets.Publish).(*packets.PublishPacket)
				ack.TopicName = "device/sn_test/ack"
				ack.Payload = []byte(`{"ack":true}`)
				reply = ack
			case *packets.DisconnectPacket:
				serverErr <- nil
				return
			case *packets.PingreqPacket:
				reply = packets.NewControlPacket(packets.Pingresp)
			}
			if reply != nil {
				if err := reply.Write(conn); err != nil {
					serverErr <- err
					return
				}
			}
		}
	}()
	broker, err := New(config.MQTTCfg{Broker: "tcp://" + listener.Addr().String(), ClientID: "wire-test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	select {
	case topics := <-unsubscribed:
		if len(topics) != 3 {
			t.Fatalf("legacy topics=%v", topics)
		}
	case <-time.After(time.Second):
		t.Fatal("legacy subscriptions not removed")
	}
	if err := broker.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := broker.PublishAndWaitACK("device/sn_test/cmd", "device/sn_test/ack", map[string]string{"type": "test"}, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestPresencePreflightRejectsMissingSubscriptionPermission(t *testing.T) {
	for _, denied := range append([]string{""}, presenceTopics...) {
		t.Run("denied="+denied, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				for {
					packet, err := packets.ReadPacket(conn)
					if err != nil {
						done <- err
						return
					}
					var reply packets.ControlPacket
					switch msg := packet.(type) {
					case *packets.ConnectPacket:
						if !msg.CleanSession {
							done <- fmt.Errorf("probe retained persistent subscriptions")
							return
						}
						reply = packets.NewControlPacket(packets.Connack)
					case *packets.SubscribePacket:
						if len(msg.Topics) != 3 {
							done <- fmt.Errorf("required topics missing")
							return
						}
						ack := packets.NewControlPacket(packets.Suback).(*packets.SubackPacket)
						ack.MessageID = msg.MessageID
						for _, topic := range msg.Topics {
							code := byte(0)
							if topic == denied {
								code = 0x80
							}
							ack.ReturnCodes = append(ack.ReturnCodes, code)
						}
						reply = ack
					case *packets.DisconnectPacket:
						done <- nil
						return
					default:
						done <- fmt.Errorf("unexpected probe packet %T", packet)
						return
					}
					if err := reply.Write(conn); err != nil {
						done <- err
						return
					}
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err = ProbePresence(ctx, config.MQTTCfg{Broker: "tcp://" + listener.Addr().String(), ClientID: "presence-preflight-fixture"})
			if denied == "" && err != nil {
				t.Fatal(err)
			}
			if denied != "" && (err == nil || !strings.Contains(err.Error(), denied)) {
				t.Fatalf("denied topic was not diagnosed: %v", err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("probe did not disconnect")
			}
		})
	}
}
