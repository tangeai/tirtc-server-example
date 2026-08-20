package yidun

import (
	"context"
	"fmt"

	captchasdk "github.com/yidun/yidun-golang-sdk/yidun/service/captcha"

	"thing-connect/internal/captcha"
)

type verifyClient interface {
	Verify(*captchasdk.CaptchaVerifyRequest) (*captchasdk.CaptchaVerifyResponse, error)
}

type verifier struct{ client verifyClient }

// New returns a captcha.Verifier backed by 网易易盾.
func New(secretID, secretKey string) captcha.Verifier {
	return &verifier{
		client: captchasdk.NewCaptchaVerifyClientWithAccessKey(secretID, secretKey),
	}
}

func (v *verifier) Verify(_ context.Context, token captcha.CaptchaToken) error {
	validate := token.Validate
	if token.Token != "" {
		validate = token.Token
	}
	captchaID := token.CaptchaID
	if id := token.Metadata["captcha_id"]; id != "" {
		captchaID = id
	}
	req := captchasdk.NewCaptchaVerifyRequest()
	req.SetCaptchaId(captchaID).
		SetValidate(validate).
		SetUser(token.User)

	resp, err := v.client.Verify(req)
	if err != nil {
		return fmt.Errorf("yidun.Verify: %w", err)
	}
	if resp.Result == nil || !*resp.Result {
		return captcha.ErrVerifyFailed
	}
	return nil
}
