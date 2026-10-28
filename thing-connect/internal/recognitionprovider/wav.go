package recognitionprovider

import "encoding/binary"

// normalizeSpeechWAV finishes known streaming headers from Qwen without changing
// PCM samples. Other length mismatches are errors, not evidence of a stream header.
func normalizeSpeechWAV(b []byte) bool {
	if len(b) < 44 || len(b) > 10<<20 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return false
	}
	riffSize := binary.LittleEndian.Uint32(b[4:8])
	hasFmt, hasData := false, false
	var align uint16
	for offset := 12; offset < len(b); {
		if len(b)-offset < 8 {
			return false
		}
		kind := string(b[offset : offset+4])
		size := binary.LittleEndian.Uint32(b[offset+4 : offset+8])
		start := offset + 8
		if kind == "data" && ((riffSize == 0x7fffffbf && size == 0x7fffff9b) || (riffSize == 0xffffffff && size == 0xffffffff)) {
			// These upstream placeholders mean data continues to the completed
			// HTTP body's end. Never apply this to ordinary truncated WAV files.
			size = uint32(len(b) - start)
			riffSize = uint32(len(b) - 8)
			binary.LittleEndian.PutUint32(b[4:8], riffSize)
			binary.LittleEndian.PutUint32(b[offset+4:offset+8], size)
		}
		if uint64(size) > uint64(len(b)-start) {
			return false
		}
		switch kind {
		case "fmt ":
			if hasFmt || hasData || size < 16 || binary.LittleEndian.Uint16(b[start:start+2]) != 1 {
				return false
			}
			channels := binary.LittleEndian.Uint16(b[start+2 : start+4])
			rate := binary.LittleEndian.Uint32(b[start+4 : start+8])
			byteRate := binary.LittleEndian.Uint32(b[start+8 : start+12])
			align = binary.LittleEndian.Uint16(b[start+12 : start+14])
			bits := binary.LittleEndian.Uint16(b[start+14 : start+16])
			if channels < 1 || channels > 2 || rate < 8000 || rate > 48000 || bits != 16 || align != channels*2 || byteRate != rate*uint32(align) {
				return false
			}
			hasFmt = true
		case "data":
			if !hasFmt || hasData || size == 0 || size%uint32(align) != 0 {
				return false
			}
			hasData = true
		}
		offset = start + int(size)
		if size%2 != 0 {
			if offset == len(b) {
				return false
			}
			offset++
		}
	}
	return hasFmt && hasData && uint64(riffSize)+8 == uint64(len(b))
}
