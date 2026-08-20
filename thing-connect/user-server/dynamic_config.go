package main

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	captchapkg "thing-connect/internal/captcha"
	"thing-connect/internal/captcha/registry"
	"thing-connect/internal/config"
	"thing-connect/internal/dynamicconfig"
	mailerpkg "thing-connect/internal/mailer"
	smtpmailer "thing-connect/internal/mailer/smtp"
	"thing-connect/internal/service"
	usrhandler "thing-connect/user-server/handler"
)

func userDynamicConfig(cfg *config.Config, redisClient *redis.Client, users *service.UserService, binds *service.BindService) (*dynamicconfig.Client, []dynamicconfig.Ref, error) {
	client, err := dynamicconfig.New(cfg.Admin.ServerURL, cfg.Internal.Key, redisClient)
	if err != nil {
		return nil, nil, err
	}
	refs := []dynamicconfig.Ref{
		{Namespace: "user-server", Key: "smtp", Apply: func(snapshot dynamicconfig.Snapshot) error {
			var value struct {
				Enabled  bool   `json:"enabled"`
				Host     string `json:"host"`
				Port     int    `json:"port"`
				TLSMode  string `json:"tls_mode"`
				Username string `json:"username"`
				From     string `json:"from"`
			}
			var secrets struct {
				Password string `json:"password"`
			}
			if err := json.Unmarshal(snapshot.Value, &value); err != nil {
				return err
			}
			_ = json.Unmarshal(snapshot.Secrets, &secrets)
			var active mailerpkg.Mailer = mailerpkg.DisabledMailer{}
			if value.Enabled {
				active = smtpmailer.New(smtpmailer.Config{Host: value.Host, Port: value.Port, TLSMode: value.TLSMode, Username: value.Username, Password: secrets.Password, From: value.From})
			}
			users.UpdateMailer(active)
			return nil
		}},
		{Namespace: "user-server", Key: "captcha", Apply: func(snapshot dynamicconfig.Snapshot) error {
			var value struct {
				Enabled      bool              `json:"enabled"`
				Provider     string            `json:"provider"`
				CaptchaID    string            `json:"captcha_id"`
				PublicConfig map[string]string `json:"public_config"`
			}
			var secrets struct {
				SecretID             string `json:"secret_id"`
				SecretKey            string `json:"secret_key"`
				AppSecretKey         string `json:"app_secret_key"`
				MiniProgramSecretKey string `json:"mini_program_secret_key"`
			}
			if err := json.Unmarshal(snapshot.Value, &value); err != nil {
				return err
			}
			_ = json.Unmarshal(snapshot.Secrets, &secrets)
			var verifier captchapkg.Verifier = captchapkg.DevVerifier{}
			if value.Enabled {
				created, err := registry.New(value.Provider, registry.Config{CaptchaID: value.CaptchaID, SecretID: secrets.SecretID, SecretKey: secrets.SecretKey, AppSecretKey: secrets.AppSecretKey, MiniProgramSecretKey: secrets.MiniProgramSecretKey, PublicConfig: value.PublicConfig})
				if err != nil {
					return err
				}
				verifier = created
			}
			users.UpdateCaptcha(verifier)
			usrhandler.SetCaptchaConfig(usrhandler.CaptchaConfig{Provider: value.Provider, Enabled: value.Enabled, CaptchaID: value.CaptchaID, PublicConfig: value.PublicConfig})
			return nil
		}},
		{Namespace: "user-server", Key: "email.code_ttl", Apply: func(snapshot dynamicconfig.Snapshot) error {
			var value struct {
				Duration string `json:"duration"`
			}
			if json.Unmarshal(snapshot.Value, &value) != nil {
				return errors.New("invalid email TTL")
			}
			duration, err := time.ParseDuration(value.Duration)
			if err != nil {
				return err
			}
			users.UpdateEmailCodeTTL(duration)
			return nil
		}},
		{Namespace: "user-server", Key: "email.send_rate_limit", Apply: func(snapshot dynamicconfig.Snapshot) error {
			var value struct {
				Window      string `json:"window"`
				MaxPerEmail int    `json:"max_per_email"`
				MaxPerIP    int    `json:"max_per_ip"`
			}
			if err := json.Unmarshal(snapshot.Value, &value); err != nil {
				return err
			}
			window, err := time.ParseDuration(value.Window)
			if err != nil {
				return err
			}
			users.UpdateEmailRateLimit(window, value.MaxPerEmail, value.MaxPerIP)
			return nil
		}},
		{Namespace: "user-server", Key: "email.template.registration_code", Apply: applyEmailTemplate(users, "registration")},
		{Namespace: "user-server", Key: "email.template.password_reset_code", Apply: applyEmailTemplate(users, "password_reset")},
		{Namespace: "user-server", Key: "user.token_policy", Apply: func(snapshot dynamicconfig.Snapshot) error {
			var value struct {
				TokenExpiry string `json:"token_expiry"`
			}
			if err := json.Unmarshal(snapshot.Value, &value); err != nil {
				return err
			}
			duration, err := time.ParseDuration(value.TokenExpiry)
			if err != nil {
				return err
			}
			users.UpdateTokenTTL(duration)
			return nil
		}},
		{Namespace: "user-server", Key: "user.default_bind_quota", Apply: func(snapshot dynamicconfig.Snapshot) error {
			var value struct {
				Quota int `json:"quota"`
			}
			if err := json.Unmarshal(snapshot.Value, &value); err != nil {
				return err
			}
			users.UpdateDefaultBindQuota(value.Quota)
			return nil
		}},
		{Namespace: "user-server", Key: "mqtt.ack_policy", Apply: func(snapshot dynamicconfig.Snapshot) error {
			var value struct {
				Timeout string `json:"timeout"`
			}
			if err := json.Unmarshal(snapshot.Value, &value); err != nil {
				return err
			}
			duration, err := time.ParseDuration(value.Timeout)
			if err != nil {
				return err
			}
			current := binds.Config()
			current.MQTTACKTimeout = duration
			binds.UpdateConfig(current)
			return nil
		}},
		{Namespace: "common", Key: "tirtc", Apply: func(snapshot dynamicconfig.Snapshot) error {
			var value struct {
				Endpoint string `json:"endpoint"`
				AppID    string `json:"app_id"`
			}
			var secrets struct {
				AccessKeyID string `json:"access_key_id"`
				SecretKeyID string `json:"secret_key_id"`
			}
			if err := json.Unmarshal(snapshot.Value, &value); err != nil {
				return err
			}
			_ = json.Unmarshal(snapshot.Secrets, &secrets)
			usrhandler.SetTirtcCredentials(value.AppID, secrets.AccessKeyID, secrets.SecretKeyID, value.Endpoint)
			return nil
		}},
	}
	return client, refs, nil
}

func applyEmailTemplate(users *service.UserService, key string) func(dynamicconfig.Snapshot) error {
	return func(snapshot dynamicconfig.Snapshot) error {
		var value struct {
			Enabled  bool   `json:"enabled"`
			Subject  string `json:"subject"`
			HTMLBody string `json:"html_body"`
			TextBody string `json:"text_body"`
		}
		if err := json.Unmarshal(snapshot.Value, &value); err != nil {
			return err
		}
		users.UpdateEmailTemplate(key, service.EmailTemplate{Enabled: value.Enabled, Subject: value.Subject, HTMLBody: value.HTMLBody, TextBody: value.TextBody})
		return nil
	}
}
