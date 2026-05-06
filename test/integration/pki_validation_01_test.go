//go:build integration

package integration

import (
	"crypto/x509"
	"testing"

	"github.com/eggsampler/acme/v3"
)

func TestPKIValidation01HappyPath(t *testing.T) {
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

	chal, ok := auth.ChallengeMap[acme.ChallengeTypePKIValidation01]
	if !ok {
		t.Fatal("pki-validation-01 challenge not offered by server")
	}

	_, err = testSrvClient.AddPKIValidation01Response(chal.KeyAuthorization)
	if err != nil {
		t.Fatalf("adding pki-validation-01 response: %s", err)
	}
	t.Cleanup(func() {
		_, err := testSrvClient.RemovePKIValidation01Response(chal.KeyAuthorization)
		if err != nil {
			t.Fatal(err)
		}
	})

	chal, err = c.Client.UpdateChallenge(c.Account, chal)
	if err != nil {
		t.Fatalf("updating challenge: %s", err)
	}

	auth, err = c.Client.FetchAuthorization(c.Account, authzURL)
	if err != nil {
		t.Fatalf("fetching authorization after challenge update: %s", err)
	}

	if auth.Status != "valid" {
		t.Fatalf("expected authorization status to be 'valid', got '%s'", auth.Status)
	}

	// Finalize the order and verify a certificate is issued.
	csr, err := makeCSR(nil, idents, false)
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
		t.Fatal("expected at least one certificate")
	}
	leaf, err := x509.ParseCertificate(certs[0].Raw)
	if err != nil {
		t.Fatalf("parsing leaf certificate: %s", err)
	}
	found := false
	for _, name := range leaf.DNSNames {
		if name == domain {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("issued certificate does not contain domain %q; DNSNames: %v", domain, leaf.DNSNames)
	}
}

func TestPKIValidation01NotOfferedForWildcard(t *testing.T) {
	t.Parallel()

	domain := "*." + random_domain()
	c, err := makeClient()
	if err != nil {
		t.Fatalf("creating client: %s", err)
	}

	idents := []acme.Identifier{{Type: "dns", Value: domain}}
	order, err := c.Client.NewOrder(c.Account, idents)
	if err != nil {
		t.Fatalf("creating new order: %s", err)
	}

	auth, err := c.Client.FetchAuthorization(c.Account, order.Authorizations[0])
	if err != nil {
		t.Fatalf("fetching authorization: %s", err)
	}

	if _, ok := auth.ChallengeMap[acme.ChallengeTypePKIValidation01]; ok {
		t.Fatal("pki-validation-01 challenge should not be offered for wildcard identifiers")
	}
}

func TestPKIValidation01BodyMismatch(t *testing.T) {
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

	auth, err := c.Client.FetchAuthorization(c.Account, order.Authorizations[0])
	if err != nil {
		t.Fatalf("fetching authorization: %s", err)
	}
	chal, ok := auth.ChallengeMap[acme.ChallengeTypePKIValidation01]
	if !ok {
		t.Fatal("pki-validation-01 challenge not offered by server")
	}

	// Intentionally publish content at a WRONG filename. The test server
	// derives the filename from the keyAuthorization's thumbprint portion,
	// so appending "-wrong" to the thumbprint creates a different filename.
	// The VA requests the CORRECT filename (derived from the real thumbprint),
	// finds no content there, and rejects with an empty-body mismatch.
	// This exercises the "no valid response at expected path" failure mode.
	// The body-comparison logic itself is covered by the unit test
	// TestValidatePKIValidation01BodyMismatch in va/pki_validation_test.go.
	parts := splitKeyAuth(chal.KeyAuthorization)
	wrongKeyAuth := parts[0] + "." + parts[1] + "-wrong"
	_, err = testSrvClient.AddPKIValidation01Response(wrongKeyAuth)
	if err != nil {
		t.Fatalf("adding pki-validation-01 response: %s", err)
	}
	t.Cleanup(func() {
		_, _ = testSrvClient.RemovePKIValidation01Response(wrongKeyAuth)
	})

	_, _ = c.Client.UpdateChallenge(c.Account, chal)
	auth, err = c.Client.FetchAuthorization(c.Account, order.Authorizations[0])
	if err != nil {
		t.Fatalf("fetching authorization: %s", err)
	}
	if auth.Status != "invalid" {
		t.Fatalf("expected authorization status to be 'invalid', got '%s'", auth.Status)
	}
}

// splitKeyAuth splits a keyAuthorization on "." into [token, thumbprint].
func splitKeyAuth(keyauth string) [2]string {
	for i := 0; i < len(keyauth); i++ {
		if keyauth[i] == '.' {
			return [2]string{keyauth[:i], keyauth[i+1:]}
		}
	}
	return [2]string{keyauth, ""}
}
