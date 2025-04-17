package va

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/letsencrypt/boulder/bdns"
	"github.com/letsencrypt/boulder/features"
	"github.com/letsencrypt/boulder/identifier"
	blog "github.com/letsencrypt/boulder/log"
	"github.com/letsencrypt/boulder/test"
)

type customMockDNS struct {
	bdns.MockClient
	keyAuthDigests map[string]string
}

func (m *customMockDNS) LookupTXT(ctx context.Context, hostname string) ([]string, bdns.ResolverAddrs, error) {
	hostname = strings.Replace(hostname, "*.", "", 1)
	if digest, ok := m.keyAuthDigests[hostname]; ok {
		return []string{digest}, bdns.ResolverAddrs{"CustomMockClient"}, nil
	}
	return m.MockClient.LookupTXT(ctx, hostname)
}

func TestDNSAccount01WildcardValidation(t *testing.T) {
	// and validates against the base domain
	mockLog := blog.NewMock()
	mockDNS := &customMockDNS{
		MockClient:     bdns.MockClient{Log: mockLog},
		keyAuthDigests: make(map[string]string),
	}
	va, _ := setup(nil, "", nil, mockDNS)
	va.dnsClient = mockDNS

	features.Set(features.Config{DNSAccount01Enabled: true})
	defer features.Reset()

	ctx := context.Background()
	accountURL := "https://example.com/acme/acct/ExampleAccount"

	keyAuth := "test-key-auth-value"

	h := sha256.New()
	h.Write([]byte(keyAuth))
	keyAuthDigest := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	label := "ujmmovf2vn55tgye" // Pre-calculated for "https://example.com/acme/acct/ExampleAccount"

	baseDomain := "good-dns01.com"
	challengeSubdomain := fmt.Sprintf("_%s._acme-challenge.%s", label, baseDomain)
	mockDNS.keyAuthDigests[challengeSubdomain] = keyAuthDigest

	baseIdent := identifier.NewDNS(baseDomain)
	// Validate the base domain
	records, err := va.validateDNSAccount01(ctx, baseIdent, keyAuth, accountURL)
	test.AssertNotError(t, err, "Expected validation to succeed for base domain")
	test.AssertEquals(t, len(records), 1)
	test.AssertEquals(t, records[0].DnsName, baseDomain)

	wildcardDomain := "*.good-dns01.com"
	wildcardIdent := identifier.NewDNS(wildcardDomain)
	// Validate the wildcard domain - should strip the "*." prefix internally
	records, err = va.validateDNSAccount01(ctx, wildcardIdent, keyAuth, accountURL)
	test.AssertNotError(t, err, "Expected validation to succeed for wildcard domain")
	test.AssertEquals(t, len(records), 1)
	test.AssertEquals(t, records[0].DnsName, wildcardDomain)
}
