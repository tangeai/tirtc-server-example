package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
)

// DeviceMediaStore atomically checks binding and replaces each supplied scene.
// Unspecified scenes are retained; an empty scene clears its reported fields.
type DeviceMediaStore interface {
	ReportMedia(context.Context, string, map[string]json.RawMessage) (bool, error)
}

type DeviceMediaService struct{ store DeviceMediaStore }

func NewDeviceMediaService(store DeviceMediaStore) *DeviceMediaService {
	return &DeviceMediaService{store: store}
}

var ErrInvalidMediaProfile = errors.New("媒体能力参数不合法")
var aspectRatioPattern = regexp.MustCompile(`^[1-9][0-9]{0,3}:[1-9][0-9]{0,3}$`)

func (s *DeviceMediaService) Report(ctx context.Context, deviceID string, profiles map[string]json.RawMessage) error {
	if err := ValidateMediaProfiles(profiles); err != nil {
		return err
	}
	bound, err := s.store.ReportMedia(ctx, deviceID, profiles)
	if err != nil {
		return fmt.Errorf("report device media: %w", err)
	}
	if !bound {
		return ErrDeviceReset
	}
	return nil
}

// ValidateMediaProfiles accepts only display capabilities, never credentials or
// per-call state. Values remain explicit, including false, zero and empty lists.
func ValidateMediaProfiles(profiles map[string]json.RawMessage) error {
	if len(profiles) == 0 || len(profiles) > 3 {
		return ErrInvalidMediaProfile
	}
	for scene, raw := range profiles {
		if scene != "stream" && scene != "call" && scene != "voip" {
			return fmt.Errorf("%w：场景必须为 stream、call 或 voip", ErrInvalidMediaProfile)
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return ErrInvalidMediaProfile
		}
		for key, value := range fields {
			if !validMediaField(key, value) {
				return fmt.Errorf("%w：请检查场景内的字段名称、类型和取值", ErrInvalidMediaProfile)
			}
		}
	}
	return nil
}

func validMediaField(key string, raw json.RawMessage) bool {
	if strings.TrimSpace(string(raw)) == "null" {
		return false
	}
	switch key {
	case "up_audio_mt", "down_audio_mt", "up_video_mt", "down_video_mt":
		var list []string
		var single string
		if json.Unmarshal(raw, &single) == nil {
			list = strings.FieldsFunc(single, func(r rune) bool { return strings.ContainsRune(",/;| \t\r\n", r) })
		} else if json.Unmarshal(raw, &list) != nil {
			return false
		}
		if len(list) > 8 {
			return false
		}
		for _, codec := range list {
			allowed := "alaw g711a pcm opus amr amr_nb amr_wb aac"
			if strings.Contains(key, "video") {
				allowed = "h264 h265 mjpeg none"
			}
			if !strings.Contains(" "+allowed+" ", " "+codec+" ") || codec == "" || strings.ContainsAny(codec, " \t\r\n") {
				return false
			}
		}
		return true
	case "audio_rate", "audio_channels", "camera_rotation":
		var n int
		if json.Unmarshal(raw, &n) != nil {
			return false
		}
		switch key {
		case "audio_rate":
			return n == 8000 || n == 16000 || n == 24000 || n == 32000 || n == 44100 || n == 48000
		case "audio_channels":
			return n == 1 || n == 2
		default:
			return n == 0 || n == 90 || n == 180 || n == 270
		}
	case "hor_mirror", "vert_mirror", "no_video":
		var b bool
		return json.Unmarshal(raw, &b) == nil
	case "aspect_ratio", "object_fit":
		if key == "aspect_ratio" {
			var number float64
			if json.Unmarshal(raw, &number) == nil {
				return number > 0 && !math.IsInf(number, 0) && !math.IsNaN(number)
			}
		}
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return false
		}
		if key == "aspect_ratio" {
			return aspectRatioPattern.MatchString(value)
		}
		return value == "fill" || value == "contain" || value == "cover"
	default:
		return false
	}
}
