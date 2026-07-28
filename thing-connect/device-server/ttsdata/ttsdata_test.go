package ttsdata

import (
	"encoding/binary"
	"testing"
)

func TestBuildAndWAV(t *testing.T) {
	pcm := Build("012345")
	if len(pcm) == 0 || len(pcm)%2 != 0 {
		t.Fatalf("Build returned invalid PCM length %d", len(pcm))
	}
	if got := Digit('x'); got != nil {
		t.Fatal("Digit should reject non-decimal input")
	}

	wav := WAV(pcm)
	if len(wav) != len(pcm)+44 {
		t.Fatalf("WAV length=%d, want %d", len(wav), len(pcm)+44)
	}
	if string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" || string(wav[36:40]) != "data" {
		t.Fatalf("invalid WAV header: %q %q %q", wav[:4], wav[8:12], wav[36:40])
	}
	if got := binary.LittleEndian.Uint32(wav[40:44]); got != uint32(len(pcm)) {
		t.Fatalf("WAV data length=%d, want %d", got, len(pcm))
	}
}

func TestWAVEmpty(t *testing.T) {
	if WAV(nil) != nil {
		t.Fatal("WAV(nil) should return nil")
	}
}
