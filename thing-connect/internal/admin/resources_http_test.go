package admin

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDevicePresenceKeyUsesFormalMQTTClientID(t *testing.T) {
	const deviceID = "TIRE88CRGXG4"
	if got, want := devicePresenceKey(deviceID), "online:sn_"+deviceID; got != want {
		t.Fatalf("devicePresenceKey(%q) = %q, want %q", deviceID, got, want)
	}
}

func TestManagedDeviceProjectsOptionalProfileMetadata(t *testing.T) {
	profile := `{"hardware":{"chip_model":"x86_64","board_model":"thingconnect-device-sim-py"},"firmware_version":"1.0.0","voip":{}}`
	device := managedDevice{Profile: &profile}
	device.projectProfileMetadata()
	if device.Hardware == nil || device.Hardware.ChipModel == nil || *device.Hardware.ChipModel != "x86_64" ||
		device.Hardware.BoardModel == nil || *device.Hardware.BoardModel != "thingconnect-device-sim-py" ||
		device.FirmwareVersion == nil || *device.FirmwareVersion != "1.0.0" {
		t.Fatalf("metadata=%+v firmware=%v", device.Hardware, device.FirmwareVersion)
	}

	legacy := managedDevice{}
	legacy.projectProfileMetadata()
	if legacy.Hardware != nil || legacy.FirmwareVersion != nil {
		t.Fatalf("legacy metadata must stay absent: %+v", legacy)
	}
}

func TestAdminDeviceProfileDataKeepsVoIPContractAndAddsMetadata(t *testing.T) {
	stored := `{"hardware":{"chip_model":"arm64","board_model":"board-a"},"firmware_version":"1.1.1","voip":{"audio_rate":8000}}`
	data := adminDeviceProfileData("device-1", stored, time.Time{})
	if data["profile"] != `{"audio_rate":8000}` {
		t.Fatalf("profile=%v", data["profile"])
	}
	attributes, ok := data["attributes"].(map[string]any)
	if !ok || attributes["audio_rate"] != float64(8000) {
		t.Fatalf("attributes=%T %#v", data["attributes"], data["attributes"])
	}
	hardware, err := json.Marshal(data["hardware"])
	if err != nil || string(hardware) != `{"chip_model":"arm64","board_model":"board-a"}` || data["firmware_version"] != "1.1.1" {
		t.Fatalf("hardware=%s firmware=%v err=%v", hardware, data["firmware_version"], err)
	}
}

func TestManagedDeviceProjectsProfilesWithoutSecrets(t *testing.T) {
	profile := `{"hardware":{"chip_model":"ESP32-S3"},"stream":{"audio_rate":16000,"up_video_mt":["h264","mjpeg"]},"call":{"audio_channels":1},"voip":{"audio_rate":8000,"device_key":"secret-value"},"credentials":{"token":"secret-value"}}`
	device := managedDevice{Profile: &profile}
	device.projectProfileMetadata()
	for _, scene := range []string{"stream", "call", "voip"} {
		if _, ok := device.Profiles[scene]; !ok {
			t.Fatalf("missing %s profile", scene)
		}
	}
	raw, err := json.Marshal(device)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-value") || strings.Contains(string(raw), "credentials") {
		t.Fatal("profile projection leaked private fields")
	}
	if string(device.Profiles["stream"]["up_video_mt"]) != `["h264","mjpeg"]` {
		t.Fatal("codec list changed")
	}
}
