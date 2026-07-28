package physid

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Hash returns a short hex hash of the MAC — the device fingerprint.
// chip_uid/device_rand are no longer part of the fingerprint identity.
func Hash(mac string) string {
	h := sha256.Sum256([]byte(mac))
	return hex.EncodeToString(h[:8])
}

// Physical returns the canonical physical_id string ("mac_{MAC}") stored in Redis.
func Physical(mac string) string {
	return "mac_" + mac
}

// Parse extracts the MAC from a "mac_{MAC}" physical_id string.
func Parse(physicalID string) string {
	const macPfx = "mac_"
	if !strings.HasPrefix(physicalID, macPfx) {
		return ""
	}
	return physicalID[len(macPfx):]
}
