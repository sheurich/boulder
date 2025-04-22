
//go:build integration

package integration

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"strings"
	"testing"

	"github.com/eggsampler/acme/v3"
)

func TestDNSAccount01WildcardDomain(t *testing.T) {
	t.Parallel()

	hostDomain := randomDomain(t)
	wildcardDomain := fmt.Sprintf("*.%s", randomDomain(t))
	
	c, err := makeClient()
	if err != nil {
		t.Fatalf("creating client: %s", err)
	}

	idents := []acme.Identifier{
		{Type: "dns", Value: hostDomain},
		{Type: "dns", Value: wildcardDomain},
	}

	order, err := c.Client.NewOrder(c.Account, idents)
	if err != nil {
		t.Fatalf("creating new order: %s", err)
	}

	for _, authzURL := range order.Authorizations {
		auth, err := c.Client.FetchAuthorization(c.Account, authzURL)
		if err != nil {
			t.Fatalf("fetching authorization: %s", err)
		}

		isWildcard := strings.HasPrefix(auth.Identifier.Value, "*.")
		domain := auth.Identifier.Value
		if isWildcard {
			domain = strings.TrimPrefix(domain, "*.")
		}

		chal, ok := auth.ChallengeMap[acme.ChallengeTypeDNSAccount01]
		if !ok {
			t.Fatalf("DNS-Account-01 challenge not offered for %s", domain)
		}

		_, err = testSrvClient.AddDNSAccount01Response(c.Account.URL, domain, chal.KeyAuthorization)
		if err != nil {
			t.Fatalf("adding DNS response: %s", err)
		}
		t.Cleanup(func() {
			_, _ = testSrvClient.RemoveDNSAccount01Response(c.Account.URL, domain)
		})

		chal, err = c.Client.UpdateChallenge(c.Account, chal)
		if err != nil {
			t.Fatalf("updating challenge: %s", err)
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating cert key: %s", err)
	}

	csr, err := makeCSR(key, idents, false)
	if err != nil {
		t.Fatalf("making CSR: %s", err)
	}

	order, err = c.Client.FinalizeOrder(c.Account, order, csr)
	if err != nil {
		t.Fatalf("finalizing order: %s", err)
	}

	certs, err := c.Client.FetchCertificates(c.Account, order.Certificate)
	if err != nil {
		t.Fatalf("fetching certificates: %s", err)
	}

	if len(certs) == 0 {
		t.Fatal("no certificates returned")
	}

	foundHost := false
	foundWildcard := false
	for _, name := range certs[0].DNSNames {
		if name == hostDomain {
			foundHost = true
		}
		if name == wildcardDomain {
			foundWildcard = true
		}
	}
	
	if !foundHost {
		t.Errorf("certificate doesn't contain host domain %s", hostDomain)
	}
	if !foundWildcard {
		t.Errorf("certificate doesn't contain wildcard domain %s", wildcardDomain)
	}
}
