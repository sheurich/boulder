//go:build integration

package integration

import (
	"testing"

	"github.com/letsencrypt/boulder/features"
	"github.com/letsencrypt/boulder/test"
)

// TestDNSAccountValidation tests that a domain can be validated using the
// dns-account-01 challenge type when the feature flag is enabled.
func TestDNSAccountValidation(t *testing.T) {
	if !features.Get().DnsAccountChallenge {
		t.Skip("DNS Account challenge feature flag not enabled")
	}

	t.Parallel()
	c, err := makeClient()
	test.AssertNotError(t, err, "makeClient failed")

	// Issue for a random domain
	domains := []string{random_domain()}
	result, err := authAndIssue(c, nil, domains, true)
	// There should be no error
	test.AssertNotError(t, err, "authAndIssue failed")
	// The order should be valid
	test.AssertEquals(t, result.Order.Status, "valid")
	// There should be one authorization URL
	test.AssertEquals(t, len(result.Order.Authorizations), 1)

	// Fetching the authz by URL shouldn't fail
	authzURL := result.Order.Authorizations[0]
	authzOb, err := c.FetchAuthorization(c.Account, authzURL)
	test.AssertNotError(t, err, "FetchAuthorization failed")

	// The authz should be valid and for the correct identifier
	test.AssertEquals(t, authzOb.Status, "valid")
	test.AssertEquals(t, authzOb.Identifier.Value, domains[0])

	// Verify that at least one challenge is of type dns-account-01
	var hasAccountChallenge bool
	for _, chall := range authzOb.Challenges {
		if chall.Type == "dns-account-01" {
			hasAccountChallenge = true
			break
		}
	}
	test.Assert(t, hasAccountChallenge, "No dns-account-01 challenge found")
}

// TestConcurrentDNSAccountValidation tests that multiple validations can occur
// simultaneously using different account URLs.
func TestConcurrentDNSAccountValidation(t *testing.T) {
	if !features.Get().DnsAccountChallenge {
		t.Skip("DNS Account challenge feature flag not enabled")
	}

	t.Parallel()
	c1, err := makeClient()
	test.AssertNotError(t, err, "makeClient 1 failed")
	c2, err := makeClient()
	test.AssertNotError(t, err, "makeClient 2 failed")

	domain := random_domain()
	domains := []string{domain}

	// Start validations concurrently
	resultChan := make(chan error, 2)
	go func() {
		_, err := authAndIssue(c1, nil, domains, true)
		resultChan <- err
	}()
	go func() {
		_, err := authAndIssue(c2, nil, domains, true)
		resultChan <- err
	}()

	// Wait for both validations to complete
	for i := 0; i < 2; i++ {
		err := <-resultChan
		test.AssertNotError(t, err, "concurrent validation failed")
	}
}
