package installer

import (
	"fmt"
	"net/url"
	"strings"
)

const (
	mqttAuthUsername = "username"
	mqttAuthClientID = "clientid"
)

type normalizedMQTTAuth struct {
	broker    string
	mode      string
	username  string
	clientIDs map[string]string
	password  string
}

// normalizeMQTTAuth is the single installer boundary for MQTT authentication.
// A missing auth_mode keeps the original username-only setup payload compatible.
func normalizeMQTTAuth(input MQTTInput, optional []string) (normalizedMQTTAuth, error) {
	broker := strings.TrimSpace(input.Broker)
	parsed, err := url.Parse(broker)
	if err != nil || (parsed.Scheme != "mqtt" && parsed.Scheme != "mqtts" && parsed.Scheme != "tcp" && parsed.Scheme != "ssl") || parsed.Host == "" || parsed.User != nil {
		return normalizedMQTTAuth{}, fmt.Errorf("%w: MQTT Broker 必须是 mqtt:// 或 mqtts:// 地址且不能包含账号", ErrInvalidInput)
	}

	services, err := enabledMQTTServices(optional)
	if err != nil {
		return normalizedMQTTAuth{}, err
	}
	mode := strings.ToLower(strings.TrimSpace(input.AuthMode))
	username := strings.TrimSpace(input.Username)
	if mode == "" {
		switch {
		case username != "" && len(input.ClientIDs) == 0:
			mode = mqttAuthUsername
		case username == "" && len(input.ClientIDs) > 0:
			mode = mqttAuthClientID
		default:
			return normalizedMQTTAuth{}, fmt.Errorf("%w: MQTT 认证方式必须是 username 或 clientid", ErrInvalidInput)
		}
	}

	auth := normalizedMQTTAuth{
		broker: broker, mode: mode, username: username, password: input.Password,
		clientIDs: make(map[string]string, len(input.ClientIDs)),
	}
	switch mode {
	case mqttAuthUsername:
		if username == "" {
			return normalizedMQTTAuth{}, fmt.Errorf("%w: username 认证模式下 MQTT 用户名不能为空", ErrInvalidInput)
		}
		if len(input.ClientIDs) != 0 {
			return normalizedMQTTAuth{}, fmt.Errorf("%w: username 认证模式不能同时填写 client_ids", ErrInvalidInput)
		}
	case mqttAuthClientID:
		if username != "" {
			return normalizedMQTTAuth{}, fmt.Errorf("%w: clientid 认证模式不能同时填写 username", ErrInvalidInput)
		}
		known := make(map[string]bool, len(serviceCatalog))
		for _, service := range serviceCatalog {
			if service.UsesMQTT {
				known[service.Name] = true
			}
		}
		for service, clientID := range input.ClientIDs {
			if !known[service] {
				return normalizedMQTTAuth{}, fmt.Errorf("%w: client_ids 包含未知或不使用 MQTT 的服务 %s", ErrInvalidInput, service)
			}
			auth.clientIDs[service] = strings.TrimSpace(clientID)
		}
		seen := make(map[string]string, len(services))
		for _, service := range services {
			clientID := auth.clientIDs[service.Name]
			if clientID == "" {
				return normalizedMQTTAuth{}, fmt.Errorf("%w: clientid 认证模式缺少 %s 的 ClientID", ErrInvalidInput, service.DisplayName)
			}
			if previous := seen[clientID]; previous != "" {
				return normalizedMQTTAuth{}, fmt.Errorf("%w: %s 与 %s 不能使用相同的 MQTT ClientID", ErrInvalidInput, previous, service.DisplayName)
			}
			seen[clientID] = service.DisplayName
		}
	default:
		return normalizedMQTTAuth{}, fmt.Errorf("%w: MQTT 认证方式必须是 username 或 clientid", ErrInvalidInput)
	}
	return auth, nil
}

func (a normalizedMQTTAuth) configFor(service string) map[string]any {
	config := map[string]any{"broker": a.broker, "password": a.password}
	if a.mode == mqttAuthUsername {
		config["username"] = a.username
	} else {
		config["client_id"] = a.clientIDs[service]
	}
	return config
}
