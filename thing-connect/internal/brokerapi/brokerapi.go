// Package brokerapi lists live device connections through the EMQX v5 HTTP
// API so device-server can restore its presence projection after lost events.
package brokerapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"thing-connect/internal/presence"
)

// Config is the YAML and Admin form. APISecret is an Admin secret and never
// part of the published value.
type Config struct {
	URL       string `json:"url" yaml:"url"`
	APIKey    string `json:"api_key" yaml:"api_key"`
	APISecret string `json:"-" yaml:"api_secret"`
}

const DefaultConfigJSON = `{"url":"","api_key":""}`

// ParseConfig strictly decodes a published value and its secrets.
func ParseConfig(value, secrets []byte) (Config, error) {
	var c Config
	d := json.NewDecoder(bytes.NewReader(value))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return Config{}, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return Config{}, errors.New("unexpected trailing broker API config")
	}
	if len(secrets) > 0 {
		var s struct {
			APISecret string `json:"api_secret"`
		}
		if err := json.Unmarshal(secrets, &s); err != nil {
			return Config{}, err
		}
		c.APISecret = s.APISecret
	}
	return c, c.Validate()
}

// Validate accepts an empty URL as "reconciliation disabled".
func (c Config) Validate() error {
	if strings.TrimSpace(c.URL) == "" {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(c.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("url must be an http(s) address without credentials, query or fragment")
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return errors.New("api_key is required when url is set")
	}
	return nil
}

const pageSize = 1000

// Client implements presence.BrokerClients.
type Client struct {
	base   string
	key    string
	secret string
	http   *http.Client
}

var _ presence.BrokerClients = (*Client)(nil)

// ErrDisabled reports a configuration without a broker API URL.
var ErrDisabled = errors.New("brokerapi: disabled")

// New returns ErrDisabled when the URL is empty.
func New(c Config) (*Client, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.URL) == "" {
		return nil, ErrDisabled
	}
	return &Client{
		base:   strings.TrimRight(strings.TrimSpace(c.URL), "/"),
		key:    c.APIKey,
		secret: c.APISecret,
		http:   &http.Client{Timeout: 15 * time.Second},
	}, nil
}

type clientPage struct {
	Data []struct {
		ClientID    string    `json:"clientid"`
		IPAddress   string    `json:"ip_address"`
		ConnectedAt time.Time `json:"connected_at"`
	} `json:"data"`
	Meta struct {
		HasNext bool `json:"hasnext"`
	} `json:"meta"`
}

// ListConnected pages through every connected client. Concurrent connects can
// shift pages; a missed device recovers through its next heartbeat.
func (c *Client) ListConnected(ctx context.Context, visit func([]presence.Observation) error) error {
	for page := 1; ; page++ {
		body, err := c.page(ctx, page)
		if err != nil {
			return err
		}
		now := time.Now()
		batch := make([]presence.Observation, 0, len(body.Data))
		for _, item := range body.Data {
			if o, ok := presence.Connected(item.ClientID, item.IPAddress, item.ConnectedAt, now); ok {
				batch = append(batch, o)
			}
		}
		if len(batch) > 0 {
			if err := visit(batch); err != nil {
				return err
			}
		}
		if !body.Meta.HasNext {
			return nil
		}
	}
}

func (c *Client) page(ctx context.Context, page int) (clientPage, error) {
	q := url.Values{}
	q.Set("conn_state", "connected")
	q.Set("fields", "clientid,ip_address,connected_at")
	q.Set("limit", fmt.Sprint(pageSize))
	q.Set("page", fmt.Sprint(page))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/v5/clients?"+q.Encode(), nil)
	if err != nil {
		return clientPage{}, err
	}
	req.SetBasicAuth(c.key, c.secret)
	resp, err := c.http.Do(req)
	if err != nil {
		return clientPage{}, fmt.Errorf("broker api: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return clientPage{}, fmt.Errorf("broker api: page %d status %d", page, resp.StatusCode)
	}
	var body clientPage
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&body); err != nil {
		return clientPage{}, fmt.Errorf("broker api: page %d: %w", page, err)
	}
	return body, nil
}

// Source holds the active client for runtime config changes.
type Source struct{ current atomic.Pointer[Client] }

// Update replaces the client atomically. An empty URL disables listing.
func (s *Source) Update(c Config) error {
	client, err := New(c)
	if errors.Is(err, ErrDisabled) {
		s.current.Store(nil)
		return nil
	}
	if err != nil {
		return err
	}
	s.current.Store(client)
	return nil
}

// Current returns nil while no broker API is configured.
func (s *Source) Current() presence.BrokerClients {
	if client := s.current.Load(); client != nil {
		return client
	}
	return nil
}
