package dynamicconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const eventChannel = "thingconnect:admin:config-events"

type Snapshot struct {
	Value    json.RawMessage `json:"value"`
	Secrets  json.RawMessage `json:"secrets"`
	Revision int64           `json:"revision"`
}

type apiResponse struct {
	Code int      `json:"code"`
	Msg  string   `json:"msg"`
	Data Snapshot `json:"data"`
}

type Ref struct {
	Namespace string
	Key       string
	Apply     func(Snapshot) error
}

type Client struct {
	baseURL string
	key     string
	http    *http.Client
	redis   *redis.Client
	mu      sync.RWMutex
	applied map[string]int64
}

func New(serverURL, internalKey string, redisClient *redis.Client) (*Client, error) {
	serverURL = strings.TrimRight(strings.TrimSpace(serverURL), "/")
	if serverURL == "" {
		return nil, errors.New("admin.server_url is empty")
	}
	parsed, err := url.Parse(serverURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("admin.server_url must be an HTTP(S) URL without userinfo")
	}
	if len(internalKey) < 32 {
		return nil, errors.New("internal.key must be at least 32 characters")
	}
	return &Client{baseURL: serverURL, key: internalKey, http: &http.Client{Timeout: 5 * time.Second}, redis: redisClient, applied: map[string]int64{}}, nil
}

func (c *Client) Load(ctx context.Context, namespace, key string) (Snapshot, error) {
	endpoint := c.baseURL + "/v1/internal/configs/" + url.PathEscape(namespace) + "/" + url.PathEscape(key) + "?scope_type=global"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Snapshot{}, err
	}
	request.Header.Set("X-Internal-Key", c.key)
	response, err := c.http.Do(request)
	if err != nil {
		return Snapshot{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	if err != nil {
		return Snapshot{}, err
	}
	if response.StatusCode != http.StatusOK {
		return Snapshot{}, fmt.Errorf("admin config returned HTTP %d", response.StatusCode)
	}
	var envelope apiResponse
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Code != 200 {
		return Snapshot{}, errors.New("admin config returned an invalid response")
	}
	return envelope.Data, nil
}

func (c *Client) Run(ctx context.Context, refs []Ref) {
	byID := make(map[string]Ref, len(refs))
	for _, ref := range refs {
		byID[ref.Namespace+"/"+ref.Key] = ref
		_ = c.apply(ctx, ref, true)
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	pubsub := c.redis.Subscribe(ctx, eventChannel)
	defer func() { _ = pubsub.Close() }()
	channel := pubsub.Channel(redis.WithChannelSize(100))
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, ref := range refs {
				_ = c.apply(ctx, ref, false)
			}
		case message, ok := <-channel:
			if !ok {
				return
			}
			var event struct {
				Namespace string `json:"namespace"`
				ConfigKey string `json:"config_key"`
				Revision  int64  `json:"revision"`
			}
			if json.Unmarshal([]byte(message.Payload), &event) == nil {
				if ref, exists := byID[event.Namespace+"/"+event.ConfigKey]; exists {
					_ = c.apply(ctx, ref, false)
				}
			}
		}
	}
}

// ApplyInitial loads every registered value before a service starts serving
// requests. This prevents a missing or slow Admin connection from silently
// falling back to similarly named values in config.yaml.
func (c *Client) ApplyInitial(ctx context.Context, refs []Ref) error {
	for _, ref := range refs {
		if err := c.apply(ctx, ref, true); err != nil {
			return fmt.Errorf("load initial config %s/%s: %w", ref.Namespace, ref.Key, err)
		}
	}
	return nil
}

func (c *Client) apply(ctx context.Context, ref Ref, initial bool) error {
	snapshot, err := c.Load(ctx, ref.Namespace, ref.Key)
	if err != nil {
		if !initial {
			slog.WarnContext(ctx, "reload dynamic config failed", "namespace", ref.Namespace, "config_key", ref.Key, "err", err)
		}
		return err
	}
	id := ref.Namespace + "/" + ref.Key
	c.mu.RLock()
	current, applied := c.applied[id]
	c.mu.RUnlock()
	if applied && snapshot.Revision <= current {
		return nil
	}
	if err := ref.Apply(snapshot); err != nil {
		slog.ErrorContext(ctx, "apply dynamic config failed", "namespace", ref.Namespace, "config_key", ref.Key, "revision", snapshot.Revision, "err", err)
		return err
	}
	c.mu.Lock()
	c.applied[id] = snapshot.Revision
	c.mu.Unlock()
	return nil
}

func (c *Client) Revisions() map[string]int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make(map[string]int64, len(c.applied))
	for key, revision := range c.applied {
		result[key] = revision
	}
	return result
}
