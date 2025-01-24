package va

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jmhodges/clock"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/letsencrypt/boulder/bdns"
	"github.com/letsencrypt/boulder/core"
	"github.com/letsencrypt/boulder/features"
	"github.com/letsencrypt/boulder/identifier"
	"github.com/letsencrypt/boulder/metrics"
	"github.com/letsencrypt/boulder/probs"
	"github.com/letsencrypt/boulder/test"
)

var dnsTestKeyAuthorization = "LoqXcYV8q5ONbJQxbmR7SCTNo3tiAXDfowyjxAjEuX0.9jg46WB3rR_AHD-EBXdN7cBkH1WOu0tA3M9fm21mqTI"
var dnsTestCtx = context.Background()

func TestDNSAccountValidationEmpty(t *testing.T) {
	features.Reset()
	features.Set(features.Config{DnsAccountChallenge: true})
	va, _ := setup(nil, "", nil, nil)

	// Test values
	domain := "empty-txts.com"
	accountURL := "https://example.com/acme/acct/1"

	// This test calls validateDNSAccount01 directly to test empty TXT record handling
	_, err := va.validateDNSAccount01(context.Background(), dnsi(domain), dnsTestKeyAuthorization, accountURL)
	test.AssertError(t, err, "Should fail with no TXT record")
	if features.Get().DnsAccountChallenge {
		// Per section 3.4 of draft-ietf-acme-dns-account-label-00:
		// "If no TXT record is found, the server SHOULD include the account URL in the error"
		test.AssertContains(t, err.Error(), "No TXT record found at _")
		test.AssertContains(t, err.Error(), accountURL)
	} else {
		test.AssertContains(t, err.Error(), "dns-account-01 challenge type is not enabled")
	}

	if features.Get().DnsAccountChallenge {
		test.AssertMetricWithLabelsEquals(t, va.metrics.validationLatency, prometheus.Labels{
			"operation":      opDCVAndCAA,
			"perspective":    va.perspective,
			"challenge_type": string(core.ChallengeTypeDNSAccount01),
			"problem_type":   string(probs.UnauthorizedProblem),
			"result":         fail,
		}, 1)
	}
}

func TestDNSAccountValidationServFail(t *testing.T) {
	features.Reset()
	features.Set(features.Config{DnsAccountChallenge: true})
	va, _ := setup(nil, "", nil, nil)
	_, err := va.validateDNSAccount01(context.Background(), dnsi("servfail.com"), dnsTestKeyAuthorization, "https://example.com/acme/acct/1")
	prob := detailedError(err)
	if features.Get().DnsAccountChallenge {
		test.AssertEquals(t, prob.Type, probs.DNSProblem)
	} else {
		test.AssertEquals(t, prob.Type, probs.MalformedProblem)
	}
}

func TestDNSAccountValidationConcurrent(t *testing.T) {
	features.Reset()
	features.Set(features.Config{DnsAccountChallenge: true})
	va, _ := setup(nil, "", nil, nil)
	domain := "concurrent-test.com"

	// Run validations concurrently with different account URLs
	resultChan := make(chan error, 2)
	go func() {
		_, err := va.validateDNSAccount01(context.Background(), dnsi(domain), dnsTestKeyAuthorization, "https://example.com/acme/acct/1")
		resultChan <- err
	}()
	go func() {
		_, err := va.validateDNSAccount01(context.Background(), dnsi(domain), dnsTestKeyAuthorization, "https://example.com/acme/acct/2")
		resultChan <- err
	}()

	// Both validations should fail independently
	for i := 0; i < 2; i++ {
		err := <-resultChan
		test.AssertError(t, err, "Should fail with no TXT record")
		if features.Get().DnsAccountChallenge {
			test.AssertContains(t, err.Error(), "No TXT record found at _")
		} else {
			test.AssertContains(t, err.Error(), "dns-account-01 challenge type is not enabled")
		}
	}
}

