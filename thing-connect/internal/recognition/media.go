package recognition

import "encoding/binary"

func ValidAudio(b []byte, limits Limits) bool {
	if int64(len(b)) > limits.MaxFileBytes || len(b) < 44 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" || uint64(binary.LittleEndian.Uint32(b[4:8]))+8 != uint64(len(b)) {
		return false
	}
	var rate, byteRate uint32
	var channels, bits, align uint16
	dataSize := 0
	hasFmt, hasData := false, false
	for offset := 12; offset < len(b); {
		if len(b)-offset < 8 {
			return false
		}
		n := int(binary.LittleEndian.Uint32(b[offset+4 : offset+8]))
		start := offset + 8
		if n > len(b)-start {
			return false
		}
		switch string(b[offset : offset+4]) {
		case "fmt ":
			if hasFmt || n < 16 || binary.LittleEndian.Uint16(b[start:start+2]) != 1 {
				return false
			}
			hasFmt = true
			channels = binary.LittleEndian.Uint16(b[start+2 : start+4])
			rate = binary.LittleEndian.Uint32(b[start+4 : start+8])
			byteRate = binary.LittleEndian.Uint32(b[start+8 : start+12])
			align = binary.LittleEndian.Uint16(b[start+12 : start+14])
			bits = binary.LittleEndian.Uint16(b[start+14 : start+16])
		case "data":
			if !hasFmt || hasData {
				return false
			}
			hasData = true
			dataSize = n
		}
		offset = start + n + (n % 2)
		if offset > len(b) {
			return false
		}
	}
	return hasFmt && channels >= 1 && channels <= 2 && bits == 16 && rate >= 8000 && rate <= 48000 && align == channels*2 && byteRate == rate*uint32(align) && dataSize > 0 && dataSize%int(align) == 0 && float64(dataSize)/float64(byteRate) <= float64(limits.MaxAudioSeconds) && float64(dataSize)/float64(byteRate) >= 1
}

// prepareAudio preserves sample bytes; raw PCM only receives a WAV container.
// Persisting the self-describing container also binds rate/channels into the
// idempotency fingerprint and preserves the existing worker/provider contract.
func prepareAudio(req Request) ([]byte, error) {
	b := req.Data
	switch req.AudioFormat {
	case "", "wav":
		if req.SampleRate != 0 || req.Channels != 0 {
			return nil, ErrInvalid
		}
	case "pcm_s16le":
		if req.SampleRate < 8000 || req.SampleRate > 48000 || req.Channels < 1 || req.Channels > 2 || len(b) > (5<<20)-44 {
			return nil, ErrInvalid
		}
		align := req.Channels * 2
		byteRate := req.SampleRate * align
		if len(b)%align != 0 || len(b) < byteRate || len(b) > 60*byteRate {
			return nil, ErrInvalid
		}
		out := make([]byte, 44+len(b))
		copy(out, "RIFF")
		binary.LittleEndian.PutUint32(out[4:], uint32(len(out)-8))
		copy(out[8:], "WAVEfmt ")
		binary.LittleEndian.PutUint32(out[16:], 16)
		binary.LittleEndian.PutUint16(out[20:], 1)
		binary.LittleEndian.PutUint16(out[22:], uint16(req.Channels))
		binary.LittleEndian.PutUint32(out[24:], uint32(req.SampleRate))
		binary.LittleEndian.PutUint32(out[28:], uint32(byteRate))
		binary.LittleEndian.PutUint16(out[32:], uint16(align))
		binary.LittleEndian.PutUint16(out[34:], 16)
		copy(out[36:], "data")
		binary.LittleEndian.PutUint32(out[40:], uint32(len(b)))
		copy(out[44:], b)
		b = out
	default:
		return nil, ErrInvalid
	}
	if !ValidAudio(b, Limits{5 << 20, 60}) {
		return nil, ErrInvalid
	}
	return b, nil
}
