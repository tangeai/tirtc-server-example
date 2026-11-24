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
	"slices"
	"strconv"
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
	Meta *struct {
		HasNext *bool  `json:"hasnext"`
		Cursor  string `json:"cursor"`
	} `json:"meta"`
}

func (p clientPage) project(visit func([]presence.Observation) error) error {
	now := time.Now()
	batch := make([]presence.Observation, 0, len(p.Data))
	for _, item := range p.Data {
		if o, ok := presence.Connected(item.ClientID, item.IPAddress, item.ConnectedAt, now); ok {
			batch = append(batch, o)
		}
	}
	if len(batch) > 0 {
		return visit(batch)
	}
	return nil
}

var errNoCursorAPI = errors.New("broker api: cursor endpoint unavailable")

// ListConnected follows the broker's continuation across nodes. An offset
// would change when another client disconnects between two requests.
func (c *Client) ListConnected(ctx context.Context, visit func([]presence.Observation) error) error {
	q := url.Values{"conn_state": {"connected"}, "limit": {strconv.Itoa(pageSize)}, "fields": {"clientid,ip_address,connected_at"}}
	seen := make(map[string]struct{})
	for {
		page, err := c.page(ctx, "clients_v2", q)
		if errors.Is(err, errNoCursorAPI) && len(seen) == 0 {
			// Older EMQX releases only support the timestamp-filtered API.
			return c.listWindow(ctx, q, 0, maxConnectionMS, visit)
		}
		if err != nil {
			return err
		}
		if err := page.project(visit); err != nil {
			return err
		}
		cursor := page.Meta.Cursor
		if cursor == "" {
			if page.Meta.HasNext != nil && *page.Meta.HasNext {
				return errors.New("broker api: missing continuation cursor")
			}
			return nil
		}
		if _, ok := seen[cursor]; ok {
			return errors.New("broker api: repeated continuation cursor")
		}
		seen[cursor] = struct{}{}
		q.Set("cursor", cursor)
	}
}

// Largest millisecond timestamp representable by EMQX's RFC3339 output.
const maxConnectionMS int64 = 253402300799999

// Split overflowing ranges into disjoint whole milliseconds. Each accepted
// range is obtained in one request, so departures in a previous range do
// not shift any unseen client. Never clear a loss on an unpageable range.
func (c *Client) listWindow(ctx context.Context, q url.Values, lo, hi int64, visit func([]presence.Observation) error) error {
	q.Set("page", "1")
	q.Del("gte_connected_at")
	q.Del("lte_connected_at")
	if lo > 0 {
		q.Set("gte_connected_at", strconv.FormatInt(lo, 10))
	}
	if hi < maxConnectionMS {
		q.Set("lte_connected_at", strconv.FormatInt(hi, 10))
	}
	page, err := c.page(ctx, "clients", q)
	if err != nil {
		return err
	}
	if page.Meta.HasNext == nil {
		return errors.New("broker api: missing pagination metadata")
	}
	if !*page.Meta.HasNext {
		return page.project(visit)
	}
	if lo == hi || len(page.Data) == 0 {
		return errors.New("broker api: a connection-time window exceeds one page; cursor API required")
	}
	// Times outside the window (second precision, missing field) are clamped:
	// the pivot only has to stay inside [lo, hi) for the split to progress.
	timestamps := make([]int64, len(page.Data))
	for i, row := range page.Data {
		timestamps[i] = min(max(row.ConnectedAt.UnixMilli(), lo), hi)
	}
	slices.Sort(timestamps)
	pivot := min(timestamps[len(timestamps)/2], hi-1)
	if err := c.listWindow(ctx, q, lo, pivot, visit); err != nil {
		return err
	}
	return c.listWindow(ctx, q, pivot+1, hi, visit)
}

func (c *Client) page(ctx context.Context, endpoint string, q url.Values) (clientPage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/v5/"+endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return clientPage{}, err
	}
	req.SetBasicAuth(c.key, c.secret)
	resp, err := c.http.Do(req)
	if err != nil {
		return clientPage{}, fmt.Errorf("broker api: %w", err)
	}
	defer resp.Body.Close()
	// Releases without the cursor endpoint answer it with 404, 400 or 405, or
	// with a non-listing body (dashboard fallback page). Authentication and
	// server errors still fail so a misconfiguration is never masked.
	cursorMissing := endpoint == "clients_v2"
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		switch resp.StatusCode {
		case http.StatusNotFound, http.StatusBadRequest, http.StatusMethodNotAllowed:
			if cursorMissing {
				return clientPage{}, fmt.Errorf("%w: status %d", errNoCursorAPI, resp.StatusCode)
			}
		}
		return clientPage{}, fmt.Errorf("broker api: %s status %d", endpoint, resp.StatusCode)
	}
	var body clientPage
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&body); err != nil {
		if cursorMissing {
			return clientPage{}, fmt.Errorf("%w: %v", errNoCursorAPI, err)
		}
		return clientPage{}, fmt.Errorf("broker api: %s: %w", endpoint, err)
	}
	if body.Data == nil || body.Meta == nil {
		if cursorMissing {
			return clientPage{}, fmt.Errorf("%w: incomplete listing response", errNoCursorAPI)
		}
		return clientPage{}, errors.New("broker api: incomplete listing response")
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
