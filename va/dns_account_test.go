package va

import (
	"context"
	"fmt"
	"testing"

	"github.com/letsencrypt/boulder/identifier"
	"github.com/letsencrypt/boulder/probs"
	"github.com/letsencrypt/boulder/test"
)

func TestComputeAccountLabel(t *testing.T) {
	// Test cases from the IETF draft specification
	testCases := []struct {
		accountURI string
		expected   string
	}{
		{
			accountURI: "https://example.com/acme/acct/1",
			expected:   "VRR7UUDRKLSHXB6L",
		},
		{
			accountURI: "https://example.com/acme/acct/12345",
			expected:   "AO3PCVMACVWYW63B",
		},
		{
			accountURI: "https://example.com/acme/acct/ExampleAccount",
			expected:   "UJMMOVF2VN55TGYE",
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("Account URI: %s", tc.accountURI), func(t *testing.T) {
			label := computeAccountLabel(tc.accountURI)
			test.AssertEquals(t, label, tc.expected)
		})
	}
}

func TestDNSAccountValidationEmpty(t *testing.T) {
	va, _ := setup(nil, "", nil, nil)

	// Mock account URI from the IETF draft
	accountURI := "https://example.com/acme/acct/1"

	// This test calls validateDNSAccount01 directly
	_, err := va.validateDNSAccount01(context.Background(), dnsi("empty-txts.com"), expectedKeyAuthorization, accountURI)
	prob := detailedError(err)
	test.AssertEquals(t, prob.Type, probs.UnauthorizedProblem)

	// The account label for "https://example.com/acme/acct/1" is "VRR7UUDRKLSHXB6L"
	expectedDetail := "No TXT record found at _VRR7UUDRKLSHXB6L._acme-challenge.empty-txts.com"
	test.AssertEquals(t, prob.Detail, expectedDetail)
}

func TestDNSAccountValidationWrong(t *testing.T) {
	va, _ := setup(nil, "", nil, nil)

	// Mock account URI from the IETF draft
	accountURI := "https://example.com/acme/acct/1"

	// The mock client is already set up to return "a" for wrong-dns01.com

	_, err := va.validateDNSAccount01(context.Background(), dnsi("wrong-dns01.com"), expectedKeyAuthorization, accountURI)
	if err == nil {
		t.Fatalf("Successful DNS-Account validation with wrong TXT record")
	}
	prob := detailedError(err)

	// The account label for "https://example.com/acme/acct/1" is "VRR7UUDRKLSHXB6L"
	expectedError := "unauthorized :: Incorrect TXT record \"a\" found at _VRR7UUDRKLSHXB6L._acme-challenge.wrong-dns01.com"
	test.AssertEquals(t, prob.Error(), expectedError)
}

func TestDNSAccountValidationOK(t *testing.T) {
	va, _ := setup(nil, "", nil, nil)

	// Mock account URI from the IETF draft
	accountURI := "https://example.com/acme/acct/1"

	// The mock client is already set up to return the correct digest for good-dns01.com
	// with the account label "VRR7UUDRKLSHXB6L" for the account URI "https://example.com/acme/acct/1"

	_, prob := va.validateDNSAccount01(ctx, dnsi("good-dns01.com"), expectedKeyAuthorization, accountURI)
	test.Assert(t, prob == nil, "Should be valid.")
}

func TestDNSAccountValidationInvalid(t *testing.T) {
	var notDNS = identifier.ACMEIdentifier{
		Type:  identifier.IdentifierType("iris"),
		Value: "790DB180-A274-47A4-855F-31C428CB1072",
	}

	va, _ := setup(nil, "", nil, nil)

	_, err := va.validateDNSAccount01(ctx, notDNS, expectedKeyAuthorization, "https://example.com/acme/acct/1")
	prob := detailedError(err)

	test.AssertEquals(t, prob.Type, probs.MalformedProblem)
}

// TestDNSAccountValidationWithDifferentAccounts tests that different account URIs
// result in different validation subdomains
func TestDNSAccountValidationWithDifferentAccounts(t *testing.T) {
	va, _ := setup(nil, "", nil, nil)

	// Two different account URIs
	accountURI1 := "https://example.com/acme/acct/1"
	accountURI2 := "https://example.com/acme/acct/2"

	// Get the account labels
	accountLabel1 := computeAccountLabel(accountURI1)
	accountLabel2 := computeAccountLabel(accountURI2)

	// Verify that the labels are different
	test.Assert(t, accountLabel1 != accountLabel2, "Account labels should be different for different accounts")

	// Verify that validation fails for both accounts (since the mock DNS client doesn't have records for them)
	_, err1 := va.validateDNSAccount01(context.Background(), dnsi("example.com"), expectedKeyAuthorization, accountURI1)
	prob1 := detailedError(err1)
	test.AssertEquals(t, prob1.Type, probs.UnauthorizedProblem)

	_, err2 := va.validateDNSAccount01(context.Background(), dnsi("example.com"), expectedKeyAuthorization, accountURI2)
	prob2 := detailedError(err2)
	test.AssertEquals(t, prob2.Type, probs.UnauthorizedProblem)

	// Verify that the error messages contain different subdomains
	expectedSubdomain1 := fmt.Sprintf("_%s._acme-challenge.example.com", accountLabel1)
	expectedSubdomain2 := fmt.Sprintf("_%s._acme-challenge.example.com", accountLabel2)

	test.Assert(t, expectedSubdomain1 != expectedSubdomain2, "Expected different subdomains for different accounts")
	test.AssertContains(t, prob1.Detail, expectedSubdomain1)
	test.AssertContains(t, prob2.Detail, expectedSubdomain2)
}
