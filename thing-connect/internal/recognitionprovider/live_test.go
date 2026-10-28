package recognitionprovider

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	r "thing-connect/internal/recognition"
)

// TestLiveAudioRecognition is opt-in: it calls a billed external model.
// It verifies the adapter, not HTTP authentication, persistence or MQTT delivery.
func TestLiveAudioRecognition(t *testing.T) {
	if os.Getenv("RECOGNITION_LIVE_TEST") != "1" {
		t.Skip("set RECOGNITION_LIVE_TEST=1 to call Qwen")
	}
	cfg := r.DefaultConfig()
	cfg.APIKey = os.Getenv("DASHSCOPE_API_KEY")
	if cfg.APIKey == "" {
		t.Fatal("DASHSCOPE_API_KEY is required")
	}
	path := os.Getenv("RECOGNITION_AUDIO_FILE")
	if path == "" {
		t.Fatal("RECOGNITION_AUDIO_FILE is required: PCM16 WAV, up to 5 MiB / 60 seconds")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal("cannot open audio fixture")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > 5<<20 {
		t.Fatal("invalid fixture size")
	}
	data, err := io.ReadAll(io.LimitReader(f, (5<<20)+1))
	if err != nil || !r.ValidAudio(data, r.Limits{MaxFileBytes: 5 << 20, MaxAudioSeconds: 60}) {
		t.Fatal("invalid PCM16 WAV fixture")
	}
	model := os.Getenv("RECOGNITION_MODEL")
	if model == "" {
		model = "qwen3-omni-flash"
	}
	scheme := r.Scheme{Model: model, Prompt: "分析音频中是否有哭声、争吵或其他异常，说明声音类别。安静或正常声音不要报警，不确定时明确说明。", Voice: "Cherry"}
	client := New()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result, usage, err := client.Analyze(ctx, cfg, scheme, data)
	if err != nil {
		t.Fatalf("audio analysis failed: %v", err)
	}
	t.Logf("summary=%s alarm=%t labels=%v", result.Summary, result.Alarm, result.Labels)
	if usage.Total != nil {
		t.Logf("analysis total_tokens=%d", *usage.Total)
	} else {
		t.Log("provider did not return token usage")
	}
	switch want := os.Getenv("RECOGNITION_EXPECT_ALARM"); want {
	case "":
	case "true", "false":
		if result.Alarm != (want == "true") {
			t.Errorf("alarm=%t, expected %s", result.Alarm, want)
		}
	default:
		t.Error("RECOGNITION_EXPECT_ALARM must be true or false")
	}
	if os.Getenv("RECOGNITION_TEST_TTS") != "1" {
		return
	}
	speechCtx, speechCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer speechCancel()
	audio, ttsUsage, err := client.Synthesize(speechCtx, cfg, scheme, result.Summary)
	if err != nil {
		t.Fatalf("speech synthesis failed: %v", err)
	}
	dir := os.Getenv("RECOGNITION_OUTPUT_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal("cannot create output directory")
	}
	output := filepath.Join(dir, "recognition-result.wav")
	if err = os.WriteFile(output, audio, 0600); err != nil {
		t.Fatal("cannot save speech")
	}
	t.Logf("speech saved: %s (%d bytes)", output, len(audio))
	if ttsUsage.Characters != nil {
		t.Logf("tts characters=%d", *ttsUsage.Characters)
	}
}
