package handler

import (
	"context"
	"fmt"

	"thing-connect/internal/tirtcapi"
)

// buildConnectToken issues a TiRTC token that authorizes connecting to
// remoteDeviceID, signed with that device's own device_key (see
// tirtcapi.BuildDeviceToken for why the device-specific secret matters).
func (s *Server) buildConnectToken(ctx context.Context, remoteDeviceID string) (string, error) {
	pool, err := s.dev.GetDeviceKey(ctx, remoteDeviceID)
	if err != nil {
		return "", fmt.Errorf("buildConnectToken: GetDeviceKey: %w", err)
	}
	if pool == nil {
		return "", fmt.Errorf("buildConnectToken: device %s has no key", remoteDeviceID)
	}
	cfg := s.Config()
	return tirtcapi.BuildDeviceToken(cfg.Tirtc.AccessKeyID, cfg.Tirtc.SecretKeyID, pool.DeviceKey, remoteDeviceID)
}
