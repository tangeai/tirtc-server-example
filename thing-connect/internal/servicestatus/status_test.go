package servicestatus

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type testPinger struct{ err error }

func (p testPinger) PingContext(context.Context) error { return p.err }

func TestRegisterHealthReportsDependencyState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterHealth(router, map[string]DependencyProbe{
		"database": func(context.Context) error { return nil },
		"redis":    func(context.Context) error { return errors.New("offline") },
	})

	live := httptest.NewRecorder()
	router.ServeHTTP(live, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if live.Code != http.StatusOK || !strings.Contains(live.Body.String(), `"live"`) {
		t.Fatalf("unexpected liveness response: %d %s", live.Code, live.Body.String())
	}
	ready := httptest.NewRecorder()
	router.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if ready.Code != http.StatusServiceUnavailable || !strings.Contains(ready.Body.String(), `"redis"`) {
		t.Fatalf("unexpected readiness response: %d %s", ready.Code, ready.Body.String())
	}
}

func TestReporterIdentityAndValidation(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer client.Close()
	if _, err := NewReporter(nil, "device-server", nil, nil); err == nil {
		t.Fatal("nil Redis client accepted")
	}
	if _, err := NewReporter(client, "unknown", nil, nil); err == nil {
		t.Fatal("unknown service accepted")
	}
	t.Setenv("SERVICE_INSTANCE_ID", "invalid instance")
	if _, err := NewReporter(client, "device-server", nil, nil); err == nil {
		t.Fatal("unsafe instance ID accepted")
	}
	t.Setenv("SERVICE_INSTANCE_ID", "node-a-1")
	t.Setenv("SERVICE_NODE_NAME", " node-a ")
	t.Setenv("SERVICE_ZONE", "zone-1")
	t.Setenv("BUILD_VERSION", "v-test")
	reporter, err := NewReporter(client, "device-server", nil, func() map[string]int64 { return map[string]int64{"device.code_policy": 3} })
	if err != nil {
		t.Fatal(err)
	}
	if reporter.instance.InstanceID != "node-a-1" || reporter.instance.Node != "node-a" || reporter.instance.Zone != "zone-1" || reporter.instance.Version != "v-test" {
		t.Fatalf("environment identity not applied: %+v", reporter.instance)
	}
	if reporter.key != heartbeatKey("device-server", "node-a-1") {
		t.Fatalf("unexpected heartbeat key: %q", reporter.key)
	}
}

func TestServiceStatusHelpers(t *testing.T) {
	if !allHealthy(nil) || !allHealthy(map[string]string{"db": "healthy", "redis": "healthy"}) || allHealthy(map[string]string{"db": "unhealthy"}) {
		t.Fatal("dependency health aggregation returned an unexpected result")
	}
	if !contains(ExpectedServices, "call-server") || contains(ExpectedServices, "admin-server") {
		t.Fatal("expected service membership returned an unexpected result")
	}
	if firstNonEmpty(" ", " node ", "fallback") != "node" || firstNonEmpty("", " ") != "" {
		t.Fatal("firstNonEmpty returned an unexpected result")
	}
	if err := SQLProbe(testPinger{})(context.Background()); err != nil {
		t.Fatalf("healthy SQL probe failed: %v", err)
	}
	want := errors.New("db down")
	if err := SQLProbe(testPinger{err: want})(context.Background()); !errors.Is(err, want) {
		t.Fatalf("SQL probe did not preserve the dependency error: %v", err)
	}
}
