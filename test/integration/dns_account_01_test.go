//go:build integration

package integration

import (
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strings"
	"testing"

	"github.com/eggsampler/acme/v3"
)

func TestDNSAccount01HappyPath(t *testing.T) {
	t.Parallel()

	domain := random_domain()
	c, err := makeClient()
	if err != nil {
		t.Fatalf("creating client: %s", err)
	}

	idents := []acme.Identifier{{Type: "dns", Value: domain}}

	order, err := c.Client.NewOrder(c.Account, idents)
	if err != nil {
		t.Fatalf("creating new order: %s", err)
	}

	authzURL := order.Authorizations[0]
	auth, err := c.Client.FetchAuthorization(c.Account, authzURL)
	if err != nil {
		t.Fatalf("fetching authorization: %s", err)
	}

	chal, ok := auth.ChallengeMap[acme.ChallengeTypeDNSAccount01]
	if !ok {
		t.Skip("DNS-Account-01 is not offered, skipping test")
	}

	accountURL := c.Account.URL
	expectedLabel := calculateDNSAccount01Label(accountURL)
	expectedValidationName := fmt.Sprintf("_%s._acme-challenge.%s", expectedLabel, domain)
	t.Logf("Expected validation name: %s", expectedValidationName)

	_, err = testSrvClient.AddDNSAccount01Response(expectedLabel, domain, chal.KeyAuthorization)
	if err != nil {
		t.Fatalf("adding DNS response: %s", err)
	}
	t.Cleanup(func() {
		_, _ = testSrvClient.RemoveDNSAccount01Response(expectedLabel, domain)
	})

	chal, err = c.Client.UpdateChallenge(c.Account, chal)
	if err != nil {
		t.Fatalf("updating challenge: %s", err)
	}

	csrKey, err := makeCSR(nil, idents, true)
	if err != nil {
		t.Fatalf("making CSR: %s", err)
	}

	order, err = c.Client.FinalizeOrder(c.Account, order, csrKey)
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

	found := false
	for _, name := range certs[0].DNSNames {
		if name == domain {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("certificate doesn't contain domain %s", domain)
	}
}

func TestDNSAccount01WrongTXTRecord(t *testing.T) {
	t.Parallel()

	domain := random_domain()
	c, err := makeClient()
	if err != nil {
		t.Fatalf("creating client: %s", err)
	}

	idents := []acme.Identifier{{Type: "dns", Value: domain}}

	order, err := c.Client.NewOrder(c.Account, idents)
	if err != nil {
		t.Fatalf("creating new order: %s", err)
	}

	authzURL := order.Authorizations[0]
	auth, err := c.Client.FetchAuthorization(c.Account, authzURL)
	if err != nil {
		t.Fatalf("fetching authorization: %s", err)
	}

	chal, ok := auth.ChallengeMap[acme.ChallengeTypeDNSAccount01]
	if !ok {
		t.Skip("DNS-Account-01 is not offered, skipping test")
	}

	accountURL := c.Account.URL
	expectedLabel := calculateDNSAccount01Label(accountURL)
	expectedValidationName := fmt.Sprintf("_%s._acme-challenge.%s", expectedLabel, domain)
	t.Logf("Expected validation name: %s", expectedValidationName)

	// Add a wrong TXT record
	_, err = testSrvClient.AddDNSAccount01Response(expectedLabel, domain, "wrong-digest")
	if err != nil {
		t.Fatalf("adding DNS response: %s", err)
	}
	t.Cleanup(func() {
		_, _ = testSrvClient.RemoveDNSAccount01Response(expectedLabel, domain)
	})

	chal, err = c.Client.UpdateChallenge(c.Account, chal)
	if err == nil {
		t.Fatalf("updating challenge: expected error, got nil")
	}
	prob, ok := err.(acme.Problem)
	if !ok {
		t.Fatalf("updating challenge: expected acme.Problem error, got %T", err)
	}
	if prob.Type != "urn:ietf:params:acme:error:unauthorized" {
		t.Fatalf("updating challenge: expected unauthorized error, got %s", prob.Type)
	}
	if !strings.Contains(prob.Detail, "Incorrect TXT record") {
		t.Fatalf("updating challenge: expected Incorrect TXT record error, got %s", prob.Detail)
	}
}

func TestDNSAccount01NoTXTRecord(t *testing.T) {
	t.Parallel()

	domain := random_domain()
	c, err := makeClient()
	if err != nil {
		t.Fatalf("creating client: %s", err)
	}

	idents := []acme.Identifier{{Type: "dns", Value: domain}}

	order, err := c.Client.NewOrder(c.Account, idents)
	if err != nil {
		t.Fatalf("creating new order: %s", err)
	}

	authzURL := order.Authorizations[0]
	auth, err := c.Client.FetchAuthorization(c.Account, authzURL)
	if err != nil {
		t.Fatalf("fetching authorization: %s", err)
	}

	chal, ok := auth.ChallengeMap[acme.ChallengeTypeDNSAccount01]
	if !ok {
		t.Skip("DNS-Account-01 is not offered, skipping test")
	}

	accountURL := c.Account.URL
	expectedLabel := calculateDNSAccount01Label(accountURL)
	expectedValidationName := fmt.Sprintf("_%s._acme-challenge.%s", expectedLabel, domain)
	t.Logf("Expected validation name: %s", expectedValidationName)

	chal, err = c.Client.UpdateChallenge(c.Account, chal)
	if err == nil {
		t.Fatalf("updating challenge: expected error, got nil")
	}
	prob, ok := err.(acme.Problem)
	if !ok {
		t.Fatalf("updating challenge: expected acme.Problem error, got %T", err)
	}
	if prob.Type != "urn:ietf:params:acme:error:unauthorized" {
		t.Fatalf("updating challenge: expected unauthorized error, got %s", prob.Type)
	}
	if !strings.Contains(prob.Detail, "No TXT record found") {
		t.Fatalf("updating challenge: expected No TXT record found error, got %s", prob.Detail)
	}
}

func TestDNSAccount01MultipleTXTRecordsNoneMatch(t *testing.T) {
	t.Parallel()

	domain := random_domain()
	c, err := makeClient()
	if err != nil {
		t.Fatalf("creating client: %s", err)
	}

	idents := []acme.Identifier{{Type: "dns", Value: domain}}

	order, err := c.Client.NewOrder(c.Account, idents)
	if err != nil {
		t.Fatalf("creating new order: %s", err)
	}

	authzURL := order.Authorizations[0]
	auth, err := c.Client.FetchAuthorization(c.Account, authzURL)
	if err != nil {
		t.Fatalf("fetching authorization: %s", err)
	}

	chal, ok := auth.ChallengeMap[acme.ChallengeTypeDNSAccount01]
	if !ok {
		t.Skip("DNS-Account-01 is not offered, skipping test")
	}

	accountURL := c.Account.URL
	expectedLabel := calculateDNSAccount01Label(accountURL)
	expectedValidationName := fmt.Sprintf("_%s._acme-challenge.%s", expectedLabel, domain)
	t.Logf("Expected validation name: %s", expectedValidationName)

	// Add multiple wrong TXT records
	_, err = testSrvClient.AddDNSAccount01Response(expectedLabel, domain, "wrong-digest-1")
	if err != nil {
		t.Fatalf("adding DNS response: %s", err)
	}
	_, err = testSrvClient.AddDNSAccount01Response(expectedLabel, domain, "wrong-digest-2")
	if err != nil {
		t.Fatalf("adding DNS response: %s", err)
	}
	t.Cleanup(func() {
		_, _ = testSrvClient.RemoveDNSAccount01Response(expectedLabel, domain)
	})

	chal, err = c.Client.UpdateChallenge(c.Account, chal)
	if err == nil {
		t.Fatalf("updating challenge: expected error, got nil")
	}
	prob, ok := err.(acme.Problem)
	if !ok {
		t.Fatalf("updating challenge: expected acme.Problem error, got %T", err)
	}
	if prob.Type != "urn:ietf:params:acme:error:unauthorized" {
		t.Fatalf("updating challenge: expected unauthorized error, got %s", prob.Type)
	}
	if !strings.Contains(prob.Detail, "Incorrect TXT record") {
		t.Fatalf("updating challenge: expected Incorrect TXT record error, got %s", prob.Detail)
	}
}

func TestDNSAccount01MultipleTXTRecordsOneMatches(t *testing.T) {
	t.Parallel()

	domain := random_domain()
	c, err := makeClient()
	if err != nil {
		t.Fatalf("creating client: %s", err)
	}

	idents := []acme.Identifier{{Type: "dns", Value: domain}}

	order, err := c.Client.NewOrder(c.Account, idents)
	if err != nil {
		t.Fatalf("creating new order: %s", err)
	}

	authzURL := order.Authorizations[0]
	auth, err := c.Client.FetchAuthorization(c.Account, authzURL)
	if err != nil {
		t.Fatalf("fetching authorization: %s", err)
	}

	chal, ok := auth.ChallengeMap[acme.ChallengeTypeDNSAccount01]
	if !ok {
		t.Skip("DNS-Account-01 is not offered, skipping test")
	}

	accountURL := c.Account.URL
	expectedLabel := calculateDNSAccount01Label(accountURL)
	expectedValidationName := fmt.Sprintf("_%s._acme-challenge.%s", expectedLabel, domain)
	t.Logf("Expected validation name: %s", expectedValidationName)

	// Add multiple TXT records, one of which is correct
	_, err = testSrvClient.AddDNSAccount01Response(expectedLabel, domain, "wrong-digest-1")
	if err != nil {
		t.Fatalf("adding DNS response: %s", err)
	}
	_, err = testSrvClient.AddDNSAccount01Response(expectedLabel, domain, chal.KeyAuthorization)
	if err != nil {
		t.Fatalf("adding DNS response: %s", err)
	}
	_, err = testSrvClient.AddDNSAccount01Response(expectedLabel, domain, "wrong-digest-2")
	if err != nil {
		t.Fatalf("adding DNS response: %s", err)
	}
	t.Cleanup(func() {
		_, _ = testSrvClient.RemoveDNSAccount01Response(expectedLabel, domain)
	})

	chal, err = c.Client.UpdateChallenge(c.Account, chal)
	if err != nil {
		t.Fatalf("updating challenge: expected no error, got %s", err)
	}
}

func calculateDNSAccount01Label(accountURL string) string {
	h := sha256.Sum256([]byte(accountURL))
	return strings.ToLower(base32.StdEncoding.EncodeToString(h[:10]))
}
