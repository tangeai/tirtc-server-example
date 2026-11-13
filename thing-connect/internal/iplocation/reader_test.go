package iplocation

import (
	"encoding/binary"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestChineseNames(t *testing.T) {
	if got := chineseName(map[string]string{"en": "Shenzhen", "zh-CN": "深圳"}); got != "深圳" {
		t.Fatal(got)
	}
	if cityAliases["CN/JS/suzhou"] != "苏州" || cityAliases["CN/AH/suzhou"] != "宿州" {
		t.Fatal("ambiguous city mapping")
	}
	for input, want := range map[string]string{"CHINANET Guangdong province network": "中国电信", "China Unicom": "中国联通", "CMNET": "中国移动", "Unknown Provider": "", "中国电信": "中国电信"} {
		if got := operatorChinese(input); got != want {
			t.Fatalf("%s: %s", input, got)
		}
	}
}
func TestMissingAndInvalidDatabases(t *testing.T) {
	dir := t.TempDir()
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for ip, want := range map[string]string{"": "empty", "bad": "invalid", "127.0.0.1": "loopback", "::ffff:127.0.0.1": "loopback", "10.0.0.1": "private", "fe80::1": "private", "0.0.0.0": "reserved", "8.8.8.8": "unavailable"} {
		if got := r.Lookup(ip).Status; got != want {
			t.Fatalf("%s: %s", ip, got)
		}
	}
	for _, name := range []string{"qqwry.dat", "GeoLite2-City.mmdb", "GeoLite2-ASN.mmdb"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("broken"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r, err = Open(dir)
	if err == nil {
		t.Fatal("corrupt data accepted")
	}
	defer r.Close()
	if r.Lookup("8.8.8.8").Status != "unavailable" {
		t.Fatal("corrupt data used")
	}
}
func TestQQWryRedirectsAndBounds(t *testing.T) {
	encode := func(s string) []byte {
		b, e := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(s))
		if e != nil {
			t.Fatal(e)
		}
		return append(b, 0)
	}
	for _, mode := range []byte{0, 1, 2} {
		t.Run(string(rune('0'+mode)), func(t *testing.T) {
			data := make([]byte, 64)
			binary.LittleEndian.PutUint32(data, 8)
			binary.LittleEndian.PutUint32(data[4:], 8)
			binary.LittleEndian.PutUint32(data[8:], 0x01020300)
			data[12] = 15
			binary.LittleEndian.PutUint32(data[15:], 0x010203ff)
			country, area := encode("广东省深圳市"), encode("电信")
			switch mode {
			case 0:
				copy(data[19:], append(country, area...))
			case 1:
				data[19] = 1
				data[20] = 24
				copy(data[24:], append(country, area...))
			case 2:
				data[19] = 2
				data[20] = 32
				copy(data[23:], area)
				copy(data[32:], country)
			}
			path := filepath.Join(t.TempDir(), "q.dat")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			q, err := openQQWry(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, ip := range []string{"1.2.3.0", "1.2.3.255"} {
				region, isp := q.lookup(netip.MustParseAddr(ip))
				if region != "广东省深圳市" || isp != "电信" {
					t.Fatalf("%s: %s/%s", ip, region, isp)
				}
			}
			for _, ip := range []string{"1.2.2.255", "1.2.4.0", "::1"} {
				region, _ := q.lookup(netip.MustParseAddr(ip))
				if region != "" {
					t.Fatal("outside range")
				}
			}
			q.data[19] = 1
			q.data[20] = 19
			q.data[21] = 0
			q.data[22] = 0
			if region, _ := q.lookup(netip.MustParseAddr("1.2.3.1")); region != "" {
				t.Fatal("redirect cycle")
			}
		})
	}
}
func TestActualDatabases(t *testing.T) {
	dir := os.Getenv("THING_CONNECT_IPDB_TEST_DIR")
	if dir == "" {
		t.Skip("set THING_CONNECT_IPDB_TEST_DIR for private offline data")
	}
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.qq == nil || r.city == nil || r.asn == nil {
		t.Fatal("all three datasets required")
	}
	for _, ip := range []string{"223.5.5.5", "114.114.114.114", "8.8.8.8", "240e::1"} {
		got := r.Lookup(ip)
		t.Logf("%s: %+v", ip, got)
		if got.Status != "found" {
			t.Fatal(got)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				r.Lookup("223.5.5.5")
			}
		}()
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
}
