// getRtcToken 示例: 自签名生成设备连接 token，并用 TGV1 签名调 TiRTC API 端到端验证凭证。
//
// 参照:
//   github.com/tangeai/tirtc-developer-tools  → token-issuer/issuer/signing.go  (自签名)
//   thing-connect/user-server/handler/rtc.go   → getRtcToken / buildTirtcToken
//   demo/main.go                                → TGV1 HTTP 签名调用
//
// 参数说明:
//   accessKeyID     — 开发者 Access Key，与控制台 appId 关联
//   secretKeyID     — 开发者 Secret Key，与控制台 appId 关联（保密）
//   remoteID        — 设备 ID (device_id)
//   deviceSecretKey — 设备密钥 (deviceKey，从 thing-connect device-server 数据库获取)
//
// 用法:
//   export TIRTC_APP_ID=你的appId
//   export TIRTC_ACCESS_KEY=你的accessKey
//   export TIRTC_SECRET_KEY=你的secretKey
//   go run . -remote-id TESTDEVICE01 -device-secret-key 你的设备密钥
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	tirtcsigning "github.com/tange-ai/token-signing/go"
)

func main() {
	appID := requireEnv("TIRTC_APP_ID")
	accessKeyID := requireEnv("TIRTC_ACCESS_KEY")
	secretKeyID := requireEnv("TIRTC_SECRET_KEY")

	remoteID := flag.String("remote-id", "TESTDEVICE01", "设备 ID")
	deviceSecretKey := flag.String("device-secret-key", "device-secret-key-example", "设备密钥")
	flag.Parse()

	// ── 1. 自签名生成设备连接 token ────────────────────────
	//   remoteID       → 设备 ID
	//   deviceSecretKey → 设备密钥（deviceKey）
	//   accessKeyID    → 开发者 Access Key（与 appId 关联）
	//   secretKeyID    → 开发者 Secret Key（与 appId 关联）
	fmt.Println("=== 1. 自签名生成设备连接 token ===")
	fmt.Printf("remoteID:        %s (设备 ID)\n", *remoteID)
	fmt.Printf("deviceSecretKey: %s (设备密钥)\n", *deviceSecretKey)
	fmt.Println()

	token, claims, err := tirtcsigning.GenerateDeviceToken(accessKeyID, secretKeyID, *remoteID, *deviceSecretKey)
	if err != nil {
		fmt.Printf("❌ 生成失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("token:  %s\n", token)
	fmt.Println("claims:")
	fmt.Printf("  sub:   %s\n", claims.Sub)
	fmt.Printf("  scope: %s\n", claims.Scope)
	fmt.Printf("  iss:   %s (accessKeyID)\n", claims.Iss)
	fmt.Printf("  iat:   %d (%s)\n", claims.Iat, time.Unix(claims.Iat, 0).UTC().Format(time.RFC3339))
	fmt.Printf("  exp:   %d (%s)\n", claims.Exp, time.Unix(claims.Exp, 0).UTC().Format(time.RFC3339))
	fmt.Printf("  nonce: %s\n", claims.Nonce)

	// 本地自验签名
	if _, err := tirtcsigning.VerifyDeviceToken(token, secretKeyID, *deviceSecretKey); err != nil {
		fmt.Printf("❌ 自验失败: %v\n", err)
	} else {
		fmt.Println("✅ 自验通过（签名正确）")
	}
	fmt.Println()

	// ── 2. TGV1 签名调 TiRTC API 验证凭证 ─────────────────
	fmt.Println("=== 2. TGV1 签名调 TiRTC API（端到端验证凭证）===")
	verifyCredentials(appID, accessKeyID, secretKeyID)
}

// verifyCredentials 参照 demo/main.go:
//
//	POST /v2/user/login/user-id  (TGV1-HMAC-SHA256 签名)
//
// 返回 code=200 说明签名和凭证均有效。
func verifyCredentials(appID, accessKeyID, secretKeyID string) {
	baseURL := envOr("TIRTC_ENDPOINT", "https://openapi-cn01.tange365.com")
	uriPath := "/v2/user/login/user-id"
	body, _ := json.Marshal(map[string]string{"user_id": "test-user-001"})

	headers := tirtcsigning.SignRequest(accessKeyID, secretKeyID, appID, "POST", uriPath, "", body, time.Now().UTC())

	req, _ := http.NewRequest("POST", baseURL+uriPath, bytes.NewReader(body))
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Set(k, v)
		}
	}
	fmt.Printf("POST %s%s\n", baseURL, uriPath)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("❌ 请求失败: %v\n", err)
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	json.Unmarshal(respBody, &result)
	fmt.Printf("HTTP %d  code=%d\n", resp.StatusCode, result.Code)

	switch result.Code {
	case 200:
		fmt.Println("✅ TGV1 签名通过，凭证有效")
	case -40001, -40002, -40003:
		fmt.Println("❌ 签名被拒，请检查 accessKey/secretKey/appId")
	default:
		fmt.Println("⚠️  业务错误（非鉴权错误），签名应已通过")
	}
}

func requireEnv(key string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	fmt.Printf("缺少环境变量 %s\n", key)
	fmt.Println("请设置:")
	fmt.Println("  export TIRTC_APP_ID=你的appId")
	fmt.Println("  export TIRTC_ACCESS_KEY=你的accessKey")
	fmt.Println("  export TIRTC_SECRET_KEY=你的secretKey")
	os.Exit(1)
	return ""
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
