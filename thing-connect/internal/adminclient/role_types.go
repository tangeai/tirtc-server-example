package adminclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"thing-connect/internal/ai/roletypes"
)

// RoleTypes reads the Admin-owned directory without creating a second database owner.
type RoleTypes struct {
	baseURL, key string
	http         *http.Client
}

func NewRoleTypes(baseURL, key string) (*RoleTypes, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || key == "" {
		return nil, fmt.Errorf("invalid role type catalog connection")
	}
	return &RoleTypes{baseURL: strings.TrimRight(baseURL, "/"), key: key, http: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *RoleTypes) Published(ctx context.Context) ([]roletypes.Type, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/internal/ai-role-types", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Internal-Key", c.key)
	res, err := c.http.Do(req)
	if err != nil {
		return nil, roletypes.ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, roletypes.ErrUnavailable
	}
	var envelope struct {
		Code int `json:"code"`
		Data struct {
			Items []roletypes.Type `json:"items"`
		} `json:"data"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&envelope); err != nil || envelope.Code != 200 || envelope.Data.Items == nil {
		return nil, roletypes.ErrUnavailable
	}
	return envelope.Data.Items, nil
}
