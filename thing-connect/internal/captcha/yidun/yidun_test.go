package yidun

import (
	"context"
	"errors"
	"testing"

	"github.com/yidun/yidun-golang-sdk/yidun/service/captcha"

	basecaptcha "thing-connect/internal/captcha"
)

type fakeClient struct {
	request *captcha.CaptchaVerifyRequest
	result  bool
	err     error
}

func (f *fakeClient) Verify(request *captcha.CaptchaVerifyRequest) (*captcha.CaptchaVerifyResponse, error) {
	f.request = request
	return &captcha.CaptchaVerifyResponse{Result: &f.result}, f.err
}

func TestVerifierMapsTokenAndResult(t *testing.T) {
	client := &fakeClient{result: true}
	v := &verifier{client: client}
	err := v.Verify(context.Background(), basecaptcha.CaptchaToken{CaptchaID: "web", Token: "ticket", User: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	if client.request == nil || client.request.CaptchaId == nil || *client.request.CaptchaId != "web" || client.request.Validate == nil || *client.request.Validate != "ticket" {
		t.Fatalf("unexpected request: %#v", client.request)
	}
	client.result = false
	if err := v.Verify(context.Background(), basecaptcha.CaptchaToken{CaptchaID: "web", Token: "ticket"}); !errors.Is(err, basecaptcha.ErrVerifyFailed) {
		t.Fatalf("failure result returned %v", err)
	}
}
