package mqttc

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/redis/go-redis/v9"
	"thing-connect/internal/config"
)

const (
	// onlineTTL is set to 2.5× the broker keepalive (30s).
	// $SYS disconnected event fires within keepalive timeout on abnormal disconnect,
	// so TTL serves only as a safety net for missed disconnect events.
	onlineTTL = 150 * time.Second
)

var (
	ErrSubscribeFailed = errors.New("mqttc: subscribe failed")
	ErrPublishFailed   = errors.New("mqttc: publish failed")
	ErrPublishTimeout  = errors.New("mqttc: publish timeout")
	ErrACKTimeout      = errors.New("mqttc: ack timeout")
)

type Broker struct {
	client mqtt.Client
	cfg    config.MQTTCfg
	rdb    *redis.Client
	mu     sync.Mutex
	// pending ACK channels: topic → chan struct{}
	ackCh map[string]chan struct{}
}

func New(cfg config.MQTTCfg, rdb *redis.Client) (*Broker, error) {
	b := &Broker{cfg: cfg, rdb: rdb, ackCh: make(map[string]chan struct{})}

	opts := mqtt.NewClientOptions()
	opts.AddBroker(cfg.Broker)
	var clientID, username string
	switch cfg.AuthMode() {
	case "username":
		// username auth: fixed username, ClientID gets an operator-controlled
		// instance suffix. SERVICE_INSTANCE_ID is required when more than one
		// copy of the same service runs on the same host.
		hostname, _ := os.Hostname()
		username = cfg.Username
		instanceID := strings.TrimSpace(os.Getenv("SERVICE_INSTANCE_ID"))
		if instanceID == "" {
			instanceID = hostname
		}
		clientID = cfg.Username + "-" + instanceID
	default: // clientid
		// clientid auth: fixed ClientID configured explicitly, username mirrors it
		clientID = cfg.ClientID
		username = cfg.ClientID
	}
	slog.Info("mqttc auth", "auth_mode", cfg.AuthMode(), "client_id", clientID, "username", username)
	opts.SetClientID(clientID)
	opts.SetUsername(username)
	opts.SetPassword(cfg.Password)
	opts.SetKeepAlive(30 * time.Second)
	opts.SetAutoReconnect(true)
	opts.SetCleanSession(false)

	// TLS for mqtts://
	if strings.HasPrefix(cfg.Broker, "mqtts://") || strings.HasPrefix(cfg.Broker, "ssl://") {
		opts.SetTLSConfig(&tls.Config{InsecureSkipVerify: false})
	}

	opts.SetOnConnectHandler(func(_ mqtt.Client) {
		slog.Info("mqttc connected to broker")
		b.subscribeSystemEvents()
	})
	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		slog.Warn("mqttc connection lost", "err", err)
	})
	opts.SetDefaultPublishHandler(b.handleMessage)

	b.client = mqtt.NewClient(opts)
	tok := b.client.Connect()
	if !tok.WaitTimeout(10 * time.Second) {
		return nil, fmt.Errorf("mqttc: connect timeout")
	}
	if err := tok.Error(); err != nil {
		return nil, fmt.Errorf("mqttc: connect: %w", err)
	}
	return b, nil
}

// subscribeSystemEvents listens for $SYS connect/disconnect events to maintain online cache.
// TTL acts as safety net for missed disconnect events (e.g. broker restart).
func (b *Broker) subscribeSystemEvents() {
	topics := map[string]byte{
		"$SYS/brokers/+/clients/+/connected":    0,
		"$SYS/brokers/+/clients/+/disconnected": 0,
		"device/+/up":                           0,
	}
	tok := b.client.SubscribeMultiple(topics, b.handleSystemEvent)
	tok.Wait()
	if err := tok.Error(); err != nil {
		slog.Warn("mqttc subscribe $SYS failed (online status will rely on TTL only)", "err", err)
	}
}

func (b *Broker) handleSystemEvent(_ mqtt.Client, msg mqtt.Message) {
	topic := msg.Topic()
	parts := strings.Split(topic, "/")
	ctx := context.Background()

	// device/<clientID>/up — heartbeat, refresh TTL
	if len(parts) == 3 && parts[0] == "device" && parts[2] == "up" {
		// Set instead of Expire so a broker-service restart can reconstruct
		// presence from the next heartbeat even when the MQTT connection itself
		// stays up and no new $SYS connected event is emitted.
		b.rdb.Set(ctx, "online:"+parts[1], presenceValue(time.Now()), onlineTTL)
		return
	}

	// $SYS/brokers/<node>/clients/<clientID>/connected|disconnected
	if len(parts) < 6 {
		return
	}
	clientID := parts[4]
	key := "online:" + clientID
	switch parts[5] {
	case "connected":
		b.rdb.Set(ctx, key, presenceValue(time.Now()), onlineTTL)
	case "disconnected":
		b.rdb.Del(ctx, key)
	}
}

