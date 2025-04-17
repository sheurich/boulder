package va

import (
	"crypto/sha256"
	"encoding/base64"
)

func calculateKeyAuthorizationDigest(keyAuth string) string {
	h := sha256.New()
	h.Write([]byte(keyAuth))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
