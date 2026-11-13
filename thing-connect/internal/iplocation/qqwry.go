package iplocation

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net/netip"
	"os"
	"strings"

	"golang.org/x/text/encoding/simplifiedchinese"
)

type qqwry struct {
	data         []byte
	first, count uint32
}

func openQQWry(path string) (*qqwry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 8 {
		return nil, errors.New("invalid QQWry header")
	}
	first, last := binary.LittleEndian.Uint32(data), binary.LittleEndian.Uint32(data[4:])
	if first < 8 || last < first || (last-first)%7 != 0 || uint64(last)+7 > uint64(len(data)) {
		return nil, errors.New("invalid QQWry index")
	}
	q := &qqwry{data: data, first: first, count: (last-first)/7 + 1}
	var previous uint32
	for i := uint32(0); i < q.count; i++ {
		off := first + i*7
		start := binary.LittleEndian.Uint32(data[off:])
		ptr, err := q.pointer(off + 4)
		if err != nil || uint64(ptr)+5 > uint64(len(data)) || (i > 0 && start <= previous) {
			return nil, errors.New("invalid QQWry record")
		}
		previous = start
	}
	return q, nil
}
func (q *qqwry) pointer(off uint32) (uint32, error) {
	if uint64(off)+3 > uint64(len(q.data)) {
		return 0, errors.New("QQWry pointer out of bounds")
	}
	return uint32(q.data[off]) | uint32(q.data[off+1])<<8 | uint32(q.data[off+2])<<16, nil
}
func (q *qqwry) text(off uint32, depth int) (string, uint32, error) {
	if depth > 8 || uint64(off) >= uint64(len(q.data)) {
		return "", 0, errors.New("invalid QQWry string")
	}
	if off == 0 {
		return "", 0, nil
	}
	if q.data[off] == 1 || q.data[off] == 2 {
		ptr, err := q.pointer(off + 1)
		if err != nil {
			return "", 0, err
		}
		value, _, err := q.text(ptr, depth+1)
		return value, off + 4, err
	}
	end := bytes.IndexByte(q.data[off:], 0)
	if end < 0 || end > 4096 {
		return "", 0, errors.New("unterminated QQWry string")
	}
	raw, err := simplifiedchinese.GBK.NewDecoder().Bytes(q.data[off : off+uint32(end)])
	value := strings.TrimSpace(string(raw))
	if value == "CZ88.NET" {
		value = ""
	}
	return value, off + uint32(end) + 1, err
}
func (q *qqwry) record(off uint32, depth int) (string, string, error) {
	if depth > 8 || uint64(off) >= uint64(len(q.data)) {
		return "", "", errors.New("invalid QQWry redirect")
	}
	if q.data[off] == 1 {
		ptr, err := q.pointer(off + 1)
		if err != nil {
			return "", "", err
		}
		return q.record(ptr, depth+1)
	}
	country, next, err := q.text(off, depth)
	if err != nil {
		return "", "", err
	}
	area, _, err := q.text(next, depth)
	return country, area, err
}
func (q *qqwry) lookup(ip netip.Addr) (string, string) {
	if q == nil || !ip.Is4() {
		return "", ""
	}
	a := ip.As4()
	target := binary.BigEndian.Uint32(a[:])
	lo, hi := uint32(0), q.count
	for lo < hi {
		mid := lo + (hi-lo)/2
		off := q.first + mid*7
		if binary.LittleEndian.Uint32(q.data[off:]) <= target {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return "", ""
	}
	ptr, err := q.pointer(q.first + (lo-1)*7 + 4)
	if err != nil {
		return "", ""
	}
	if target > binary.LittleEndian.Uint32(q.data[ptr:]) {
		return "", ""
	}
	country, area, err := q.record(ptr+4, 0)
	if err != nil {
		return "", ""
	}
	return country, area
}
