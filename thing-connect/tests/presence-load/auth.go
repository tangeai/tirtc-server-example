package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type credential struct{ id, key string }

var (
	errTokenRejected  = errors.New("token request rejected")
	errTokenTemporary = errors.New("token request temporarily unavailable")
)

func loadCredentials(path string, shard, shards int) ([]credential, error) {
	if shards < 1 || shard < 0 || shard >= shards {
		return nil, errors.New("invalid fixture shard")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	r := csv.NewReader(f)
	header, err := r.Read()
	if err != nil || len(header) != 2 || header[0] != "device_id" || header[1] != "device_key" {
		return nil, errors.New("fixture requires device_id,device_key columns")
	}
	seen := map[string]bool{}
	var result []credential
	for n := 0; ; n++ {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil || len(row) != 2 || row[0] == "" || row[1] == "" || seen[row[0]] {
			return nil, errors.New("invalid or duplicate fixture row")
		}
		seen[row[0]] = true
		if n%shards == shard {
			result = append(result, credential{row[0], row[1]})
		}
	}
	if len(result) == 0 {
		return nil, errors.New("fixture shard is empty")
	}
	return result, nil
}

func validateTokenURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("token-url must be HTTP(S) without credentials, query or fragment")
	}
	return nil
}

// One bounded request per MQTT connection attempt, including reconnects after
// token expiry. No response body or credentials are returned in errors.
func fetchToken(ctx context.Context, client *http.Client, endpoint string, c credential) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", errors.New("nonce generation failed")
	}
	stamp, nonceHex := strconv.FormatInt(time.Now().Unix(), 10), hex.EncodeToString(nonce)
	mac := hmac.New(sha256.New, []byte(c.key))
	_, _ = mac.Write([]byte(c.id + stamp + nonceHex))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return "", errors.New("invalid token request")
	}
	req.Header.Set("X-Device-Id", c.id)
	req.Header.Set("X-Timestamp", stamp)
	req.Header.Set("X-Nonce", nonceHex)
	req.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errTokenTemporary
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return "", errTokenTemporary
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			Token string `json:"mqtt_token"`
		} `json:"data"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&body) != nil {
		return "", errTokenRejected
	}
	if body.Code == 429 || body.Code == 50000 {
		return "", errTokenTemporary
	}
	if body.Code != 200 || body.Data.Token == "" {
		return "", errTokenRejected
	}
	return body.Data.Token, nil
}

type loadSession struct {
	lost  <-chan struct{}
	close func()
}

// One owner handles token acquisition and MQTT reconnection. Permanent token
// rejection never reaches MQTT; each outage has at most eight attempts.
func runAuthenticated(ctx context.Context, fetch func(context.Context) (string, error), connect func(context.Context, string) (loadSession, error), wait func(context.Context, time.Duration) bool) error {
	base, ceiling := randomDuration(2*time.Second, 4*time.Second), randomDuration(60*time.Second, 120*time.Second)
	attempts := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		token, err := fetch(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil && token == "" {
			err = errTokenRejected
		}
		if err == nil {
			var session loadSession
			session, err = connect(ctx, token)
			if err == nil {
				attempts = 0
				select {
				case <-ctx.Done():
					session.close()
					return ctx.Err()
				case <-session.lost:
					session.close()
				}
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			attempts++
			if errors.Is(err, errTokenRejected) || attempts >= 8 {
				return err
			}
		}
		upper := min(ceiling, base*time.Duration(1<<min(6, max(0, attempts-1))))
		if !wait(ctx, randomDuration(max(time.Second, upper/2), upper)) {
			return ctx.Err()
		}
	}
}

func waitRetry(ctx context.Context, delay time.Duration) bool {
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (f *fleet) client(index int) mqtt.Client {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clients[index]
}

func (f *fleet) runRealDevice(ctx context.Context, index int, base *mqtt.ClientOptions) {
	fetch := func(ctx context.Context) (string, error) {
		select {
		case f.authSlots <- struct{}{}:
		default:
			f.tokenRejected.Add(1)
			return "", errTokenTemporary
		}
		defer func() { <-f.authSlots }()
		f.tokenRequests.Add(1)
		token, err := fetchToken(ctx, f.http, f.opts.tokenURL, f.credentials[index])
		if err != nil {
			f.tokenFailures.Add(1)
		}
		return token, err
	}
	connect := func(ctx context.Context, token string) (loadSession, error) {
		attemptCtx, cancel := context.WithCancel(ctx)
		lost := make(chan struct{}, 1)
		opts := *base
		opts.SetAutoReconnect(false).SetConnectRetry(false).SetPassword(token)
		opts.SetOnConnectHandler(func(c mqtt.Client) {
			if attemptCtx.Err() != nil {
				c.Disconnect(0)
				return
			}
			base.OnConnect(c)
		})
		opts.SetConnectionLostHandler(func(_ mqtt.Client, _ error) {
			f.losses.Add(1)
			select {
			case lost <- struct{}{}:
			default:
			}
		})
		// Paho warns against reusing a client after Disconnect. Each successful
		// token acquisition gets a fresh client and its own loss notification.
		client := mqtt.NewClient(&opts)
		f.mu.Lock()
		f.clients[index] = client
		f.mu.Unlock()
		session := loadSession{lost: lost, close: func() { cancel(); client.Disconnect(0) }}
		if !waitLoadToken(ctx, client.Connect()) {
			session.close()
			f.connectFailures.Add(1)
			return loadSession{}, errors.New("MQTT connection failed")
		}
		return session, nil
	}
	if err := runAuthenticated(ctx, fetch, connect, waitRetry); err != nil && ctx.Err() == nil {
		f.authenticationStopped.Add(1)
	}
}

func tokenClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
