// Package deviceprofile owns validation, persistence boundaries, and public
// projection for device capability snapshots.
package deviceprofile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
)

var (
	ErrInvalid = errors.New("设备能力参数不合法")
	ErrUnbound = errors.New("设备未绑定")
)

type Store interface {
	ReplaceScenes(context.Context, string, map[string]json.RawMessage) (bool, error)
	Get(context.Context, string) (*Snapshot, error)
}

type Snapshot struct {
	DeviceID string
	Profile  json.RawMessage
}

type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }

func (s *Service) Report(ctx context.Context, deviceID string, profiles map[string]json.RawMessage) error {
	if err := Validate(profiles); err != nil {
		return err
	}
	return s.replace(ctx, deviceID, profiles)
}

// ReportLegacyVoIP stores a VoIP scene already normalized by the deprecated
// endpoint. That endpoint keeps its historical validation boundary.
func (s *Service) ReportLegacyVoIP(ctx context.Context, deviceID string, voip json.RawMessage) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(voip, &fields) != nil || fields == nil {
		return ErrInvalid
	}
	return s.replace(ctx, deviceID, map[string]json.RawMessage{"voip": voip})
}

func (s *Service) replace(ctx context.Context, deviceID string, profiles map[string]json.RawMessage) error {
	bound, err := s.store.ReplaceScenes(ctx, deviceID, profiles)
	if err != nil {
		return fmt.Errorf("保存设备能力: %w", err)
	}
	if !bound {
		return ErrUnbound
	}
	return nil
}

func (s *Service) VoIP(ctx context.Context, deviceID string) (json.RawMessage, error) {
	snapshot, err := s.store.Get(ctx, deviceID)
	if err != nil || snapshot == nil {
		return nil, err
	}
	return Scene(snapshot.Profile, "voip"), nil
}

var aspectRatioPattern = regexp.MustCompile(`^[1-9][0-9]{0,3}:[1-9][0-9]{0,3}$`)

func Validate(profiles map[string]json.RawMessage) error {
	if len(profiles) == 0 || len(profiles) > 3 {
		return ErrInvalid
	}
	for scene, raw := range profiles {
		if scene != "stream" && scene != "call" && scene != "voip" {
			return fmt.Errorf("%w：场景必须为 stream、call 或 voip", ErrInvalid)
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return ErrInvalid
		}
		for key, value := range fields {
			if !validField(scene, key, value) {
				return fmt.Errorf("%w：请检查 %s 场景内的字段名称、类型和取值", ErrInvalid, scene)
			}
		}
	}
	return nil
}

func validField(scene, key string, raw json.RawMessage) bool {
	if strings.TrimSpace(string(raw)) == "null" {
		return false
	}
	switch key {
	case "up_audio_mt", "down_audio_mt", "up_video_mt", "down_video_mt":
		if scene == "voip" && key == "up_audio_mt" {
			return false
		}
		var codecs []string
		var single string
		if json.Unmarshal(raw, &single) == nil {
			if scene != "voip" {
				codecs = strings.FieldsFunc(single, func(r rune) bool { return strings.ContainsRune(",/;| \t\r\n", r) })
			} else {
				codecs = []string{single}
			}
		} else if scene == "voip" || json.Unmarshal(raw, &codecs) != nil {
			return false
		}
		if len(codecs) > 8 {
			return false
		}
		for _, codec := range codecs {
			allowed := "alaw g711a pcm opus amr amr_nb amr_wb aac"
			if strings.Contains(key, "video") {
				allowed = "h264 h265 mjpeg none"
			}
			if !strings.Contains(" "+allowed+" ", " "+codec+" ") || codec == "" || strings.ContainsAny(codec, " \t\r\n") {
				return false
			}
		}
		return true
	case "audio_rate", "audio_channels", "camera_rotation", "screen_width", "screen_height", "calling_timeout_sec":
		var n int
		if json.Unmarshal(raw, &n) != nil {
			return false
		}
		switch key {
		case "audio_rate":
			return n == 8000 || n == 16000 || n == 24000 || n == 32000 || n == 44100 || n == 48000
		case "audio_channels":
			return n == 1 || n == 2
		case "camera_rotation":
			return n == 0 || n == 90 || n == 180 || n == 270
		case "screen_width", "screen_height":
			return scene == "voip" && n > 0 && n <= 16384
		default:
			return scene == "voip" && n > 0 && n <= 300
		}
	case "hor_mirror", "vert_mirror", "no_video":
		var b bool
		return json.Unmarshal(raw, &b) == nil
	case "aspect_ratio", "object_fit", "video_res_mode":
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
		switch key {
		case "aspect_ratio":
			return aspectRatioPattern.MatchString(value)
		case "object_fit":
			if scene == "voip" {
				return value == "fill" || value == "contain"
			}
			return value == "fill" || value == "contain" || value == "cover"
		default:
			return scene == "voip" && (value == "auto" || value == "fit_screen" || value == "fill_screen")
		}
	default:
		return false
	}
}

func Scene(profile json.RawMessage, name string) json.RawMessage {
	var scenes map[string]json.RawMessage
	if json.Unmarshal(profile, &scenes) != nil {
		return nil
	}
	raw := scenes[name]
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}

func Public(profile *string) map[string]map[string]json.RawMessage {
	result := map[string]map[string]json.RawMessage{}
	if profile == nil {
		return result
	}
	var scenes map[string]json.RawMessage
	if json.Unmarshal([]byte(*profile), &scenes) != nil {
		return result
	}
	for name, raw := range scenes {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			continue
		}
		if name == "voip" {
			fields = publicLegacyVoIP(fields)
		} else if Validate(map[string]json.RawMessage{name: raw}) != nil {
			continue
		}
		result[name] = fields
	}
	if voip, ok := result["voip"]; ok {
		camera, screen, roomType := DerivedVoIP(voip)
		voip["has_camera"] = json.RawMessage(fmt.Sprintf("%t", camera))
		voip["has_screen"] = json.RawMessage(fmt.Sprintf("%t", screen))
		encoded, _ := json.Marshal(roomType)
		voip["voip_room_type"] = encoded
	}
	return result
}

func publicLegacyVoIP(input map[string]json.RawMessage) map[string]json.RawMessage {
	result := make(map[string]json.RawMessage)
	for key, value := range input {
		if validField("voip", key, value) {
			result[key] = value
		}
	}
	if legacy, ok := input["video_mt"]; ok {
		if validField("voip", "up_video_mt", legacy) {
			if _, exists := result["up_video_mt"]; !exists {
				result["up_video_mt"] = legacy
			}
			if _, exists := result["down_video_mt"]; !exists {
				result["down_video_mt"] = legacy
			}
		}
	}
	return result
}

func DerivedVoIP(fields map[string]json.RawMessage) (bool, bool, string) {
	var noVideo bool
	_ = json.Unmarshal(fields["no_video"], &noVideo)
	if noVideo {
		return false, false, "voice"
	}
	codec := func(key string) string {
		var value string
		_ = json.Unmarshal(fields[key], &value)
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "none" {
			return ""
		}
		return value
	}
	camera := codec("up_video_mt") != ""
	screen := codec("down_video_mt") != ""
	if camera || screen {
		return camera, screen, "video"
	}
	return false, false, "voice"
}
