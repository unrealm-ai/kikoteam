package service

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// HumanShortCode is a convenience locator, not authorization. Manager checks
// the complete identity and resolves any collision rather than guessing.
func HumanShortCode(id string, version int64) string {
	key := strings.ToLower(id)
	if version > 1 {
		key += ":" + strconv.FormatInt(version, 10)
	}
	hash := sha256.Sum256([]byte(key))
	return "H-" + strings.ToUpper(hex.EncodeToString(hash[:6]))
}

func IsHumanCode(value string) bool {
	if ValidID(value) {
		return true
	}
	if len(value) != 14 || !strings.EqualFold(value[:2], "H-") {
		return false
	}
	_, err := hex.DecodeString(value[2:])
	return err == nil
}
