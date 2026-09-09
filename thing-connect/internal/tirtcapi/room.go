package tirtcapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"thing-connect/internal/room"
	"time"
)

type RoomClient struct {
	config AgentAPIConfig
	http   *http.Client
}

func NewRoomClient(cfg AgentAPIConfig, client *http.Client) *RoomClient {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &RoomClient{cfg, client}
}
func (c *RoomClient) Issue(ctx context.Context, roomID, deviceID string) (room.Credential, error) {
	if c.config.AppID == "" || c.config.AccessKeyID == "" || c.config.SecretKeyID == "" {
		return room.Credential{}, room.ErrUnavailable
	}
	const path = "/v1/token/room"
	body, err := json.Marshal(map[string]string{"room_id": roomID, "device_id": deviceID})
	if err != nil {
		return room.Credential{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.config.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return room.Credential{}, err
	}
	request.Header = SignTGV1Request(c.config.SecretKeyID, c.config.AccessKeyID, c.config.AppID, http.MethodPost, path, "", body, "application/json", time.Now())
	response, err := c.http.Do(request)
	if err != nil {
		return room.Credential{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return room.Credential{}, fmt.Errorf("room token HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil {
		return room.Credential{}, err
	}
	if len(raw) > 16384 {
		return room.Credential{}, room.ErrUnavailable
	}
	var credential room.Credential
	if err = json.Unmarshal(raw, &credential); err != nil {
		return room.Credential{}, err
	}
	if credential.PeerID == "" || len(credential.PeerID) > 255 || credential.Token == "" || len(credential.Token) > 8192 {
		return room.Credential{}, room.ErrUnavailable
	}
	return credential, nil
}
