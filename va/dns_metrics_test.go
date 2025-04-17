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

func TestDNSAccount01Metrics(t *testing.T) {
	mockLog := blog.NewMock()
	mockDNS := &customMockDNSClient{
		MockClient:    bdns.MockClient{Log: mockLog},
		keyAuthDigest: calculateKeyAuthorizationDigest(expectedKeyAuthorization),
	}
	fmt.Printf("Testing DNS-ACCOUNT-01 metrics with key auth digest: %s\n", mockDNS.keyAuthDigest)
	va, _ := setup(nil, "", nil, mockDNS)
	va.dnsClient = mockDNS

	features.Set(features.Config{DNSAccount01Enabled: true})
	defer features.Reset()

	ctx := context.Background()
	domain := "good-dns01.com"
	accountURL := "https://example.com/acme/acct/ExampleAccount"
	_, err := va.validateDNSAccount01(ctx, identifier.NewDNS(domain), expectedKeyAuthorization, accountURL)
	test.AssertNotError(t, err, "Expected validation to succeed")

	domain = "wrong-dns01.com"
	_, err = va.validateDNSAccount01(ctx, identifier.NewDNS(domain), expectedKeyAuthorization, accountURL)
	test.AssertError(t, err, "Expected validation to fail")
	test.AssertContains(t, err.Error(), "Incorrect TXT record")

	domain = "timeout.com"
	_, err = va.validateDNSAccount01(ctx, identifier.NewDNS(domain), expectedKeyAuthorization, accountURL)
	test.AssertError(t, err, "Expected validation to fail with timeout")
	test.AssertContains(t, err.Error(), "so sloooow")

	domain = "servfail.com"
	_, err = va.validateDNSAccount01(ctx, identifier.NewDNS(domain), expectedKeyAuthorization, accountURL)
	test.AssertError(t, err, "Expected validation to fail with server failure")
	test.AssertContains(t, err.Error(), "SERVFAIL")

	domain = "multiple-one-match.com"
	_, err = va.validateDNSAccount01(ctx, identifier.NewDNS(domain), expectedKeyAuthorization, accountURL)
	test.AssertNotError(t, err, "Expected validation to succeed with one matching record")

	domain = "multiple-none-match.com"
	_, err = va.validateDNSAccount01(ctx, identifier.NewDNS(domain), expectedKeyAuthorization, accountURL)
	test.AssertError(t, err, "Expected validation to fail with no matching record")
	test.AssertContains(t, err.Error(), "Incorrect TXT record")
}
