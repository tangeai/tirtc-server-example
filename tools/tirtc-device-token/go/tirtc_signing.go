// Package tirtcsigning implements TGV1-HMAC-SHA256 request signing for tange.ai server APIs.
//
// Reference: https://tange-ai.feishu.cn/wiki/wikcn6GaETEIVO0jgCP1ASHf3Mg
package tirtcsigning

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// AlgTGV1 is the algorithm identifier used in Authorization and X-Tg-Algorithm headers.
	AlgTGV1 = "TGV1-HMAC-SHA256"
	// credentialScopeSuffix is the fixed terminator in the credential scope.
	credentialScopeSuffix = "tgv1_request"
)

// SignRequest builds the full set of TGV1-HMAC-SHA256 signed HTTP headers.
//
// Parameters:
//   - accessKey: appears in the Authorization Credential field
//   - accessSecret: used for HMAC key derivation (keep this secret)
//   - appID: TiRTC application ID (X-Tg-App-Id header)
//   - method: HTTP method (GET, POST, PUT, PATCH, DELETE)
//   - uriPath: URI path, e.g. "/v2/device/info"
//   - rawQuery: raw query string without leading "?" (ignored for POST/PUT/PATCH)
//   - body: request body bytes (empty for GET/DELETE)
//   - signingTime: the time used for X-Tg-Date and credential scope (use UTC)
func SignRequest(accessKey, accessSecret, appID, method, uriPath, rawQuery string, body []byte, signingTime time.Time) http.Header {
	method = strings.ToUpper(method)
	tgDate := signingTime.UTC().Format("20060102T150405Z")
	scope := buildCredentialScope(signingTime)
	payloadHash := sha256Hex(body)

	// Step 1: build header values map
	hv := map[string]string{
		"x-tg-algorithm":      AlgTGV1,
		"x-tg-date":           tgDate,
		"x-tg-app-id":         strings.TrimSpace(appID),
		"x-tg-content-sha256": payloadHash,
	}
	if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
		hv["content-type"] = "application/json"
		hv["content-length"] = strconv.Itoa(len(body))
	}

	// Step 2: sorted, lowercased signed header names
	names := sortedKeysLower(hv)
	signedHeaders := strings.Join(names, ";")

	// Step 3: build canonical header string
	var canonLines []string
	for _, name := range names {
		canonLines = append(canonLines, name+":"+strings.TrimSpace(hv[name]))
	}
	hCanon := strings.Join(canonLines, "\n")

	// Step 4: build canonical request
	uriP := canonicalURIPath(uriPath)
	qCanon := canonicalQuery(method, strings.TrimPrefix(rawQuery, "?"))
	canonicalReq := strings.Join([]string{method, uriP, qCanon, hCanon, signedHeaders, payloadHash}, "\n")

	// Step 5: build string-to-sign
	hashCanon := sha256Hex([]byte(canonicalReq))
	strToSign := strings.Join([]string{AlgTGV1, tgDate, scope, hashCanon}, "\n")

	// Step 6: derive signing key
	k := hmacSHA256(signingTime.UTC().Format("20060102"), []byte("TGV1"+accessSecret))
	k = hmacSHA256(uriP, k)
	k = hmacSHA256(credentialScopeSuffix, k)

	// Step 7: compute signature
	sig := hex.EncodeToString(hmacSHA256(strToSign, k))

	// Step 8: assemble Authorization header
	cred := accessKey + "/" + scope
	auth := AlgTGV1 + " Credential=" + cred + ", SignedHeaders=" + signedHeaders + ", Signature=" + sig

	// Build output headers
	out := http.Header{}
	out.Set("X-Tg-Algorithm", hv["x-tg-algorithm"])
	out.Set("X-Tg-Date", hv["x-tg-date"])
	out.Set("X-Tg-App-Id", hv["x-tg-app-id"])
	out.Set("X-Tg-Content-Sha256", hv["x-tg-content-sha256"])
	out.Set("X-Tg-Signed-Headers", signedHeaders)
	if ct := hv["content-type"]; ct != "" {
		out.Set("Content-Type", ct)
	}
	if cl := hv["content-length"]; cl != "" {
		out.Set("Content-Length", cl)
	}
	out.Set("Authorization", auth)
	return out
}

// sha256Hex returns the hex-encoded SHA-256 digest of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// buildCredentialScope returns "YYYYMMDD/tgv1_request" where YYYYMMDD is signingTime + 7 days.
func buildCredentialScope(signingTime time.Time) string {
	return signingTime.UTC().Add(7 * 24 * time.Hour).Format("20060102") + "/" + credentialScopeSuffix
}

