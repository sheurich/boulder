package va

import (
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strings"
	"testing"

	"github.com/letsencrypt/boulder/test"
)

func TestDNSAccount01SpecificationExample(t *testing.T) {
	const accountResourceURL = "https://example.com/acme/acct/ExampleAccount"
	const domain = "example.org"
	const expectedLabel = "ujmmovf2vn55tgye"

	label := calculateLabel(accountResourceURL)
	test.AssertEquals(t, strings.ToLower(label), expectedLabel)

	expectedValidationDomain := fmt.Sprintf("_%s._acme-challenge.%s", expectedLabel, domain)
	validationDomain := fmt.Sprintf("_%s._acme-challenge.%s", strings.ToLower(label), domain)
	test.AssertEquals(t, validationDomain, expectedValidationDomain)
}

func calculateLabel(accountURI string) string {
	x := sha256.Sum256([]byte(accountURI))
	return base32.StdEncoding.EncodeToString(x[0:10])[:16]
}
