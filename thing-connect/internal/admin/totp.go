package admin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // RFC 6238 interoperability profile intentionally uses SHA-1.
	"crypto/subtle"
	"encoding/base32"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	totpPeriod = int64(30)
	totpDigits = 6
)

func GenerateTOTPSecret() (string, error) {
	raw := make([]byte, 20)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", fmt.Errorf("generate TOTP secret: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), nil
}

func TOTPUri(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	query := url.Values{}
	query.Set("secret", secret)
	query.Set("issuer", issuer)
	query.Set("algorithm", "SHA1")
	query.Set("digits", strconv.Itoa(totpDigits))
	query.Set("period", strconv.FormatInt(totpPeriod, 10))
	return "otpauth://totp/" + label + "?" + query.Encode()
}

// ValidateTOTP accepts the current step and one adjacent step on either side.
// A matched step must be newer than lastUsedStep to prevent replay.
func ValidateTOTP(secret, code string, at time.Time, lastUsedStep int64) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return 0, false
	}
	step := at.Unix() / totpPeriod
	for offset := int64(-1); offset <= 1; offset++ {
		candidateStep := step + offset
		if candidateStep <= lastUsedStep {
			continue
		}
		candidate, err := totpCode(secret, candidateStep)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(code)) == 1 {
			return candidateStep, true
		}
	}
	return 0, false
}

func totpCode(secret string, step int64) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", err
	}
	msg := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		msg[i] = byte(step)
		step >>= 8
	}
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(msg)
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset])&0x7f)<<24 |
		uint32(sum[offset+1])<<16 |
		uint32(sum[offset+2])<<8 |
		uint32(sum[offset+3])
	return fmt.Sprintf("%06d", value%1_000_000), nil
}

func GenerateRecoveryCodes(count int) ([]string, error) {
	codes := make([]string, count)
	for i := range codes {
		raw := make([]byte, 10)
		if _, err := io.ReadFull(rand.Reader, raw); err != nil {
			return nil, fmt.Errorf("generate recovery code: %w", err)
		}
		encoded := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw))
		codes[i] = encoded[:8] + "-" + encoded[8:]
	}
	return codes, nil
}
