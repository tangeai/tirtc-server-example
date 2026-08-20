package registry

import (
	"context"
	"errors"
	"testing"

	"thing-connect/internal/captcha"
)

type fakeVerifier struct {
	called bool
	err    error
}

func (v *fakeVerifier) Verify(context.Context, captcha.CaptchaToken) error {
	v.called = true
	return v.err
}

func TestRegistryRejectsIncompleteProviderCredentials(t *testing.T) {
	tests := []struct {
		provider string
		config   Config
	}{
		{provider: "unknown", config: Config{}},
		{provider: "yidun", config: Config{CaptchaID: "id"}},
		{provider: "geetest", config: Config{CaptchaID: "id", SecretKey: "key", PublicConfig: map[string]string{"mini_program_captcha_id": "mini"}}},
		{provider: "aliyun", config: Config{CaptchaID: "scene", SecretID: "id"}},
		{provider: "tencent", config: Config{CaptchaID: "not-a-number", SecretID: "id", SecretKey: "key", AppSecretKey: "app"}},
	}
	for _, test := range tests {
		if _, err := New(test.provider, test.config); err == nil {
			t.Errorf("provider %s accepted incomplete credentials", test.provider)
		}
	}
}

func TestRegistryConstructsLocalProviders(t *testing.T) {
	if _, err := New(" YIDUN ", Config{CaptchaID: "id", SecretID: "secret-id", SecretKey: "secret-key"}); err != nil {
		t.Fatalf("valid NetEase configuration rejected: %v", err)
	}
	if _, err := New("geetest", Config{CaptchaID: "id", SecretKey: "secret-key", PublicConfig: map[string]string{}}); err != nil {
		t.Fatalf("valid GeeTest configuration rejected: %v", err)
	}
}

func TestBoundVerifierEnforcesProviderAndCaptchaID(t *testing.T) {
	delegate := &fakeVerifier{}
	verifier := boundVerifier{provider: "geetest", captchaID: "web", miniProgramCaptchaID: "mini", delegate: delegate}
	if err := verifier.Verify(context.Background(), captcha.CaptchaToken{Provider: "tencent", CaptchaID: "web"}); !errors.Is(err, captcha.ErrVerifyFailed) {
		t.Fatalf("provider mismatch returned %v", err)
	}
	if err := verifier.Verify(context.Background(), captcha.CaptchaToken{Provider: "geetest", CaptchaID: "other"}); !errors.Is(err, captcha.ErrVerifyFailed) {
		t.Fatalf("captcha ID mismatch returned %v", err)
	}
	if err := verifier.Verify(context.Background(), captcha.CaptchaToken{Provider: "GEETEST", CaptchaID: "mini"}); err != nil || !delegate.called {
		t.Fatalf("valid mini-program token was not delegated: called=%v err=%v", delegate.called, err)
	}
}
