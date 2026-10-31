package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"thing-connect/internal/config"
)

type unbindFunc func(context.Context, string) error

func (f unbindFunc) Unbind(ctx context.Context, id string) error { return f(ctx, id) }

func TestInternalUnbindDelegatesAndPreservesResponseContract(t *testing.T) {
	for _, fail := range []bool{false, true} {
		called := false
		service := unbindFunc(func(_ context.Context, id string) error {
			called = true
			if id != "device-1" {
				t.Fatalf("device=%q", id)
			}
			if fail {
				return errors.New("private database failure")
			}
			return nil
		})
		s := NewServer(&config.Config{Internal: config.InternalCfg{Key: "test-key"}}, nil, nil, nil, nil, service)
		r := gin.New()
		r.POST("/", s.postInternalUnbind)
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"device_id":"device-1"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Key", "test-key")
		res := httptest.NewRecorder()
		r.ServeHTTP(res, req)
		var body struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		want := 0
		if fail {
			want = 50000
		}
		if !called || res.Code != 200 || body.Code != want {
			t.Fatalf("called=%v status=%d body=%s", called, res.Code, res.Body.String())
		}
		if strings.Contains(res.Body.String(), "private database") {
			t.Fatal("raw failure leaked")
		}
	}
}
