// Package servicestatus provides health endpoints and Redis heartbeats shared
// by every independently deployed ThingConnect service instance.
package servicestatus

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	instanceIndex = "thingconnect:services:instances"
	heartbeatTTL  = 45 * time.Second
	// DependencyInstanceID reports that another live process uses the same
	// SERVICE_INSTANCE_ID. It degrades the instance without removing it from
	// HTTP readiness, because the traffic path itself is unaffected.
	DependencyInstanceID = "instance_id"
)

var ExpectedServices = []string{"device-server", "user-server", "voip-server", "ai-server", "call-server", "recognition-server"}

// BuildVersion and BuildCommit are set by build.sh through Go linker flags.
// Environment variables retain precedence for packaged deployments.
var BuildVersion, BuildCommit string

type DependencyProbe func(context.Context) error

type Instance struct {
	Service        string            `json:"service"`
	InstanceID     string            `json:"instance_id"`
	Node           string            `json:"node"`
	Zone           string            `json:"zone,omitempty"`
	Version        string            `json:"version,omitempty"`
	Commit         string            `json:"commit,omitempty"`
	StartedAt      time.Time         `json:"started_at"`
	LastHeartbeat  time.Time         `json:"last_heartbeat"`
	Dependencies   map[string]string `json:"dependencies"`
	Metrics        map[string]int64  `json:"metrics,omitempty"`
	ConfigRevision map[string]int64  `json:"config_revision,omitempty"`
}

type Reporter struct {
	client    *redis.Client
	instance  Instance
	key       string
	probes    map[string]DependencyProbe
	revisions func() map[string]int64
	metrics   func() map[string]int64
}

func NewReporter(client *redis.Client, service string, probes map[string]DependencyProbe, revisions func() map[string]int64) (*Reporter, error) {
	if client == nil || !contains(ExpectedServices, service) {
		return nil, errors.New("service status requires Redis and a known service name")
	}
	hostname, _ := os.Hostname()
	node := firstNonEmpty(os.Getenv("SERVICE_NODE_NAME"), hostname, "unknown")
	instanceID := strings.TrimSpace(os.Getenv("SERVICE_INSTANCE_ID"))
	if instanceID == "" {
		token := strconv.FormatInt(time.Now().UnixNano(), 36)
		instanceID = fmt.Sprintf("%s-%d-%s", node, os.Getpid(), token)
	}
	if len(instanceID) > 128 || strings.ContainsAny(instanceID, "{} \t\r\n") {
		return nil, errors.New("SERVICE_INSTANCE_ID is invalid")
	}
	started := time.Now().UTC()
	return &Reporter{
		client: client, key: heartbeatKey(service, instanceID), probes: probes, revisions: revisions,
		instance: Instance{Service: service, InstanceID: instanceID, Node: node, Zone: os.Getenv("SERVICE_ZONE"), Version: firstNonEmpty(os.Getenv("BUILD_VERSION"), BuildVersion), Commit: firstNonEmpty(os.Getenv("BUILD_COMMIT"), BuildCommit), StartedAt: started},
	}, nil
}

// SetMetrics attaches a non-blocking snapshot before Run starts.
func (r *Reporter) SetMetrics(snapshot func() map[string]int64) { r.metrics = snapshot }

// MergeMetrics combines fixed-name, non-blocking snapshots at reporting time.
func MergeMetrics(sources ...func() map[string]int64) func() map[string]int64 {
	return func() map[string]int64 {
		values := map[string]int64{}
		for _, source := range sources {
			for name, value := range source() {
				values[name] = value
			}
		}
		return values
	}
}

// PoolMetrics exposes process-local cumulative waits without querying either
// dependency. Compare deltas within the same instance lifetime.
func PoolMetrics(db interface{ Stats() sql.DBStats }, client *redis.Client) func() map[string]int64 {
	return func() map[string]int64 {
		s := db.Stats()
		r := client.PoolStats()
		return map[string]int64{
			"sql_pool_open": int64(s.OpenConnections), "sql_pool_in_use": int64(s.InUse),
			"sql_pool_idle": int64(s.Idle), "sql_pool_wait_total": s.WaitCount,
			"sql_pool_wait_ms_total": s.WaitDuration.Milliseconds(),
			"redis_pool_connections": int64(r.TotalConns), "redis_pool_idle": int64(r.IdleConns),
			"redis_pool_timeouts_total": int64(r.Timeouts), "redis_pool_misses_total": int64(r.Misses),
		}
	}
}

func (r *Reporter) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		r.report(ctx)
		select {
		case <-ctx.Done():
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, _ = r.client.TxPipelined(cleanupCtx, func(pipe redis.Pipeliner) error {
				pipe.Del(cleanupCtx, r.key)
				pipe.SRem(cleanupCtx, instanceIndex, r.key)
				return nil
			})
			cancel()
			return
		case <-ticker.C:
		}
	}
}

