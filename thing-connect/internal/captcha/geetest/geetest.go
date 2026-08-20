package geetest

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"thing-connect/internal/captcha"
)

const verifyEndpoint = "https://gcaptcha4.geetest.com/validate"

type verifier struct {
	captchaID      string
	captchaKey     string
	miniCaptchaID  string
	miniCaptchaKey string
	endpoint       string
	client         *http.Client
}

// New returns a GeeTest CAPTCHA v4 server-side verifier.
func New(captchaID, captchaKey, miniCaptchaID, miniCaptchaKey string) captcha.Verifier {
	return &verifier{
		captchaID: captchaID, captchaKey: captchaKey, miniCaptchaID: miniCaptchaID, miniCaptchaKey: miniCaptchaKey, endpoint: verifyEndpoint,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

func (v *verifier) Verify(ctx context.Context, token captcha.CaptchaToken) error {
	metadata := token.Metadata
	captchaID := strings.TrimSpace(metadata["captcha_id"])
	if captchaID == "" {
		captchaID = v.captchaID
	}
	captchaKey := v.captchaKey
	if v.miniCaptchaID != "" && captchaID == v.miniCaptchaID {
		captchaKey = v.miniCaptchaKey
	}
	lotNumber := strings.TrimSpace(metadata["lot_number"])
	captchaOutput := strings.TrimSpace(metadata["captcha_output"])
	passToken := strings.TrimSpace(metadata["pass_token"])
	genTime := strings.TrimSpace(metadata["gen_time"])
	if lotNumber == "" || captchaOutput == "" || passToken == "" || genTime == "" {
		return captcha.ErrVerifyFailed
	}

	mac := hmac.New(sha256.New, []byte(captchaKey))
	_, _ = mac.Write([]byte(lotNumber))
	form := url.Values{
		"lot_number":     {lotNumber},
		"captcha_output": {captchaOutput},
		"pass_token":     {passToken},
		"gen_time":       {genTime},
		"sign_token":     {hex.EncodeToString(mac.Sum(nil))},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, v.endpoint+"?captcha_id="+url.QueryEscape(captchaID), strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("geetest request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := v.client.Do(request)
	if err != nil {
		return fmt.Errorf("geetest verify: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("geetest verify: unexpected status %d", response.StatusCode)
	}
	var result struct {
		Result string `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return fmt.Errorf("geetest response: %w", err)
	}
	if result.Result != "success" {
		return captcha.ErrVerifyFailed
	}
	return nil
}
