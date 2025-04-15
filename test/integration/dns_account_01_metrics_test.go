
//go:build integration

package integration

import (
	"strings"
	"testing"

	"github.com/eggsampler/acme/v3"
)

func TestDNSAccount01Metrics(t *testing.T) {
	t.Parallel()

	domain := random_domain()
	
	c, err := makeClient()
	if err != nil {
		t.Fatalf("creating client: %s", err)
	}

	order, err := c.Client.NewOrder(c.Account, []acme.Identifier{{Type: "dns", Value: domain}})
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

	_, err = testSrvClient.AddDNSAccount01Response(c.Account.URL, domain, "incorrect-value")
	if err != nil {
		t.Fatalf("adding DNS response: %s", err)
	}
	t.Cleanup(func() {
		_, _ = testSrvClient.RemoveDNSAccount01Response(c.Account.URL, domain)
	})

	_, err = c.Client.UpdateChallenge(c.Account, chal)
	if err == nil {
		t.Fatal("expected validation to fail, but it succeeded")
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

	newOrder, err := c.Client.NewOrder(c.Account, []acme.Identifier{{Type: "dns", Value: domain}})
	if err != nil {
		t.Fatalf("creating new order for successful test: %s", err)
	}

	newAuthzURL := newOrder.Authorizations[0]
	newAuth, err := c.Client.FetchAuthorization(c.Account, newAuthzURL)
	if err != nil {
		t.Fatalf("fetching new authorization: %s", err)
	}

	newChal, ok := newAuth.ChallengeMap[acme.ChallengeTypeDNSAccount01]
	if !ok {
		t.Fatal("DNS-Account-01 challenge not found in new authorization")
	}

	_, err = testSrvClient.AddDNSAccount01Response(c.Account.URL, domain, newChal.KeyAuthorization)
	if err != nil {
		t.Fatalf("adding DNS response for new challenge: %s", err)
	}

	newChal, err = c.Client.UpdateChallenge(c.Account, newChal)
	if err != nil {
		t.Fatalf("updating new challenge: %s", err)
	}

	if newChal.Status != "valid" {
		t.Fatalf("expected new challenge status to be 'valid', got: %s", newChal.Status)
	}
}
