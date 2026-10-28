package recognitionprovider

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	r "thing-connect/internal/recognition"
)

func speechWAV(rate, channels, frames int) []byte {
	b := make([]byte, 44+frames*channels*2)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], uint16(channels))
	binary.LittleEndian.PutUint32(b[24:], uint32(rate))
	binary.LittleEndian.PutUint32(b[28:], uint32(rate*channels*2))
	binary.LittleEndian.PutUint16(b[32:], uint16(channels*2))
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(b)-44))
	for i := 44; i < len(b); i++ {
		b[i] = byte(i)
	}
	return b
}

func TestSynthesizeCompletesStreamingWAVHeaders(t *testing.T) {
	for _, pair := range [][2]uint32{{0x7fffffbf, 0x7fffff9b}, {0xffffffff, 0xffffffff}} {
		// The observed Qwen file is 24 kHz mono, 17.68 seconds. No real
		// recording is needed to reproduce its streaming length placeholders.
		want := speechWAV(24000, 1, 424320)
		upstream := bytes.Clone(want)
		binary.LittleEndian.PutUint32(upstream[4:], pair[0])
		binary.LittleEndian.PutUint32(upstream[40:], pair[1])
		c := speechClient(upstream)
		got, usage, err := c.Synthesize(context.Background(), r.DefaultConfig(), r.Scheme{Voice: "Cherry"}, "声音分析")
		if err != nil || !bytes.Equal(got, want) || usage.Characters == nil || *usage.Characters != 4 {
			t.Fatalf("streaming header pair %x: err=%v; normalized samples or usage differ", pair, err)
		}
	}
}

func speechClient(wav []byte) *Client {
	return &Client{HTTP: &http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
		var body io.Reader = bytes.NewReader(wav)
		if req.Method == http.MethodPost {
			body = strings.NewReader(`{"output":{"audio":{"url":"https://example.oss-cn-beijing.aliyuncs.com/result.wav"}},"usage":{"characters":4}}`)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(body), Header: make(http.Header)}, nil
	})}}
}

func TestSynthesizeRejectsMalformedWAVAndPreservesUsage(t *testing.T) {
	tests := map[string]func([]byte) []byte{
		"truncated":                  func(b []byte) []byte { return b[:len(b)-2] },
		"unknown length placeholder": func(b []byte) []byte { binary.LittleEndian.PutUint32(b[4:], 0x7fffffff); return b },
		"incomplete sample": func(b []byte) []byte {
			b = b[:len(b)-1]
			binary.LittleEndian.PutUint32(b[4:], 0xffffffff)
			binary.LittleEndian.PutUint32(b[40:], 0xffffffff)
			return b
		},
		"compressed":        func(b []byte) []byte { binary.LittleEndian.PutUint16(b[20:], 6); return b },
		"invalid align":     func(b []byte) []byte { binary.LittleEndian.PutUint16(b[32:], 4); return b },
		"invalid byte rate": func(b []byte) []byte { binary.LittleEndian.PutUint32(b[28:], 16000); return b },
		"missing format":    func(b []byte) []byte { copy(b[12:], "JUNK"); return b },
		"missing data":      func(b []byte) []byte { copy(b[36:], "JUNK"); return b },
		"incomplete trailing chunk": func(b []byte) []byte {
			b = append(b, 'x')
			binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
			return b
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			c := speechClient(mutate(speechWAV(24000, 1, 24000)))
			b, usage, err := c.Synthesize(context.Background(), r.DefaultConfig(), r.Scheme{Voice: "Cherry"}, "声音分析")
			if !errors.Is(err, r.ErrInvalid) || b != nil || usage.Characters == nil || *usage.Characters != 4 {
				t.Fatalf("malformed WAV accepted or billed usage lost: err=%v usage=%+v", err, usage)
			}
		})
	}
}

func TestNormalizeSpeechWAVPreservesOrdinaryPCMAndMetadata(t *testing.T) {
	for _, channels := range []int{1, 2} {
		b := speechWAV(48000, channels, 12000) // Short utterances may be under a second.
		b = append(b, []byte{'J', 'U', 'N', 'K', 1, 0, 0, 0, 'x', 0}...)
		binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
		want := bytes.Clone(b)
		if !normalizeSpeechWAV(b) || !bytes.Equal(b, want) {
			t.Fatalf("valid %d-channel WAV changed or rejected", channels)
		}
	}
}
