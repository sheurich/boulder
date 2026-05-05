package va

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/letsencrypt/boulder/core"
	berrors "github.com/letsencrypt/boulder/errors"
	"github.com/letsencrypt/boulder/features"
	"github.com/letsencrypt/boulder/identifier"
	"github.com/letsencrypt/boulder/probs"
	"github.com/letsencrypt/boulder/test"
)

// pkiValidationPath returns the well-known path a pki-validation-01
// challenge response is expected at, given a thumbprint.
func pkiValidationPath(thumbprint string) string {
	return "/.well-known/pki-validation/" + thumbprint
}

// startPKIValidationSrv returns an httptest.Server that serves the
// provided body at the expected pki-validation-01 path for the test
// account thumbprint.
func startPKIValidationSrv(t *testing.T, body string) *httptest.Server {
	t.Helper()
	m := http.NewServeMux()
	hs := httptest.NewUnstartedServer(m)
	m.HandleFunc(pkiValidationPath(expectedThumbprint), func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	})
	hs.Start()
	return hs
}

func mustIP(s string) netip.Addr {
	a, err := netip.ParseAddr(s)
	if err != nil {
		panic(err)
	}
	return a
}

func TestValidatePKIValidation01Success(t *testing.T) {
	hs := startPKIValidationSrv(t, expectedKeyAuthorization)
	defer hs.Close()

	va, _ := setup(hs, "", nil, &ipFakeDNS{})
	records, err := va.validatePKIValidation01(ctx,
		identifier.NewDNS("localhost.com"),
		expectedToken, expectedKeyAuthorization)
	if err != nil {
		t.Fatalf("unexpected failure: %s", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 validation record, got %d", len(records))
	}
	if !strings.Contains(records[0].URL, "/.well-known/pki-validation/"+expectedThumbprint) {
		t.Errorf("record URL = %q, want containing /.well-known/pki-validation/%s",
			records[0].URL, expectedThumbprint)
	}
}

func TestValidatePKIValidation01BodyMismatch(t *testing.T) {
	hs := startPKIValidationSrv(t, "not the key authorization")
	defer hs.Close()

	va, _ := setup(hs, "", nil, &ipFakeDNS{})
	_, err := va.validatePKIValidation01(ctx,
		identifier.NewDNS("localhost.com"),
		expectedToken, expectedKeyAuthorization)
	if err == nil {
		t.Fatalf("expected body mismatch error, got nil")
	}
	test.AssertErrorIs(t, err, berrors.Unauthorized)
}

func TestValidatePKIValidation01IPIdentifierRejected(t *testing.T) {
	va, _ := setup(nil, "", nil, nil)
	_, err := va.validatePKIValidation01(ctx,
		identifier.NewIP(mustIP("127.0.0.1")),
		expectedToken, expectedKeyAuthorization)
	if err == nil {
		t.Fatalf("expected malformed error for IP identifier, got nil")
	}
	test.AssertErrorIs(t, err, berrors.Malformed)
}

func TestValidatePKIValidation01MalformedKeyAuthorization(t *testing.T) {
	va, _ := setup(nil, "", nil, nil)
	cases := []struct {
		name    string
		keyAuth string
	}{
		{"empty", ""},
		{"no dot", "nodot"},
		{"dot at end", "token."},
		{"just a dot", "."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := va.validatePKIValidation01(ctx,
				identifier.NewDNS("example.com"), "tok", tc.keyAuth)
			test.AssertErrorIs(t, err, berrors.Malformed)
		})
	}
}

func TestValidatePKIValidation01LeadingWhitespaceRejected(t *testing.T) {
	// Leading whitespace must NOT be tolerated (only trailing is trimmed,
	// matching HTTP-01 behavior). This test documents intent and guards
	// against a future contributor changing TrimRightFunc to TrimSpace.
	hs := startPKIValidationSrv(t, " "+expectedKeyAuthorization)
	defer hs.Close()

	va, _ := setup(hs, "", nil, &ipFakeDNS{})
	_, err := va.validatePKIValidation01(ctx,
		identifier.NewDNS("localhost.com"),
		expectedToken, expectedKeyAuthorization)
	test.AssertErrorIs(t, err, berrors.Unauthorized)
}

func TestValidatePKIValidation01DisabledByFeatureFlag(t *testing.T) {
	// When PKIValidation01Enabled is false, validateChallenge must reject
	// the challenge type with a MalformedProblem. setup() calls
	// features.Reset() which sets the flag to false.
	va, _ := setup(nil, "", nil, nil)

	// Confirm the flag is off.
	test.Assert(t, !features.Get().PKIValidation01Enabled,
		"expected PKIValidation01Enabled to be false after setup")

	_, err := va.validateChallenge(ctx,
		identifier.NewDNS("example.com"),
		core.ChallengeTypePKIValidation01,
		expectedToken, expectedKeyAuthorization, "")
	prob := detailedError(err)
	test.AssertEquals(t, prob.Type, probs.MalformedProblem)
}
