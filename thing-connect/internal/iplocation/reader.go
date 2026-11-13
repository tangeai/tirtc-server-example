// Package iplocation adapts private offline IP databases to Admin display queries.
package iplocation

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	"github.com/oschwald/maxminddb-golang"
	"thing-connect/internal/admin"
)

type Reader struct {
	mu        sync.RWMutex
	city, asn *maxminddb.Reader
	qq        *qqwry
}

// Open tolerates absent optional databases and returns partial capability when
// one file is damaged. The caller logs the error without disabling Admin.
func Open(directory string) (*Reader, error) {
	r := &Reader{}
	var errs []error
	open := func(name string, target **maxminddb.Reader) {
		db, err := maxminddb.Open(filepath.Join(directory, name))
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if err == nil {
			err = db.Verify()
		}
		if err != nil {
			if db != nil {
				_ = db.Close()
			}
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			return
		}
		*target = db
	}
	open("GeoLite2-City.mmdb", &r.city)
	open("GeoLite2-ASN.mmdb", &r.asn)
	q, err := openQQWry(filepath.Join(directory, "qqwry.dat"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, err)
	} else {
		r.qq = q
	}
	return r, errors.Join(errs...)
}
func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var errs []error
	if r.city != nil {
		errs = append(errs, r.city.Close())
		r.city = nil
	}
	if r.asn != nil {
		errs = append(errs, r.asn.Close())
		r.asn = nil
	}
	r.qq = nil
	return errors.Join(errs...)
}

type namedPlace struct {
	Names map[string]string `maxminddb:"names"`
	Code  string            `maxminddb:"iso_code"`
}
type cityRecord struct {
	Country      namedPlace   `maxminddb:"country"`
	Subdivisions []namedPlace `maxminddb:"subdivisions"`
	City         namedPlace   `maxminddb:"city"`
}

func (r *Reader) Lookup(value string) admin.IPLocation {
	result := admin.IPLocation{Status: "unknown"}
	ip, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil {
		if value == "" {
			result.Status = "empty"
		} else {
			result.Status = "invalid"
		}
		return result
	}
	ip = ip.Unmap()
	if ip.IsLoopback() {
		result.Status = "loopback"
		return result
	}
	if ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		result.Status = "private"
		return result
	}
	if !ip.IsGlobalUnicast() {
		result.Status = "reserved"
		return result
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.city == nil && r.asn == nil && r.qq == nil {
		result.Status = "unavailable"
		return result
	}
	if r.city != nil {
		var record cityRecord
		if r.city.Lookup(net.IP(ip.AsSlice()), &record) == nil {
			result.Country = chineseName(record.Country.Names)
			if len(record.Subdivisions) > 0 {
				result.Province = chineseName(record.Subdivisions[0].Names)
				if result.Province == "" && record.Country.Code == "CN" {
					result.Province = provinceCodes[record.Subdivisions[0].Code]
				}
			}
			result.City = strings.TrimSpace(record.City.Names["zh-CN"])
			if !hasChinese(result.City) {
				result.City = ""
				if len(record.Subdivisions) > 0 {
					result.City = cityAliases[record.Country.Code+"/"+record.Subdivisions[0].Code+"/"+strings.ToLower(strings.TrimSpace(record.City.Names["en"]))]
				}
			}
		}
	}
	region, provider := r.qq.lookup(ip)
	if hasChinese(region) {
		result.Region = region
	}
	result.ISP = operatorChinese(provider)
	if result.ISP == "" && r.asn != nil {
		var record struct {
			Organization string `maxminddb:"autonomous_system_organization"`
		}
		if r.asn.Lookup(net.IP(ip.AsSlice()), &record) == nil {
			result.ISP = operatorChinese(record.Organization)
		}
	}
	if result.Region != "" || result.Country != "" || result.Province != "" || result.City != "" || result.ISP != "" {
		result.Status = "found"
	}
	return result
}
func hasChinese(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}
func chineseName(names map[string]string) string {
	if name := strings.TrimSpace(names["zh-CN"]); hasChinese(name) {
		return name
	}
	// Exact place aliases avoid guessing homophones. Database Chinese names win.
	return placeAliases[strings.ToLower(strings.TrimSpace(names["en"]))]
}
func operatorChinese(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name == "CZ88.NET" {
		return ""
	}
	if hasChinese(name) {
		return name
	}
	upper := strings.ToUpper(name)
	for _, alias := range operatorAliases {
		if strings.Contains(upper, alias.match) {
			return alias.chinese
		}
	}
	return ""
}

var operatorAliases = []struct{ match, chinese string }{
	{"CHINANET", "中国电信"}, {"CHINA TELECOM", "中国电信"}, {"UNICOM", "中国联通"}, {"CMNET", "中国移动"}, {"CHINA MOBILE", "中国移动"}, {"CERNET", "中国教育和科研计算机网"}, {"ALIBABA", "阿里云"}, {"TENCENT", "腾讯云"}, {"HUAWEI", "华为云"}, {"GOOGLE", "谷歌网络"}, {"AMAZON", "亚马逊网络"}, {"MICROSOFT", "微软网络"},
}
var provinceCodes = map[string]string{"BJ": "北京", "TJ": "天津", "HE": "河北", "SX": "山西", "NM": "内蒙古", "LN": "辽宁", "JL": "吉林", "HL": "黑龙江", "SH": "上海", "JS": "江苏", "ZJ": "浙江", "AH": "安徽", "FJ": "福建", "JX": "江西", "SD": "山东", "HA": "河南", "HB": "湖北", "HN": "湖南", "GD": "广东", "GX": "广西", "HI": "海南", "CQ": "重庆", "SC": "四川", "GZ": "贵州", "YN": "云南", "XZ": "西藏", "SN": "陕西", "GS": "甘肃", "QH": "青海", "NX": "宁夏", "XJ": "新疆"}
var placeAliases = map[string]string{"china": "中国", "hong kong": "香港", "taiwan": "台湾", "macau": "澳门"}

// Qualified aliases distinguish homophones such as Suzhou in Jiangsu/Anhui.
var cityAliases = map[string]string{"CN/GD/shenzhen": "深圳", "CN/GD/guangzhou": "广州", "CN/ZJ/hangzhou": "杭州", "CN/JS/nanjing": "南京", "CN/JS/suzhou": "苏州", "CN/AH/suzhou": "宿州", "CN/SC/chengdu": "成都", "CN/HB/wuhan": "武汉", "CN/SN/xi'an": "西安", "CN/FJ/xiamen": "厦门", "CN/FJ/fuzhou": "福州", "CN/SD/jinan": "济南", "CN/SD/qingdao": "青岛", "CN/BJ/beijing": "北京", "CN/SH/shanghai": "上海", "CN/TJ/tianjin": "天津", "CN/CQ/chongqing": "重庆"}
