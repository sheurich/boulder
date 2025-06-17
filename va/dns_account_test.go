// dns_account_test.go
package va

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/jmhodges/clock"

	"github.com/letsencrypt/boulder/bdns"
	"github.com/letsencrypt/boulder/features"
	"github.com/letsencrypt/boulder/identifier"
	blog "github.com/letsencrypt/boulder/log"
	"github.com/letsencrypt/boulder/metrics"
	"github.com/letsencrypt/boulder/probs"
	"github.com/letsencrypt/boulder/test"
)

// testReservedIPFunc is a mock function for testing
func testReservedIPFunc(ip net.IP) bool {
	return false
}

// setupDNSAccount01 creates a VA configured to use RFC example account URI
func setupDNSAccount01() (*ValidationAuthorityImpl, *blog.Mock) {
	features.Reset()
	fc := clock.NewFake()
	logger := blog.NewMock()
	
	// Configure VA to use RFC example account URI prefix
	rfcAccountURIPrefixes := []string{"https://example.com/acme/acct/"}
	
	va, err := NewValidationAuthorityImpl(
		&bdns.MockClient{Log: logger},
		nil, // no remote VAs
		"user agent 1.0",
		"letsencrypt.org",
		metrics.NoopRegisterer,
		fc,
		logger,
		rfcAccountURIPrefixes,
		"https://example.com/acme/acct/ExampleAccount", // This will have regID appended
		"test perspective",
		"",
		testReservedIPFunc,
	)
	if err != nil {
		panic(fmt.Sprintf("Failed to create validation authority: %v", err))
	}
	
	return va, logger
}

const testAccountURI = "https://example.com/acme/acct/ExampleAccount"
const expectedLabelPrefix = "_ujmmovf2vn55tgye._acme-challenge"

const testRegID = 0

func TestDNSAccount01ValidationWrong(t *testing.T) {
	va, _ := setupDNSAccount01()
	_, err := va.validateDNSAccount01(context.Background(), identifier.NewDNS("wrong-dns01.com"), expectedKeyAuthorization, testRegID)
	if err == nil {
		t.Fatalf("Successful DNS validation with wrong TXT record")
	}
	prob := detailedError(err)
	expectedErr := "unauthorized :: Incorrect TXT record \"a\" found at " + expectedLabelPrefix + ".wrong-dns01.com" +
		" (account: " + testAccountURI + ")"
	test.AssertEquals(t, prob.String(), expectedErr)
}

func TestDNSAccount01ValidationWrongMany(t *testing.T) {
	va, _ := setupDNSAccount01()

	_, err := va.validateDNSAccount01(context.Background(), identifier.NewDNS("wrong-many-dns01.com"), expectedKeyAuthorization, testRegID)
	if err == nil {
		t.Fatalf("Successful DNS validation with wrong TXT record")
	}
	prob := detailedError(err)
	expectedErr := "unauthorized :: Incorrect TXT record \"a\" (and 4 more) found at " + expectedLabelPrefix + ".wrong-many-dns01.com" +
		" (account: " + testAccountURI + ")"
	test.AssertEquals(t, prob.String(), expectedErr)
}

func TestDNSAccount01ValidationWrongLong(t *testing.T) {
	va, _ := setupDNSAccount01()

	_, err := va.validateDNSAccount01(context.Background(), identifier.NewDNS("long-dns01.com"), expectedKeyAuthorization, testRegID)
	if err == nil {
		t.Fatalf("Successful DNS validation with wrong TXT record")
	}
	prob := detailedError(err)
	expectedErr := "unauthorized :: Incorrect TXT record \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa...\" found at " + expectedLabelPrefix + ".long-dns01.com" +
		" (account: " + testAccountURI + ")"
	test.AssertEquals(t, prob.String(), expectedErr)
}

