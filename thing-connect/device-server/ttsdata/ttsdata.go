// Package ttsdata embeds pre-generated PCM audio segments (Microsoft Edge TTS,
// Chinese female voice) and exposes a concatenation API for TTS requests.
//
// Build-time dependency: device-server/scripts/gen_tts.py
// Run-time dependency: none — all data is embedded in the binary.
package ttsdata

import (
	"embed"
	"encoding/binary"
	"fmt"
)

const (
	// PCMRate is the sample rate of all embedded audio segments.
	PCMRate = 8000
	// PCMChannels is the number of channels (mono).
	PCMChannels = 1
	// PCMBits is bits per sample (16-bit signed PCM).
	PCMBits = 16
)

//go:embed *.pcm
var fs embed.FS

// Segment name constants.
const (
	Silence200ms = "_silence_200ms.pcm"
)

// Digit returns the PCM segment for a single decimal digit ('0'..'9').
// Returns nil if r is not a digit.
func Digit(r rune) []byte {
	if r < '0' || r > '9' {
		return nil
	}
	return load(fmt.Sprintf("%c.pcm", r))
}

// WAV wraps raw PCM data with a standard RIFF WAV header so browsers and
// media players (VLC, etc.) can play it directly. Returns nil if data is empty.
func WAV(pcm []byte) []byte {
	if len(pcm) == 0 {
		return nil
	}
	dataLen := len(pcm)
	// RIFF header: 12 bytes + fmt chunk (24) + data chunk header (8) = 44
	headerLen := 44
	out := make([]byte, headerLen+dataLen)

	byteRate := PCMRate * PCMChannels * PCMBits / 8
	blockAlign := PCMChannels * PCMBits / 8

	// RIFF
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], uint32(headerLen+dataLen-8))
	copy(out[8:12], "WAVE")

	// fmt chunk
	copy(out[12:16], "fmt ")
	binary.LittleEndian.PutUint32(out[16:20], 16)          // chunk size
	binary.LittleEndian.PutUint16(out[20:22], 1)            // PCM format
	binary.LittleEndian.PutUint16(out[22:24], PCMChannels)
	binary.LittleEndian.PutUint32(out[24:28], PCMRate)
	binary.LittleEndian.PutUint32(out[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(out[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(out[34:36], PCMBits)

	// data chunk
	copy(out[36:40], "data")
	binary.LittleEndian.PutUint32(out[40:44], uint32(dataLen))

	copy(out[headerLen:], pcm)
	return out
}

// Silence returns the 200ms silence segment.
func Silence() []byte {
	return load(Silence200ms)
}

// load reads a named segment from the embedded filesystem.
// Returns nil on any error (file not found, read error).
func load(name string) []byte {
	data, err := fs.ReadFile(name)
	if err != nil {
		return nil
	}
	return data
}

// Build concatenates PCM segments for a digit string.
// Prepends the Chinese prefix "您的验证码是" for natural speech.
// Inserts Silence between the prefix and each digit for natural pacing.
// Returns the concatenated PCM 16-bit 8kHz mono raw bytes.
func Build(code string) []byte {
	silence := Silence()
	var out []byte

	// Prefix: "您的验证码是"
	if prefix := load("prefix_zh.pcm"); prefix != nil {
		out = append(out, prefix...)
		if silence != nil {
			out = append(out, silence...)
		}
	}

	// Digits with silence gaps
	for i, r := range code {
		seg := Digit(r)
		if seg == nil {
			continue
		}
		if i > 0 && silence != nil {
			out = append(out, silence...)
		}
		out = append(out, seg...)
	}
	return out
}
