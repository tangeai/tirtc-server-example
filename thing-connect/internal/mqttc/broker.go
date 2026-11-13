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
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/redis/go-redis/v9"
	"thing-connect/internal/config"
)

var (
	ErrSubscribeFailed = errors.New("mqttc: subscribe failed")
	ErrPublishFailed   = errors.New("mqttc: publish failed")
	ErrPublishTimeout  = errors.New("mqttc: publish timeout")
	ErrACKTimeout      = errors.New("mqttc: ack timeout")
)

// onlineReader deliberately excludes writes: MQTT command clients do not own
// the online projection. The Redis adapter is injected only for compatibility
// with existing online queries.
type onlineReader interface {
	Get(context.Context, string) *redis.StringCmd
}

type Broker struct {
	client mqtt.Client
	cfg    config.MQTTCfg
	rdb    onlineReader
	mu     sync.Mutex
	// pending ACK channels: topic → chan struct{}
	ackCh           map[string]chan struct{}
	ready           atomic.Bool
	readyGeneration atomic.Uint64
	presenceHandler mqtt.MessageHandler
	cancel          context.CancelFunc
	recoveryDone    chan struct{}
	reconnect       chan struct{}
	generation      atomic.Uint64
	initialized     chan struct{}
	initializeOnce  sync.Once
	closeOnce       sync.Once
}

func New(cfg config.MQTTCfg, rdb *redis.Client) (*Broker, error) {
	return newBroker(cfg, rdb, nil)
}

func newBroker(cfg config.MQTTCfg, rdb *redis.Client, handler mqtt.MessageHandler) (*Broker, error) {
	b := &Broker{cfg: cfg, rdb: rdb, ackCh: make(map[string]chan struct{}), presenceHandler: handler}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	b.recoveryDone = make(chan struct{})
	b.reconnect = make(chan struct{}, 1)
	b.initialized = make(chan struct{})

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
	opts.SetConnectTimeout(5 * time.Second)
	opts.SetAutoReconnect(true)
	opts.SetCleanSession(false)

	// TLS for mqtts://
	if strings.HasPrefix(cfg.Broker, "mqtts://") || strings.HasPrefix(cfg.Broker, "ssl://") {
		opts.SetTLSConfig(&tls.Config{InsecureSkipVerify: false})
	}

	opts.SetOnConnectHandler(func(_ mqtt.Client) { b.connectionChanged() })
	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		b.connectionChanged()
		slog.Warn("mqttc connection lost", "err", err)
	})
	opts.SetDefaultPublishHandler(b.handleMessage)

	b.client = mqtt.NewClient(opts)
	go b.recoverSubscriptions(ctx)
	tok := b.client.Connect()
	if !tok.WaitTimeout(10 * time.Second) {
		b.Close()
		return nil, fmt.Errorf("mqttc: connect timeout")
	}
	if err := tok.Error(); err != nil {
		b.Close()
		return nil, fmt.Errorf("mqttc: connect: %w", err)
	}
	select {
	case <-b.initialized:
	case <-time.After(10 * time.Second):
		b.Close()
		return nil, errors.New("mqttc: subscription setup timeout")
	}
	return b, nil
}

// Probe checks broker reachability and authentication without subscriptions,
// Redis writes or a persistent session. It is intended for an explicit
// server-side startup preflight while the target service is stopped.
func Probe(ctx context.Context, cfg config.MQTTCfg) error {
	return probe(ctx, cfg, false)
}

// ProbePresence verifies the required SUBACK grants in a clean temporary
// session. It discards received messages and never updates online state.
func ProbePresence(ctx context.Context, cfg config.MQTTCfg) error {
	return probe(ctx, cfg, true)
}

func probe(ctx context.Context, cfg config.MQTTCfg, presence bool) error {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(cfg.Broker)
	hostname, _ := os.Hostname()
	clientID, username := cfg.ClientID, cfg.ClientID
	if cfg.AuthMode() == "username" {
		username = cfg.Username
		clientID = fmt.Sprintf("%s-config-check-%s-%d", cfg.Username, hostname, os.Getpid())
	}
	opts.SetClientID(clientID)
	opts.SetUsername(username)
	opts.SetPassword(cfg.Password)
	opts.SetCleanSession(true)
	opts.SetAutoReconnect(false)
	opts.SetConnectTimeout(10 * time.Second)
	if strings.HasPrefix(cfg.Broker, "mqtts://") || strings.HasPrefix(cfg.Broker, "ssl://") {
		opts.SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	client := mqtt.NewClient(opts)
	token := client.Connect()
	wait := 10 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < wait {
			wait = remaining
		}
	}
	if wait <= 0 || !token.WaitTimeout(wait) {
		return errors.New("mqttc: connection check timeout")
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("mqttc: connection check failed: %w", err)
	}
	defer client.Disconnect(100)
	if presence {
		b := &Broker{presenceHandler: func(mqtt.Client, mqtt.Message) {}}
		return b.configureSubscriptionsContext(ctx, client)
	}
	return nil
}

