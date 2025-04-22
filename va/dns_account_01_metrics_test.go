package va

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/letsencrypt/boulder/bdns"
	"github.com/letsencrypt/boulder/features"
	"github.com/letsencrypt/boulder/identifier"
	blog "github.com/letsencrypt/boulder/log"
	"github.com/letsencrypt/boulder/test"
)

type customMockDNSClient struct {
	bdns.MockClient
	keyAuthDigest string
}

func (m *customMockDNSClient) LookupTXT(ctx context.Context, hostname string) ([]string, bdns.ResolverAddrs, error) {
	if hostname == "_ujmmovf2vn55tgye._acme-challenge.good-dns01.com" {
		return []string{m.keyAuthDigest}, bdns.ResolverAddrs{"MockClient"}, nil
	}
	if hostname == "_ujmmovf2vn55tgye._acme-challenge.multiple-one-match.com" {
		return []string{"a", m.keyAuthDigest, "c"}, bdns.ResolverAddrs{"MockClient"}, nil
	}
	if hostname == "_ujmmovf2vn55tgye._acme-challenge.wrong-dns01.com" {
		return []string{"a"}, bdns.ResolverAddrs{"MockClient"}, nil
	}
	if hostname == "_ujmmovf2vn55tgye._acme-challenge.timeout.com" {
		return nil, bdns.ResolverAddrs{"MockClient"}, fmt.Errorf("so sloooow")
	}
	if hostname == "_ujmmovf2vn55tgye._acme-challenge.servfail.com" {
		return nil, bdns.ResolverAddrs{"MockClient"}, fmt.Errorf("SERVFAIL")
	}
	if hostname == "_ujmmovf2vn55tgye._acme-challenge.multiple-none-match.com" {
		return []string{"a", "b", "c", "d", "e"}, bdns.ResolverAddrs{"MockClient"}, nil
	}
	if hostname == "_ujmmovf2vn55tgye._acme-challenge.empty-txts.com" {
		return []string{}, bdns.ResolverAddrs{"MockClient"}, nil
	}
	return m.MockClient.LookupTXT(ctx, hostname)
}

func TestDNSAccount01ValidationMetrics(t *testing.T) {
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

	time.Sleep(10 * time.Millisecond)
}
