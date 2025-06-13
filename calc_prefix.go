package main

import (
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strings"
)

func main() {
	accountURI := "https://example.com/acme/acct/12345"
	sha256sum := sha256.Sum256([]byte(accountURI))
	prefixBytes := sha256sum[0:10]
	prefixLabel := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(prefixBytes)
	prefixLabel = strings.ToLower(prefixLabel)
	fmt.Printf("Expected label prefix: _%s\n", prefixLabel)
}