func TestDNSValidationEmpty(t *testing.T) {
	va, _ := setup(nil, "", nil, nil)

	// This test calls PerformValidation directly, because that is where the
	// metrics checked below are incremented.
	req := createValidationRequest("empty-txts.com", core.ChallengeTypeDNS01)
	res, _ := va.PerformValidation(context.Background(), req)
	test.AssertEquals(t, res.Problem.ProblemType, "unauthorized")
	test.AssertEquals(t, res.Problem.Detail, "No TXT record found at _acme-challenge.empty-txts.com")

	test.AssertMetricWithLabelsEquals(t, va.metrics.validationLatency, prometheus.Labels{
		"operation":      opDCVAndCAA,
		"perspective":    va.perspective,
		"challenge_type": string(core.ChallengeTypeDNS01),
		"problem_type":   string(probs.UnauthorizedProblem),
		"result":         fail,
	}, 1)
}

func TestDNSAccountValidationWrong(t *testing.T) {
	features.Reset()
	features.Set(features.Config{DnsAccountChallenge: true})
	va, _ := setup(nil, "", nil, nil)

	// Test values
	domain := "wrong-dns01.com"
	accountURL := "https://example.com/acme/acct/1"

	testCtx := context.WithValue(context.Background(), core.AccountURLContextKey{}, accountURL)
	_, err := va.validateDNSAccount01(testCtx, dnsi(domain), dnsTestKeyAuthorization, accountURL)
	if err == nil {
		t.Fatalf("Successful DNS validation with wrong TXT record")
	}
	prob := detailedError(err)
	if features.Get().DnsAccountChallenge {
		// Per section 3.3 of draft-ietf-acme-dns-account-label-00:
		// "The server MUST mark the challenge as invalid if any verification fails"
		test.AssertContains(t, prob.Error(), "Incorrect TXT record")
		test.AssertContains(t, prob.Error(), "._acme-challenge.wrong-dns01.com")
		// Per section 3.4: Follow RFC8555 Section 6.7 error guidelines
		test.AssertEquals(t, prob.Type, probs.UnauthorizedProblem)
	} else {
		test.AssertContains(t, prob.Error(), "dns-account-01 challenge type is not enabled")
	}
}

func TestDNSValidationWrong(t *testing.T) {
	va, _ := setup(nil, "", nil, nil)
	_, err := va.validateDNS01(context.Background(), dnsi("wrong-dns01.com"), dnsTestKeyAuthorization)
	if err == nil {
		t.Fatalf("Successful DNS validation with wrong TXT record")
	}
	prob := detailedError(err)
	test.AssertEquals(t, prob.Error(), "unauthorized :: Incorrect TXT record \"a\" found at _acme-challenge.wrong-dns01.com")
}

func TestDNSValidationWrongMany(t *testing.T) {
	va, _ := setup(nil, "", nil, nil)

	_, err := va.validateDNS01(context.Background(), dnsi("wrong-many-dns01.com"), dnsTestKeyAuthorization)
	if err == nil {
		t.Fatalf("Successful DNS validation with wrong TXT record")
	}
	prob := detailedError(err)
	test.AssertEquals(t, prob.Error(), "unauthorized :: Incorrect TXT record \"a\" (and 4 more) found at _acme-challenge.wrong-many-dns01.com")
}

func TestDNSValidationWrongLong(t *testing.T) {
	va, _ := setup(nil, "", nil, nil)

	_, err := va.validateDNS01(context.Background(), dnsi("long-dns01.com"), dnsTestKeyAuthorization)
	if err == nil {
		t.Fatalf("Successful DNS validation with wrong TXT record")
	}
	prob := detailedError(err)
	test.AssertEquals(t, prob.Error(), "unauthorized :: Incorrect TXT record \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa...\" found at _acme-challenge.long-dns01.com")
}

func TestDNSValidationFailure(t *testing.T) {
	va, _ := setup(nil, "", nil, nil)

	_, err := va.validateDNS01(dnsTestCtx, dnsi("localhost"), dnsTestKeyAuthorization)
	prob := detailedError(err)

	test.AssertEquals(t, prob.Type, probs.UnauthorizedProblem)
}

