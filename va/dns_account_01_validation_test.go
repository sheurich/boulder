package va

import (
	"context"
	"fmt"
	"testing"

	"github.com/letsencrypt/boulder/bdns"
	"github.com/letsencrypt/boulder/features"
	"github.com/letsencrypt/boulder/identifier"
	blog "github.com/letsencrypt/boulder/log"
	"github.com/letsencrypt/boulder/test"
)

func TestDNSAccount01ValidationEdgeCases(t *testing.T) {
	mockLog := blog.NewMock()
	mockDNS := &customMockDNSClient{
		MockClient:    bdns.MockClient{Log: mockLog},
		keyAuthDigest: calculateKeyAuthorizationDigest(expectedKeyAuthorization),
	}
	va, _ := setup(nil, "", nil, mockDNS)
	va.dnsClient = mockDNS

	features.Set(features.Config{DNSAccount01Enabled: true})
	defer features.Reset()

	ctx := context.Background()
	accountURL := "https://example.com/acme/acct/ExampleAccount"
	emptyDomain := ""
	var err error
	_, err = va.validateDNSAccount01(ctx, identifier.NewDNS(emptyDomain), expectedKeyAuthorization, accountURL)
	test.AssertError(t, err, fmt.Sprintf("Expected validation to fail with empty domain %q", emptyDomain))

	_, err = va.validateDNSAccount01(ctx, identifier.NewDNS("good-dns01.com"), expectedKeyAuthorization, "")
	test.AssertError(t, err, "Expected validation to fail with empty account URL")

	_, err = va.validateDNSAccount01(ctx, identifier.NewDNS("good-dns01.com"), "", accountURL)
	test.AssertError(t, err, "Expected validation to fail with empty key authorization")

	_, err = va.validateDNSAccount01(ctx, identifier.NewDNS("empty-txts.com"), expectedKeyAuthorization, accountURL)
	test.AssertError(t, err, "Expected validation to fail with empty TXT records")

	_, err = va.validateDNSAccount01(ctx, identifier.NewDNS("long-txt-dns01.com"), expectedKeyAuthorization, accountURL)
	test.AssertError(t, err, "Expected validation to fail with long TXT record")

	features.Set(features.Config{DNSAccount01Enabled: false})
	_, err = va.validateDNSAccount01(ctx, identifier.NewDNS("good-dns01.com"), expectedKeyAuthorization, accountURL)
	test.AssertError(t, err, "Expected validation to fail with feature flag disabled")
	features.Set(features.Config{DNSAccount01Enabled: true})

	_, err = va.validateDNSAccount01(ctx, identifier.NewDNS("good-dns01.com"), expectedKeyAuthorization, "https://example.com/acme/acct/DifferentAccount")
	test.AssertError(t, err, "Expected validation to fail with different account URL")

	test.AssertContains(t, err.Error(), "Incorrect TXT record")
}
