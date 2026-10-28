package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	r "thing-connect/internal/recognition"
)

type stubService struct {
	Service
	submitted int
	request   r.Request
	fresh     bool
	err       error
}

func (s *stubService) Submit(_ context.Context, a r.Actor, id string, req r.Request) (r.Task, bool, error) {
	s.submitted++
	s.request = req
	return r.Task{ID: req.ID, DeviceID: id, Status: "queued"}, s.fresh, s.err
}
func token(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	claims["exp"] = time.Now().Add(time.Hour).Unix()
	v, e := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("test-secret"))
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func engine(s Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	Register(e, "test-secret", s)
	return e
}
func TestRejectInvalidIdentities(t *testing.T) {
	s := &stubService{}
	e := engine(s)
	for _, auth := range []string{"", "Bearer bad", "Bearer " + token(t, jwt.MapClaims{"device_id": "tmp_device"}), "Bearer " + token(t, jwt.MapClaims{"device_id": "dev", "user_id": 1})} {
		req := httptest.NewRequest(http.MethodPost, "/v1/recognition/requests", nil)
		req.Header.Set("Authorization", auth)
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		if w.Code != 401 {
			t.Fatalf("got %d", w.Code)
		}
	}
	if s.submitted != 0 {
		t.Fatal("invalid identity reached service")
	}
}
func TestUploadAcceptanceReplayAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name         string
		fresh        bool
		err          error
		status, code int
	}{{"new", true, nil, 202, 200}, {"replay", false, nil, 200, 200}, {"unbound", false, r.ErrUnbound, 410, 6006}, {"forbidden", false, r.ErrForbidden, 403, 40300}, {"quota", false, r.ErrQuota, 429, 429}, {"disabled", false, r.ErrDisabled, 409, 40901}} {
		t.Run(tc.name, func(t *testing.T) {
			s := &stubService{fresh: tc.fresh, err: tc.err}
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			for k, v := range map[string]string{"request_id": "request-1", "config_revision": "3", "media_type": "audio"} {
				if err := form.WriteField(k, v); err != nil {
					t.Fatal(err)
				}
			}
			f, err := form.CreateFormFile("files", "sample.wav")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.Write([]byte("test-audio")); err != nil {
				t.Fatal(err)
			}
			if err = form.Close(); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/recognition/requests", &body)
			req.Header.Set("Content-Type", form.FormDataContentType())
			req.Header.Set("Authorization", "Bearer "+token(t, jwt.MapClaims{"device_id": "device-1"}))
			w := httptest.NewRecorder()
			engine(s).ServeHTTP(w, req)
			var got struct {
				Code int `json:"code"`
			}
			if err = json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || got.Code != tc.code || s.submitted != 1 || s.request.Revision != 3 || string(s.request.Data) != "test-audio" {
				t.Fatalf("response %d %s, request %+v", w.Code, w.Body, s.request)
			}
		})
	}
}
func TestInvalidCursor(t *testing.T) {
	for _, cursor := range []string{"-1", "bad", "999999999999999999999999"} {
		req := httptest.NewRequest(http.MethodGet, "/v1/recognition/requests?cursor="+cursor, nil)
		req.Header.Set("Authorization", "Bearer "+token(t, jwt.MapClaims{"device_id": "device-1"}))
		w := httptest.NewRecorder()
		engine(&stubService{}).ServeHTTP(w, req)
		if w.Code != 400 {
			t.Fatalf("cursor %s: %d", cursor, w.Code)
		}
	}
}

func TestRawPCMUploadMetadata(t *testing.T) {
	for _, tc := range []struct {
		rate, channels string
		status         int
	}{
		{"16000", "1", 202}, {"oops", "1", 400}, {"16000", "0", 400}, {"", "1", 400}, {"999999999999999999999999", "1", 400},
	} {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		for k, v := range map[string]string{"request_id": "pcm1", "config_revision": "3", "media_type": "audio", "audio_format": "pcm_s16le", "sample_rate": tc.rate, "channels": tc.channels} {
			if err := form.WriteField(k, v); err != nil {
				t.Fatal(err)
			}
		}
		f, err := form.CreateFormFile("files", "audio.pcm")
		if err != nil {
			t.Fatal(err)
		}
		raw := bytes.Repeat([]byte{1, 2}, 16000)
		if _, err = f.Write(raw); err != nil {
			t.Fatal(err)
		}
		if err = form.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/recognition/requests", &body)
		req.Header.Set("Content-Type", form.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+token(t, jwt.MapClaims{"device_id": "device-1"}))
		s := &stubService{fresh: true}
		w := httptest.NewRecorder()
		engine(s).ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("got %d want %d", w.Code, tc.status)
		}
		if tc.status == 202 {
			if s.request.AudioFormat != "pcm_s16le" || s.request.SampleRate != 16000 || s.request.Channels != 1 || !bytes.Equal(s.request.Data, raw) {
				t.Fatal("PCM metadata or payload changed")
			}
		} else if s.submitted != 0 {
			t.Fatal("invalid fields reached service")
		}
	}
}