func TestDNSValidationInvalid(t *testing.T) {
	var notDNS = identifier.ACMEIdentifier{
		Type:  identifier.IdentifierType("iris"),
		Value: "790DB180-A274-47A4-855F-31C428CB1072",
	}

	va, _ := setup(nil, "", nil, nil)

	_, err := va.validateDNS01(dnsTestCtx, notDNS, dnsTestKeyAuthorization)
	prob := detailedError(err)

	test.AssertEquals(t, prob.Type, probs.MalformedProblem)
}

func TestDNSValidationServFail(t *testing.T) {
	va, _ := setup(nil, "", nil, nil)

	_, err := va.validateDNS01(dnsTestCtx, dnsi("servfail.com"), dnsTestKeyAuthorization)

	prob := detailedError(err)
	test.AssertEquals(t, prob.Type, probs.DNSProblem)
}

func TestDNSAccountValidationNoAccountURL(t *testing.T) {
	features.Reset()
	features.Set(features.Config{DnsAccountChallenge: true})
	va, _ := setup(nil, "", nil, nil)

	testCtx := context.Background() // No account URL in context
	_, err := va.validateDNSAccount01(testCtx, dnsi("example.com"), dnsTestKeyAuthorization, "")
	test.AssertError(t, err, "Should fail without account URL")
	if features.Get().DnsAccountChallenge {
		test.AssertEquals(t, err.Error(), "account URL cannot be empty")
	} else {
		test.AssertEquals(t, err.Error(), "dns-account-01 challenge type is not enabled")
	}
}

func TestDNSValidationNoServer(t *testing.T) {
	va, log := setup(nil, "", nil, nil)
	staticProvider, err := bdns.NewStaticProvider([]string{})
	test.AssertNotError(t, err, "Couldn't make new static provider")

	va.dnsClient = bdns.NewTest(
		time.Second*5,
		staticProvider,
		metrics.NoopRegisterer,
		clock.New(),
		1,
		log,
		nil)

	_, err = va.validateDNS01(dnsTestCtx, dnsi("localhost"), dnsTestKeyAuthorization)
	prob := detailedError(err)
	test.AssertEquals(t, prob.Type, probs.DNSProblem)
}

func TestDNSAccountValidationOK(t *testing.T) {
	features.Reset()
	features.Set(features.Config{DnsAccountChallenge: true})
	va, _ := setup(nil, "", nil, nil)

	testCtx := context.WithValue(context.Background(), core.AccountURLContextKey{}, "https://example.com/acme/acct/1")
	_, err := va.validateDNSAccount01(testCtx, dnsi("good-dns01.com"), dnsTestKeyAuthorization, testCtx.Value(core.AccountURLContextKey{}).(string))

	if features.Get().DnsAccountChallenge {
		test.Assert(t, err == nil, "Should be valid.")
	} else {
		test.AssertError(t, err, "Should fail when feature flag is disabled")
		test.AssertContains(t, err.Error(), "dns-account-01 challenge type is not enabled")
	}
}

func TestDNSValidationOK(t *testing.T) {
	features.Reset()
	va, _ := setup(nil, "", nil, nil)

	_, err := va.validateDNS01(dnsTestCtx, dnsi("good-dns01.com"), dnsTestKeyAuthorization)
	test.Assert(t, err == nil, "Should be valid.")
}

func TestDNSValidationNoAuthorityOK(t *testing.T) {
	features.Reset()
	va, _ := setup(nil, "", nil, nil)

	_, err := va.validateDNS01(dnsTestCtx, dnsi("no-authority-dns01.com"), dnsTestKeyAuthorization)
	test.Assert(t, err == nil, "Should be valid.")
}

