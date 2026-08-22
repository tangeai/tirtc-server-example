package dynamicconfig

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"thing-connect/internal/config"
)

// ResolveTiRTC validates the blocking TiRTC configuration used at service
// startup. A published Admin value is authoritative; legacy YAML is consulted
// only when no database row has been published.
func ResolveTiRTC(snapshot Snapshot, fallback config.TirtcCfg) (config.TirtcCfg, error) {
	if snapshot.Revision == 0 {
		if err := ValidateTiRTC(fallback); err != nil {
			return config.TirtcCfg{}, errors.New("TiRTC 尚未在管理后台发布")
		}
		return fallback, nil
	}
	var value struct {
		Endpoint string `json:"endpoint"`
		AppID    string `json:"app_id"`
	}
	var secrets struct {
		AccessKeyID string `json:"access_key_id"`
		SecretKeyID string `json:"secret_key_id"`
	}
	if json.Unmarshal(snapshot.Value, &value) != nil || json.Unmarshal(snapshot.Secrets, &secrets) != nil {
		return config.TirtcCfg{}, errors.New("管理后台中的 TiRTC 配置格式无效")
	}
	resolved := config.TirtcCfg{
		Endpoint: strings.TrimSpace(value.Endpoint), AppID: strings.TrimSpace(value.AppID),
		AccessKeyID: strings.TrimSpace(secrets.AccessKeyID), SecretKeyID: strings.TrimSpace(secrets.SecretKeyID),
	}
	if err := ValidateTiRTC(resolved); err != nil {
		return config.TirtcCfg{}, err
	}
	return resolved, nil
}

func ValidateTiRTC(cfg config.TirtcCfg) error {
	if strings.TrimSpace(cfg.AppID) == "" || strings.TrimSpace(cfg.AccessKeyID) == "" || strings.TrimSpace(cfg.SecretKeyID) == "" {
		return errors.New("TiRTC 必须填写 App ID、Access Key ID 和 Secret Key ID")
	}
	if strings.TrimSpace(cfg.Endpoint) != "" {
		parsed, err := url.Parse(cfg.Endpoint)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
			return errors.New("TiRTC 服务地址必须是有效的 HTTP(S) 地址且不能包含账号")
		}
	}
	return nil
}
