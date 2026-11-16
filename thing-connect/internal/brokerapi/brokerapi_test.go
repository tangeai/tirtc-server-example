package brokerapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
		if r.URL.Path != "/api/v5/clients" || q.Get("conn_state") != "connected" || q.Get("fields") == "" {
			t.Errorf("unexpected request %s", r.URL)
		}
		switch q.Get("page") {
		case "1":
			fmt.Fprint(w, `{"data":[{"clientid":"sn_a","ip_address":"::ffff:192.0.2.1","connected_at":"2026-01-01T08:00:00.123+08:00"},{"clientid":"devsrv-1","ip_address":"127.0.0.1"}],"meta":{"hasnext":true}}`)
		case "2":
			fmt.Fprint(w, `{"data":[{"clientid":"tmp_a1b2c3d4","ip_address":"192.0.2.2","connected_at":"2026-01-01T08:00:00+08:00"}],"meta":{"hasnext":false}}`)
		default:
			t.Errorf("unexpected page %s", q.Get("page"))
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
