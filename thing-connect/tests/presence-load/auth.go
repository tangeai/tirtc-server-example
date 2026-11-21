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
)

type credential struct{ id, key string }

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
		return "", errors.New("token transport failed")
	}
	defer resp.Body.Close()
	var body struct {
		Code int `json:"code"`
		Data struct {
			Token string `json:"mqtt_token"`
		} `json:"data"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&body) != nil || body.Code != 200 || body.Data.Token == "" {
		return "", errors.New("token request rejected")
	}
	return body.Data.Token, nil
}

func tokenClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
