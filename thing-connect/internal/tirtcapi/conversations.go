package tirtcapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"thing-connect/internal/ai/conversations"
)

func (c *AgentAPIClient) Scope() string {
	sum := sha256.Sum256([]byte(strings.TrimRight(c.cfg.BaseURL, "/") + "\x00" + c.cfg.AppID))
	return hex.EncodeToString(sum[:])
}
func (c *AgentAPIClient) ListConversations(ctx context.Context, device string, page, size int) ([]conversations.Entry, error) {
	var items []conversations.Entry
	err := c.conversationRequest(ctx, http.MethodGet, "list", url.Values{"device_id": {device}, "page": {strconv.Itoa(page)}, "page_size": {strconv.Itoa(size)}}, &items)
	return items, err
}
func (c *AgentAPIClient) ConversationDetail(ctx context.Context, id string) ([]conversations.Message, error) {
	var messages []conversations.Message
	err := c.conversationRequest(ctx, http.MethodGet, "detail", url.Values{"id": {id}}, &messages)
	return messages, err
}
func (c *AgentAPIClient) DeleteConversation(ctx context.Context, id string) error {
	return c.conversationRequest(ctx, http.MethodDelete, "delete", url.Values{"id": {id}}, nil)
}
func (c *AgentAPIClient) conversationRequest(ctx context.Context, method, action string, query url.Values, out any) error {
	path := "/ai/aigcrtc/conversation/" + action
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.cfg.BaseURL, "/")+path+"?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	c.signRequest(req, method, path, query.Encode(), nil)
	res, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	const limit = 4 << 20
	body, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return err
	}
	if len(body) > limit {
		return fmt.Errorf("conversation response exceeds limit")
	}
	var envelope struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if res.StatusCode == http.StatusNotFound {
		return conversations.ErrNotFound
	}
	if err = json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("invalid conversation response")
	}
	if envelope.Code == 404 || envelope.Code == 40400 {
		return conversations.ErrNotFound
	}
	if res.StatusCode != http.StatusOK || envelope.Code != 200 {
		return fmt.Errorf("conversation upstream HTTP %d code %d", res.StatusCode, envelope.Code)
	}
	if out != nil {
		if len(envelope.Data) == 0 {
			return fmt.Errorf("missing conversation data")
		}
		return json.Unmarshal(envelope.Data, out)
	}
	return nil
}
