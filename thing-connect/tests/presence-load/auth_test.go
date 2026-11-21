package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/eclipse/paho.mqtt.golang/packets"
)

func TestRealMQTTLossRefreshesTokenAndStopsOnPermanentRejection(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"code":200,"data":{"mqtt_token":"signed-token"}}`))
		} else {
			w.WriteHeader(410)
		}
	}))
	defer server.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connected := make(chan struct{}, 1)
	brokerDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			brokerDone <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
		packet, err := packets.ReadPacket(conn)
		if err != nil {
			brokerDone <- err
			return
		}
		connect, ok := packet.(*packets.ConnectPacket)
		if !ok || connect.Username != "d" || string(connect.Password) != "signed-token" {
			brokerDone <- errors.New("invalid CONNECT credentials")
			return
		}
		ack := packets.NewControlPacket(packets.Connack).(*packets.ConnackPacket)
		if err := ack.Write(conn); err != nil {
			brokerDone <- err
			return
		}
		select {
		case <-connected:
			brokerDone <- nil
		case <-ctx.Done():
			brokerDone <- ctx.Err()
		}
		// Closing a successfully authenticated connection exercises Paho's
		// actual loss callback, not a synthetic channel notification.
	}()
	f := &fleet{opts: options{tokenURL: server.URL}, credentials: []credential{{"d", "secret"}}, authSlots: make(chan struct{}, 1), http: tokenClient(), clients: make([]mqtt.Client, 1)}
	defer f.http.CloseIdleConnections()
	opts := mqtt.NewClientOptions().AddBroker("tcp://" + listener.Addr().String()).SetUsername("d").SetClientID("sn_d").SetConnectTimeout(time.Second)
	opts.SetOnConnectHandler(func(mqtt.Client) { f.connects.Add(1); connected <- struct{}{} })
	f.runRealDevice(ctx, 0, opts)
	_ = listener.Close()
	if err := <-brokerDone; err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != nil || requests.Load() != 2 || f.connects.Load() != 1 || f.losses.Load() != 1 || f.authenticationStopped.Load() != 1 {
		t.Fatalf("ctx=%v requests=%d connects=%d losses=%d stopped=%d", ctx.Err(), requests.Load(), f.connects.Load(), f.losses.Load(), f.authenticationStopped.Load())
	}
}

func TestPermanentTokenRejectionsNeverConnectOrRetry(t *testing.T) {
	for _, tc := range []struct{ status, code int }{{401, 6008}, {410, 6006}, {400, 40000}, {200, 6008}, {200, 6006}} {
		t.Run(fmt.Sprintf("%d_%d", tc.status, tc.code), func(t *testing.T) {
			var requests atomic.Int64
			connects, waits := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprintf(w, `{"code":%d}`, tc.code)
			}))
			defer server.Close()
			client := tokenClient()
			defer client.CloseIdleConnections()
			err := runAuthenticated(context.Background(), func(ctx context.Context) (string, error) {
				return fetchToken(ctx, client, server.URL, credential{"d", "secret"})
			}, func(context.Context, string) (loadSession, error) { connects++; return loadSession{}, nil }, func(context.Context, time.Duration) bool { waits++; return true })
			if !errors.Is(err, errTokenRejected) || requests.Load() != 1 || connects != 0 || waits != 0 {
				t.Fatalf("err=%v requests=%d connects=%d waits=%d", err, requests.Load(), connects, waits)
			}
		})
	}
}

func TestTransientTokenFailuresRecoverAndRefreshAfterDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var requests atomic.Int64
	connects, closed, waits := 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := requests.Add(1)
		switch n {
		case 1:
			w.WriteHeader(429)
		case 2:
			w.WriteHeader(503)
		default:
			_, _ = fmt.Fprintf(w, `{"code":200,"data":{"mqtt_token":"token-%d"}}`, n)
		}
	}))
	defer server.Close()
	client := tokenClient()
	defer client.CloseIdleConnections()
	err := runAuthenticated(ctx, func(ctx context.Context) (string, error) {
		return fetchToken(ctx, client, server.URL, credential{"d", "secret"})
	}, func(_ context.Context, token string) (loadSession, error) {
		connects++
		if token != fmt.Sprintf("token-%d", connects+2) {
			t.Errorf("stale or empty token: %s", token)
		}
		lost := make(chan struct{})
		if connects == 1 {
			close(lost)
		} else {
			cancel()
		}
		return loadSession{lost: lost, close: func() { closed++ }}, nil
	}, func(context.Context, time.Duration) bool { waits++; return true })
	if !errors.Is(err, context.Canceled) || requests.Load() != 4 || connects != 2 || closed != 2 || waits != 3 {
		t.Fatalf("err=%v requests=%d connects=%d closed=%d waits=%d", err, requests.Load(), connects, closed, waits)
	}
}

func TestRealDevicePermanentFailureCreatesNoMQTTClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) }))
	defer server.Close()
	f := &fleet{opts: options{tokenURL: server.URL}, credentials: []credential{{"d", "secret"}}, authSlots: make(chan struct{}, 1), http: tokenClient(), clients: make([]mqtt.Client, 1)}
	defer f.http.CloseIdleConnections()
	f.runRealDevice(context.Background(), 0, mqtt.NewClientOptions().AddBroker("tcp://127.0.0.1:1"))
	if f.client(0) != nil || f.tokenRequests.Load() != 1 || f.authenticationStopped.Load() != 1 {
		t.Fatal("permanent failure entered MQTT or was not recorded")
	}
}

func TestAuthenticationRetryCapAndCancellation(t *testing.T) {
	requests, waits := 0, 0
	err := runAuthenticated(context.Background(), func(context.Context) (string, error) { requests++; return "", errTokenTemporary }, func(context.Context, string) (loadSession, error) {
		t.Fatal("connect without token")
		return loadSession{}, nil
	}, func(_ context.Context, d time.Duration) bool {
		waits++
		if d < time.Second || d > 120*time.Second {
			t.Errorf("invalid backoff %s", d)
		}
		return true
	})
	if !errors.Is(err, errTokenTemporary) || requests != 8 || waits != 7 {
		t.Fatalf("err=%v requests=%d waits=%d", err, requests, waits)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if waitRetry(ctx, time.Minute) || time.Since(start) > time.Second {
		t.Fatal("retry wait ignored cancellation")
	}
}

func TestRealTokenSigningAndBusinessFailure(t *testing.T) {
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mac := hmac.New(sha256.New, []byte("test-secret"))
		_, _ = mac.Write([]byte("d1" + r.Header.Get("X-Timestamp") + r.Header.Get("X-Nonce")))
		if r.Method != "POST" || r.Header.Get("X-Device-Id") != "d1" || r.Header.Get("X-Signature") != base64.StdEncoding.EncodeToString(mac.Sum(nil)) {
			t.Error("invalid signed token request")
		}
		nonce := r.Header.Get("X-Nonce")
		if len(nonce) != 32 || seen[nonce] {
			t.Error("nonce reused")
		}
		seen[nonce] = true
		if len(seen) == 1 {
			_, _ = w.Write([]byte(`{"code":200,"data":{"mqtt_token":"signed-token"}}`))
		} else {
			_, _ = w.Write([]byte(`{"code":6006,"msg":"private detail"}`))
		}
	}))
	defer server.Close()
	c := tokenClient()
	defer c.CloseIdleConnections()
	token, err := fetchToken(context.Background(), c, server.URL, credential{"d1", "test-secret"})
	if err != nil || token != "signed-token" {
		t.Fatalf("token=%q err=%v", token, err)
	}
	if _, err := fetchToken(context.Background(), c, server.URL, credential{"d1", "test-secret"}); err == nil || err.Error() != "token request rejected" {
		t.Fatalf("business failure: %v", err)
	}
}

func TestTokenDoesNotForwardCredentialsAndHonorsCancellation(t *testing.T) {
	forwarded := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	c := tokenClient()
	defer c.CloseIdleConnections()
	if _, err := fetchToken(context.Background(), c, redirect.URL, credential{"d1", "secret"}); err == nil || forwarded {
		t.Fatal("redirect accepted")
	}
	hung := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer hung.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := fetchToken(ctx, c, hung.URL, credential{"d1", "secret"}); err == nil || time.Since(start) > time.Second {
		t.Fatal("caller deadline ignored")
	}
}

func TestFixtureShardsHaveDisjointIdentities(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.csv")
	if err := os.WriteFile(path, []byte("device_id,device_key\na,1\nb,2\nc,3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := loadCredentials(path, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	b, err := loadCredentials(path, 1, 2)
	if err != nil || len(a) != 2 || len(b) != 1 || a[0].id != "a" || a[1].id != "c" || b[0].id != "b" {
		t.Fatal("incorrect shards")
	}
}
