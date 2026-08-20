package tencent

import (
	"context"
	"errors"
	"testing"

	captchasdk "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/captcha/v20190722"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"

	"thing-connect/internal/captcha"
)

type fakeVerifyClient struct {
	webRequest  *captchasdk.DescribeCaptchaResultRequest
	miniRequest *captchasdk.DescribeCaptchaMiniResultRequest
	code        int64
}

func (f *fakeVerifyClient) DescribeCaptchaResultWithContext(_ context.Context, request *captchasdk.DescribeCaptchaResultRequest) (*captchasdk.DescribeCaptchaResultResponse, error) {
	f.webRequest = request
	return &captchasdk.DescribeCaptchaResultResponse{Response: &captchasdk.DescribeCaptchaResultResponseParams{CaptchaCode: common.Int64Ptr(f.code)}}, nil
}

func (f *fakeVerifyClient) DescribeCaptchaMiniResultWithContext(_ context.Context, request *captchasdk.DescribeCaptchaMiniResultRequest) (*captchasdk.DescribeCaptchaMiniResultResponse, error) {
	f.miniRequest = request
	return &captchasdk.DescribeCaptchaMiniResultResponse{Response: &captchasdk.DescribeCaptchaMiniResultResponseParams{CaptchaCode: common.Int64Ptr(f.code)}}, nil
}

func TestVerifierSupportsWebAndMiniProgram(t *testing.T) {
	client := &fakeVerifyClient{code: 1}
	v := &verifier{client: client, captchaAppID: 123, appSecretKey: "web-secret", miniAppID: 456, miniSecretKey: "mini-secret"}
	if err := v.Verify(context.Background(), captcha.CaptchaToken{Token: "ticket", UserIP: "127.0.0.1", Metadata: map[string]string{"randstr": "rand"}}); err != nil {
		t.Fatal(err)
	}
	if client.webRequest == nil || client.webRequest.CaptchaAppId == nil || *client.webRequest.CaptchaAppId != 123 {
		t.Fatalf("unexpected web request: %#v", client.webRequest)
	}
	if err := v.Verify(context.Background(), captcha.CaptchaToken{Token: "mini-ticket", UserIP: "127.0.0.1", Metadata: map[string]string{"client_type": "mini_program"}}); err != nil {
		t.Fatal(err)
	}
	if client.miniRequest == nil || client.miniRequest.CaptchaAppId == nil || *client.miniRequest.CaptchaAppId != 456 {
		t.Fatalf("unexpected mini request: %#v", client.miniRequest)
	}
	client.code = 7
	if err := v.Verify(context.Background(), captcha.CaptchaToken{Token: "ticket", UserIP: "127.0.0.1", Metadata: map[string]string{"randstr": "rand"}}); !errors.Is(err, captcha.ErrVerifyFailed) {
		t.Fatalf("failure code returned %v", err)
	}
}
