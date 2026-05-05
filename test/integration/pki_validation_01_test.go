//go:build integration

package integration

import (
	"os"
	"testing"

	"github.com/eggsampler/acme/v3"
)

func TestPKIValidation01HappyPath(t *testing.T) {
	t.Parallel()

	if os.Getenv("BOULDER_CONFIG_DIR") == "test/config" {
		t.Skip("Test requires pki-validation-01 to be enabled")
	}

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
}

func TestPKIValidation01NotOfferedForWildcard(t *testing.T) {
	t.Parallel()

	if os.Getenv("BOULDER_CONFIG_DIR") == "test/config" {
		t.Skip("Test requires pki-validation-01 to be enabled")
	}

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

	if os.Getenv("BOULDER_CONFIG_DIR") == "test/config" {
		t.Skip("Test requires pki-validation-01 to be enabled")
	}

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

	// Intentionally publish wrong content — the filename is correct but
	// the body is junk, so validation should fail with an unauthorized error.
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
