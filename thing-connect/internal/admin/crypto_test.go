package admin

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestCipherRoundTripAndAAD(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	c, err := NewCipher("local-v1", key)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := c.Encrypt([]byte(`{"password":"secret"}`), "user-server/smtp/global/")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := c.Decrypt(encrypted, "user-server/smtp/global/")
	if err != nil || string(plain) != `{"password":"secret"}` {
		t.Fatalf("decrypt = %q, %v", plain, err)
	}
	if _, err := c.Decrypt(encrypted, "user-server/captcha/global/"); err == nil {
		t.Fatal("ciphertext must not decrypt with another config entry AAD")
	}
}

func TestTOTPValidationRejectsReplay(t *testing.T) {
	secret := "JBSWY3DPEHPK3PXP"
	at := time.Unix(1_700_000_000, 0)
	step := at.Unix() / totpPeriod
	code, err := totpCode(secret, step)
	if err != nil {
		t.Fatal(err)
	}
	matched, ok := ValidateTOTP(secret, code, at, 0)
	if !ok || matched != step {
		t.Fatalf("valid code rejected: matched=%d ok=%v", matched, ok)
	}
	if _, ok := ValidateTOTP(secret, code, at, matched); ok {
		t.Fatal("replayed code accepted")
	}
}

func TestRecoveryCodesAreDistinct(t *testing.T) {
	codes, err := GenerateRecoveryCodes(10)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, code := range codes {
		if seen[code] || !strings.Contains(code, "-") {
			t.Fatalf("bad recovery code %q", code)
		}
		seen[code] = true
	}
}
