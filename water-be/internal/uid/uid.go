package uid

import (
	"crypto/rand"
	"encoding/hex"
)

func New(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return prefix + "_unknown"
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}
