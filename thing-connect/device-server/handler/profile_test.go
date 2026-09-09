package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"thing-connect/internal/service"
)

type mediaReporterStub struct {
	id  string
	err error
}

func (s *mediaReporterStub) Report(_ context.Context, id string, _ map[string]json.RawMessage) error {
	s.id = id
	return s.err
}
func TestMediaProfileHTTPIdentityAndErrors(t *testing.T) {
	service.RegisterErrors()
	sign := func(claims jwt.MapClaims) string {
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("media-test-secret"))
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	device := sign(jwt.MapClaims{"device_id": "device-a", "exp": time.Now().Add(time.Hour).Unix()})
	tests := []struct {
		name, token, body string
		failure           error
		status, code      int
		called            bool
	}{
		{"valid", device, `{"profiles":{"stream":{}}}`, nil, 200, 200, true},
		{"missing token", "", `{}`, nil, 401, 401, false},
		{"user token", sign(jwt.MapClaims{"user_id": 1, "exp": time.Now().Add(time.Hour).Unix()}), `{}`, nil, 401, 401, false},
		{"temporary token", sign(jwt.MapClaims{"device_id": "tmp_fake", "exp": time.Now().Add(time.Hour).Unix()}), `{}`, nil, 401, 401, false},
		{"missing expiry", sign(jwt.MapClaims{"device_id": "device-a"}), `{}`, nil, 401, 401, false},
		{"expired", sign(jwt.MapClaims{"device_id": "device-a", "exp": 1}), `{}`, nil, 401, 401, false},
		{"spoof identity", device, `{"device_id":"device-b","profiles":{"stream":{}}}`, nil, 400, 40000, false},
		{"trailing body", device, `{} {}`, nil, 400, 40000, false},
		{"oversized", device, strings.Repeat(" ", 17000) + `{}`, nil, 400, 40000, false},
		{"validation", device, `{}`, service.ErrInvalidMediaProfile, 400, 40000, true},
		{"unbound", device, `{}`, service.ErrDeviceReset, 410, 6006, true},
		{"storage failure", device, `{}`, errors.New("mysql private password secret"), 500, 50000, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reporter := &mediaReporterStub{err: tt.failure}
			r := gin.New()
			RegisterDeviceProfile(r, reporter, "media-test-secret")
			req := httptest.NewRequest("POST", "/v1/device/profile", strings.NewReader(tt.body))
			req.Header.Set("Authorization", "Bearer "+tt.token)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			var body struct {
				Code int `json:"code"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			if w.Code != tt.status || body.Code != tt.code || (reporter.id != "") != tt.called {
				t.Fatalf("status=%d body=%s caller=%q", w.Code, w.Body.String(), reporter.id)
			}
			if tt.called && reporter.id != "device-a" {
				t.Fatal("identity was not taken from JWT")
			}
			if strings.Contains(w.Body.String(), "password") {
				t.Fatal("internal error leaked")
			}
		})
	}
}
