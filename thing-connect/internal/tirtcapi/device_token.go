package tirtcapi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// BuildDeviceToken builds a TiRTC connect token (`v1.{payload}.{signature}`) that
// authorizes connecting to the device identified by remoteID. The payload is
// signed twice: once with the target device's own secret key (deviceSecretKey,
// proving the server knows that specific device's key), then again with the
// app-level secretKeyID. Both signatures must be present for the TiRTC SDK to
// accept the token.
func BuildDeviceToken(accessKeyID, secretKeyID, deviceSecretKey, remoteID string) (string, error) {
	now := time.Now().Unix()
	nonce, err := randB64(16)
	if err != nil {
		return "", err
	}
	payload := map[string]interface{}{
		"sub":   "thing-connect",
		"scope": "connect:device://" + remoteID,
		"iss":   accessKeyID,
		"iat":   now,
		"exp":   now + 3600,
		"nonce": nonce,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)
	deviceSig := hmacSHA256B64(deviceSecretKey, payloadB64)
	appSig := hmacSHA256B64(secretKeyID, payloadB64+"."+deviceSig)
	return fmt.Sprintf("v1.%s.%s", payloadB64, appSig), nil
}

func hmacSHA256B64(key, data string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func randB64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
