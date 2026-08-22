// Package servicestatus provides health endpoints and Redis heartbeats shared
// by every independently deployed ThingConnect service instance.
package servicestatus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
)

var ExpectedServices = []string{"device-server", "user-server", "voip-server", "ai-server", "call-server"}

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
	ConfigRevision map[string]int64  `json:"config_revision,omitempty"`
}

type Reporter struct {
	client    *redis.Client
	instance  Instance
	key       string
	probes    map[string]DependencyProbe
	revisions func() map[string]int64
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
	instance := r.instance
	instance.Dependencies = dependencies
	instance.LastHeartbeat = time.Now().UTC()
	if r.revisions != nil {
		instance.ConfigRevision = r.revisions()
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
