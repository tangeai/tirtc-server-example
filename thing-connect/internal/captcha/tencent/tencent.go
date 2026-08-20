package tencent

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	captchasdk "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/captcha/v20190722"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"

	"thing-connect/internal/captcha"
)

type verifyClient interface {
	DescribeCaptchaResultWithContext(context.Context, *captchasdk.DescribeCaptchaResultRequest) (*captchasdk.DescribeCaptchaResultResponse, error)
	DescribeCaptchaMiniResultWithContext(context.Context, *captchasdk.DescribeCaptchaMiniResultRequest) (*captchasdk.DescribeCaptchaMiniResultResponse, error)
}

type verifier struct {
	client        verifyClient
	captchaAppID  uint64
	appSecretKey  string
	miniAppID     uint64
	miniSecretKey string
}

// New returns a Tencent Cloud CAPTCHA 2.0 Web/App ticket verifier.
func New(secretID, secretKey, captchaAppID, appSecretKey, miniCaptchaAppID, miniAppSecretKey string) (captcha.Verifier, error) {
	appID, err := strconv.ParseUint(strings.TrimSpace(captchaAppID), 10, 64)
	if err != nil || appID == 0 {
		return nil, fmt.Errorf("tencent captcha_app_id must be a positive integer")
	}
	clientProfile := profile.NewClientProfile()
	clientProfile.HttpProfile.ReqTimeout = 5
	client, err := captchasdk.NewClient(common.NewCredential(secretID, secretKey), "", clientProfile)
	if err != nil {
		return nil, fmt.Errorf("tencent captcha client: %w", err)
	}
	var miniAppID uint64
	if strings.TrimSpace(miniCaptchaAppID) != "" {
		miniAppID, err = strconv.ParseUint(strings.TrimSpace(miniCaptchaAppID), 10, 64)
		if err != nil || miniAppID == 0 || strings.TrimSpace(miniAppSecretKey) == "" {
			return nil, fmt.Errorf("tencent mini_program_captcha_id requires a positive ID and mini_program_secret_key")
		}
	}
	return &verifier{client: client, captchaAppID: appID, appSecretKey: appSecretKey, miniAppID: miniAppID, miniSecretKey: miniAppSecretKey}, nil
}

func (v *verifier) Verify(ctx context.Context, token captcha.CaptchaToken) error {
	ticket := strings.TrimSpace(token.Token)
	randstr := strings.TrimSpace(token.Metadata["randstr"])
	userIP := strings.TrimSpace(token.UserIP)
	if ticket == "" || randstr == "" || userIP == "" {
		if token.Metadata["client_type"] != "mini_program" || ticket == "" || userIP == "" {
			return captcha.ErrVerifyFailed
		}
	}
	if token.Metadata["client_type"] == "mini_program" {
		appID, appSecretKey := v.captchaAppID, v.appSecretKey
		if v.miniAppID != 0 {
			appID, appSecretKey = v.miniAppID, v.miniSecretKey
		}
		request := captchasdk.NewDescribeCaptchaMiniResultRequest()
		request.CaptchaType = common.Uint64Ptr(9)
		request.Ticket = common.StringPtr(ticket)
		request.UserIp = common.StringPtr(userIP)
		request.CaptchaAppId = common.Uint64Ptr(appID)
		request.AppSecretKey = common.StringPtr(appSecretKey)
		response, err := v.client.DescribeCaptchaMiniResultWithContext(ctx, request)
		if err != nil {
			return fmt.Errorf("tencent mini captcha verify: %w", err)
		}
		if response == nil || response.Response == nil || response.Response.CaptchaCode == nil || *response.Response.CaptchaCode != 1 {
			return captcha.ErrVerifyFailed
		}
		return nil
	}
	request := captchasdk.NewDescribeCaptchaResultRequest()
	request.CaptchaType = common.Uint64Ptr(9)
	request.Ticket = common.StringPtr(ticket)
	request.UserIp = common.StringPtr(userIP)
	request.Randstr = common.StringPtr(randstr)
	request.CaptchaAppId = common.Uint64Ptr(v.captchaAppID)
	request.AppSecretKey = common.StringPtr(v.appSecretKey)
	response, err := v.client.DescribeCaptchaResultWithContext(ctx, request)
	if err != nil {
		return fmt.Errorf("tencent captcha verify: %w", err)
	}
	if response == nil || response.Response == nil || response.Response.CaptchaCode == nil || *response.Response.CaptchaCode != 1 {
		return captcha.ErrVerifyFailed
	}
	return nil
}
