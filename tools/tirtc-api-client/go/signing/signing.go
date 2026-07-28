// Package signing implements TGV1-HMAC-SHA256 request signing for tange.ai server APIs.
package signing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const algTGV1 = "TGV1-HMAC-SHA256"

// SignRequest builds signed HTTP headers for a tange.ai OpenAPI request.
func SignRequest(accessKey, accessSecret, appID, method, uriPath, rawQuery string, body []byte, signingTime time.Time) http.Header {
	method = strings.ToUpper(method)
	now := signingTime.UTC()
	tgDate := now.Format("20060102T150405Z")
	payloadHash := sha256Hex(body)

	// Build all headers that will be sent
	h := http.Header{}
	h.Set("X-Tg-Algorithm", algTGV1)
	h.Set("X-Tg-Date", tgDate)
	h.Set("X-Tg-App-Id", strings.TrimSpace(appID))
	h.Set("X-Tg-Content-Sha256", payloadHash)

	// Signed headers: only x-tg-app-id, x-tg-date, and (for methods with body) content-type, content-length.
	// x-tg-algorithm and x-tg-content-sha256 are sent but NOT included in the signature.
	signedNames := []string{"x-tg-app-id", "x-tg-date"}
	if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
		h.Set("Content-Type", "application/json")
		h.Set("Content-Length", strconv.Itoa(len(body)))
		signedNames = append([]string{"content-length", "content-type"}, signedNames...)
	}
	sort.Strings(signedNames)
	signedHdrs := strings.Join(signedNames, ";")
	h.Set("X-Tg-Signed-Headers", signedHdrs)

	// Canonical headers: only signed headers, key:value, sorted
	var canonLines []string
	for _, name := range signedNames {
		canonLines = append(canonLines, name+":"+strings.TrimSpace(h.Get(name)))
	}
	canonHdrs := strings.Join(canonLines, "\n")

	// Canonical request
	uri := canonicalURI(uriPath)
	qCanon := canonicalQuery(method, strings.TrimPrefix(rawQuery, "?"))
	canonReq := strings.Join([]string{method, uri, qCanon, canonHdrs, signedHdrs, payloadHash}, "\n")

	// String to sign
	scope := now.Add(7 * 24 * time.Hour).Format("20060102") + "/tgv1_request"
	strToSign := strings.Join([]string{algTGV1, tgDate, scope, sha256Hex([]byte(canonReq))}, "\n")

	// Derive signing key (3-level HMAC chain)
	k := hmacSHA256(now.Format("20060102"), []byte("TGV1"+accessSecret))
	k = hmacSHA256(uri, k)
	k = hmacSHA256("tgv1_request", k)

	// Signature
	sig := hex.EncodeToString(hmacSHA256(strToSign, k))

	// Authorization
	auth := fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algTGV1, accessKey, scope, signedHdrs, sig)
	h.Set("Authorization", auth)

	return h
}

func sha256Hex(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

func hmacSHA256(data string, key []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func canonicalURI(p string) string {
	p = strings.TrimSpace(p)
	if len(p) > 1 && strings.HasSuffix(p, "/") {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

func canonicalQuery(method, rawQuery string) string {
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return ""
	default:
		return strings.ReplaceAll(rawQuery, "+", "%20")
	}
}
