package userauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const testJWTSecret = "test-jwt-secret"

type fakeStateGetter struct {
	state accountState
	err   error
	calls int
}

func (f *fakeStateGetter) GetContext(_ context.Context, destination any, _ string, _ ...any) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	state, ok := destination.(*accountState)
	if !ok {
		return errors.New("unexpected destination")
	}
	*state = f.state
	return nil
}

func TestClaimRevision(t *testing.T) {
	tests := []struct {
		name   string
		claims jwt.MapClaims
		want   int64
		ok     bool
	}{
		{name: "legacy token", claims: jwt.MapClaims{"user_id": float64(7)}, want: 1, ok: true},
		{name: "current token", claims: jwt.MapClaims{"user_id": float64(7), "auth_revision": float64(3)}, want: 3, ok: true},
		{name: "zero revision", claims: jwt.MapClaims{"auth_revision": float64(0)}, ok: false},
		{name: "fractional revision", claims: jwt.MapClaims{"auth_revision": 1.5}, ok: false},
		{name: "wrong type", claims: jwt.MapClaims{"auth_revision": "1"}, ok: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := ClaimRevision(test.claims)
			if ok != test.ok || got != test.want {
				t.Fatalf("ClaimRevision() = (%d, %v), want (%d, %v)", got, ok, test.want, test.ok)
			}
		})
	}
}

func TestEnforceStateLegacyTokenCompatibility(t *testing.T) {
	tests := []struct {
		name       string
		claims     jwt.MapClaims
		state      accountState
		wantStatus int
		wantCalls  int
	}{
		{
			name: "legacy token stays valid at initial revision", claims: jwt.MapClaims{"user_id": 7},
			state: accountState{Status: 1, AuthRevision: 1}, wantStatus: http.StatusOK, wantCalls: 1,
		},
		{
			name: "legacy token is revoked after account change", claims: jwt.MapClaims{"user_id": 7},
			state: accountState{Status: 1, AuthRevision: 2}, wantStatus: http.StatusUnauthorized, wantCalls: 1,
		},
		{
			name: "current token follows its revision", claims: jwt.MapClaims{"user_id": 7, "auth_revision": 2},
			state: accountState{Status: 1, AuthRevision: 2}, wantStatus: http.StatusOK, wantCalls: 1,
		},
		{
			name: "disabled account is rejected", claims: jwt.MapClaims{"user_id": 7},
			state: accountState{Status: 0, AuthRevision: 1}, wantStatus: http.StatusUnauthorized, wantCalls: 1,
		},
		{
			name: "device token bypasses user state", claims: jwt.MapClaims{"device_id": "dev-1"},
			state: accountState{Status: 1, AuthRevision: 1}, wantStatus: http.StatusOK, wantCalls: 0,
		},
	}

	gin.SetMode(gin.TestMode)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			getter := &fakeStateGetter{state: test.state}
			router := gin.New()
			router.Use(enforceState(getter, testJWTSecret))
			router.GET("/protected", func(c *gin.Context) { c.Status(http.StatusOK) })

			token := jwt.NewWithClaims(jwt.SigningMethodHS256, test.claims)
			signed, err := token.SignedString([]byte(testJWTSecret))
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			request.Header.Set("Authorization", "Bearer "+signed)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if getter.calls != test.wantCalls {
				t.Fatalf("state lookups = %d, want %d", getter.calls, test.wantCalls)
			}
		})
	}
}
