package requestguard

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIndependentAdmissionAndCancellation(t *testing.T) {
	g, err := New(Config{Upload: Limit{Concurrency: 1, Timeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	h := g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if Classify(r) == "upload" {
			close(entered)
			<-r.Context().Done()
			return
		}
		w.WriteHeader(204)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/recognition/requests", nil).WithContext(ctx))
	}()
	<-entered
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"POST", "/v1/recognition/requests", 500}, {"GET", "/v1/recognition/config", 204}, {"POST", "/v1/device/token", 204}, {"GET", "/health/ready", 204}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status {
			t.Fatalf("%s status=%d", tc.path, w.Code)
		}
	}
	cancel()
	<-done
	metrics := g.Metrics()
	if metrics["request_upload_active"] != 0 || metrics["request_upload_rejected_total"] != 1 {
		t.Fatal(metrics)
	}
}

func TestDeadlinePropagatesAndReleasesSlot(t *testing.T) {
	g, _ := New(Config{Query: Limit{Concurrency: 1, Timeout: 20 * time.Millisecond}})
	h := g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	for i := 0; i < 2; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/user/me", nil))
	}
	v := g.Metrics()
	if v["request_query_deadline_exceeded_total"] != 2 || v["request_query_active"] != 0 {
		t.Fatal(v)
	}
}

func TestResourceSuffixCannotBypassAdmission(t *testing.T) {
	if got := Classify(httptest.NewRequest("GET", "/v1/ai/roles/example.js", nil)); got != "upstream" {
		t.Fatalf("resource classified as %q", got)
	}
}

func TestErrorEnvelopeCompatibility(t *testing.T) {
	for _, tc := range []struct {
		path, field string
		status      int
	}{{"/v1/device/token", `"code":50000`, 500}, {"/v1/call/request", `"code":50000`, 200}, {"/v1/voip/device/call", `"code":50000`, 200}, {"/v1/voip/notification/app", `"errcode":10`, 200}} {
		w := httptest.NewRecorder()
		reject(w, httptest.NewRequest("POST", tc.path, nil))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.field) || w.Header().Get("Retry-After") == "" {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
}

func TestSlowBodyReadIsInterrupted(t *testing.T) {
	g, _ := New(Config{Upload: Limit{Concurrency: 1, Timeout: 50 * time.Millisecond}})
	done := make(chan error, 1)
	srv := httptest.NewServer(g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		done <- err
		w.WriteHeader(400)
	})))
	defer srv.Close()
	c, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = io.WriteString(c, "POST /v1/recognition/requests HTTP/1.1\r\nHost: example\r\nContent-Length: 1000\r\n\r\nx")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("truncated body succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("slow body escaped total budget")
	}
}

func TestInvalidLimitsFailBeforeServing(t *testing.T) {
	for _, c := range []Config{{Query: Limit{Concurrency: -1}}, {Token: Limit{Timeout: -time.Second}}, {Upload: Limit{Timeout: 4 * time.Minute}}} {
		if _, err := New(c); err == nil {
			t.Fatal("accepted invalid limits")
		}
	}
}
