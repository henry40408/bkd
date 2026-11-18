package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// UnixNow is the current time as Unix seconds, the unit every timestamp column uses.
func UnixNow() int64 {
	return time.Now().Unix()
}

// randomBytes panics when the operating system's random source fails: every
// caller needs the bytes for a secret, and continuing with predictable bytes
// would be worse than stopping.
func randomBytes(size int) []byte {
	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		panic("operating system random source failed: " + err.Error())
	}
	return bytes
}

func toHex(bytes []byte) string {
	return hex.EncodeToString(bytes)
}

func sha256Hex(input string) string {
	sum := sha256.Sum256([]byte(input))
	return toHex(sum[:])
}
