package va

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"testing"

	"github.com/letsencrypt/boulder/core"
	berrors "github.com/letsencrypt/boulder/errors"
	"github.com/letsencrypt/boulder/features"
	"github.com/letsencrypt/boulder/identifier"
	"github.com/letsencrypt/boulder/probs"
	"github.com/letsencrypt/boulder/test"
)

// pkiValidationPath returns the well-known path a pki-validation-01
// challenge response is expected at, given a full key authorization.
func pkiValidationPath(keyAuthorization string) string {
	h := sha256.Sum256([]byte(keyAuthorization))
	return "/.well-known/pki-validation/" + base64.RawURLEncoding.EncodeToString(h[:])
}

// startPKIValidationSrv returns an httptest.Server that serves the
// provided body at the expected pki-validation-01 path for the test
// key authorization.
func startPKIValidationSrv(t *testing.T, body string) *httptest.Server {
	t.Helper()
	m := http.NewServeMux()
	hs := httptest.NewUnstartedServer(m)
	m.HandleFunc(pkiValidationPath(expectedKeyAuthorization), func(w http.ResponseWriter, r *http.Request) {
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
	expectedPath := pkiValidationPath(expectedKeyAuthorization)
	recordURL, err := url.Parse(records[0].URL)
	if err != nil {
		t.Fatalf("failed to parse record URL %q: %v", records[0].URL, err)
	}
	if recordURL.Path != expectedPath {
		t.Errorf("record URL path = %q, want %q",
			recordURL.Path, expectedPath)
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
	// Only completely empty keyAuthorization is rejected now; any non-empty
	// string is valid to hash.
	_, err := va.validatePKIValidation01(ctx,
		identifier.NewDNS("example.com"), "tok", "")
	test.AssertErrorIs(t, err, berrors.Malformed)
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

func TestValidatePKIValidation01UniqueFilenames(t *testing.T) {
	// The primary motivation for using SHA-256(keyAuthorization) as the
	// filename is that different tokens (same account) produce different
	// paths. Under the old thumbprint-only scheme, these would collide.
	ka1 := "tokenAAA." + expectedThumbprint
	ka2 := "tokenBBB." + expectedThumbprint
	path1 := pkiValidationPath(ka1)
	path2 := pkiValidationPath(ka2)
	if path1 == path2 {
		t.Errorf("different tokens produced same path: %s", path1)
	}
	// Verify a known oracle value to catch algorithm/encoding regressions.
	// Precomputed: base64url(SHA-256("LoqXcYV8q5ONbJQxbmR7SCTNo3tiAXDfowyjxAjEuX0.9jg46WB3rR_AHD-EBXdN7cBkH1WOu0tA3M9fm21mqTI"))
	const oracleKeyAuth = "LoqXcYV8q5ONbJQxbmR7SCTNo3tiAXDfowyjxAjEuX0.9jg46WB3rR_AHD-EBXdN7cBkH1WOu0tA3M9fm21mqTI"
	const oraclePath = "/.well-known/pki-validation/LPsIwTo7o8BoG0-vjCyGQGBWSVIPxI-i_X336eUOQZo"
	if got := pkiValidationPath(oracleKeyAuth); got != oraclePath {
		t.Errorf("oracle mismatch: got %q, want %q", got, oraclePath)
	}
}
