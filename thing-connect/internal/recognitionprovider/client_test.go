package recognitionprovider

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	r "thing-connect/internal/recognition"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
func TestAnalyzeUsageAndTruncatedStream(t *testing.T) {
	for _, complete := range []bool{false, true} {
		tail := ""
		if complete {
			tail = "data: [DONE]\n\n"
		}
		client := &Client{HTTP: &http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
			if req.Header.Get("Authorization") != "Bearer key" {
				t.Fatal("missing key")
			}
			body := `data: {"id":"upstream","choices":[{"delta":{"content":"{\"summary\":\"疑似哭声\",\"alarm\":true,\"labels\":[\"crying\"],\"details\":{}}"},"finish_reason":"stop"}]}` + "\n\ndata: {\"id\":\"upstream\",\"usage\":{\"prompt_tokens\":40,\"completion_tokens\":20,\"total_tokens\":60},\"choices\":[]}\n\n" + tail
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})}}
		cfg := r.DefaultConfig()
		cfg.APIKey = "key"
		v, u, e := client.Analyze(context.Background(), cfg, r.Scheme{Model: "qwen3-omni-flash", Prompt: "分析声音"}, []byte("audio"))
		if u.Total == nil || *u.Total != 60 {
			t.Fatal("usage lost")
		}
		if complete && (e != nil || !v.Alarm) {
			t.Fatal("valid result lost", e)
		}
		if !complete && e == nil {
			t.Fatal("truncated stream accepted")
		}
	}
}
func TestMissingUsageIsUnknown(t *testing.T) {
	u := usage(nil, "")
	if u.Input != nil || u.Output != nil || u.Total != nil {
		t.Fatal("missing usage represented as zero")
	}
}
func TestTTSRejectsUntrustedDownload(t *testing.T) {
	calls := 0
	c := &Client{HTTP: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"output":{"audio":{"url":"http://127.0.0.1/private"}},"usage":{"characters":4}}`)), Header: make(http.Header)}, nil
	})}}
	_, u, e := c.Synthesize(context.Background(), r.DefaultConfig(), r.Scheme{Voice: "Cherry"}, "测试声音")
	if e == nil || calls != 1 || u.Characters == nil {
		t.Fatal("unsafe audio URL followed or usage lost")
	}
}

func TestTTSSynthesizesAndDownloadsWithoutLeakingKey(t *testing.T) {
	calls := 0
	wav := speechWAV(24000, 1, 24000)
	c := &Client{HTTP: &http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
		calls++
		body := ""
		if calls == 1 {
			if req.Method != "POST" || req.Header.Get("Authorization") != "Bearer test-key" {
				t.Fatal("invalid synthesis request")
			}
			body = `{"output":{"audio":{"url":"http://example.oss-cn-beijing.aliyuncs.com/result.wav"}},"usage":{"characters":4},"request_id":"tts-1"}`
		} else {
			if req.Method != "GET" || req.URL.Scheme != "https" || req.Header.Get("Authorization") != "" {
				t.Fatal("download must use HTTPS without model credentials")
			}
			body = string(wav)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	cfg := r.DefaultConfig()
	cfg.APIKey = "test-key"
	audio, u, err := c.Synthesize(context.Background(), cfg, r.Scheme{Voice: "Cherry"}, "疑似哭声")
	if err != nil || calls != 2 || string(audio) != string(wav) || u.Characters == nil || *u.Characters != 4 || u.ProviderID != "tts-1" {
		t.Fatalf("synthesis failed: calls=%d err=%v usage=%+v", calls, err, u)
	}
}
