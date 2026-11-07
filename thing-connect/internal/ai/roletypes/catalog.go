// Package roletypes defines the editable catalog used to initialize user roles.
package roletypes

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"
)

const VoiceModel = "cosyvoice-v3-flash"

func SupportedModel(model string) bool {
	return model == "cosyvoice-v3" || model == VoiceModel || model == "seed-tts-2.0"
}

var ErrInvalid = errors.New("invalid AI role type")
var ErrUnsupportedModel = errors.New("unsupported role voice model")
var ErrUnavailable = errors.New("AI role type catalog unavailable")

type Type struct {
	Language      string `json:"language" db:"language"`
	SourceVoiceID string `json:"source_voice_id,omitempty" db:"source_voice_id"`
	MatchKind     string `json:"match_kind" db:"match_kind"`
	ID            string `json:"id" db:"id"`
	Name          string `json:"name" db:"name"`
	VoiceID       string `json:"voice_id" db:"voice_id"`
	VoiceModel    string `json:"voice_model" db:"voice_model"`
	Prompt        string `json:"prompt" db:"prompt"`
	SortOrder     int    `json:"sort_order" db:"sort_order"`
	Status        string `json:"status" db:"status"`
	Revision      int64  `json:"revision" db:"revision"`
}

type Voice struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Model     string   `json:"model"`
	Languages []string `json:"languages"`
	SampleURL string   `json:"sample_url,omitempty"`
}

type VoiceCatalog interface {
	Voices(context.Context) ([]Voice, error)
}
type Reader interface {
	Published(context.Context) ([]Type, error)
}

func Normalize(v Type) Type {
	v.Language = strings.TrimSpace(v.Language)
	if v.Language == "" {
		v.Language = "zh"
	}
	if v.MatchKind == "" {
		v.MatchKind = "manual"
	}
	v.Name = strings.TrimSpace(v.Name)
	v.VoiceID = strings.TrimSpace(v.VoiceID)
	v.Prompt = strings.TrimSpace(v.Prompt)
	v.VoiceModel = strings.TrimSpace(v.VoiceModel)
	return v
}
func Validate(v Type) error {
	if v.Name == "" || utf8.RuneCountInString(v.Name) > 64 || (v.Status == "published" && v.Prompt == "") || utf8.RuneCountInString(v.Prompt) > 12000 || len(v.Language) > 16 || len(v.SourceVoiceID) > 200 || len(v.MatchKind) > 32 || len(v.VoiceID) > 200 || v.VoiceModel == "" || len(v.VoiceModel) > 64 || v.SortOrder < 0 || v.SortOrder > 99999 {
		return ErrInvalid
	}
	return nil
}
