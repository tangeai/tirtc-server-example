// Package requestguard bounds whole HTTP use cases, including body reads and
// dependency pool waits. Admission is immediate; no hidden waiting queue grows
// during an outage. It never retries or executes handlers in detached goroutines.
package requestguard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

type Limit struct {
	Concurrency int           `yaml:"concurrency"`
	Timeout     time.Duration `yaml:"timeout"`
}

// Config is per process and restart-only, like database pool sizing.
type Config struct {
	Token    Limit `yaml:"token"`
	Query    Limit `yaml:"query"`
	Upload   Limit `yaml:"upload"`
	Upstream Limit `yaml:"upstream"`
}

func (c Config) Resolve() (Config, error) {
	for _, entry := range []struct {
		name    string
		limit   *Limit
		count   int
		timeout time.Duration
	}{
		{"token", &c.Token, 128, 10 * time.Second}, {"query", &c.Query, 64, 10 * time.Second},
		{"upload", &c.Upload, 8, 25 * time.Second}, {"upstream", &c.Upstream, 32, 25 * time.Second},
	} {
		if entry.limit.Concurrency == 0 {
			entry.limit.Concurrency = entry.count
		}
		if entry.limit.Timeout == 0 {
			entry.limit.Timeout = entry.timeout
		}
		if entry.limit.Concurrency < 1 || entry.limit.Concurrency > 10000 || entry.limit.Timeout < time.Millisecond || entry.limit.Timeout > 3*time.Minute {
			return Config{}, fmt.Errorf("request_limits.%s requires concurrency 1..10000 and timeout 1ms..3m", entry.name)
		}
	}
	return c, nil
}

type lane struct {
	limit                                           Limit
	slots                                           chan struct{}
	active, rejected, completed, expired, elapsedMS atomic.Int64
}

type Guard struct {
	lanes      map[string]*lane
	maxTimeout time.Duration
}

func New(config Config) (*Guard, error) {
	c, err := config.Resolve()
	if err != nil {
		return nil, err
	}
	g := &Guard{lanes: map[string]*lane{}}
	for name, limit := range map[string]Limit{"token": c.Token, "query": c.Query, "upload": c.Upload, "upstream": c.Upstream} {
		g.lanes[name] = &lane{limit: limit, slots: make(chan struct{}, limit.Concurrency)}
		g.maxTimeout = max(g.maxTimeout, limit.Timeout)
	}
	return g, nil
}

func (g *Guard) MaxTimeout() time.Duration { return g.maxTimeout }

// Classify uses fixed labels, never a user-controlled URL as a metric key.
func Classify(r *http.Request) string {
	p := r.URL.Path
	if !strings.HasPrefix(p, "/v1/") || p == "/v1/config/language.js" || p == "/v1/ai/config/language.js" || p == "/v1/call/config/language.js" || strings.HasPrefix(p, "/v1/board-images/") || p == "/v1/ai/agent" || p == "/v1/call/group/page" {
		return ""
	}
	if r.Method == http.MethodPost && (p == "/v1/ai/knowledge/files" || p == "/v1/recognition/requests") {
		return "upload"
	}
	if p == "/v1/device/token" || p == "/v1/ai/token" || p == "/v1/user/device/rtc-token" || p == "/v1/call/group/device/connect-token" {
		return "token"
	}
	if strings.HasPrefix(p, "/v1/ai/") || strings.HasPrefix(p, "/v1/voip/notification/") {
		return "upstream"
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return "query"
	}
	return "upstream"
}

func (g *Guard) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := g.lanes[Classify(r)]
		if l == nil {
			next.ServeHTTP(w, r)
			return
		}
		if r.Context().Err() != nil {
			reject(w, r)
			return
		}
		select {
		case l.slots <- struct{}{}:
		default:
			l.rejected.Add(1)
			reject(w, r)
			return
		}
		start := time.Now()
		ctx, cancel := context.WithTimeout(r.Context(), l.limit.Timeout)
		defer cancel()
		l.active.Add(1)
		defer func() {
			<-l.slots
			l.active.Add(-1)
			l.completed.Add(1)
			l.elapsedMS.Add(time.Since(start).Milliseconds())
			if ctx.Err() == context.DeadlineExceeded {
				l.expired.Add(1)
			}
		}()
		// A context alone cannot interrupt a client stalled while sending its
		// body. Set the underlying net/http read deadline before logging/parsing.
		controller := http.NewResponseController(w)
		deadline, _ := ctx.Deadline()
		if err := controller.SetReadDeadline(deadline); err == nil {
			defer func() { _ = controller.SetReadDeadline(time.Time{}) }()
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func reject(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Retry-After", "1")
	p := r.URL.Path
	if strings.HasPrefix(p, "/v1/voip/notification/") {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 10, "errmsg": "服务繁忙，请稍后重试"})
		return
	}
	status := http.StatusInternalServerError
	if strings.HasPrefix(p, "/v1/voip/") || strings.HasPrefix(p, "/v1/call/") {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 50000, "msg": "服务器内部错误，请稍后重试"})
}

func (g *Guard) Metrics() map[string]int64 {
	v := make(map[string]int64, len(g.lanes)*6)
	for name, l := range g.lanes {
		prefix := "request_" + name + "_"
		v[prefix+"active"] = l.active.Load()
		v[prefix+"rejected_total"] = l.rejected.Load()
		v[prefix+"completed_total"] = l.completed.Load()
		v[prefix+"deadline_exceeded_total"] = l.expired.Load()
		v[prefix+"duration_ms_total"] = l.elapsedMS.Load()
		v[prefix+"limit"] = int64(l.limit.Concurrency)
	}
	return v
}
