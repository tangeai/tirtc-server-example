package cleanup

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type cleanupTransport func(*http.Request) (*http.Response, error)

func (f cleanupTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDeliveryRequiresBusinessSuccess(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		success    bool
	}{
		{"voip success", `{"code":0}`, 200, true},
		{"other service success", `{"code":200}`, 200, true},
		{"business failure", `{"code":50000}`, 200, false},
		{"missing code", `{}`, 200, false},
		{"invalid response", `not json`, 200, false},
		{"http failure", `{"code":0}`, 503, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := NewOutbox(nil, []Target{{Name: "voip", URL: "http://cleanup.invalid", InternalKey: "test-key"}})
			o.client.Transport = cleanupTransport(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("X-Internal-Key") != "test-key" {
					t.Fatal("missing credential")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})
			err := o.deliver(context.Background(), task{DeviceID: "device", Target: "voip"})
			if (err == nil) != tc.success {
				t.Fatalf("success=%v err=%v", tc.success, err)
			}
		})
	}
}
