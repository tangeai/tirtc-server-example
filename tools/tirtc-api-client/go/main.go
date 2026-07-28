// tirtc-api-client: 服务端调用探鸽云 OpenAPI 示例
//
// 用法:
//
//	export TIRTC_APP_ID=你的appId
//	export TIRTC_ACCESS_KEY=你的accessKey
//	export TIRTC_SECRET_KEY=你的secretKey
//
//	go run . wxvoip   — POST /v1/token/wxvoip         (微信 VoIP 凭证)
//	go run . aichat   — POST /v1/token/aichat          (AI 语音对话凭证)
//	go run . login    — POST /v2/user/login/user-id    (用户登录)
//	go run . plans    — GET  /v2/cloud-service/plans    (套餐列表)
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/tange-ai/tirtc-api-client/signing"
)

const (
	endpointToken   = "https://api-tirtc.tange365.com"
	endpointOpenAPI = "https://openapi-cn01.tange365.com"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	appID := requireEnv("TIRTC_APP_ID")
	accessKey := requireEnv("TIRTC_ACCESS_KEY")
	secretKey := requireEnv("TIRTC_SECRET_KEY")

	switch os.Args[1] {
	case "wxvoip":
		runWxVoip(appID, accessKey, secretKey)
	case "aichat":
		runAiChat(appID, accessKey, secretKey)
	case "login":
		runLogin(appID, accessKey, secretKey)
	case "plans":
		runPlans(appID, accessKey, secretKey)
	default:
		fmt.Fprintf(os.Stderr, "未知 API: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "用法: go run . <api>")
	fmt.Fprintln(os.Stderr, "  wxvoip  — POST /v1/token/wxvoip (微信 VoIP)")
	fmt.Fprintln(os.Stderr, "  aichat  — POST /v1/token/aichat  (AI 语音对话)")
	fmt.Fprintln(os.Stderr, "  login   — POST /v2/user/login/user-id (用户登录)")
	fmt.Fprintln(os.Stderr, "  plans   — GET  /v2/cloud-service/plans (套餐列表)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "环境变量:")
	fmt.Fprintln(os.Stderr, "  TIRTC_APP_ID      应用 ID")
	fmt.Fprintln(os.Stderr, "  TIRTC_ACCESS_KEY  控制台 Access Key")
	fmt.Fprintln(os.Stderr, "  TIRTC_SECRET_KEY  控制台 Secret Key")
	fmt.Fprintln(os.Stderr, "  TIRTC_ENDPOINT    可选，默认 https://api-tirtc.tange365.com")
}

// ── WeChat VoIP ─────────────────────────────────────────────────────────

type WxVoipRequest struct {
	DeviceID          string `json:"device_id"`
	WxSessionKey      string `json:"wx_session_key"`
	WxRoomID          string `json:"wx_room_id"`
	WxSessionToken    string `json:"wx_session_token"`
	WxAppID           string `json:"wx_app_id"`
	WxModelID         string `json:"wx_model_id"`
	AudioRate         int    `json:"audio_rate"`
	AudioChannels     int    `json:"audio_channels"`
	WxPayload         string `json:"wx_payload,omitempty"`
	CallingTimeoutSec int    `json:"calling_timeout_sec,omitempty"`
	NoVideo           bool   `json:"no_video,omitempty"`
	UpVideoMT         string `json:"up_video_mt,omitempty"`
	DownVideoMT       string `json:"down_video_mt,omitempty"`
	DownAudioMT       string `json:"down_audio_mt,omitempty"`
	ScreenWidth       int    `json:"screen_width,omitempty"`
	ScreenHeight      int    `json:"screen_height,omitempty"`
}

func runWxVoip(appID, accessKey, secretKey string) {
	body := WxVoipRequest{
		DeviceID:       "TESTDEVICE01",
		WxSessionKey:   "test-session-key",
		WxRoomID:       "test-room-001",
		WxSessionToken: "test-server-token",
		WxAppID:        "wx0123456789abcdef",
		WxModelID:      "model-001",
		AudioRate:      8000,
		AudioChannels:  1,
	}

	fmt.Println("=== POST /v1/token/wxvoip (微信 VoIP) ===")
	fmt.Printf("device_id: %s\n", body.DeviceID)
	fmt.Println()

	statusCode, respBody := doPost(appID, accessKey, secretKey, endpointToken, "/v1/token/wxvoip", body)
	printResult(statusCode, respBody)
}

// ── AI Chat ──────────────────────────────────────────────────────────────

type AiChatRequest struct {
	DeviceID string `json:"device_id"`
	RoleID   string `json:"role_id"`
}

func runAiChat(appID, accessKey, secretKey string) {
	body := AiChatRequest{
		DeviceID: "TESTDEVICE01",
		RoleID:   "your-role-id",
	}

	fmt.Println("=== POST /v1/token/aichat (AI 语音对话) ===")
	fmt.Printf("device_id: %s\n", body.DeviceID)
	fmt.Printf("role_id:   %s\n", body.RoleID)
	fmt.Println()

	statusCode, respBody := doPost(appID, accessKey, secretKey, endpointToken, "/v1/token/aichat", body)
	printResult(statusCode, respBody)
}

// ── 用户登录 ─────────────────────────────────────────────────────────────

type LoginRequest struct {
	UserID string `json:"user_id"`
}

func runLogin(appID, accessKey, secretKey string) {
	body := LoginRequest{UserID: "test-user-001"}

	fmt.Println("=== POST /v2/user/login/user-id (用户登录) ===")
	fmt.Printf("user_id: %s\n", body.UserID)
	fmt.Println()

	statusCode, respBody := doPost(appID, accessKey, secretKey, endpointOpenAPI, "/v2/user/login/user-id", body)
	printResult(statusCode, respBody)
}

// ── 套餐列表 ─────────────────────────────────────────────────────────────

func runPlans(appID, accessKey, secretKey string) {
	fmt.Println("=== GET /v2/cloud-service/plans (套餐列表) ===")
	fmt.Println()

	statusCode, respBody := doGet(appID, accessKey, secretKey, endpointOpenAPI, "/v2/cloud-service/plans", "")
	printResult(statusCode, respBody)
}

// ── 通用 HTTP 请求 ───────────────────────────────────────────────────────

func doPost(appID, accessKey, secretKey, endpoint, uriPath string, body any) (int, []byte) {
	bodyBytes, _ := json.Marshal(body)
	return doRequest(appID, accessKey, secretKey, endpoint, "POST", uriPath, "", bodyBytes)
}

func doGet(appID, accessKey, secretKey, endpoint, uriPath, rawQuery string) (int, []byte) {
	return doRequest(appID, accessKey, secretKey, endpoint, "GET", uriPath, rawQuery, nil)
}

func doRequest(appID, accessKey, secretKey, endpoint, method, uriPath, rawQuery string, bodyBytes []byte) (int, []byte) {
	headers := signing.SignRequest(accessKey, secretKey, appID, method, uriPath, rawQuery, bodyBytes, time.Now().UTC())

	// 环境变量可覆盖 endpoint
	if ep := os.Getenv("TIRTC_ENDPOINT"); ep != "" {
		endpoint = ep
	}

	fullURL := endpoint + uriPath
	if rawQuery != "" {
		fullURL += "?" + rawQuery
	}

	req, _ := http.NewRequest(method, fullURL, bytes.NewReader(bodyBytes))
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Set(k, v)
		}
	}

	fmt.Printf("→ %s %s\n", method, fullURL)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("❌ 请求失败: %v\n", err)
		return 0, nil
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, respBody
}

func printResult(statusCode int, body []byte) {
	fmt.Printf("HTTP %d\n", statusCode)
	if len(body) > 0 {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, body, "", "  "); err == nil {
			fmt.Println(pretty.String())
		} else {
			fmt.Println(string(body))
		}
	}

	var r struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if json.Unmarshal(body, &r) == nil {
		switch {
		case r.Code == 0 || r.Code == 200:
			fmt.Println("✅ 成功")
		case r.Code == 401 || r.Code == 40105:
			fmt.Println("❌ 签名验证失败，请检查 accessKey / secretKey / appId")
		default:
			fmt.Printf("⚠️  code=%d, msg=%s\n", r.Code, r.Msg)
		}
	}
}

func requireEnv(key string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	fmt.Fprintf(os.Stderr, "缺少环境变量 %s\n", key)
	fmt.Fprintf(os.Stderr, "请设置:\n")
	fmt.Fprintf(os.Stderr, "  export TIRTC_APP_ID=你的appId\n")
	fmt.Fprintf(os.Stderr, "  export TIRTC_ACCESS_KEY=你的accessKey\n")
	fmt.Fprintf(os.Stderr, "  export TIRTC_SECRET_KEY=你的secretKey\n")
	os.Exit(1)
	return ""
}
