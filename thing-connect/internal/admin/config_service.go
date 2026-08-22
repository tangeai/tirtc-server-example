package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
)

const configEventChannel = "thingconnect:admin:config-events"

// ConfigEntry is the administrator read model. Secrets are populated only by
// HTTP handlers after a separate permission check; audit records and publish
// events continue to use entries without Secrets.
type ConfigEntry struct {
	ID               int64           `db:"id" json:"id,omitempty"`
	Namespace        string          `db:"namespace" json:"namespace"`
	ConfigKey        string          `db:"config_key" json:"config_key"`
	ScopeType        string          `db:"scope_type" json:"scope_type"`
	ScopeID          string          `db:"scope_id" json:"scope_id"`
	Value            json.RawMessage `db:"value" json:"value"`
	Secrets          json.RawMessage `db:"-" json:"secrets,omitempty"`
	SecretConfigured bool            `db:"-" json:"secret_configured"`
	UsingDefault     bool            `db:"-" json:"using_default"`
	Status           int8            `db:"status" json:"status"`
	Revision         int64           `db:"revision" json:"revision"`
	UpdatedBy        int64           `db:"updated_by" json:"updated_by"`
	CreatedAt        time.Time       `db:"created_at" json:"created_at,omitempty"`
	UpdatedAt        time.Time       `db:"updated_at" json:"updated_at,omitempty"`
}

type ConfigWrite struct {
	Namespace        string
	ConfigKey        string
	ScopeType        string
	ScopeID          string
	Value            json.RawMessage
	Secrets          json.RawMessage
	SecretsProvided  bool
	Status           int8
	ExpectedRevision int64
	Actor            AccessIdentity
	RequestID        string
	Method           string
	Path             string
	ClientIP         string
	UserAgent        string
	Reason           string
}

type ConfigRequirementStatus struct {
	Namespace  string `json:"namespace"`
	ConfigKey  string `json:"config_key"`
	Name       string `json:"name"`
	Reload     string `json:"reload"`
	Configured bool   `json:"configured"`
	Reason     string `json:"reason,omitempty"`
}

type ConfigService struct {
	db           *sqlx.DB
	registry     *ConfigRegistry
	legacyCipher *Cipher
}

func NewConfigService(db *sqlx.DB, registry *ConfigRegistry, cipher *Cipher) *ConfigService {
	return &ConfigService{db: db, registry: registry, legacyCipher: cipher}
}

func (s *ConfigService) Definitions(namespace string) ([]ConfigDefinition, error) {
	if namespace != "" && !validNamespaces[namespace] {
		return nil, fmt.Errorf("未知配置命名空间 %q", namespace)
	}
	return s.registry.List(namespace), nil
}