func (r *Reporter) report(ctx context.Context) {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	dependencies := make(map[string]string, len(r.probes))
	for name, probe := range r.probes {
		if err := probe(probeCtx); err != nil {
			dependencies[name] = "unhealthy"
		} else {
			dependencies[name] = "healthy"
		}
	}
	if r.instanceIDShared(ctx) {
		dependencies[DependencyInstanceID] = "unhealthy"
		slog.Error("SERVICE_INSTANCE_ID 被另一个运行中的进程使用，请为每个进程设置唯一实例 ID", "service", r.instance.Service, "instance_id", r.instance.InstanceID)
	}
	instance := r.instance
	instance.Dependencies = dependencies
	instance.LastHeartbeat = time.Now().UTC()
	if r.revisions != nil {
		instance.ConfigRevision = r.revisions()
	}
	if r.metrics != nil {
		instance.Metrics = r.metrics()
	}
	payload, err := json.Marshal(instance)
	if err != nil {
		return
	}
	_, _ = r.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, r.key, payload, heartbeatTTL)
		pipe.SAdd(ctx, instanceIndex, r.key)
		return nil
	})
}

// instanceIDShared reports whether the heartbeat key was written by another
// process after this one started. A previous generation of the same instance
// stopped reporting before this process started, so only a live duplicate can
// leave a later heartbeat. Read failures never mark a conflict.
func (r *Reporter) instanceIDShared(ctx context.Context) bool {
	raw, err := r.client.Get(ctx, r.key).Bytes()
	if err != nil {
		return false
	}
	var existing Instance
	if json.Unmarshal(raw, &existing) != nil {
		return false
	}
	return !existing.StartedAt.Equal(r.instance.StartedAt) && existing.LastHeartbeat.After(r.instance.StartedAt)
}

type ServiceSummary struct {
	Service       string     `json:"service"`
	Status        string     `json:"status"`
	InstanceCount int        `json:"instance_count"`
	HealthyCount  int        `json:"healthy_count"`
	Instances     []Instance `json:"instances"`
}

type Aggregator struct{ client *redis.Client }

func NewAggregator(client *redis.Client) *Aggregator { return &Aggregator{client: client} }

func (a *Aggregator) List(ctx context.Context) ([]ServiceSummary, error) {
	keys, err := a.client.SMembers(ctx, instanceIndex).Result()
	if err != nil {
		return nil, err
	}
	values := []interface{}{}
	if len(keys) > 0 {
		values, err = a.client.MGet(ctx, keys...).Result()
		if err != nil {
			return nil, err
		}
	}
	byService := make(map[string][]Instance)
	stale := make([]interface{}, 0)
	for index, value := range values {
		if value == nil {
			stale = append(stale, keys[index])
			continue
		}
		var instance Instance
		if err := json.Unmarshal([]byte(value.(string)), &instance); err != nil {
			stale = append(stale, keys[index])
			continue
		}
		byService[instance.Service] = append(byService[instance.Service], instance)
	}
	if len(stale) > 0 {
		_ = a.client.SRem(ctx, instanceIndex, stale...).Err()
	}
	return summarizeServices(byService), nil
}

func summarizeServices(byService map[string][]Instance) []ServiceSummary {
	result := make([]ServiceSummary, 0, len(ExpectedServices))
	for _, service := range ExpectedServices {
		instances := byService[service]
		if instances == nil {
			instances = []Instance{}
		}
		sort.Slice(instances, func(i, j int) bool { return instances[i].InstanceID < instances[j].InstanceID })
		healthy := 0
		for _, instance := range instances {
			if allHealthy(instance.Dependencies) {
				healthy++
			}
		}
		status := "offline"
		if healthy == len(instances) && healthy > 0 {
			status = "healthy"
		} else if len(instances) > 0 {
			status = "degraded"
		}
		result = append(result, ServiceSummary{Service: service, Status: status, InstanceCount: len(instances), HealthyCount: healthy, Instances: instances})
	}
	return result
}

func RegisterHealth(r *gin.Engine, probes map[string]DependencyProbe) {
	r.GET("/health/live", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "live"})
	})
	r.GET("/health/ready", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		failed := make([]string, 0)
		for name, probe := range probes {
			if err := probe(ctx); err != nil {
				failed = append(failed, name)
			}
		}
		sort.Strings(failed)
		if len(failed) > 0 {
			c.JSON(503, gin.H{"status": "not_ready", "failed_dependencies": failed})
			return
		}
		c.JSON(200, gin.H{"status": "ready"})
	})
}

type sqlPinger interface {
	PingContext(context.Context) error
}

func SQLProbe(db sqlPinger) DependencyProbe {
	return func(ctx context.Context) error { return db.PingContext(ctx) }
}

func RedisProbe(client *redis.Client) DependencyProbe {
	return func(ctx context.Context) error { return client.Ping(ctx).Err() }
}

// DeviceHeartbeatKeys returns only registered Device instances, including any
// index members whose heartbeat may expire before the caller reads it.
func DeviceHeartbeatKeys(ctx context.Context, client *redis.Client) ([]string, error) {
	keys, err := client.SMembers(ctx, instanceIndex).Result()
	if err != nil {
		return nil, err
	}
	device := keys[:0]
	for _, key := range keys {
		if strings.HasPrefix(key, heartbeatKey("device-server", "")) {
			device = append(device, key)
		}
	}
	return device, nil
}

// CurrentAt checks the same freshness window used by the Redis heartbeat.
func (i Instance) CurrentAt(now time.Time) bool {
	age := now.Sub(i.LastHeartbeat)
	return age >= 0 && age < heartbeatTTL
}

func heartbeatKey(service, instance string) string {
	return "thingconnect:service:instance:" + service + ":" + instance
}

func allHealthy(dependencies map[string]string) bool {
	for _, status := range dependencies {
		if status != "healthy" {
			return false
		}
	}
	return true
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