func TestDNSAccount01ValidationFailure(t *testing.T) {
	va, _ := setupDNSAccount01()

	_, err := va.validateDNSAccount01(ctx, identifier.NewDNS("localhost"), expectedKeyAuthorization, testRegID)
	prob := detailedError(err)

	test.AssertEquals(t, prob.Type, probs.UnauthorizedProblem)

	expectedErr := "unauthorized :: Incorrect TXT record \"hostname\" found at " + expectedLabelPrefix + ".localhost" +
		" (account: " + testAccountURI + ")"
	test.AssertEquals(t, prob.String(), expectedErr)
}

func TestDNSAccount01ValidationIP(t *testing.T) {
	va, _ := setupDNSAccount01()

	_, err := va.validateDNSAccount01(ctx, identifier.NewIP(netip.MustParseAddr("127.0.0.1")), expectedKeyAuthorization, testRegID)
	prob := detailedError(err)

	test.AssertEquals(t, prob.Type, probs.MalformedProblem)
}

func TestDNSAccount01ValidationInvalid(t *testing.T) {
	var notDNS = identifier.ACMEIdentifier{
		Type:  identifier.IdentifierType("iris"),
		Value: "790DB180-A274-47A4-855F-31C428CB1072",
	}

	va, _ := setupDNSAccount01()

	_, err := va.validateDNSAccount01(ctx, notDNS, expectedKeyAuthorization, testRegID)
	prob := detailedError(err)

	test.AssertEquals(t, prob.Type, probs.MalformedProblem)
}

func TestDNSAccount01ValidationServFail(t *testing.T) {
	va, _ := setupDNSAccount01()

	_, err := va.validateDNSAccount01(ctx, identifier.NewDNS("servfail.com"), expectedKeyAuthorization, testRegID)

	prob := detailedError(err)
	test.AssertEquals(t, prob.Type, probs.DNSProblem)
}

func TestDNSAccount01ValidationNoServer(t *testing.T) {
	va, log := setupDNSAccount01()
	staticProvider, err := bdns.NewStaticProvider([]string{})
	test.AssertNotError(t, err, "Couldn't make new static provider")

	va.dnsClient = bdns.NewTest(
		time.Second*5,
		staticProvider,
		metrics.NoopRegisterer,
		clock.New(),
		1,
		"",
		log,
		nil)

	_, err = va.validateDNSAccount01(ctx, identifier.NewDNS("localhost"), expectedKeyAuthorization, testRegID)
	prob := detailedError(err)
	test.AssertEquals(t, prob.Type, probs.DNSProblem)
}

func TestDNSAccount01ValidationOK(t *testing.T) {
	va, _ := setupDNSAccount01()

	_, prob := va.validateDNSAccount01(ctx, identifier.NewDNS("good-dns01.com"), expectedKeyAuthorization, testRegID)

	test.Assert(t, prob == nil, "Should be valid.")
}

func TestDNSAccount01ValidationNoAuthorityOK(t *testing.T) {
	va, _ := setupDNSAccount01()

	_, prob := va.validateDNSAccount01(ctx, identifier.NewDNS("no-authority-dns01.com"), expectedKeyAuthorization, testRegID)

	test.Assert(t, prob == nil, "Should be valid.")
}

func TestDNSAccount01ValidationNoAccountURIPrefixes(t *testing.T) {
	// Try to create a VA with no accountURIPrefixes to test error handling
	_, err := NewValidationAuthorityImpl(
		&bdns.MockClient{},
		nil,
		"user agent 1.0",
		"letsencrypt.org",
		metrics.NoopRegisterer,
		clock.NewFake(),
		blog.NewMock(),
		[]string{}, // Empty accountURIPrefixes
		"",
		"test perspective",
		"",
		testReservedIPFunc,
	)

	// Assert that an error was returned during construction
	test.Assert(t, err != nil, "VA construction succeeded unexpectedly with no accountURIPrefixes")
	test.AssertEquals(t, err.Error(), "no account URI prefixes configured")
}