func TestDNSAccountLabelGeneration(t *testing.T) {
	features.Reset()
	features.Set(features.Config{DnsAccountChallenge: true})
	va, _ := setup(nil, "", nil, nil)

	// Example values from draft-ietf-acme-dns-account-label-00 section 3.2
	accountURL := "https://example.com/acme/acct/ExampleAccount"
	domain := "example.org"
	expectedSubdomain := "_ujmmovf2vn55tgye._acme-challenge.example.org"

	// Compute the challenge subdomain using our implementation
	accountHash := sha256.Sum256([]byte(accountURL))
	accountLabel := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(accountHash[:10])
	challengeSubdomain := fmt.Sprintf("_%s._acme-challenge.%s", strings.ToLower(accountLabel), domain)

	// Verify it matches the example from the draft
	test.AssertEquals(t, challengeSubdomain, expectedSubdomain)

	// Also verify through the validation function
	_, err := va.validateDNSAccount01(context.Background(), dnsi(domain), dnsTestKeyAuthorization, accountURL)
	if features.Get().DnsAccountChallenge {
		// The validation will fail because we don't have the TXT record set up,
		// but we can check that the error message contains the correct subdomain
		test.AssertError(t, err, "Should fail with no TXT record")
		test.AssertContains(t, err.Error(), expectedSubdomain)
	} else {
		test.AssertError(t, err, "Should fail when feature flag is disabled")
		test.AssertContains(t, err.Error(), "dns-account-01 challenge type is not enabled")
	}
}

func TestAvailableAddresses(t *testing.T) {
	v6a := net.ParseIP("::1")
	v6b := net.ParseIP("2001:db8::2:1") // 2001:DB8 is reserved for docs (RFC 3849)
	v4a := net.ParseIP("127.0.0.1")
	v4b := net.ParseIP("192.0.2.1") // 192.0.2.0/24 is reserved for docs (RFC 5737)

	testcases := []struct {
		input []net.IP
		v4    []net.IP
		v6    []net.IP
	}{
		// An empty validation record
		{
			[]net.IP{},
			[]net.IP{},
			[]net.IP{},
		},
		// A validation record with one IPv4 address
		{
			[]net.IP{v4a},
			[]net.IP{v4a},
			[]net.IP{},
		},
		// A dual homed record with an IPv4 and IPv6 address
		{
			[]net.IP{v4a, v6a},
			[]net.IP{v4a},
			[]net.IP{v6a},
		},
		// The same as above but with the v4/v6 order flipped
		{
			[]net.IP{v6a, v4a},
			[]net.IP{v4a},
			[]net.IP{v6a},
		},
		// A validation record with just IPv6 addresses
		{
			[]net.IP{v6a, v6b},
			[]net.IP{},
			[]net.IP{v6a, v6b},
		},
		// A validation record with interleaved IPv4/IPv6 records
		{
			[]net.IP{v6a, v4a, v6b, v4b},
			[]net.IP{v4a, v4b},
			[]net.IP{v6a, v6b},
		},
	}

	for _, tc := range testcases {
		// Split the input record into v4/v6 addresses
		v4result, v6result := availableAddresses(tc.input)

		// Test that we got the right number of v4 results
		test.Assert(t, len(tc.v4) == len(v4result),
			fmt.Sprintf("Wrong # of IPv4 results: expected %d, got %d", len(tc.v4), len(v4result)))

		// Check that all of the v4 results match expected values
		for i, v4addr := range tc.v4 {
			test.Assert(t, v4addr.String() == v4result[i].String(),
				fmt.Sprintf("Wrong v4 result index %d: expected %q got %q", i, v4addr.String(), v4result[i].String()))
		}

		// Test that we got the right number of v6 results
		test.Assert(t, len(tc.v6) == len(v6result),
			fmt.Sprintf("Wrong # of IPv6 results: expected %d, got %d", len(tc.v6), len(v6result)))

		// Check that all of the v6 results match expected values
		for i, v6addr := range tc.v6 {
			test.Assert(t, v6addr.String() == v6result[i].String(),
				fmt.Sprintf("Wrong v6 result index %d: expected %q got %q", i, v6addr.String(), v6result[i].String()))
		}
	}
}
