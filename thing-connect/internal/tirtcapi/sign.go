// Package tirtcapi implements TGV1-HMAC-SHA256 request signing for tange.ai server APIs.
package tirtcapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const TGV1Alg = "TGV1-HMAC-SHA256"

// SignTGV1Request builds the full set of TGV1-signed HTTP headers for a request.
func SignTGV1Request(secretKey, accessKey, appID, method, uriPath, rawQuery string, body []byte, contentType string, signing time.Time) http.Header {
	method = strings.ToUpper(method)
	tgDate := signing.UTC().Format("20060102T150405Z")
	scope := buildCredentialScope(signing)
	payloadHash := SHA256Hex(body)

	hv := map[string]string{
		"x-tg-algorithm":      TGV1Alg,
		"x-tg-date":           tgDate,
		"x-tg-app-id":         strings.TrimSpace(appID),
		"x-tg-content-sha256": payloadHash,
	}
	if requestContentHeadersMustBeSigned(method, body) {
		ct := contentType
		if ct == "" {
			ct = "application/json"
		}
		hv["content-type"] = ct
		hv["content-length"] = strconv.Itoa(len(body))
	}

	names := sortedKeysLower(hv)
	signedHeaders := strings.Join(names, ";")

	var canonLines []string
	for _, name := range names {
		canonLines = append(canonLines, name+":"+strings.TrimSpace(hv[name]))
	}
	hCanon := strings.Join(canonLines, "\n")

	uriP := canonicalURIPath(uriPath)
	qCanon := tgv1CanonicalQuery(method, strings.TrimPrefix(rawQuery, "?"))

	canonicalReq := strings.Join([]string{method, uriP, qCanon, hCanon, signedHeaders, payloadHash}, "\n")
	hashCanon := SHA256Hex([]byte(canonicalReq))
	strToSign := strings.Join([]string{TGV1Alg, tgDate, scope, hashCanon}, "\n")

	k := hmacSHA256(signing.UTC().Format("20060102"), []byte("TGV1"+secretKey))
	k = hmacSHA256(uriP, k)
	k = hmacSHA256("tgv1_request", k)
	sig := hex.EncodeToString(hmacSHA256(strToSign, k))

	cred := accessKey + "/" + scope
	auth := TGV1Alg + " Credential=" + cred + ", SignedHeaders=" + signedHeaders + ", Signature=" + sig

	out := http.Header{}
	out.Set("X-Tg-Algorithm", hv["x-tg-algorithm"])
	out.Set("X-Tg-Date", hv["x-tg-date"])
	out.Set("X-Tg-App-Id", hv["x-tg-app-id"])
	out.Set("X-Tg-Content-Sha256", hv["x-tg-content-sha256"])
	out.Set("X-Tg-Signed-Headers", signedHeaders)
	if ct := hv["content-type"]; ct != "" {
		out.Set("Content-Type", ct)
	}
	if cl := hv["content-length"]; cl != "" {
		out.Set("Content-Length", cl)
	}
	out.Set("Authorization", auth)
	return out
}

func requestContentHeadersMustBeSigned(method string, body []byte) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	case http.MethodDelete:
		return len(body) > 0
	default:
		return false
	}
}

func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func buildCredentialScope(signing time.Time) string {
	return signing.UTC().Add(7*24*time.Hour).Format("20060102") + "/tgv1_request"
}

func hmacSHA256(data string, key []byte) []byte {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte(data))
	return m.Sum(nil)
}

func canonicalURIPath(p string) string {
	p = strings.TrimSpace(p)
	if len(p) > 1 && strings.HasSuffix(p, "/") {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

func tgv1CanonicalQuery(method, rawQuery string) string {
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return ""
	default:
		return strings.ReplaceAll(rawQuery, "+", "%20")
	}
}

func sortedKeysLower(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, strings.ToLower(k))
	}
	sort.Strings(keys)
	return keys
}
