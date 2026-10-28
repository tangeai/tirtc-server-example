package recognition

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"strings"
)

type Config struct {
	BaseURL  string `json:"base_url" yaml:"base_url"`
	APIKey   string `json:"api_key,omitempty" yaml:"api_key"`
	TTSURL   string `json:"tts_url" yaml:"tts_url"`
	TTSModel string `json:"tts_model" yaml:"tts_model"`
}

func DefaultConfig() Config {
	return Config{BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", TTSURL: "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation", TTSModel: "qwen3-tts-flash"}
}
func (c Config) Validate() error {
	for _, raw := range []string{c.BaseURL, c.TTSURL} {
		u, e := url.Parse(raw)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return ErrInvalid
		}
	}
	if strings.TrimSpace(c.TTSModel) == "" || strings.ContainsAny(c.APIKey, "\r\n") {
		return ErrInvalid
	}
	return nil
}
func DecodeConfig(raw []byte) (Config, error) {
	var c Config
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil {
		return c, ErrInvalid
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return c, ErrInvalid
	}
	return c, c.Validate()
}
func ValidateScheme(s Scheme) error {
	if strings.TrimSpace(s.Name) == "" || len([]rune(s.Name)) > 50 || len(s.Description) > 1000 || s.MediaType != "audio" || strings.TrimSpace(s.Prompt) == "" || len(s.Prompt) > 8000 || strings.TrimSpace(s.Model) == "" || len(s.Model) > 128 || s.Limits.MaxFileBytes < 1 || s.Limits.MaxFileBytes > 5<<20 || s.Limits.MaxAudioSeconds < 1 || s.Limits.MaxAudioSeconds > 60 {
		return ErrInvalid
	}
	if s.OutputMode != "text" && s.OutputMode != "audio" && s.OutputMode != "text_audio" {
		return ErrInvalid
	}
	if s.OutputMode != "text" && (strings.TrimSpace(s.Voice) == "" || len(s.Voice) > 128) {
		return ErrInvalid
	}
	return nil
}
func PublicScheme(s Scheme) Scheme { s.Prompt = ""; return s }