// BlockingStatus is a read-only startup gate. A blocking configuration is
// complete only after an administrator has explicitly published a valid value
// and all required secrets; registry defaults never silently satisfy it.
func (s *ConfigService) BlockingStatus(ctx context.Context, target string) ([]ConfigRequirementStatus, error) {
	definitions := s.registry.BlockingForTarget(strings.TrimSpace(target))
	statuses := make([]ConfigRequirementStatus, 0, len(definitions))
	for _, definition := range definitions {
		status := ConfigRequirementStatus{
			Namespace: definition.Namespace, ConfigKey: definition.Key,
			Name: definition.Name, Reload: definition.Reload,
		}
		value, secrets, revision, err := s.Resolved(ctx, definition.Namespace, definition.Key, "global", "")
		if err != nil {
			return nil, err
		}
		if revision == 0 {
			status.Reason = "尚未在管理后台发布"
			statuses = append(statuses, status)
			continue
		}
		if err := s.registry.Validate(definition.Namespace, definition.Key, value); err != nil {
			status.Reason = "已发布的配置值无效"
			statuses = append(statuses, status)
			continue
		}
		if err := validateRequiredSecrets(definition.Namespace, definition.Key, value, secrets); err != nil {
			status.Reason = err.Error()
			statuses = append(statuses, status)
			continue
		}
		status.Configured = true
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (s *ConfigService) List(ctx context.Context, namespace string) ([]ConfigEntry, error) {
	if namespace != "" && !validNamespaces[namespace] {
		return nil, fmt.Errorf("未知配置命名空间 %q", namespace)
	}
	query := `SELECT id,namespace,config_key,scope_type,scope_id,value,secret_value,status,revision,updated_by,created_at,updated_at FROM config_entries`
	args := []any{}
	if namespace != "" {
		query += ` WHERE namespace=?`
		args = append(args, namespace)
	}
	query += ` ORDER BY namespace,config_key,scope_type,scope_id`
	var rows []configRow
	if err := s.db.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	byID := make(map[string]ConfigEntry, len(rows))
	for _, row := range rows {
		entry := row.safe()
		byID[definitionID(entry.Namespace, entry.ConfigKey)] = entry
	}
	definitions := s.registry.List(namespace)
	entries := make([]ConfigEntry, 0, len(definitions))
	for _, definition := range definitions {
		if entry, ok := byID[definitionID(definition.Namespace, definition.Key)]; ok {
			entries = append(entries, entry)
			continue
		}
		entries = append(entries, ConfigEntry{
			Namespace: definition.Namespace, ConfigKey: definition.Key,
			ScopeType: "global", Value: cloneJSON(definition.Default), Status: 1,
			UsingDefault: true,
		})
	}
	return entries, nil
}

func (s *ConfigService) Get(ctx context.Context, namespace, key, scopeType, scopeID string) (ConfigEntry, error) {
	if _, ok := s.registry.Lookup(namespace, key); !ok {
		return ConfigEntry{}, ErrNotFound
	}
	scopeType, scopeID, err := normalizeScope(scopeType, scopeID)
	if err != nil {
		return ConfigEntry{}, err
	}
	var row configRow
	err = s.db.GetContext(ctx, &row, `SELECT id,namespace,config_key,scope_type,scope_id,value,secret_value,status,revision,updated_by,created_at,updated_at FROM config_entries WHERE namespace=? AND config_key=? AND scope_type=? AND scope_id=?`, namespace, key, scopeType, scopeID)
	if errors.Is(err, sql.ErrNoRows) {
		definition, _ := s.registry.Lookup(namespace, key)
		return ConfigEntry{Namespace: namespace, ConfigKey: key, ScopeType: scopeType, ScopeID: scopeID, Value: cloneJSON(definition.Default), Status: 1, UsingDefault: true}, nil
	}
	if err != nil {
		return ConfigEntry{}, err
	}
	return row.safe(), nil
}

func (s *ConfigService) Validate(namespace, key string, value, secrets json.RawMessage, secretsProvided bool) error {
	if err := s.registry.Validate(namespace, key, value); err != nil {
		return err
	}
	if !secretsProvided {
		return nil
	}
	if len(secrets) == 0 || !json.Valid(secrets) || string(secrets) == "null" {
		return errors.New("密钥配置必须是 JSON 对象")
	}
	var object map[string]any
	if err := json.Unmarshal(secrets, &object); err != nil || object == nil {
		return errors.New("密钥配置必须是 JSON 对象")
	}
	if containsMaskedSecret(object) {
		return errors.New("不能把掩码占位符作为密钥保存，请留空以保留原密钥")
	}
	definition, _ := s.registry.Lookup(namespace, key)
	if len(definition.SecretPaths) == 0 && len(object) != 0 {
		return errors.New("该配置项不接受密钥字段")
	}
	if err := validateSecretShape(namespace, key, object); err != nil {
		return err
	}
	return nil
}

func (s *ConfigService) Put(ctx context.Context, input ConfigWrite) (ConfigEntry, error) {
	input.Namespace = strings.TrimSpace(input.Namespace)
	input.ConfigKey = strings.TrimSpace(input.ConfigKey)
	var err error
	input.ScopeType, input.ScopeID, err = normalizeScope(input.ScopeType, input.ScopeID)
	if err != nil {
		return ConfigEntry{}, err
	}
	if input.Status != 1 {
		return ConfigEntry{}, errors.New("注册配置项必须保持生效；需要停用功能时请使用配置中的启用开关")
	}
	if err := s.Validate(input.Namespace, input.ConfigKey, input.Value, input.Secrets, input.SecretsProvided); err != nil {
		return ConfigEntry{}, err
	}
	if input.ExpectedRevision < 0 {
		return ConfigEntry{}, errors.New("配置版本号不能小于 0")
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return ConfigEntry{}, err
	}
	defer tx.Rollback()

	var before configRow
	err = tx.GetContext(ctx, &before, `SELECT id,namespace,config_key,scope_type,scope_id,value,secret_value,status,revision,updated_by,created_at,updated_at FROM config_entries WHERE namespace=? AND config_key=? AND scope_type=? AND scope_id=? FOR UPDATE`, input.Namespace, input.ConfigKey, input.ScopeType, input.ScopeID)
	creating := errors.Is(err, sql.ErrNoRows)
	if err != nil && !creating {
		return ConfigEntry{}, err
	}
	if creating && input.ExpectedRevision != 0 || !creating && input.ExpectedRevision != before.Revision {
		return ConfigEntry{}, ErrConflict
	}
	mfaPolicyChanged := false
	if definitionID(input.Namespace, input.ConfigKey) == "system/mfa.policy" {
		beforeValue := json.RawMessage(before.Value)
		if creating {
			definition, _ := s.registry.Lookup(input.Namespace, input.ConfigKey)
			beforeValue = definition.Default
		}
		beforeEnabled, beforeOK := mfaPolicyEnabled(beforeValue)
		afterEnabled, afterOK := mfaPolicyEnabled(input.Value)
		mfaPolicyChanged = beforeOK && afterOK && beforeEnabled != afterEnabled
	}
	providerChanged := !creating && definitionID(input.Namespace, input.ConfigKey) == "user-server/captcha" && captchaProvider(before.Value) != captchaProvider(string(input.Value))
	secretValue := before.SecretValue
	if input.SecretsProvided {
		mergedSecrets := input.Secrets
		if before.SecretValue.Valid && !providerChanged {
			mergedSecrets, err = mergeSecretJSON(json.RawMessage(before.SecretValue.String), input.Secrets)
			if err != nil {
				return ConfigEntry{}, err
			}
		}
		if err := validateRequiredSecrets(input.Namespace, input.ConfigKey, input.Value, mergedSecrets); err != nil {
			return ConfigEntry{}, err
		}
		secretValue = sql.NullString{String: string(mergedSecrets), Valid: true}
	} else if providerChanged {
		// Captcha vendors use overlapping field names for unrelated credentials.
		// Never reuse the previous vendor's values after a provider
		// switch; an enabled provider must receive its own complete credentials.
		secretValue = sql.NullString{}
		if err := validateExistingSecretRequirement(input.Namespace, input.ConfigKey, input.Value, false); err != nil {
			return ConfigEntry{}, err
		}
	} else if secretValue.Valid {
		if err := validateRequiredSecrets(input.Namespace, input.ConfigKey, input.Value, json.RawMessage(secretValue.String)); err != nil {
			return ConfigEntry{}, err
		}
	} else if err := validateExistingSecretRequirement(input.Namespace, input.ConfigKey, input.Value, false); err != nil {
		return ConfigEntry{}, err
	}

	var id, revision int64
	if creating {
		revision = 1
		result, err := tx.ExecContext(ctx, `INSERT INTO config_entries (namespace,config_key,scope_type,scope_id,value,secret_value,status,revision,updated_by) VALUES (?,?,?,?,?,?,?,?,?)`, input.Namespace, input.ConfigKey, input.ScopeType, input.ScopeID, string(input.Value), nullableSQLString(secretValue), input.Status, revision, input.Actor.UserID)
		if err != nil {
			return ConfigEntry{}, err
		}
		id, err = result.LastInsertId()
		if err != nil {
			return ConfigEntry{}, err
		}
	} else {
		id, revision = before.ID, before.Revision+1
		result, err := tx.ExecContext(ctx, `UPDATE config_entries SET value=?,secret_value=?,status=?,revision=?,updated_by=? WHERE id=? AND revision=?`, string(input.Value), nullableSQLString(secretValue), input.Status, revision, input.Actor.UserID, id, before.Revision)
		if err != nil {
			return ConfigEntry{}, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ConfigEntry{}, ErrConflict
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO config_publish_outbox (config_entry_id,revision) VALUES (?,?)`, id, revision); err != nil {
		return ConfigEntry{}, err
	}
	if mfaPolicyChanged {
		if _, err := tx.ExecContext(ctx, `UPDATE admin_users SET auth_revision=auth_revision+1`); err != nil {
			return ConfigEntry{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE admin_sessions SET revoked_at=COALESCE(revoked_at,NOW()),revoked_reason=IF(revoked_reason='', 'MFA policy updated', revoked_reason)`); err != nil {
			return ConfigEntry{}, err
		}
	}
	beforeSafe := ""
	if !creating {
		beforeJSON, _ := json.Marshal(before.safe())
		beforeSafe = string(beforeJSON)
	}
	after := ConfigEntry{ID: id, Namespace: input.Namespace, ConfigKey: input.ConfigKey, ScopeType: input.ScopeType, ScopeID: input.ScopeID, Value: cloneJSON(input.Value), SecretConfigured: secretConfigured(secretValue), Status: input.Status, Revision: revision, UpdatedBy: input.Actor.UserID}
	afterJSON, _ := json.Marshal(after)
	if err := insertAuditTx(ctx, tx, AuditEvent{AdminUserID: input.Actor.UserID, RoleCodes: strings.Join(input.Actor.Roles, ","), RequestID: input.RequestID, Method: input.Method, Path: input.Path, HTTPStatus: 200, Action: "config.write", Resource: "config", ResourceID: definitionID(input.Namespace, input.ConfigKey), Reason: input.Reason, Before: beforeSafe, After: string(afterJSON), ClientIP: input.ClientIP, UserAgent: input.UserAgent, Success: true}); err != nil {
		return ConfigEntry{}, err
	}
	if err := tx.Commit(); err != nil {
		return ConfigEntry{}, err
	}
	return after, nil
}

func mfaPolicyEnabled(raw json.RawMessage) (bool, bool) {
	var policy struct {
		Enabled bool `json:"enabled"`
	}
	if json.Unmarshal(raw, &policy) != nil {
		return false, false
	}
	return policy.Enabled, true
}

func captchaProvider(raw string) string {
	var value struct {
		Provider string `json:"provider"`
	}
	if json.Unmarshal([]byte(raw), &value) != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(value.Provider))
}

// Resolved returns the effective database value, or the registered default
// when no row has been published. Secret values are stored as plaintext JSON
// and are returned only to trusted internal consumers or explicitly authorized
// administrator handlers.
func (s *ConfigService) Resolved(ctx context.Context, namespace, key, scopeType, scopeID string) (json.RawMessage, json.RawMessage, int64, error) {
	entry, err := s.Get(ctx, namespace, key, scopeType, scopeID)
	if err != nil {
		return nil, nil, 0, err
	}
	if entry.ID == 0 {
		return entry.Value, nil, 0, nil
	}
	var stored sql.NullString
	if err := s.db.GetContext(ctx, &stored, `SELECT secret_value FROM config_entries WHERE id=?`, entry.ID); err != nil {
		return nil, nil, 0, err
	}
	if stored.Valid {
		var object map[string]any
		if json.Unmarshal([]byte(stored.String), &object) != nil || object == nil {
			return nil, nil, 0, errors.New("已保存的配置密钥格式无效")
		}
		return entry.Value, json.RawMessage(stored.String), entry.Revision, nil
	}
	return entry.Value, nil, entry.Revision, nil
}

// PopulateSecrets adds the stored plaintext secret object to an administrator
// read model after the HTTP layer has authorized secret access.
func (s *ConfigService) PopulateSecrets(ctx context.Context, entry *ConfigEntry) error {
	if entry == nil || entry.ID == 0 || !entry.SecretConfigured {
		return nil
	}
	var stored sql.NullString
	if err := s.db.GetContext(ctx, &stored, `SELECT secret_value FROM config_entries WHERE id=?`, entry.ID); err != nil {
		return err
	}
	if stored.Valid {
		var object map[string]any
		if json.Unmarshal([]byte(stored.String), &object) != nil || object == nil {
			return errors.New("已保存的密钥数据无效")
		}
		entry.Secrets = json.RawMessage(stored.String)
	}
	return nil
}

// EffectiveSecrets returns the stored value merged with a candidate patch.
// It is used only by server-side integration tests and never by a read API.
func (s *ConfigService) EffectiveSecrets(ctx context.Context, namespace, key string, candidate json.RawMessage, provided bool) (json.RawMessage, error) {
	_, stored, _, err := s.Resolved(ctx, namespace, key, "global", "")
	if err != nil {
		return nil, err
	}
	if !provided {
		return stored, nil
	}
	if len(stored) == 0 {
		return cloneJSON(candidate), nil
	}
	return mergeSecretJSON(stored, candidate)
}

type configRow struct {
	ID          int64          `db:"id"`
	Namespace   string         `db:"namespace"`
	ConfigKey   string         `db:"config_key"`
	ScopeType   string         `db:"scope_type"`
	ScopeID     string         `db:"scope_id"`
	Value       string         `db:"value"`
	SecretValue sql.NullString `db:"secret_value"`
	Status      int8           `db:"status"`
	Revision    int64          `db:"revision"`
	UpdatedBy   int64          `db:"updated_by"`
	CreatedAt   time.Time      `db:"created_at"`
	UpdatedAt   time.Time      `db:"updated_at"`
}

func (r configRow) safe() ConfigEntry {
	return ConfigEntry{ID: r.ID, Namespace: r.Namespace, ConfigKey: r.ConfigKey, ScopeType: r.ScopeType, ScopeID: r.ScopeID, Value: json.RawMessage(r.Value), SecretConfigured: secretConfigured(r.SecretValue), Status: r.Status, Revision: r.Revision, UpdatedBy: r.UpdatedBy, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// MigratePlaintextSecrets converts values written by releases that encrypted
// configuration secrets. The schema keeps the legacy column only for this
// lossless transition; all reads and new writes use secret_value.
func (s *ConfigService) MigratePlaintextSecrets(ctx context.Context) error {
	var legacyColumn int
	if err := s.db.GetContext(ctx, &legacyColumn, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='config_entries' AND column_name='secret_value_enc'`); err != nil {
		return fmt.Errorf("inspect legacy config secret column: %w", err)
	}
	if legacyColumn == 0 {
		return nil
	}
	var rows []struct {
		ID             int64          `db:"id"`
		Namespace      string         `db:"namespace"`
		ConfigKey      string         `db:"config_key"`
		ScopeType      string         `db:"scope_type"`
		ScopeID        string         `db:"scope_id"`
		SecretValueEnc sql.NullString `db:"secret_value_enc"`
	}
	if err := s.db.SelectContext(ctx, &rows, `SELECT id,namespace,config_key,scope_type,scope_id,secret_value_enc FROM config_entries WHERE secret_value IS NULL AND secret_value_enc IS NOT NULL AND secret_value_enc<>''`); err != nil {
		return fmt.Errorf("read legacy config secrets: %w", err)
	}
	for _, row := range rows {
		if s.legacyCipher == nil {
			return errors.New("legacy configuration secrets require the configured data encryption key")
		}
		plain, err := s.legacyCipher.Decrypt(row.SecretValueEnc.String, configAAD(row.Namespace, row.ConfigKey, row.ScopeType, row.ScopeID))
		if err != nil {
			return fmt.Errorf("decrypt legacy config secret %s/%s: %w", row.Namespace, row.ConfigKey, err)
		}
		var object map[string]any
		if json.Unmarshal(plain, &object) != nil || object == nil {
			return fmt.Errorf("legacy config secret %s/%s is not valid JSON", row.Namespace, row.ConfigKey)
		}
		result, err := s.db.ExecContext(ctx, `UPDATE config_entries SET secret_value=?,secret_value_enc=NULL WHERE id=? AND secret_value IS NULL`, string(plain), row.ID)
		if err != nil {
			return fmt.Errorf("store plaintext config secret %s/%s: %w", row.Namespace, row.ConfigKey, err)
		}
		if affected, _ := result.RowsAffected(); affected > 1 {
			return fmt.Errorf("migrate config secret %s/%s updated %d rows", row.Namespace, row.ConfigKey, affected)
		}
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE config_entries SET secret_value_enc=NULL WHERE secret_value IS NOT NULL AND secret_value_enc IS NOT NULL`); err != nil {
		return fmt.Errorf("clear migrated encrypted config secrets: %w", err)
	}
	return nil
}

func secretConfigured(value sql.NullString) bool {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return false
	}
	var object map[string]any
	return json.Unmarshal([]byte(value.String), &object) == nil && len(object) > 0
}

func normalizeScope(scopeType, scopeID string) (string, string, error) {
	scopeType, scopeID = strings.TrimSpace(scopeType), strings.TrimSpace(scopeID)
	if scopeType == "" {
		scopeType = "global"
	}
	switch scopeType {
	case "global":
		if scopeID != "" {
			return "", "", errors.New("全局配置不能指定实例标识")
		}
	default:
		return "", "", errors.New("当前配置项仅支持全局范围")
	}
	return scopeType, scopeID, nil
}

func containsMaskedSecret(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for _, child := range typed {
			if containsMaskedSecret(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsMaskedSecret(child) {
				return true
			}
		}
	case string:
		trimmed := strings.TrimSpace(typed)
		return trimmed != "" && (strings.Trim(trimmed, "*") == "" || strings.Trim(trimmed, "•") == "")
	}
	return false
}

func validateSecretShape(namespace, key string, object map[string]any) error {
	allowed := map[string]bool{}
	switch definitionID(namespace, key) {
	case "user-server/mqtt.connection", "voip-server/mqtt.connection", "call-server/mqtt.connection":
		allowed["password"] = true
	case "user-server/smtp":
		allowed["password"] = true
	case "user-server/captcha":
		allowed["secret_id"], allowed["secret_key"], allowed["app_secret_key"], allowed["mini_program_secret_key"] = true, true, true, true
	case "common/tirtc":
		allowed["access_key_id"], allowed["secret_key_id"] = true, true
	case "voip-server/wechat.apps":
		if len(object) != 1 {
			return errors.New("微信密钥配置只能包含应用列表 apps")
		}
		apps, ok := object["apps"].(map[string]any)
		if !ok {
			return errors.New("微信应用密钥列表 apps 必须是对象")
		}
		for appID, raw := range apps {
			app, ok := raw.(map[string]any)
			if !ok || strings.TrimSpace(appID) == "" {
				return errors.New("每个微信应用的密钥配置必须是对象")
			}
			for field, value := range app {
				if field != "secret" && field != "token" && field != "encoding_aes_key" {
					return fmt.Errorf("不支持的微信密钥字段 %q", field)
				}
				if value == nil {
					return errors.New("密钥值不能为 null；保留原值时请不要提交该字段")
				}
				if _, ok := value.(string); !ok {
					return fmt.Errorf("微信密钥字段 %s 必须是字符串", field)
				}
			}
		}
		return nil
	default:
		return errors.New("该配置项未注册密钥字段")
	}
	for field, value := range object {
		if !allowed[field] {
			return fmt.Errorf("不支持的密钥字段 %q", field)
		}
		if value == nil {
			return errors.New("密钥值不能为 null；保留原值时请不要提交该字段")
		}
		if _, ok := value.(string); !ok {
			return fmt.Errorf("密钥字段 %s 必须是字符串", field)
		}
	}
	return nil
}

func mergeSecretJSON(oldRaw, newRaw json.RawMessage) (json.RawMessage, error) {
	var oldValue, newValue map[string]any
	if err := json.Unmarshal(oldRaw, &oldValue); err != nil {
		return nil, errors.New("已保存的密钥数据无效")
	}
	if err := json.Unmarshal(newRaw, &newValue); err != nil {
		return nil, errors.New("新密钥数据格式无效")
	}
	mergeSecretObjects(oldValue, newValue)
	return json.Marshal(oldValue)
}

func mergeSecretObjects(target, updates map[string]any) {
	for key, value := range updates {
		updateObject, updateOK := value.(map[string]any)
		targetObject, targetOK := target[key].(map[string]any)
		if updateOK && targetOK {
			mergeSecretObjects(targetObject, updateObject)
			continue
		}
		target[key] = value
	}
}

func validateExistingSecretRequirement(namespace, key string, value json.RawMessage, configured bool) error {
	var public map[string]any
	_ = json.Unmarshal(value, &public)
	if definitionID(namespace, key) == "common/tirtc" && !configured {
		return errors.New("TiRTC 必须填写 Access Key ID 和 Secret Key ID")
	}
	if strings.HasSuffix(definitionID(namespace, key), "/mqtt.connection") && !configured {
		return errors.New("MQTT 连接必须填写密码")
	}
	if enabled, _ := public["enabled"].(bool); enabled && !configured && (definitionID(namespace, key) == "user-server/smtp" || definitionID(namespace, key) == "user-server/captcha") {
		return errors.New("启用该服务前必须先配置所需密钥")
	}
	if definitionID(namespace, key) == "voip-server/wechat.apps" && !configured {
		apps, _ := public["apps"].(map[string]any)
		for _, raw := range apps {
			app, _ := raw.(map[string]any)
			if enabled, _ := app["enabled"].(bool); enabled {
				return errors.New("启用微信应用前必须填写小程序密钥（AppSecret）")
			}
		}
	}
	return nil
}

func validateRequiredSecrets(namespace, key string, value, secrets json.RawMessage) error {
	if err := validateExistingSecretRequirement(namespace, key, value, len(secrets) > 0); err != nil {
		return err
	}
	var secretObject map[string]any
	_ = json.Unmarshal(secrets, &secretObject)
	switch definitionID(namespace, key) {
	case "user-server/mqtt.connection", "voip-server/mqtt.connection", "call-server/mqtt.connection":
		password, _ := secretObject["password"].(string)
		if strings.TrimSpace(password) == "" {
			return errors.New("MQTT 连接必须填写密码")
		}
	case "user-server/smtp":
		var public map[string]any
		_ = json.Unmarshal(value, &public)
		if enabled, _ := public["enabled"].(bool); enabled {
			if password, _ := secretObject["password"].(string); password == "" {
				return errors.New("启用邮件服务前必须填写 SMTP 密码或授权码")
			}
		}
	case "user-server/captcha":
		var public map[string]any
		_ = json.Unmarshal(value, &public)
		if enabled, _ := public["enabled"].(bool); enabled {
			provider, _ := public["provider"].(string)
			spec, ok := captchaProviderSpecFor(provider)
			if !ok {
				return errors.New("人机验证服务商无效")
			}
			for _, field := range spec.RequiredSecrets {
				secret, _ := secretObject[field].(string)
				if strings.TrimSpace(secret) == "" {
					return fmt.Errorf("启用%s人机验证前必须填写密钥 %s", spec.Label, field)
				}
			}
			if spec.RequiresMiniProgramSecret {
				publicConfig, _ := public["public_config"].(map[string]any)
				miniID, _ := publicConfig["mini_program_captcha_id"].(string)
				miniSecret, _ := secretObject["mini_program_secret_key"].(string)
				if strings.TrimSpace(miniID) != "" && strings.TrimSpace(miniSecret) == "" {
					return fmt.Errorf("%s小程序验证码 ID 需要配套的小程序密钥 mini_program_secret_key", spec.Label)
				}
			}
		}
	case "common/tirtc":
		access, _ := secretObject["access_key_id"].(string)
		secret, _ := secretObject["secret_key_id"].(string)
		if access == "" || secret == "" {
			return errors.New("TiRTC 必须填写 Access Key ID 和 Secret Key ID")
		}
	case "voip-server/wechat.apps":
		var public struct {
			Apps map[string]struct {
				Enabled bool `json:"enabled"`
			} `json:"apps"`
		}
		_ = json.Unmarshal(value, &public)
		secretApps, _ := secretObject["apps"].(map[string]any)
		for appID, app := range public.Apps {
			if !app.Enabled {
				continue
			}
			raw, _ := secretApps[appID].(map[string]any)
			secret, _ := raw["secret"].(string)
			if secret == "" {
				return fmt.Errorf("启用微信应用 %s 前必须填写小程序密钥（AppSecret）", appID)
			}
		}
	}
	return nil
}

func configAAD(namespace, key, scopeType, scopeID string) string {
	return namespace + "\x00" + key + "\x00" + scopeType + "\x00" + scopeID
}

func nullableSQLString(value sql.NullString) any {
	if !value.Valid || value.String == "" {
		return nil
	}
	return value.String
}

func cloneJSON(raw json.RawMessage) json.RawMessage { return append(json.RawMessage(nil), raw...) }

func insertAuditTx(ctx context.Context, tx *sqlx.Tx, event AuditEvent) error {
	success := 0
	if event.Success {
		success = 1
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO admin_audit_log (admin_user_id,role_codes,request_id,method,path,http_status,latency_ms,action,resource_type,resource_id,reason,before_value,after_value,client_ip,user_agent,success,error_message) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, event.AdminUserID, event.RoleCodes, event.RequestID, event.Method, event.Path, event.HTTPStatus, event.LatencyMS, event.Action, event.Resource, event.ResourceID, event.Reason, nullableText(event.Before), nullableText(event.After), event.ClientIP, event.UserAgent, success, event.Error)
	return err
}

type configEvent struct {
	OutboxID  int64  `db:"id" json:"-"`
	EntryID   int64  `db:"entry_id" json:"entry_id"`
	Namespace string `db:"namespace" json:"namespace"`
	ConfigKey string `db:"config_key" json:"config_key"`
	ScopeType string `db:"scope_type" json:"scope_type"`
	ScopeID   string `db:"scope_id" json:"scope_id"`
	Revision  int64  `db:"revision" json:"revision"`
}

// PublishPending implements the transactional-outbox relay. Publishing the
// same revision more than once is allowed; consumers compare revision numbers.
func (s *ConfigService) PublishPending(ctx context.Context, client *redis.Client, limit int) (int, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var rows []configEvent
	if err := s.db.SelectContext(ctx, &rows, `SELECT o.id,e.id entry_id,e.namespace,e.config_key,e.scope_type,e.scope_id,o.revision FROM config_publish_outbox o JOIN config_entries e ON e.id=o.config_entry_id WHERE o.delivered_at IS NULL AND o.next_attempt_at<=NOW() ORDER BY o.id LIMIT ?`, limit); err != nil {
		return 0, err
	}
	published := 0
	for _, row := range rows {
		payload, _ := json.Marshal(row)
		if err := client.Publish(ctx, configEventChannel, payload).Err(); err != nil {
			_, _ = s.db.ExecContext(ctx, `UPDATE config_publish_outbox SET attempts=attempts+1,last_error=?,next_attempt_at=DATE_ADD(NOW(),INTERVAL LEAST(300,POW(2,LEAST(attempts,8))) SECOND) WHERE id=? AND delivered_at IS NULL`, truncateString(err.Error(), 1024), row.OutboxID)
			continue
		}
		result, err := s.db.ExecContext(ctx, `UPDATE config_publish_outbox SET delivered_at=NOW(),attempts=attempts+1,last_error='' WHERE id=? AND delivered_at IS NULL`, row.OutboxID)
		if err != nil {
			return published, err
		}
		if n, _ := result.RowsAffected(); n == 1 {
			published++
		}
	}
	return published, nil
}

func truncateString(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
