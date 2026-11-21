// Command presence-load measures the device presence pipeline: simulated
// devices connect to EMQX and send heartbeats, K in-process consumers run the
// same shared-subscription projector as device-server, and the tool samples
// how many devices are online in Redis. Run it against a test broker and a
// dedicated Redis database only; it deletes that database's presence keys.
package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"math/big"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"

	"thing-connect/internal/brokerapi"
	"thing-connect/internal/cache"
	"thing-connect/internal/config"
	"thing-connect/internal/mqttc"
	"thing-connect/internal/presence"
)

type options struct {
	broker      string
	devices     int
	prefix      string
	connectRate int
	heartbeat   time.Duration
	duration    time.Duration
	jwtSecret   string
	consumers   int
	redisAddr   string
	redisDB     int
	apiURL      string
	apiKey      string
	apiSecret   string
}

func (o options) brokers() []string {
	var result []string
	for _, b := range strings.Split(o.broker, ",") {
		if b = strings.TrimSpace(b); b != "" {
			result = append(result, b)
		}
	}
	if len(result) == 0 {
		log.Fatal("broker must not be empty")
	}
	return result
}

func main() {
	var o options
	flag.StringVar(&o.broker, "broker", "tcp://127.0.0.1:1885", "test MQTT broker; comma-separated addresses spread devices past the per-address port limit, consumers use the first")
	flag.IntVar(&o.devices, "devices", 5000, "simulated formal devices")
	flag.StringVar(&o.prefix, "prefix", "load-", "device ID prefix")
	flag.IntVar(&o.connectRate, "connect-rate", 500, "initial connections per second")
	flag.DurationVar(&o.heartbeat, "heartbeat", 30*time.Second, "heartbeat interval")
	flag.DurationVar(&o.duration, "duration", 3*time.Minute, "run time after all devices started")
	flag.StringVar(&o.jwtSecret, "jwt-secret", "", "sign device passwords like device-server (empty: no password)")
	flag.IntVar(&o.consumers, "consumers", 2, "in-process Device presence consumers (0: devices only)")
	flag.StringVar(&o.redisAddr, "redis", "127.0.0.1:6379", "Redis address")
	flag.IntVar(&o.redisDB, "redis-db", 15, "dedicated Redis database; must not be 0")
	flag.StringVar(&o.apiURL, "broker-api", "", "EMQX HTTP API for reconciliation (optional)")
	flag.StringVar(&o.apiKey, "broker-api-key", "", "EMQX API key")
	flag.StringVar(&o.apiSecret, "broker-api-secret", "", "EMQX API secret")
	flag.Parse()
	if o.redisDB == 0 {
		log.Fatal("redis-db must be a dedicated non-zero database")
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	rdb := redis.NewClient(&redis.Options{Addr: o.redisAddr, DB: o.redisDB})
	defer func() { _ = rdb.Close() }()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("redis: %v", err)
	}
	defer cleanup(rdb, o)

	consumers, err := startConsumers(ctx, o)
	if err != nil {
		log.Fatalf("consumers: %v", err)
	}
	defer func() {
		for _, c := range consumers {
			c.close()
		}
	}()

	fleet := &fleet{opts: o}
	go fleet.run(ctx)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	started := time.Now()
	deadline := started.Add(time.Duration(o.devices/max(o.connectRate, 1))*time.Second + o.duration)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		fleet.report(ctx, rdb, consumers, time.Since(started))
		if time.Now().After(deadline) {
			return
		}
	}
}

type consumer struct {
	projector *presence.Projector
	source    mqttc.PresenceSource
	cancel    context.CancelFunc
	done      sync.WaitGroup
	redis     *redis.Client
}

func (c *consumer) close() {
	c.source.Close()
	c.cancel()
	c.done.Wait()
	_ = c.redis.Close()
}

func startConsumers(ctx context.Context, o options) ([]*consumer, error) {
	var api brokerapi.Source
	if err := api.Update(brokerapi.Config{URL: o.apiURL, APIKey: o.apiKey, APISecret: o.apiSecret}); err != nil {
		return nil, err
	}
	var result []*consumer
	for i := 1; i <= o.consumers; i++ {
		c := &consumer{redis: redis.NewClient(&redis.Options{
			Addr: o.redisAddr, DB: o.redisDB, PoolSize: 2, MaxRetries: -1, ContextTimeoutEnabled: true,
			DialTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second,
		})}
		c.projector = presence.New(&cache.PresenceStore{Client: c.redis}, nil, nil)
		runCtx, cancel := context.WithCancel(ctx)
		c.cancel = cancel
		c.done.Add(2)
		go func() { defer c.done.Done(); c.projector.Run(runCtx) }()
		source, err := mqttc.NewPresence(config.MQTTCfg{Broker: o.brokers()[0], ClientID: fmt.Sprintf("presence-load-consumer-%d", i)}, c.projector)
		if err != nil {
			cancel()
			return result, err
		}
		c.source = source
		go func() { defer c.done.Done(); c.projector.RunReconciler(runCtx, api.Current) }()
		result = append(result, c)
	}
	return result, nil
}

