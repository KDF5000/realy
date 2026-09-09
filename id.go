package relay

import (
	"crypto/rand"
	"encoding/hex"
)

func newID(prefix string) string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("relay: crypto/rand unavailable: " + err.Error())
	}
	return prefix + "_" + hex.EncodeToString(value[:])
}