var presenceTopics = []string{
	"$SYS/brokers/+/clients/+/connected",
	"$SYS/brokers/+/clients/+/disconnected",
	"device/+/up",
}

func (b *Broker) configureSubscriptions(client mqtt.Client) error {
	return b.configureSubscriptionsContext(context.Background(), client)
}

func (b *Broker) configureSubscriptionsContext(ctx context.Context, client mqtt.Client) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var token mqtt.Token
	if b.presenceHandler == nil {
		// CleanSession=false retains subscriptions across upgrades. Every reconnect
		// must explicitly remove the legacy presence subscriptions.
		token = client.Unsubscribe(presenceTopics...)
	} else {
		topics := make(map[string]byte, len(presenceTopics))
		for _, topic := range presenceTopics {
			topics[topic] = 0
		}
		token = client.SubscribeMultiple(topics, b.presenceHandler)
	}
	wait := 5 * time.Second
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < wait {
		wait = time.Until(deadline)
	}
	if wait <= 0 || !token.WaitTimeout(wait) {
		return errors.New("mqttc: subscription setup timeout")
	}
	if err := token.Error(); err != nil {
		return err
	}
	if result, ok := token.(interface{ Result() map[string]byte }); ok {
		for _, topic := range presenceTopics {
			qos, exists := result.Result()[topic]
			if !exists || qos == 0x80 {
				return fmt.Errorf("mqttc: presence subscription rejected: topic=%s granted=%t code=0x%02x", topic, exists, qos)
			}
		}
	}
	return nil
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

// Ping reports whether this service instance currently has an open MQTT
// connection. It matches servicestatus.DependencyProbe without coupling the
// MQTT package to the status package.
func (b *Broker) Ping(context.Context) error {
	if b == nil || b.client == nil || !b.client.IsConnectionOpen() || !b.ready.Load() || b.readyGeneration.Load() != b.generation.Load() {
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

// Close disconnects from the MQTT broker, waiting up to 500ms for in-flight
// messages to complete. Safe to call multiple times.
func (b *Broker) Close() {
	b.closeOnce.Do(func() {
		if b.cancel != nil {
			b.cancel()
		}
		if b.client != nil {
			b.client.Disconnect(500)
		}
		if b.recoveryDone != nil {
			<-b.recoveryDone
		}
		b.ready.Store(false)
	})
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

// Connected reports whether the current MQTT transport is available.
func (b *Broker) Connected() bool { return b.client != nil && b.client.IsConnectionOpen() }

func (b *Broker) connectionChanged() {
	b.generation.Add(1)
	b.ready.Store(false)
	select {
	case b.reconnect <- struct{}{}:
	default:
	}
}

// One lifecycle-owned worker retries subscriptions even while the transport
// stays connected. An old connection's completion cannot mark a new one ready.
func (b *Broker) recoverSubscriptions(ctx context.Context) {
	defer close(b.recoveryDone)
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.reconnect:
		}
		delay := time.Second
		for b.client.IsConnectionOpen() && ctx.Err() == nil {
			generation := b.generation.Load()
			err := b.configureSubscriptions(b.client)
			if ctx.Err() != nil {
				return
			}
			if generation != b.generation.Load() {
				continue
			}
			if err == nil && b.client.IsConnectionOpen() {
				b.readyGeneration.Store(generation)
				b.ready.Store(true)
				b.initializeOnce.Do(func() { close(b.initialized) })
				break
			}
			b.ready.Store(false)
			slog.Warn("mqttc subscription setup failed; retrying", "err", err, "retry_in", delay)
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-b.reconnect:
				timer.Stop()
				delay = time.Second
			case <-timer.C:
				if delay < 30*time.Second {
					delay *= 2
					if delay > 30*time.Second {
						delay = 30 * time.Second
					}
				}
			}
		}
	}
}
