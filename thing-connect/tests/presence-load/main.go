// Command presence-load measures the device presence pipeline: simulated
// devices connect to EMQX and send heartbeats, K in-process consumers run the
// same shared-subscription projector as device-server, and the tool samples
// how many devices are online in Redis. Run it against a test broker and a
// dedicated Redis database only. Synthetic runs clean only their own prefix.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"thing-connect/internal/servicestatus"
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
	syncHeartbeats     bool
	fixtures, tokenURL string
	shard, shards      int
	jsonOutput         bool
	startAt            int64
	broker             string
	devices            int
	prefix             string
	connectRate        int
	heartbeat          time.Duration
	duration           time.Duration
	jwtSecret          string
	consumers          int
	redisAddr          string
	redisDB            int
	apiURL             string
	apiKey             string
	apiSecret          string
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
	flag.BoolVar(&o.syncHeartbeats, "sync-heartbeats", false, "align heartbeats to wall-clock interval boundaries for burst tests")
	flag.DurationVar(&o.duration, "duration", 3*time.Minute, "run time after all devices started")
	flag.StringVar(&o.jwtSecret, "jwt-secret", os.Getenv("LOAD_JWT_SECRET"), "sign device passwords like device-server (empty: no password)")
	flag.IntVar(&o.consumers, "consumers", 2, "in-process Device presence consumers (0: devices only)")
	flag.StringVar(&o.redisAddr, "redis", "127.0.0.1:6379", "Redis address")
	flag.IntVar(&o.redisDB, "redis-db", 15, "dedicated Redis database; must not be 0")
	flag.StringVar(&o.apiURL, "broker-api", "", "EMQX HTTP API for reconciliation (optional)")
	flag.StringVar(&o.apiKey, "broker-api-key", os.Getenv("LOAD_BROKER_API_KEY"), "EMQX API key")
	flag.StringVar(&o.apiSecret, "broker-api-secret", os.Getenv("LOAD_BROKER_API_SECRET"), "EMQX API secret")
	flag.BoolVar(&o.jsonOutput, "json", false, "emit timestamped JSONL samples")
	flag.Int64Var(&o.startAt, "start-at", 0, "shared Unix start time for distributed workers")
	flag.StringVar(&o.fixtures, "fixtures", "", "private device_id,device_key CSV; its shard determines device count")
	flag.StringVar(&o.tokenURL, "token-url", "", "real /v1/device/token endpoint (requires fixtures and consumers=0)")
	flag.IntVar(&o.shard, "shard", 0, "zero-based CSV shard")
	flag.IntVar(&o.shards, "shards", 1, "number of CSV shards")
	flag.Parse()
	var credentials []credential
	if o.fixtures != "" || o.tokenURL != "" {
		if o.fixtures == "" || o.tokenURL == "" || o.consumers != 0 || o.jwtSecret != "" {
			log.Fatal("real authentication requires fixtures, token-url, consumers=0 and no jwt-secret")
		}
		if err := validateTokenURL(o.tokenURL); err != nil {
			log.Fatal(err)
		}
		var err error
		credentials, err = loadCredentials(o.fixtures, o.shard, o.shards)
		if err != nil {
			log.Fatal(err)
		}
		o.devices = len(credentials)
	}
	if o.devices < 1 || o.devices > 1000000 || o.connectRate < 1 || o.connectRate > 100000 || o.heartbeat < time.Millisecond || o.duration <= 0 || o.duration > 7*24*time.Hour || o.consumers < 0 || o.consumers > 32 || !regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`).MatchString(o.prefix) {
		log.Fatal("invalid load limits or prefix")
	}
	if o.redisDB == 0 {
		log.Fatal("redis-db must be a dedicated non-zero database")
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	rdb := redis.NewClient(&redis.Options{Addr: o.redisAddr, DB: o.redisDB, Password: os.Getenv("LOAD_REDIS_PASSWORD"), MaxRetries: -1, ContextTimeoutEnabled: true})
	defer func() { _ = rdb.Close() }()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("redis: %v", err)
	}
	defer cleanup(rdb, o)
	if o.startAt > 0 {
		timer := time.NewTimer(time.Until(time.Unix(o.startAt, 0)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}

	consumers, err := startConsumers(ctx, o)
	if err != nil {
		log.Fatalf("consumers: %v", err)
	}
	defer func() {
		for _, c := range consumers {
			c.close()
		}
	}()

	fleet := &fleet{opts: o, credentials: credentials, authSlots: make(chan struct{}, 32), http: tokenClient(), ackJobs: make(chan ackJob, 1024)}
	defer fleet.http.CloseIdleConnections()
	fleetDone := make(chan struct{})
	go func() { defer close(fleetDone); fleet.run(ctx) }()
	defer func() { stop(); <-fleetDone }()
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
			Addr: o.redisAddr, DB: o.redisDB, Password: os.Getenv("LOAD_REDIS_PASSWORD"), PoolSize: 2, MaxRetries: -1, ContextTimeoutEnabled: true,
			DialTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second,
		})}
		c.projector = presence.New(&cache.PresenceStore{Client: c.redis}, nil, nil)
		runCtx, cancel := context.WithCancel(ctx)
		c.cancel = cancel
		c.done.Add(1)
		go func() { defer c.done.Done(); c.projector.Run(runCtx) }()
		source, err := mqttc.NewPresence(config.MQTTCfg{Broker: o.brokers()[0], ClientID: fmt.Sprintf("presence-load-%s-%d", o.prefix, i)}, c.projector)
		if err != nil {
			cancel()
			c.done.Wait()
			_ = c.redis.Close()
			for _, previous := range result {
				previous.close()
			}
			return nil, err
		}
		c.source = source
		c.done.Add(1)
		go func() { defer c.done.Done(); c.projector.RunReconciler(runCtx, api.Current) }()
		result = append(result, c)
	}
	return result, nil
}

type fleet struct {
	authenticationStopped, connectFailures                                       atomic.Int64
	ackJobs                                                                      chan ackJob
	credentials                                                                  []credential
	authSlots                                                                    chan struct{}
	http                                                                         *http.Client
	tokenRequests, tokenFailures, tokenRejected                                  atomic.Int64
	subscribed, subscriptionFailures, commands, notifications, acks, ackFailures atomic.Int64
	opts                                                                         options
	mu                                                                           sync.Mutex
	clients                                                                      []mqtt.Client
	heartbeats                                                                   atomic.Int64
	connects                                                                     atomic.Int64
	losses                                                                       atomic.Int64
	failures                                                                     atomic.Int64
}

type ackJob struct {
	client mqtt.Client
	topic  string
}

func (f *fleet) deviceID(i int) string {
	if len(f.credentials) > 0 {
		return f.credentials[i].id
	}
	return fmt.Sprintf("%s%d", f.opts.prefix, i)
}

func waitLoadToken(ctx context.Context, token mqtt.Token) bool {
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	case <-token.Done():
		return token.Error() == nil
	}
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
		"device_id": id, "clientid": "sn_" + id, "exp": time.Now().Add(f.opts.duration + time.Duration(f.opts.devices/max(1, f.opts.connectRate)+1)*time.Second + time.Hour).Unix(),
	}).SignedString([]byte(f.opts.jwtSecret))
	if err != nil {
		log.Fatal(err)
	}
	return signed
}

func (f *fleet) run(ctx context.Context) {
	var heartbeats sync.WaitGroup
	defer heartbeats.Wait()
	for i := 0; i < 16; i++ {
		heartbeats.Add(1)
		go func() {
			defer heartbeats.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case job := <-f.ackJobs:
					if waitLoadToken(ctx, job.client.Publish(job.topic, 1, false, `{"ack":true}`)) {
						f.acks.Add(1)
					} else {
						f.ackFailures.Add(1)
					}
				}
			}
		}()
	}
	interval := time.Second / time.Duration(max(f.opts.connectRate, 1))
	brokers := f.opts.brokers()
	for i := 0; i < f.opts.devices && ctx.Err() == nil; i++ {
		id := f.deviceID(i)
		opts := mqtt.NewClientOptions().AddBroker(brokers[i%len(brokers)]).
			SetClientID("sn_" + id).SetUsername(id).SetPassword(f.password(id)).
			SetCleanSession(true).SetKeepAlive(60 * time.Second).
			SetWriteTimeout(10 * time.Second).SetConnectTimeout(10 * time.Second).
			SetAutoReconnect(true).SetConnectRetry(true).
			SetConnectRetryInterval(randomDuration(time.Second, 4*time.Second)).
			SetMaxReconnectInterval(randomDuration(60*time.Second, 120*time.Second)).
			SetOnConnectHandler(func(c mqtt.Client) {
				f.connects.Add(1)
				topic := "device/sn_" + id
				tok := c.SubscribeMultiple(map[string]byte{topic + "/cmd": 1, topic + "/notify": 1}, func(c mqtt.Client, m mqtt.Message) {
					if m.Topic() == topic+"/cmd" {
						f.commands.Add(1)
						// MQTT callbacks must not wait for PUBACK; use a bounded
						// queue so command bursts cannot spawn unbounded goroutines.
						select {
						case f.ackJobs <- ackJob{c, topic + "/ack"}:
						default:
							f.ackFailures.Add(1)
						}
					} else {
						f.notifications.Add(1)
					}
				})
				ok := waitLoadToken(ctx, tok)
				if result, yes := tok.(interface{ Result() map[string]byte }); yes && ok {
					granted := result.Result()
					for _, suffix := range []string{"/cmd", "/notify"} {
						qos, exists := granted[topic+suffix]
						ok = ok && exists && qos <= 2
					}
				}
				if ok {
					f.subscribed.Add(1)
				} else {
					f.subscriptionFailures.Add(1)
				}
			}).
			SetConnectionLostHandler(func(mqtt.Client, error) { f.losses.Add(1) })
		index := i
		var client mqtt.Client
		if len(f.credentials) == 0 {
			client = mqtt.NewClient(opts)
		}
		f.mu.Lock()
		f.clients = append(f.clients, client)
		f.mu.Unlock()
		if len(f.credentials) > 0 {
			heartbeats.Add(1)
			go func() { defer heartbeats.Done(); f.runRealDevice(ctx, index, opts) }()
		} else {
			client.Connect()
		}
		heartbeats.Add(1)
		go func() { defer heartbeats.Done(); f.heartbeat(ctx, index, "device/sn_"+id+"/up") }()
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	<-ctx.Done()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.clients {
		if c != nil {
			c.Disconnect(0)
		}
	}
}

func (f *fleet) heartbeat(ctx context.Context, index int, topic string) {
	delay := randomDuration(0, f.opts.heartbeat)
	if f.opts.syncHeartbeats {
		delay = time.Until(time.Now().Truncate(f.opts.heartbeat).Add(f.opts.heartbeat))
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		client := f.client(index)
		if client != nil && client.IsConnectionOpen() {
			token := client.Publish(topic, 1, false, `{"type":"heartbeat"}`)
			if waitLoadToken(ctx, token) {
				f.heartbeats.Add(1)
			} else {
				f.failures.Add(1)
			}
		}
		delay = f.opts.heartbeat
		if f.opts.syncHeartbeats {
			delay = time.Until(time.Now().Truncate(f.opts.heartbeat).Add(f.opts.heartbeat))
		}
		timer.Reset(delay)
	}
}

func (f *fleet) report(ctx context.Context, rdb *redis.Client, consumers []*consumer, elapsed time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	f.mu.Lock()
	clients := append([]mqtt.Client(nil), f.clients...)
	f.mu.Unlock()
	connected := 0
	for _, c := range clients {
		if c != nil && c.IsConnectionOpen() {
			connected++
		}
	}
	online := 0
	pipe := rdb.Pipeline()
	cmds := make([]*redis.IntCmd, len(clients))
	for i := range clients {
		cmds[i] = pipe.Exists(ctx, "online:sn_"+f.deviceID(i))
	}
	_, onlineErr := pipe.Exec(ctx)
	if onlineErr == nil {
		for _, cmd := range cmds {
			online += int(cmd.Val())
		}
	}
	line := fmt.Sprintf("t=%3.0fs started=%d connected=%d online_redis=%d (%.1f%%) heartbeats=%d publish_failures=%d connects=%d losses=%d",
		elapsed.Seconds(), len(clients), connected, online, 100*float64(online)/float64(max(len(clients), 1)),
		f.heartbeats.Load(), f.failures.Load(), f.connects.Load(), f.losses.Load())
	if onlineErr != nil {
		line += " online_sample_failed=true"
	}
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
	if f.opts.jsonOutput {
		states, statusErr := servicestatus.NewAggregator(rdb).List(ctx)
		projections := make([]map[string]int64, 0, len(consumers))
		for _, c := range consumers {
			projections = append(projections, c.projector.Metrics())
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"at": time.Now().UTC(), "prefix": f.opts.prefix, "elapsed_ms": elapsed.Milliseconds(), "started": len(clients), "connected": connected, "online": online, "heartbeats": f.heartbeats.Load(), "publish_failures": f.failures.Load(), "connects": f.connects.Load(), "losses": f.losses.Load(), "consumers": projections, "services": states, "status_sample_failed": statusErr != nil, "online_sample_failed": onlineErr != nil,
			"authentication_stopped": f.authenticationStopped.Load(), "connect_failures": f.connectFailures.Load(), "token_requests": f.tokenRequests.Load(), "token_failures": f.tokenFailures.Load(), "token_admission_rejected": f.tokenRejected.Load(), "subscriptions": f.subscribed.Load(), "subscription_failures": f.subscriptionFailures.Load(), "commands": f.commands.Load(), "notifications": f.notifications.Load(), "acks": f.acks.Load(), "ack_failures": f.ackFailures.Load()})
		return
	}
	fmt.Println(line)
}

func cleanup(rdb *redis.Client, o options) {
	if o.fixtures != "" {
		return
	} // Real fixture projections belong to Device.
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
	// Shared reconciliation state belongs to Device, never to one load shard.
}
