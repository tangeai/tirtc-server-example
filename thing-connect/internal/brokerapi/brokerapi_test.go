package brokerapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"thing-connect/internal/presence"
)

func TestParseConfig(t *testing.T) {
	c, err := ParseConfig([]byte(DefaultConfigJSON), nil)
	if err != nil || c.URL != "" {
		t.Fatalf("default=%+v err=%v", c, err)
	}
	c, err = ParseConfig([]byte(`{"url":"http://127.0.0.1:18083","api_key":"k"}`), []byte(`{"api_secret":"s"}`))
	if err != nil || c.APISecret != "s" {
		t.Fatalf("parsed=%+v err=%v", c, err)
	}
	for _, bad := range []string{
		`{"url":"http://127.0.0.1:18083"}`,
		`{"url":"mqtt://127.0.0.1","api_key":"k"}`,
		`{"url":"http://u:p@127.0.0.1","api_key":"k"}`,
		`{"url":"","api_key":"","api_secret":"inline"}`,
		`{"url":"","api_key":""} {}`,
	} {
		if _, err := ParseConfig([]byte(bad), nil); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestListConnectedPagesAndFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "k" || pass != "s" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		q := r.URL.Query()
		if r.URL.Path != "/api/v5/clients_v2" || q.Get("conn_state") != "connected" || q.Get("fields") == "" || q.Has("gte_connected_at") {
			t.Errorf("unexpected request %s", r.URL)
		}
		switch q.Get("cursor") {
		case "":
			fmt.Fprint(w, `{"data":[{"clientid":"sn_a","ip_address":"::ffff:192.0.2.1","connected_at":"2026-01-01T08:00:00.123+08:00"},{"clientid":"devsrv-1","ip_address":"127.0.0.1"}],"meta":{"cursor":"after+/="}}`)
		case "after+/=":
			fmt.Fprint(w, `{"data":[{"clientid":"tmp_a1b2c3d4","ip_address":"192.0.2.2","connected_at":"2026-01-01T08:00:00+08:00"}],"meta":{"hasnext":false}}`)
		default:
			t.Errorf("unexpected page %s", q.Get("cursor"))
		}
	}))
	defer server.Close()

	client, err := New(Config{URL: server.URL + "/", APIKey: "k", APISecret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	var got []presence.Observation
	if err := client.ListConnected(context.Background(), func(batch []presence.Observation) error {
		got = append(got, batch...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ClientID != "sn_a" || got[0].IP != "192.0.2.1" || got[0].ConnectedAt.UnixMilli() != 1767225600123 {
		t.Fatalf("formal=%+v", got)
	}
	if got[1].ClientID != "tmp_a1b2c3d4" || got[1].IP != "" {
		t.Fatalf("temporary=%+v", got[1])
	}

	wrong, _ := New(Config{URL: server.URL, APIKey: "k", APISecret: "bad"})
	if err := wrong.ListConnected(context.Background(), func([]presence.Observation) error { return nil }); err == nil {
		t.Fatal("unauthorized listing succeeded")
	}
}

func TestLegacyListingRecoversLaterConnectionAfterEarlierDisconnect(t *testing.T) {
	const pivot = "1767225600000"
	var ranges []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v5/clients_v2" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if q.Get("page") != "1" {
			t.Errorf("offset paging would skip a device after the disconnect: %s", r.URL)
		}
		lo, hi := q.Get("gte_connected_at"), q.Get("lte_connected_at")
		ranges = append(ranges, lo+".."+hi)
		switch {
		case lo == "" && hi == "":
			fmt.Fprint(w, `{"data":[{"clientid":"sn_gone","connected_at":"2026-01-01T00:00:00Z"}],"meta":{"hasnext":true}}`)
		case lo == "" && hi == pivot:
			// The old first page is now empty.
			fmt.Fprint(w, `{"data":[],"meta":{"hasnext":false}}`)
		case lo == "1767225600001" && hi == "":
			fmt.Fprint(w, `{"data":[{"clientid":"sn_lost","connected_at":"2026-01-01T00:00:01Z","ip_address":"192.0.2.8"}],"meta":{"hasnext":false}}`)
		default:
			t.Errorf("unexpected interval %s", r.URL)
		}
	}))
	defer server.Close()
	c, _ := New(Config{URL: server.URL, APIKey: "key"})
	var got []presence.Observation
	if err := c.ListConnected(context.Background(), func(batch []presence.Observation) error { got = append(got, batch...); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(ranges) != 3 || len(got) != 1 || got[0].ClientID != "sn_lost" || got[0].IP != "192.0.2.8" {
		t.Fatalf("ranges=%v observations=%+v", ranges, got)
	}
}

func TestIncompleteListingsReturnErrors(t *testing.T) {
	for i, tc := range []struct {
		legacy   bool
		response string
		want     string
	}{
		{false, `{"data":[],"meta":{"cursor":"again"}}`, "repeated"},
		{false, `{"data":[],"meta":{"hasnext":true}}`, "missing continuation"},
		{false, `{"data":[]}`, "incomplete"},
		{true, `{"data":[],"meta":{}}`, "pagination"},
		{true, `{"data":[],"meta":{"hasnext":true}}`, "exceeds"},
	} {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.legacy && r.URL.Path == "/api/v5/clients_v2" {
					http.NotFound(w, r)
					return
				}
				fmt.Fprint(w, tc.response)
			}))
			defer server.Close()
			c, _ := New(Config{URL: server.URL, APIKey: "key"})
			if err := c.ListConnected(context.Background(), func([]presence.Observation) error { return nil }); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want %s", err, tc.want)
			}
		})
	}
}

