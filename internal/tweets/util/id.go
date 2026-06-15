package util

import (
	"crypto/rand"
	"encoding/hex"
)

// GenID generates a random hexadecimal ID (32 characters)
func GenID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