type fleet struct {
	opts       options
	mu         sync.Mutex
	clients    []mqtt.Client
	heartbeats atomic.Int64
	connects   atomic.Int64
	losses     atomic.Int64
	failures   atomic.Int64
}

func randomDuration(low, high time.Duration) time.Duration {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(high-low)+1))
	if err != nil {
		return low
	}
	return low + time.Duration(n.Int64())
}

func (f *fleet) password(id string) string {
	if f.opts.jwtSecret == "" {
		return ""
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"device_id": id, "clientid": "sn_" + id, "exp": time.Now().Add(24 * time.Hour).Unix(),
	}).SignedString([]byte(f.opts.jwtSecret))
	if err != nil {
		log.Fatal(err)
	}
	return signed
}

func (f *fleet) run(ctx context.Context) {
	interval := time.Second / time.Duration(max(f.opts.connectRate, 1))
	brokers := f.opts.brokers()
	for i := 0; i < f.opts.devices && ctx.Err() == nil; i++ {
		id := fmt.Sprintf("%s%d", f.opts.prefix, i)
		opts := mqtt.NewClientOptions().AddBroker(brokers[i%len(brokers)]).
			SetClientID("sn_" + id).SetUsername(id).SetPassword(f.password(id)).
			SetCleanSession(true).SetKeepAlive(60 * time.Second).
			SetAutoReconnect(true).SetConnectRetry(true).
			SetConnectRetryInterval(randomDuration(time.Second, 4*time.Second)).
			SetMaxReconnectInterval(randomDuration(60*time.Second, 120*time.Second)).
			SetOnConnectHandler(func(mqtt.Client) { f.connects.Add(1) }).
			SetConnectionLostHandler(func(mqtt.Client, error) { f.losses.Add(1) })
		client := mqtt.NewClient(opts)
		client.Connect()
		f.mu.Lock()
		f.clients = append(f.clients, client)
		f.mu.Unlock()
		go f.heartbeat(ctx, client, "device/sn_"+id+"/up")
		time.Sleep(interval)
	}
	<-ctx.Done()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.clients {
		c.Disconnect(50)
	}
}

func (f *fleet) heartbeat(ctx context.Context, client mqtt.Client, topic string) {
	timer := time.NewTimer(randomDuration(0, f.opts.heartbeat))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if client.IsConnectionOpen() {
			token := client.Publish(topic, 1, false, `{"type":"heartbeat"}`)
			if token.WaitTimeout(10*time.Second) && token.Error() == nil {
				f.heartbeats.Add(1)
			} else {
				f.failures.Add(1)
			}
		}
		timer.Reset(f.opts.heartbeat)
	}
}

func (f *fleet) report(ctx context.Context, rdb *redis.Client, consumers []*consumer, elapsed time.Duration) {
	f.mu.Lock()
	clients := append([]mqtt.Client(nil), f.clients...)
	f.mu.Unlock()
	connected := 0
	for _, c := range clients {
		if c.IsConnectionOpen() {
			connected++
		}
	}
	online := 0
	pipe := rdb.Pipeline()
	cmds := make([]*redis.IntCmd, len(clients))
	for i := range clients {
		cmds[i] = pipe.Exists(ctx, fmt.Sprintf("online:sn_%s%d", f.opts.prefix, i))
	}
	if _, err := pipe.Exec(ctx); err == nil {
		for _, cmd := range cmds {
			online += int(cmd.Val())
		}
	}
	line := fmt.Sprintf("t=%3.0fs started=%d connected=%d online_redis=%d (%.1f%%) heartbeats=%d publish_failures=%d connects=%d losses=%d",
		elapsed.Seconds(), len(clients), connected, online, 100*float64(online)/float64(max(len(clients), 1)),
		f.heartbeats.Load(), f.failures.Load(), f.connects.Load(), f.losses.Load())
	for i, c := range consumers {
		m := c.projector.Metrics()
		ready := "ready"
		if err := c.projector.Ping(ctx); err != nil {
			ready = err.Error()
		}
		line += fmt.Sprintf(" | c%d queued=%d pending=%d rejected=%d write_failures=%d gaps=%d reconciled=%d [%s]",
			i+1, m["presence_queued"], m["presence_pending_devices"], m["presence_rejected_total"],
			m["presence_write_failures_total"], m["presence_subscription_gaps_total"], m["presence_reconcile_clients"], ready)
	}
	fmt.Println(line)
}

func cleanup(rdb *redis.Client, o options) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	iter := rdb.Scan(ctx, 0, "online:sn_"+o.prefix+"*", 1000).Iterator()
	var keys []string
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
		if len(keys) == 1000 {
			rdb.Del(ctx, keys...)
			keys = keys[:0]
		}
	}
	if len(keys) > 0 {
		rdb.Del(ctx, keys...)
	}
	rdb.Del(ctx, "presence:connections:pending", "presence:connections:pending_since", "presence:connections:gap")
}