// hmacSHA256 returns the HMAC-SHA256 of data keyed with key.
func hmacSHA256(data string, key []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// canonicalURIPath normalizes the URI path: trim space, remove trailing slash unless root.
func canonicalURIPath(p string) string {
	p = strings.TrimSpace(p)
	if len(p) > 1 && strings.HasSuffix(p, "/") {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

// canonicalQuery returns the canonical query string.
// POST/PUT/PATCH requests always return empty string.
// Otherwise replaces "+" with "%20" (url.Values.Encode uses "+" for spaces, but canonical form uses %20).
func canonicalQuery(method, rawQuery string) string {
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return ""
	default:
		return strings.ReplaceAll(rawQuery, "+", "%20")
	}
}

// sortedKeysLower returns the keys of m, lowercased and sorted alphabetically.
func sortedKeysLower(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, strings.ToLower(k))
	}
	sort.Strings(keys)
	return keys
}

// ── Device Token (v1 格式) ──────────────────────────────────────────────
//
// 参照: https://github.com/tangeai/tirtc-developer-tools/blob/main/token-issuer/issuer/signing.go
//
// 自签名生成设备连接 token，设备端用此 token 调用 TiRTC SDK 连接云端。
//
// 参数说明（对应 TiRTC 控制台和 device-server 的设备信息）:
//   accessKeyID  — 开发者 Access Key，与控制台 appId 关联
//   secretKeyID  — 开发者 Secret Key，与控制台 appId 关联（保密）
//   remoteID     — 设备 ID (即 device_id)
//   deviceSecretKey — 设备密钥 (即 deviceKey，从 thing-connect device-server 数据库获取)
//
// token 格式: v1.<base64url claims>.<base64url signature>
// 签名算法: HMAC-SHA256 两次签名（deviceKey → appSecret）

// Claims 是 v1 token 内部的 JWT-style payload。
type Claims struct {
	Sub   string `json:"sub"`   // 调用方标识
	Scope string `json:"scope"` // 连接范围 "connect:device://<deviceID>"
	Iss   string `json:"iss"`   // 签发者 = accessKeyID
	Iat   int64  `json:"iat"`   // 签发时间 (unix seconds)
	Exp   int64  `json:"exp"`   // 过期时间 (unix seconds)
	Nonce string `json:"nonce"` // 随机数 (base64url, 16 bytes)
}

// GenerateDeviceToken 生成 v1 格式设备连接 token。
//
// 用法（业务服务器 getRtcToken 接口中调用）:
//
//	token, claims, err := tirtcsigning.GenerateDeviceToken(
//	    accessKeyID,      // 开发者 Access Key（控制台获取）
//	    secretKeyID,      // 开发者 Secret Key（控制台获取，保密）
//	    remoteID,         // 设备 ID
//	    deviceSecretKey,  // 设备密钥（deviceKey，从数据库获取）
//	)
func GenerateDeviceToken(accessKeyID, secretKeyID, remoteID, deviceSecretKey string) (token string, claims *Claims, err error) {
	// remoteID 即设备 device_id
	deviceID := strings.TrimSpace(remoteID)
	if deviceID == "" {
		return "", nil, fmt.Errorf("remoteID (device_id) is required")
	}

	now := time.Now().Unix()
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", nil, err
	}
	nonceStr := base64.RawURLEncoding.EncodeToString(nonce)

	c := &Claims{
		Sub:   "thing-connect",
		Scope: "connect:device://" + deviceID,
		Iss:   accessKeyID,
		Iat:   now,
		Exp:   now + 3600, // 默认 1 小时
		Nonce: nonceStr,
	}
	payloadJSON, _ := json.Marshal(c)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	// 两级 HMAC-SHA256 签名
	deviceSig := hmacSHA256B64(deviceSecretKey, payloadB64)                    // 设备密钥签名
	appSig := hmacSHA256B64(secretKeyID, payloadB64+"."+deviceSig)             // 应用密钥签名

	token = "v1." + payloadB64 + "." + appSig
	return token, c, nil
}

func hmacSHA256B64(key, data string) string {
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// VerifyDeviceToken 验证 v1 token 签名，返回解码后的 Claims。
// 不检查过期时间 — 调用者自行判断 claims.Exp。
func VerifyDeviceToken(token, secretKeyID, deviceSecretKey string) (*Claims, error) {
	if !strings.HasPrefix(token, "v1.") {
		return nil, fmt.Errorf("invalid token prefix")
	}
	parts := strings.SplitN(token[3:], ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid token format: expected v1.<payload>.<sig>")
	}
	payloadB64, sigB64 := parts[0], parts[1]

	// Decode payload
	payloadJSON, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	var c Claims
	if err := json.Unmarshal(payloadJSON, &c); err != nil {
		return nil, fmt.Errorf("parse payload: %w", err)
	}

	// Recompute expected signature
	deviceSig := hmacSHA256B64(deviceSecretKey, payloadB64)
	expectedSig := hmacSHA256B64(secretKeyID, payloadB64+"."+deviceSig)

	if !hmac.Equal([]byte(expectedSig), []byte(sigB64)) {
		return nil, fmt.Errorf("signature mismatch")
	}
	return &c, nil
}
