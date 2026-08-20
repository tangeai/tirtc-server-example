package aliyun

import (
	"context"
	"errors"
	"testing"

	captchasdk "github.com/alibabacloud-go/captcha-20230305/client"
	"github.com/alibabacloud-go/tea/dara"

	"thing-connect/internal/captcha"
)

type fakeVerifyClient struct {
	request *captchasdk.VerifyIntelligentCaptchaRequest
	result  bool
}

func (f *fakeVerifyClient) VerifyIntelligentCaptchaWithOptions(request *captchasdk.VerifyIntelligentCaptchaRequest, _ *dara.RuntimeOptions) (*captchasdk.VerifyIntelligentCaptchaResponse, error) {
	f.request = request
	return &captchasdk.VerifyIntelligentCaptchaResponse{Body: (&captchasdk.VerifyIntelligentCaptchaResponseBody{}).SetResult((&captchasdk.VerifyIntelligentCaptchaResponseBodyResult{}).SetVerifyResult(f.result))}, nil
}

func TestVerifierMapsSceneAndResult(t *testing.T) {
	client := &fakeVerifyClient{result: true}
	v := &verifier{client: client, sceneID: "default-scene"}
	if err := v.Verify(context.Background(), captcha.CaptchaToken{Token: "verify-param", Metadata: map[string]string{"captcha_id": "mini-scene"}}); err != nil {
		t.Fatal(err)
	}
	if client.request == nil || client.request.SceneId == nil || *client.request.SceneId != "mini-scene" || client.request.CaptchaVerifyParam == nil || *client.request.CaptchaVerifyParam != "verify-param" {
		t.Fatalf("unexpected request: %#v", client.request)
	}
	client.result = false
	if err := v.Verify(context.Background(), captcha.CaptchaToken{Token: "verify-param"}); !errors.Is(err, captcha.ErrVerifyFailed) {
		t.Fatalf("failure result returned %v", err)
	}
}