func TestCursorEndpointFallbackOnlyForMissingEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		fallback bool
	}{
		{"not found", http.StatusNotFound, "", true},
		{"bad request", http.StatusBadRequest, `{"code":"BAD_REQUEST"}`, true},
		{"method not allowed", http.StatusMethodNotAllowed, "", true},
		{"dashboard page", http.StatusOK, "<!doctype html><html></html>", true},
		{"unauthorized", http.StatusUnauthorized, "", false},
		{"forbidden", http.StatusForbidden, "", false},
		{"unavailable", http.StatusServiceUnavailable, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			legacy := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v5/clients_v2" {
					w.WriteHeader(tc.status)
					fmt.Fprint(w, tc.body)
					return
				}
				legacy++
				fmt.Fprint(w, `{"data":[{"clientid":"sn_a","connected_at":"2026-01-01T00:00:00Z"}],"meta":{"hasnext":false}}`)
			}))
			defer server.Close()
			c, _ := New(Config{URL: server.URL, APIKey: "key"})
			err := c.ListConnected(context.Background(), func([]presence.Observation) error { return nil })
			if tc.fallback && (err != nil || legacy != 1) {
				t.Fatalf("fallback err=%v legacy=%d", err, legacy)
			}
			if !tc.fallback && (err == nil || legacy != 0) {
				t.Fatalf("error masked by fallback: err=%v legacy=%d", err, legacy)
			}
		})
	}
}

func TestLegacyListingSplitsSecondPrecisionTimes(t *testing.T) {
	for _, tc := range []struct {
		name, timeField string
		maxRequests     int
	}{
		{"whole second", `,"connected_at":"2026-01-01T00:00:01Z"`, 25},
		{"missing", "", 110},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const second = int64(1767225601000)
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v5/clients_v2" {
					http.NotFound(w, r)
					return
				}
				requests++
				if requests > tc.maxRequests {
					http.Error(w, "too many listing requests", http.StatusServiceUnavailable)
					return
				}
				q := r.URL.Query()
				lo, _ := strconv.ParseInt(q.Get("gte_connected_at"), 10, 64)
				hi := maxConnectionMS
				if q.Has("lte_connected_at") {
					hi, _ = strconv.ParseInt(q.Get("lte_connected_at"), 10, 64)
				}
				clients := make([]string, 0, 1200)
				for i := 0; i < 1200; i++ {
					ms := second + 899 + int64(i/600)
					if lo <= ms && ms <= hi {
						clients = append(clients, fmt.Sprintf(`{"clientid":"sn_%d"%s}`, i, tc.timeField))
					}
				}
				fmt.Fprintf(w, `{"data":[%s],"meta":{"hasnext":%t}}`, strings.Join(clients[:min(len(clients), pageSize)], ","), len(clients) > pageSize)
			}))
			defer server.Close()
			c, _ := New(Config{URL: server.URL, APIKey: "key"})
			got := make(map[string]bool)
			err := c.ListConnected(context.Background(), func(batch []presence.Observation) error {
				for _, o := range batch {
					got[o.ClientID] = true
				}
				return nil
			})
			t.Logf("listed %d devices in %d legacy requests", len(got), requests)
			if err != nil || len(got) != 1200 {
				t.Fatalf("devices=%d requests=%d err=%v", len(got), requests, err)
			}
		})
	}
}

func TestSourceDisabledByEmptyURL(t *testing.T) {
	var s Source
	if s.Current() != nil {
		t.Fatal("unconfigured source returned a client")
	}
	if err := s.Update(Config{URL: "http://127.0.0.1:18083", APIKey: "k"}); err != nil || s.Current() == nil {
		t.Fatal("configured source has no client")
	}
	if err := s.Update(Config{}); err != nil || s.Current() != nil {
		t.Fatal("empty URL did not disable listing")
	}
	if err := s.Update(Config{URL: "ftp://x", APIKey: "k"}); err == nil {
		t.Fatal("invalid config accepted")
	}
}
