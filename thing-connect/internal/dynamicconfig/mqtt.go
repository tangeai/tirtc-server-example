package dynamicconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"thing-connect/internal/config"
)

var ErrMQTTNotConfigured = errors.New("MQTT 连接尚未在管理后台发布")

type SnapshotLoader interface {
	Load(context.Context, string, string) (Snapshot, error)
}

// ResolveMQTT loads the restart-only MQTT connection for one service. Static
// YAML remains a compatibility fallback only when Admin has no published row;
// once a row exists it is authoritative and cannot silently fall back.
func ResolveMQTT(ctx context.Context, loader SnapshotLoader, namespace string, fallback config.MQTTCfg) (config.MQTTCfg, int64, error) {
	if loader == nil {
		return config.MQTTCfg{}, 0, errors.New("dynamic config client is unavailable")
	}
	snapshot, err := loader.Load(ctx, namespace, "mqtt.connection")
	if err != nil {
		return config.MQTTCfg{}, 0, fmt.Errorf("读取 %s/mqtt.connection 失败: %w", namespace, err)
	}
	if snapshot.Revision == 0 {
		if strings.TrimSpace(fallback.Broker) == "" {
			return config.MQTTCfg{}, 0, ErrMQTTNotConfigured
		}
		if err := ValidateMQTT(fallback); err != nil {
			return config.MQTTCfg{}, 0, fmt.Errorf("config.yaml 中的 MQTT 配置无效: %w", err)
		}
		return fallback, 0, nil
	}
	var value struct {
		Broker   string `json:"broker"`
		AuthMode string `json:"auth_mode"`
		Username string `json:"username"`
		ClientID string `json:"client_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(snapshot.Value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return config.MQTTCfg{}, snapshot.Revision, errors.New("管理后台中的 MQTT 连接格式无效")
	}
	var secrets struct {
		Password string `json:"password"`
	}
	decoder = json.NewDecoder(bytes.NewReader(snapshot.Secrets))
	decoder.DisallowUnknownFields()
	if len(snapshot.Secrets) == 0 || decoder.Decode(&secrets) != nil {
		return config.MQTTCfg{}, snapshot.Revision, errors.New("管理后台中的 MQTT 密码未配置或格式无效")
	}
	resolved := config.MQTTCfg{Broker: strings.TrimSpace(value.Broker), Password: secrets.Password}
	switch value.AuthMode {
	case "username":
		if strings.TrimSpace(value.ClientID) != "" {
			return config.MQTTCfg{}, snapshot.Revision, errors.New("Username 认证不能同时配置固定 ClientID")
		}
		resolved.Username = strings.TrimSpace(value.Username)
	case "clientid":
		if strings.TrimSpace(value.Username) != "" {
			return config.MQTTCfg{}, snapshot.Revision, errors.New("固定 ClientID 认证不能同时配置 Username")
		}
		resolved.ClientID = strings.TrimSpace(value.ClientID)
	default:
		return config.MQTTCfg{}, snapshot.Revision, errors.New("管理后台中的 MQTT 认证方式无效")
	}
	if err := ValidateMQTT(resolved); err != nil {
		return config.MQTTCfg{}, snapshot.Revision, err
	}
	return resolved, snapshot.Revision, nil
}

func ValidateMQTT(cfg config.MQTTCfg) error {
	parsed, err := url.Parse(strings.TrimSpace(cfg.Broker))
	if err != nil || (parsed.Scheme != "mqtt" && parsed.Scheme != "mqtts") || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("MQTT Broker 必须是 mqtt:// 或 mqtts:// 地址，且不能包含账号、路径、查询参数或片段")
	}
	if strings.TrimSpace(cfg.Password) == "" {
		return errors.New("MQTT 密码不能为空")
	}
	hasUsername := strings.TrimSpace(cfg.Username) != ""
	hasClientID := strings.TrimSpace(cfg.ClientID) != ""
	if hasUsername == hasClientID {
		return errors.New("MQTT Username 和固定 ClientID 必须且只能填写一个")
	}
	return nil
}
