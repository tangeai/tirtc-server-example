package yidun

import (
	"context"
	"fmt"

	captchasdk "github.com/yidun/yidun-golang-sdk/yidun/service/captcha"

	"thing-connect/internal/captcha"
)

type verifier struct {
	client *captchasdk.CaptchaVerifyClient
}

// New returns a captcha.Verifier backed by 网易易盾.
func New(secretID, secretKey string) captcha.Verifier {
	return &verifier{
		client: captchasdk.NewCaptchaVerifyClientWithAccessKey(secretID, secretKey),
	}
}

func (v *verifier) Verify(_ context.Context, token captcha.CaptchaToken) error {
	req := captchasdk.NewCaptchaVerifyRequest()
	req.SetCaptchaId(token.CaptchaID).
		SetValidate(token.Validate).
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
