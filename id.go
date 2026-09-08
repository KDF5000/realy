package realy

import (
	"crypto/rand"
	"encoding/hex"
)

func newID(prefix string) string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("realy: crypto/rand unavailable: " + err.Error())
	}
	return prefix + "_" + hex.EncodeToString(value[:])
}
