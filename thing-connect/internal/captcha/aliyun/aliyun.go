package aliyun

import (
	"context"
	"fmt"
	"strings"

	captchasdk "github.com/alibabacloud-go/captcha-20230305/client"
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	"github.com/alibabacloud-go/tea/dara"

	"thing-connect/internal/captcha"
)

type verifyClient interface {
	VerifyIntelligentCaptchaWithOptions(*captchasdk.VerifyIntelligentCaptchaRequest, *dara.RuntimeOptions) (*captchasdk.VerifyIntelligentCaptchaResponse, error)
}

type verifier struct {
	client  verifyClient
	sceneID string
}

// New returns an Alibaba Cloud CAPTCHA 2.0 verifier. Region is cn or sgp.
func New(accessKeyID, accessKeySecret, sceneID, region string) (captcha.Verifier, error) {
	endpoint := "captcha.cn-shanghai.aliyuncs.com"
	if strings.EqualFold(strings.TrimSpace(region), "sgp") {
		endpoint = "captcha.ap-southeast-1.aliyuncs.com"
	}
	client, err := captchasdk.NewClient(&openapi.Config{
		AccessKeyId: &accessKeyID, AccessKeySecret: &accessKeySecret, Endpoint: &endpoint,
	})
	if err != nil {
		return nil, fmt.Errorf("aliyun captcha client: %w", err)
	}
	return &verifier{client: client, sceneID: sceneID}, nil
}

func (v *verifier) Verify(ctx context.Context, token captcha.CaptchaToken) error {
	sceneID := strings.TrimSpace(token.Metadata["captcha_id"])
	if sceneID == "" {
		sceneID = v.sceneID
	}
	verifyParam := strings.TrimSpace(token.Token)
	if verifyParam == "" && token.Metadata != nil {
		verifyParam = strings.TrimSpace(token.Metadata["captcha_verify_param"])
	}
	if verifyParam == "" {
		return captcha.ErrVerifyFailed
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	request := (&captchasdk.VerifyIntelligentCaptchaRequest{}).
		SetCaptchaVerifyParam(verifyParam).
		SetSceneId(sceneID)
	timeoutMS := 5000
	response, err := v.client.VerifyIntelligentCaptchaWithOptions(request, &dara.RuntimeOptions{
		ConnectTimeout: &timeoutMS, ReadTimeout: &timeoutMS,
	})
	if err != nil {
		return fmt.Errorf("aliyun captcha verify: %w", err)
	}
	if response == nil || response.Body == nil || response.Body.Result == nil || response.Body.Result.VerifyResult == nil || !*response.Body.Result.VerifyResult {
		return captcha.ErrVerifyFailed
	}
	return nil
}
