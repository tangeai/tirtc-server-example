package geetest

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"thing-connect/internal/captcha"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestVerifierSendsSignedV4Request(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("captcha_id") != "captcha-id" {
			t.Errorf("captcha_id=%q", r.URL.Query().Get("captcha_id"))
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		form, err := url.ParseQuery(string(body))
		if err != nil {
			return nil, err
		}
		mac := hmac.New(sha256.New, []byte("captcha-key"))
		_, _ = mac.Write([]byte("lot-1"))
		if form.Get("sign_token") != hex.EncodeToString(mac.Sum(nil)) {
			t.Errorf("invalid sign_token")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"result":"success"}`)), Header: make(http.Header)}, nil
	})}

	v := &verifier{captchaID: "captcha-id", captchaKey: "captcha-key", endpoint: "https://captcha.example.test/validate", client: client}
	err := v.Verify(context.Background(), captcha.CaptchaToken{Metadata: map[string]string{
		"lot_number": "lot-1", "captcha_output": "output", "pass_token": "pass", "gen_time": "123",
	}})
	if err != nil {
		t.Fatal(err)
	}
}