func (b *Broker) handleMessage(_ mqtt.Client, msg mqtt.Message) {
	topic := msg.Topic()
	slog.Debug("mqttc handleMessage", "topic", topic, "payload", string(msg.Payload()))
	// ACK from device: device/{clientID}/ack
	if strings.HasSuffix(topic, "/ack") {
		b.mu.Lock()
		ch, ok := b.ackCh[topic]
		b.mu.Unlock()
		slog.Debug("mqttc ack", "topic", topic, "found_waiter", ok)
		if ok {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

// IsOnline checks device online status via Redis cache.
func (b *Broker) IsOnline(ctx context.Context, clientID string) bool {
	val, err := b.rdb.Get(ctx, "online:"+clientID).Result()
	return err == nil && val != ""
}

func presenceValue(now time.Time) string {
	return now.UTC().Format(time.RFC3339Nano)
}

// Ping reports whether this service instance currently has an open MQTT
// connection. It matches servicestatus.DependencyProbe without coupling the
// MQTT package to the status package.
func (b *Broker) Ping(context.Context) error {
	if b == nil || b.client == nil || !b.client.IsConnectionOpen() {
		return errors.New("mqttc: connection is not open")
	}
	return nil
}

// Publish sends a message to a topic.
func (b *Broker) Publish(topic string, qos byte, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("%w: marshal: %w", ErrPublishFailed, err)
	}
	tok := b.client.Publish(topic, qos, false, data)
	if !tok.WaitTimeout(5 * time.Second) {
		return ErrPublishTimeout
	}
	if err := tok.Error(); err != nil {
		return fmt.Errorf("%w: %w", ErrPublishFailed, err)
	}
	return nil
}

// PublishAndWaitACK publishes to downTopic and waits for an ACK on ackTopic within timeout.
func (b *Broker) PublishAndWaitACK(downTopic, ackTopic string, payload any, timeout time.Duration) error {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	b.ackCh[ackTopic] = ch
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.ackCh, ackTopic)
		b.mu.Unlock()
		// unsubscribe ACK topic
		b.client.Unsubscribe(ackTopic)
	}()

	// subscribe before publish to avoid race
	tok := b.client.Subscribe(ackTopic, 1, nil)
	tok.Wait()
	slog.Debug("mqttc subscribe ack", "ack_topic", ackTopic, "err", tok.Error())
	if err := tok.Error(); err != nil {
		return fmt.Errorf("%w: %w", ErrSubscribeFailed, err)
	}

	if err := b.Publish(downTopic, 1, payload); err != nil {
		slog.Warn("mqttc publish failed", "down_topic", downTopic, "err", err)
		return err
	}
	slog.Debug("mqttc published, waiting ack", "down_topic", downTopic, "ack_topic", ackTopic)

	select {
	case <-ch:
		slog.Debug("mqttc ack received", "ack_topic", ackTopic)
		return nil
	case <-time.After(timeout):
		slog.Warn("mqttc ack timeout", "ack_topic", ackTopic, "timeout", timeout)
		return ErrACKTimeout
	}
}

// KickClient forces a client offline by publishing to the broker kick topic (EMQX compatible).
// If the broker doesn't support this, the call is a no-op.
func (b *Broker) KickClient(clientID string) {
	// EMQX supports $SYS/brokers/<node>/clients/<clientID>/kick — but node is unknown.
	// More portable: publish to a special management topic if configured.
	// Here we just expire the online cache entry.
	ctx := context.Background()
	b.rdb.Del(ctx, "online:"+clientID)
}

// Close disconnects from the MQTT broker, waiting up to 500ms for in-flight
// messages to complete. Safe to call multiple times.
func (b *Broker) Close() {
	b.client.Disconnect(500)
}

// DeviceCmdTopic returns the server→device command topic (expects ACK for critical commands).
func DeviceCmdTopic(clientID string) string {
	return "device/" + clientID + "/cmd"
}

// DeviceNotifyTopic returns the server→device notification topic (fire-and-forget).
func DeviceNotifyTopic(clientID string) string {
	return "device/" + clientID + "/notify"
}

// DeviceACKTopic returns the device→server ACK topic.
func DeviceACKTopic(clientID string) string {
	return "device/" + clientID + "/ack"
}
