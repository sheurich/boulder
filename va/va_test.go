package va

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	mrand "math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jmhodges/clock"
	"github.com/letsencrypt/boulder/bdns"
	"github.com/letsencrypt/boulder/cmd"
	"github.com/letsencrypt/boulder/core"
	blog "github.com/letsencrypt/boulder/log"
	"github.com/letsencrypt/boulder/metrics"
	"github.com/letsencrypt/boulder/probs"
	"github.com/letsencrypt/boulder/test"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/net/context"
	"gopkg.in/square/go-jose.v2"
)

func bigIntFromB64(b64 string) *big.Int {
	bytes, _ := base64.URLEncoding.DecodeString(b64)
	x := big.NewInt(0)
	x.SetBytes(bytes)
	return x
}

func intFromB64(b64 string) int {
	return int(bigIntFromB64(b64).Int64())
}

var n = bigIntFromB64("n4EPtAOCc9AlkeQHPzHStgAbgs7bTZLwUBZdR8_KuKPEHLd4rHVTeT-O-XV2jRojdNhxJWTDvNd7nqQ0VEiZQHz_AJmSCpMaJMRBSFKrKb2wqVwGU_NsYOYL-QtiWN2lbzcEe6XC0dApr5ydQLrHqkHHig3RBordaZ6Aj-oBHqFEHYpPe7Tpe-OfVfHd1E6cS6M1FZcD1NNLYD5lFHpPI9bTwJlsde3uhGqC0ZCuEHg8lhzwOHrtIQbS0FVbb9k3-tVTU4fg_3L_vniUFAKwuCLqKnS2BYwdq_mzSnbLY7h_qixoR7jig3__kRhuaxwUkRz5iaiQkqgc5gHdrNP5zw==")
var e = intFromB64("AQAB")
var d = bigIntFromB64("bWUC9B-EFRIo8kpGfh0ZuyGPvMNKvYWNtB_ikiH9k20eT-O1q_I78eiZkpXxXQ0UTEs2LsNRS-8uJbvQ-A1irkwMSMkK1J3XTGgdrhCku9gRldY7sNA_AKZGh-Q661_42rINLRCe8W-nZ34ui_qOfkLnK9QWDDqpaIsA-bMwWWSDFu2MUBYwkHTMEzLYGqOe04noqeq1hExBTHBOBdkMXiuFhUq1BU6l-DqEiWxqg82sXt2h-LMnT3046AOYJoRioz75tSUQfGCshWTBnP5uDjd18kKhyv07lhfSJdrPdM5Plyl21hsFf4L_mHCuoFau7gdsPfHPxxjVOcOpBrQzwQ==")
var p = bigIntFromB64("uKE2dh-cTf6ERF4k4e_jy78GfPYUIaUyoSSJuBzp3Cubk3OCqs6grT8bR_cu0Dm1MZwWmtdqDyI95HrUeq3MP15vMMON8lHTeZu2lmKvwqW7anV5UzhM1iZ7z4yMkuUwFWoBvyY898EXvRD-hdqRxHlSqAZ192zB3pVFJ0s7pFc=")
var q = bigIntFromB64("uKE2dh-cTf6ERF4k4e_jy78GfPYUIaUyoSSJuBzp3Cubk3OCqs6grT8bR_cu0Dm1MZwWmtdqDyI95HrUeq3MP15vMMON8lHTeZu2lmKvwqW7anV5UzhM1iZ7z4yMkuUwFWoBvyY898EXvRD-hdqRxHlSqAZ192zB3pVFJ0s7pFc=")

var TheKey = rsa.PrivateKey{
	PublicKey: rsa.PublicKey{N: n, E: e},
	D:         d,
	Primes:    []*big.Int{p, q},
}

var accountKey = &jose.JSONWebKey{Key: TheKey.Public()}

// Return an ACME DNS identifier for the given hostname
func dnsi(hostname string) core.AcmeIdentifier {
	return core.AcmeIdentifier{Type: core.IdentifierDNS, Value: hostname}
}

var ctx context.Context

func init() {
	ctx, _ = context.WithTimeout(context.Background(), 10*time.Minute)
}

var accountURIPrefixes = []string{"http://boulder:4000/acme/reg/"}

// All paths that get assigned to tokens MUST be valid tokens
const expectedToken = "LoqXcYV8q5ONbJQxbmR7SCTNo3tiAXDfowyjxAjEuX0"
const expectedKeyAuthorization = "LoqXcYV8q5ONbJQxbmR7SCTNo3tiAXDfowyjxAjEuX0.9jg46WB3rR_AHD-EBXdN7cBkH1WOu0tA3M9fm21mqTI"
const pathWrongToken = "i6lNAC4lOOLYCl-A08VJt9z_tKYvVk63Dumo8icsBjQ"
const path404 = "404"
const path500 = "500"
const pathFound = "GBq8SwWq3JsbREFdCamk5IX3KLsxW5ULeGs98Ajl_UM"
const pathMoved = "5J4FIMrWNfmvHZo-QpKZngmuhqZGwRm21-oEgUDstJM"
const pathRedirectInvalidPort = "port-redirect"
const pathWait = "wait"
const pathWaitLong = "wait-long"
const pathReLookup = "7e-P57coLM7D3woNTp_xbJrtlkDYy6PWf3mSSbLwCr4"
const pathReLookupInvalid = "re-lookup-invalid"
const pathRedirectToFailingURL = "re-to-failing-url"
const pathLooper = "looper"
const pathValid = "valid"
const rejectUserAgent = "rejectMe"

func httpSrv(t *testing.T, token string) *httptest.Server {
	m := http.NewServeMux()

	server := httptest.NewUnstartedServer(m)

	defaultToken := token
	currentToken := defaultToken

	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, path404) {
			t.Logf("HTTPSRV: Got a 404 req\n")
			http.NotFound(w, r)
		} else if strings.HasSuffix(r.URL.Path, path500) {
			t.Logf("HTTPSRV: Got a 500 req\n")
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		} else if strings.HasSuffix(r.URL.Path, pathMoved) {
			t.Logf("HTTPSRV: Got a 301 redirect req\n")
			if currentToken == defaultToken {
				currentToken = pathMoved
			}
			http.Redirect(w, r, pathValid, 301)
		} else if strings.HasSuffix(r.URL.Path, pathFound) {
			t.Logf("HTTPSRV: Got a 302 redirect req\n")
			if currentToken == defaultToken {
				currentToken = pathFound
			}
			http.Redirect(w, r, pathMoved, 302)
		} else if strings.HasSuffix(r.URL.Path, pathWait) {
			t.Logf("HTTPSRV: Got a wait req\n")
			time.Sleep(time.Second * 3)
		} else if strings.HasSuffix(r.URL.Path, pathWaitLong) {
			t.Logf("HTTPSRV: Got a wait-long req\n")
			time.Sleep(time.Second * 10)
		} else if strings.HasSuffix(r.URL.Path, pathReLookup) {
			t.Logf("HTTPSRV: Got a redirect req to a valid hostname\n")
			if currentToken == defaultToken {
				currentToken = pathReLookup
			}
			port := getPort(server)
			http.Redirect(w, r, fmt.Sprintf("http://other.valid.com:%d/path", port), 302)
		} else if strings.HasSuffix(r.URL.Path, pathReLookupInvalid) {
			t.Logf("HTTPSRV: Got a redirect req to an invalid hostname\n")
			http.Redirect(w, r, "http://invalid.invalid/path", 302)
		} else if strings.HasSuffix(r.URL.Path, pathRedirectToFailingURL) {
			t.Logf("HTTPSRV: Redirecting to a URL that will fail\n")
			port := getPort(server)
			http.Redirect(w, r, fmt.Sprintf("http://other.valid.com:%d/%s", port, path500), 301)
		} else if strings.HasSuffix(r.URL.Path, pathLooper) {
			t.Logf("HTTPSRV: Got a loop req\n")
			http.Redirect(w, r, r.URL.String(), 301)
		} else if strings.HasSuffix(r.URL.Path, pathRedirectInvalidPort) {
			t.Logf("HTTPSRV: Got a port redirect req\n")
			// Port 8080 is not the VA's httpPort or httpsPort and should be rejected
			http.Redirect(w, r, "http://other.valid.com:8080/path", 302)
		} else if r.Header.Get("User-Agent") == rejectUserAgent {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("found trap User-Agent"))
		} else {
			t.Logf("HTTPSRV: Got a valid req\n")
			t.Logf("HTTPSRV: Path = %s\n", r.URL.Path)

			ch := core.Challenge{Token: currentToken}
			keyAuthz, _ := ch.ExpectedKeyAuthorization(accountKey)
			t.Logf("HTTPSRV: Key Authz = '%s%s'\n", keyAuthz, "\\n\\r \\t")

			fmt.Fprint(w, keyAuthz, "\n\r \t")
			currentToken = defaultToken
		}
	})

	server.Start()
	return server
}

func tlsCertTemplate(names []string) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber: big.NewInt(1337),
		Subject: pkix.Name{
			Organization: []string{"tests"},
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().AddDate(0, 0, 1),

		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,

		DNSNames: names,
	}
}

func makeACert(names []string) *tls.Certificate {
	template := tlsCertTemplate(names)
	certBytes, _ := x509.CreateCertificate(rand.Reader, template, template, &TheKey.PublicKey, &TheKey)
	return &tls.Certificate{
		Certificate: [][]byte{certBytes},
		PrivateKey:  &TheKey,
	}
}

// tlssniSrvWithNames is kept around for the use of TestValidateTLSALPN01UnawareSrv
func tlssniSrvWithNames(t *testing.T, chall core.Challenge, names ...string) *httptest.Server {
	cert := makeACert(names)
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*cert},
		ClientAuth:   tls.NoClientCert,
		GetCertificate: func(clientHello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			return cert, nil
		},
		NextProtos: []string{"http/1.1"},
	}

	hs := httptest.NewUnstartedServer(http.DefaultServeMux)
	hs.TLS = tlsConfig
	hs.StartTLS()
	return hs
}

func tlsalpn01Srv(t *testing.T, chall core.Challenge, oid asn1.ObjectIdentifier, names ...string) *httptest.Server {
	template := tlsCertTemplate(names)
	certBytes, _ := x509.CreateCertificate(rand.Reader, template, template, &TheKey.PublicKey, &TheKey)
	cert := &tls.Certificate{
		Certificate: [][]byte{certBytes},
		PrivateKey:  &TheKey,
	}

	shasum := sha256.Sum256([]byte(chall.ProvidedKeyAuthorization))
	encHash, _ := asn1.Marshal(shasum[:])
	acmeExtension := pkix.Extension{
		Id:       oid,
		Critical: true,
		Value:    encHash,
	}

	template.ExtraExtensions = []pkix.Extension{acmeExtension}
	certBytes, _ = x509.CreateCertificate(rand.Reader, template, template, &TheKey.PublicKey, &TheKey)
	acmeCert := &tls.Certificate{
		Certificate: [][]byte{certBytes},
		PrivateKey:  &TheKey,
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{},
		ClientAuth:   tls.NoClientCert,
		GetCertificate: func(clientHello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if clientHello.ServerName != names[0] {
				return nil, nil
			}
			if len(clientHello.SupportedProtos) == 1 && clientHello.SupportedProtos[0] == ACMETLS1Protocol {
				return acmeCert, nil
			}
			return cert, nil
		},
		NextProtos: []string{"http/1.1", ACMETLS1Protocol},
	}

	hs := httptest.NewUnstartedServer(http.DefaultServeMux)
	hs.TLS = tlsConfig
	hs.Config.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){
		ACMETLS1Protocol: func(_ *http.Server, conn *tls.Conn, _ http.Handler) {
			_ = conn.Close()
		},
	}
	hs.StartTLS()
	return hs
}

func TestHTTPBadPort(t *testing.T) {
	chall := core.HTTPChallenge01("")
	setChallengeToken(&chall, expectedToken)

	hs := httpSrv(t, chall.Token)
	defer hs.Close()

	va, _ := setup(hs, 0)

	// Pick a random port between 40000 and 65000 - with great certainty we won't
	// have an HTTP server listening on this port and the test will fail as
	// intended
	badPort := 40000 + mrand.Intn(25000)
	va.httpPort = badPort

	_, prob := va.validateHTTP01(ctx, dnsi("localhost"), chall)
	if prob == nil {
		t.Fatalf("Server's down; expected refusal. Where did we connect?")
	}
	test.AssertEquals(t, prob.Type, probs.ConnectionProblem)
	if !strings.Contains(prob.Detail, "Connection refused") {
		t.Errorf("Expected a connection refused error, got %q", prob.Detail)
	}
}

func TestHTTP(t *testing.T) {
	chall := core.HTTPChallenge01("")
	setChallengeToken(&chall, expectedToken)

	// NOTE: We do not attempt to shut down the server. The problem is that the
	// "wait-long" handler sleeps for ten seconds, but this test finishes in less
	// than that. So if we try to call hs.Close() at the end of the test, we'll be
	// closing the test server while a request is still pending. Unfortunately,
	// there appears to be an issue in httptest that trips Go's race detector when
	// that happens, failing the test. So instead, we live with leaving the server
	// around till the process exits.
	// TODO(#1989): close hs
	hs := httpSrv(t, chall.Token)

	va, log := setup(hs, 0)

	log.Clear()
	t.Logf("Trying to validate: %+v\n", chall)
	_, prob := va.validateHTTP01(ctx, dnsi("localhost.com"), chall)
	if prob != nil {
		t.Errorf("Unexpected failure in HTTP validation: %s", prob)
	}
	test.AssertEquals(t, len(log.GetAllMatching(`\[AUDIT\] `)), 1)

	log.Clear()
	setChallengeToken(&chall, path404)
	_, prob = va.validateHTTP01(ctx, dnsi("localhost.com"), chall)
	if prob == nil {
		t.Fatalf("Should have found a 404 for the challenge.")
	}
	test.AssertEquals(t, prob.Type, probs.UnauthorizedProblem)
	test.AssertEquals(t, len(log.GetAllMatching(`\[AUDIT\] `)), 1)

	log.Clear()
	setChallengeToken(&chall, pathWrongToken)
	// The "wrong token" will actually be the expectedToken.  It's wrong
	// because it doesn't match pathWrongToken.
	_, prob = va.validateHTTP01(ctx, dnsi("localhost.com"), chall)
	if prob == nil {
		t.Fatalf("Should have found the wrong token value.")
	}
	test.AssertEquals(t, prob.Type, probs.UnauthorizedProblem)
	test.AssertEquals(t, len(log.GetAllMatching(`\[AUDIT\] `)), 1)

	log.Clear()
	setChallengeToken(&chall, pathMoved)
	_, prob = va.validateHTTP01(ctx, dnsi("localhost.com"), chall)
	if prob != nil {
		t.Fatalf("Failed to follow 301 redirect")
	}
	redirectValid := `following redirect to host "" url "http://localhost.com/.well-known/acme-challenge/` + pathValid + `"`
	matchedValidRedirect := log.GetAllMatching(redirectValid)
	test.AssertEquals(t, len(matchedValidRedirect), 1)

	log.Clear()
	setChallengeToken(&chall, pathFound)
	_, prob = va.validateHTTP01(ctx, dnsi("localhost.com"), chall)
	if prob != nil {
		t.Fatalf("Failed to follow 302 redirect")
	}
	redirectMoved := `following redirect to host "" url "http://localhost.com/.well-known/acme-challenge/` + pathMoved + `"`
	matchedMovedRedirect := log.GetAllMatching(redirectMoved)
	test.AssertEquals(t, len(matchedValidRedirect), 1)
	test.AssertEquals(t, len(matchedMovedRedirect), 1)

	ipIdentifier := core.AcmeIdentifier{Type: core.IdentifierType("ip"), Value: "127.0.0.1"}
	_, prob = va.validateHTTP01(ctx, ipIdentifier, chall)
	if prob == nil {
		t.Fatalf("IdentifierType IP shouldn't have worked.")
	}
	test.AssertEquals(t, prob.Type, probs.MalformedProblem)

	_, prob = va.validateHTTP01(ctx, core.AcmeIdentifier{Type: core.IdentifierDNS, Value: "always.invalid"}, chall)
	if prob == nil {
		t.Fatalf("Domain name is invalid.")
	}
	test.AssertEquals(t, prob.Type, probs.ConnectionProblem)
}

func TestHTTPTimeout(t *testing.T) {
	chall := core.HTTPChallenge01("")
	setChallengeToken(&chall, expectedToken)

	hs := httpSrv(t, chall.Token)
	// TODO(#1989): close hs

	va, _ := setup(hs, 0)
	setChallengeToken(&chall, pathWaitLong)

	expectMatch := regexp.MustCompile(
		"Fetching http://localhost/.well-known/acme-challenge/wait-long: Timeout after connect")

	started := time.Now()
	timeout := 250 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, prob := va.validateHTTP01(ctx, dnsi("localhost"), chall)
	if prob == nil {
		t.Fatalf("Connection should've timed out")
	}

	took := time.Since(started)
	// Check that the HTTP connection doesn't return before a timeout, and times
	// out after the expected time
	if took < timeout-200*time.Millisecond {
		t.Fatalf("HTTP timed out before %s: %s with %s", timeout, took, prob)
	}
	if took > 2*timeout {
		t.Fatalf("HTTP connection didn't timeout after %s", timeout)
	}
	test.AssertEquals(t, prob.Type, probs.ConnectionProblem)

	if !expectMatch.MatchString(prob.Detail) {
		t.Errorf("Problem details incorrect. Got %q, expected to match %q",
			prob.Detail, expectMatch)
	}
}

// dnsMockReturnsUnroutable is a DNSClient mock that always returns an
// unroutable address for LookupHost. This is useful in testing connect
// timeouts.
type dnsMockReturnsUnroutable struct {
	*bdns.MockDNSClient
}

func (mock dnsMockReturnsUnroutable) LookupHost(_ context.Context, hostname string) ([]net.IP, error) {
	return []net.IP{net.ParseIP("198.51.100.1")}, nil
}

// TestHTTPDialTimeout tests that we give the proper "Timeout during connect"
// error when dial fails. We do this by using a mock DNS client that resolves
// everything to an unroutable IP address.
func TestHTTPDialTimeout(t *testing.T) {
	va, _ := setup(nil, 0)

	started := time.Now()
	timeout := 250 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	va.dnsClient = dnsMockReturnsUnroutable{&bdns.MockDNSClient{}}
	// The only method I've found so far to trigger a connect timeout is to
	// connect to an unrouteable IP address. This usuall generates a connection
	// timeout, but will rarely return "Network unreachable" instead. If we get
	// that, just retry until we get something other than "Network unreachable".
	var prob *probs.ProblemDetails
	for i := 0; i < 20; i++ {
		_, prob = va.validateHTTP01(ctx, dnsi("unroutable.invalid"), core.HTTPChallenge01(""))
		if prob != nil && strings.Contains(prob.Detail, "Network unreachable") {
			continue
		} else {
			break
		}
	}
	if prob == nil {
		t.Fatalf("Connection should've timed out")
	}
	took := time.Since(started)
	// Check that the HTTP connection doesn't return too fast, and times
	// out after the expected time
	if took < (timeout-200*time.Millisecond)/2 {
		t.Fatalf("HTTP returned before %s (%s) with %#v", timeout, took, prob)
	}
	if took > 2*timeout {
		t.Fatalf("HTTP connection didn't timeout after %s seconds", timeout)
	}
	test.AssertEquals(t, prob.Type, probs.ConnectionProblem)
	expectMatch := regexp.MustCompile(
		"Fetching http://unroutable.invalid/.well-known/acme-challenge/.*: Timeout during connect")
	if !expectMatch.MatchString(prob.Detail) {
		t.Errorf("Problem details incorrect. Got %q, expected to match %q",
			prob.Detail, expectMatch)
	}
}

func TestHTTPRedirectLookup(t *testing.T) {
	chall := core.HTTPChallenge01("")
	setChallengeToken(&chall, expectedToken)

	hs := httpSrv(t, expectedToken)
	defer hs.Close()
	va, log := setup(hs, 0)

	setChallengeToken(&chall, pathMoved)
	_, prob := va.validateHTTP01(ctx, dnsi("localhost.com"), chall)
	if prob != nil {
		t.Fatalf("Unexpected failure in redirect (%s): %s", pathMoved, prob)
	}
	redirectValid := `following redirect to host "" url "http://localhost.com/.well-known/acme-challenge/` + pathValid + `"`
	matchedValidRedirect := log.GetAllMatching(redirectValid)
	test.AssertEquals(t, len(matchedValidRedirect), 1)
	test.AssertEquals(t, len(log.GetAllMatching(`Resolved addresses for localhost.com: \[127.0.0.1\]`)), 2)

	log.Clear()
	setChallengeToken(&chall, pathFound)
	_, prob = va.validateHTTP01(ctx, dnsi("localhost.com"), chall)
	if prob != nil {
		t.Fatalf("Unexpected failure in redirect (%s): %s", pathFound, prob)
	}
	redirectMoved := `following redirect to host "" url "http://localhost.com/.well-known/acme-challenge/` + pathMoved + `"`
	matchedMovedRedirect := log.GetAllMatching(redirectMoved)
	test.AssertEquals(t, len(matchedMovedRedirect), 1)
	test.AssertEquals(t, len(log.GetAllMatching(`Resolved addresses for localhost.com: \[127.0.0.1\]`)), 3)

	log.Clear()
	setChallengeToken(&chall, pathReLookupInvalid)
	_, err := va.validateHTTP01(ctx, dnsi("localhost.com"), chall)
	test.AssertError(t, err, chall.Token)
	test.AssertEquals(t, len(log.GetAllMatching(`Resolved addresses for localhost.com: \[127.0.0.1\]`)), 1)
	test.AssertDeepEquals(t, err, probs.ConnectionFailure("Fetching http://invalid.invalid/path: Invalid hostname in redirect target, must end in IANA registered TLD"))

	log.Clear()
	setChallengeToken(&chall, pathReLookup)
	_, prob = va.validateHTTP01(ctx, dnsi("localhost.com"), chall)
	if prob != nil {
		t.Fatalf("Unexpected error in redirect (%s): %s", pathReLookup, prob)
	}
	redirectPattern := `following redirect to host "" url "http://other.valid.com:\d+/path"`
	test.AssertEquals(t, len(log.GetAllMatching(redirectPattern)), 1)
	test.AssertEquals(t, len(log.GetAllMatching(`Resolved addresses for localhost.com: \[127.0.0.1\]`)), 1)
	test.AssertEquals(t, len(log.GetAllMatching(`Resolved addresses for other.valid.com: \[127.0.0.1\]`)), 1)

	log.Clear()
	setChallengeToken(&chall, pathRedirectInvalidPort)
	_, prob = va.validateHTTP01(ctx, dnsi("localhost.com"), chall)
	test.AssertNotNil(t, prob, "Problem details for pathRedirectInvalidPort should not be nil")
	test.AssertEquals(t, prob.Detail, fmt.Sprintf(
		"Fetching http://other.valid.com:8080/path: Invalid port in redirect target. "+
			"Only ports %d and %d are supported, not 8080", va.httpPort, va.httpsPort))

	// This case will redirect from a valid host to a host that is throwing
	// HTTP 500 errors. The test case is ensuring that the connection error
	// is referencing the redirected to host, instead of the original host.
	log.Clear()
	setChallengeToken(&chall, pathRedirectToFailingURL)
	_, prob = va.validateHTTP01(ctx, dnsi("localhost.com"), chall)
	test.AssertNotNil(t, prob, "Problem Details should not be nil")
	test.AssertDeepEquals(t, prob,
		probs.Unauthorized(
			fmt.Sprintf("Invalid response from http://other.valid.com:%d/500 [127.0.0.1]: 500",
				va.httpPort)))
}

func TestHTTPRedirectLoop(t *testing.T) {
	chall := core.HTTPChallenge01("")
	setChallengeToken(&chall, "looper")

	hs := httpSrv(t, expectedToken)
	defer hs.Close()
	va, _ := setup(hs, 0)

	_, prob := va.validateHTTP01(ctx, dnsi("localhost"), chall)
	if prob == nil {
		t.Fatalf("Challenge should have failed for %s", chall.Token)
	}
}

func TestHTTPRedirectUserAgent(t *testing.T) {
	chall := core.HTTPChallenge01("")
	setChallengeToken(&chall, expectedToken)

	hs := httpSrv(t, expectedToken)
	defer hs.Close()
	va, _ := setup(hs, 0)
	va.userAgent = rejectUserAgent

	setChallengeToken(&chall, pathMoved)
	_, prob := va.validateHTTP01(ctx, dnsi("localhost"), chall)
	if prob == nil {
		t.Fatalf("Challenge with rejectUserAgent should have failed (%s).", pathMoved)
	}

	setChallengeToken(&chall, pathFound)
	_, prob = va.validateHTTP01(ctx, dnsi("localhost"), chall)
	if prob == nil {
		t.Fatalf("Challenge with rejectUserAgent should have failed (%s).", pathFound)
	}
}

func getPort(hs *httptest.Server) int {
	url, err := url.Parse(hs.URL)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse hs URL: %q - %s", hs.URL, err.Error()))
	}
	_, portString, err := net.SplitHostPort(url.Host)
	if err != nil {
		panic(fmt.Sprintf("Failed to split hs URL host: %q - %s", url.Host, err.Error()))
	}
	port, err := strconv.ParseInt(portString, 10, 64)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse hs URL port: %q - %s", portString, err.Error()))
	}
	return int(port)
}

func TestTLSALPN01FailIP(t *testing.T) {
	chall := createChallenge(core.ChallengeTypeTLSALPN01)
	hs := tlsalpn01Srv(t, chall, IdPeAcmeIdentifier, "localhost")
	va, _ := setup(hs, 0)

	port := getPort(hs)
	_, prob := va.validateTLSALPN01(ctx, core.AcmeIdentifier{
		Type:  core.IdentifierType("ip"),
		Value: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
	}, chall)
	if prob == nil {
		t.Fatalf("IdentifierType IP shouldn't have worked.")
	}
	test.AssertEquals(t, prob.Type, probs.MalformedProblem)
}

func slowTLSSrv() *httptest.Server {
	server := httptest.NewUnstartedServer(http.DefaultServeMux)
	server.TLS = &tls.Config{
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			time.Sleep(100 * time.Millisecond)
			return makeACert([]string{"nomatter"}), nil
		},
	}
	server.StartTLS()
	return server
}

func TestTLSALPNTimeoutAfterConnect(t *testing.T) {
	chall := createChallenge(core.ChallengeTypeTLSALPN01)
	hs := slowTLSSrv()
	va, _ := setup(hs, 0)

	timeout := 50 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	started := time.Now()
	_, prob := va.validateTLSALPN01(ctx, dnsi("slow.server"), chall)
	if prob == nil {
		t.Fatalf("Validation should've failed")
	}
	// Check that the TLS connection doesn't return before a timeout, and times
	// out after the expected time
	took := time.Since(started)
	// Check that the HTTP connection doesn't return too fast, and times
	// out after the expected time
	if took < timeout/2 {
		t.Fatalf("TLSSNI returned before %s (%s) with %#v", timeout, took, prob)
	}
	if took > 2*timeout {
		t.Fatalf("TLSSNI didn't timeout after %s (took %s to return %#v)", timeout,
			took, prob)
	}
	if prob == nil {
		t.Fatalf("Connection should've timed out")
	}
	test.AssertEquals(t, prob.Type, probs.ConnectionProblem)
	expected := "Timeout during read (your server may be slow or overloaded)"
	if prob.Detail != expected {
		t.Errorf("Wrong error detail. Expected %q, got %q", expected, prob.Detail)
	}
}

func TestTLSALPN01DialTimeout(t *testing.T) {
	chall := createChallenge(core.ChallengeTypeTLSALPN01)
	hs := slowTLSSrv()
	va, _ := setup(hs, 0)
	va.dnsClient = dnsMockReturnsUnroutable{&bdns.MockDNSClient{}}
	started := time.Now()

	timeout := 50 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// The only method I've found so far to trigger a connect timeout is to
	// connect to an unrouteable IP address. This usuall generates a connection
	// timeout, but will rarely return "Network unreachable" instead. If we get
	// that, just retry until we get something other than "Network unreachable".
	var prob *probs.ProblemDetails
	for i := 0; i < 20; i++ {
		_, prob = va.validateTLSALPN01(ctx, dnsi("unroutable.invalid"), chall)
		if prob != nil && strings.Contains(prob.Detail, "Network unreachable") {
			continue
		} else {
			break
		}
	}

	if prob == nil {
		t.Fatalf("Validation should've failed")
	}
	// Check that the TLS connection doesn't return before a timeout, and times
	// out after the expected time
	took := time.Since(started)
	// Check that the HTTP connection doesn't return too fast, and times
	// out after the expected time
	if took < timeout/2 {
		t.Fatalf("TLSSNI returned before %s (%s) with %#v", timeout, took, prob)
	}
	if took > 2*timeout {
		t.Fatalf("TLSSNI didn't timeout after %s", timeout)
	}
	if prob == nil {
		t.Fatalf("Connection should've timed out")
	}
	test.AssertEquals(t, prob.Type, probs.ConnectionProblem)
	expected := "Timeout during connect (likely firewall problem)"
	if prob.Detail != expected {
		t.Errorf("Wrong error detail. Expected %q, got %q", expected, prob.Detail)
	}
}

func TestTLSALPN01Refused(t *testing.T) {
	chall := createChallenge(core.ChallengeTypeTLSALPN01)
	hs := tlsalpn01Srv(t, chall, IdPeAcmeIdentifier, "localhost")
	va, _ := setup(hs, 0)
	// Take down validation server and check that validation fails.
	hs.Close()
	_, prob := va.validateTLSALPN01(ctx, dnsi("localhost"), chall)
	if prob == nil {
		t.Fatalf("Server's down; expected refusal. Where did we connect?")
	}
	test.AssertEquals(t, prob.Type, probs.ConnectionProblem)
	expected := "Connection refused"
	if prob.Detail != expected {
		t.Errorf("Wrong error detail. Expected %q, got %q", expected, prob.Detail)
	}
}

func TestTLSALPN01TalkingToHTTP(t *testing.T) {
	chall := createChallenge(core.ChallengeTypeTLSALPN01)
	hs := tlsalpn01Srv(t, chall, IdPeAcmeIdentifier, "localhost")
	va, _ := setup(hs, 0)
	httpOnly := httpSrv(t, "")
	va.tlsPort = getPort(httpOnly)

	_, prob := va.validateTLSALPN01(ctx, dnsi("localhost"), chall)
	test.AssertError(t, prob, "TLS-SNI-01 validation passed when talking to a HTTP-only server")
	expected := "Server only speaks HTTP, not TLS"
	if !strings.HasSuffix(prob.Detail, expected) {
		t.Errorf("Got wrong error detail. Expected %q, got %q", expected, prob.Detail)
	}
}

func brokenTLSSrv() *httptest.Server {
	server := httptest.NewUnstartedServer(http.DefaultServeMux)
	server.TLS = &tls.Config{
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return nil, fmt.Errorf("Failing on purpose")
		},
	}
	server.StartTLS()
	return server
}

func TestTLSError(t *testing.T) {
	chall := createChallenge(core.ChallengeTypeTLSALPN01)
	hs := brokenTLSSrv()

	va, _ := setup(hs, 0)

	_, prob := va.validateTLSALPN01(ctx, dnsi("localhost"), chall)
	if prob == nil {
		t.Fatalf("TLS validation should have failed: What cert was used?")
	}
	if prob.Type != probs.TLSProblem {
		t.Errorf("Wrong problem type: got %s, expected type %s",
			prob, probs.TLSProblem)
	}
}

func TestCertNames(t *testing.T) {
	// We duplicate names inside the SAN set
	names := []string{
		"hello.world", "goodbye.world",
		"hello.world", "goodbye.world",
		"bonjour.le.monde", "au.revoir.le.monde",
		"bonjour.le.monde", "au.revoir.le.monde",
		"f\xffoo", "f\xffoo",
	}
	// We expect only unique names, in sorted order and with any invalid utf-8
	// replaced.
	expected := []string{
		"au.revoir.le.monde", "bonjour.le.monde",
		"f\ufffdoo", "goodbye.world", "hello.world",
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1337),
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(0, 0, 1),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,

		Subject: pkix.Name{
			// We also duplicate a name from the SANs as the CN
			CommonName: names[0],
		},
		DNSNames: names,
	}

	// Create the certificate, check that certNames provides the expected result
	certBytes, _ := x509.CreateCertificate(rand.Reader, template, template, &TheKey.PublicKey, &TheKey)
	cert, _ := x509.ParseCertificate(certBytes)
	actual := certNames(cert)
	test.AssertDeepEquals(t, actual, expected)
}

func TestValidateHTTP(t *testing.T) {
	chall := core.HTTPChallenge01("")
	setChallengeToken(&chall, core.NewToken())

	hs := httpSrv(t, chall.Token)
	defer hs.Close()

	va, _ := setup(hs, 0)

	_, prob := va.validateChallenge(ctx, dnsi("localhost"), chall)
	test.Assert(t, prob == nil, "validation failed")
}

// challengeType == "tls-sni-00" or "dns-00", since they're the same
func createChallenge(challengeType string) core.Challenge {
	chall := core.Challenge{
		Type:                     challengeType,
		Status:                   core.StatusPending,
		Token:                    expectedToken,
		ValidationRecord:         []core.ValidationRecord{},
		ProvidedKeyAuthorization: expectedKeyAuthorization,
	}

	return chall
}

// setChallengeToken sets the token value, and sets the ProvidedKeyAuthorization
// to match.
func setChallengeToken(ch *core.Challenge, token string) {
	ch.Token = token
	ch.ProvidedKeyAuthorization = token + ".9jg46WB3rR_AHD-EBXdN7cBkH1WOu0tA3M9fm21mqTI"
}

func TestTLSALPN01Success(t *testing.T) {
	chall := createChallenge(core.ChallengeTypeTLSALPN01)
	hs := tlsalpn01Srv(t, chall, IdPeAcmeIdentifier, "localhost")

	va, _ := setup(hs, 0)

	_, prob := va.validateChallenge(ctx, dnsi("localhost"), chall)
	if prob != nil {
		t.Errorf("Validation failed: %v", prob)
	}
	test.AssertEquals(t, test.CountCounterVec("oid", IdPeAcmeIdentifier.String(), va.metrics.tlsALPNOIDCounter), 1)

	hs.Close()
	chall = createChallenge(core.ChallengeTypeTLSALPN01)
	hs = tlsalpn01Srv(t, chall, IdPeAcmeIdentifierV1Obsolete, "localhost")

	va, _ = setup(hs, 0)

	_, prob = va.validateChallenge(ctx, dnsi("localhost"), chall)
	if prob != nil {
		t.Errorf("Validation failed: %v", prob)
	}
	test.AssertEquals(t, test.CountCounterVec("oid", IdPeAcmeIdentifierV1Obsolete.String(), va.metrics.tlsALPNOIDCounter), 1)
}

func TestValidateTLSALPN01BadChallenge(t *testing.T) {
	chall := createChallenge(core.ChallengeTypeTLSALPN01)
	chall2 := chall
	setChallengeToken(&chall2, "bad token")

	hs := tlsalpn01Srv(t, chall2, IdPeAcmeIdentifier, "localhost")
	va, _ := setup(hs, 0)

	_, prob := va.validateTLSALPN01(ctx, dnsi("localhost"), chall)

	if prob == nil {
		t.Fatalf("TLS ALPN validation should have failed.")
	}
	test.AssertEquals(t, prob.Type, probs.UnauthorizedProblem)
}

func TestValidateTLSALPN01BrokenSrv(t *testing.T) {
	chall := createChallenge(core.ChallengeTypeTLSALPN01)
	hs := brokenTLSSrv()

	va, _ := setup(hs, 0)

	_, prob := va.validateTLSALPN01(ctx, dnsi("localhost"), chall)
	if prob == nil {
		t.Fatalf("TLS ALPN validation should have failed.")
	}
	test.AssertEquals(t, prob.Type, probs.TLSProblem)
}

func TestValidateTLSALPN01UnawareSrv(t *testing.T) {
	chall := createChallenge(core.ChallengeTypeTLSALPN01)
	hs := tlssniSrvWithNames(t, chall, "localhost")

	va, _ := setup(hs, 0)

	_, prob := va.validateTLSALPN01(ctx, dnsi("localhost"), chall)
	if prob == nil {
		t.Fatalf("TLS ALPN validation should have failed.")
	}
	test.AssertEquals(t, prob.Type, probs.UnauthorizedProblem)
}

// TestValidateTLSALPN01BadUTFSrv tests that validating TLS-ALPN-01 against
// a host that returns a certificate with a SAN/CN that contains invalid UTF-8
// will result in a problem with the invalid UTF-8 replaced.
func TestValidateTLSALPN01BadUTFSrv(t *testing.T) {
	chall := createChallenge(core.ChallengeTypeTLSALPN01)
	hs := tlsalpn01Srv(t, chall, IdPeAcmeIdentifier, "localhost", "\xf0\x28\x8c\xbc")
	port := getPort(hs)
	va, _ := setup(hs, 0)

	_, prob := va.validateTLSALPN01(ctx, dnsi("localhost"), chall)
	if prob == nil {
		t.Fatalf("TLS ALPN validation should have failed.")
	}
	test.AssertEquals(t, prob.Type, probs.UnauthorizedProblem)
	test.AssertEquals(t, prob.Detail, fmt.Sprintf(
		"Incorrect validation certificate for tls-alpn-01 challenge. "+
			"Requested localhost from 127.0.0.1:%d. Received 1 certificate(s), "+
			`first certificate had names "localhost, %s"`,
		port, "\ufffd(\ufffd\ufffd"))
}

func TestPerformValidationInvalid(t *testing.T) {
	va, _ := setup(nil, 0)

	chalDNS := createChallenge(core.ChallengeTypeDNS01)
	_, prob := va.PerformValidation(context.Background(), "foo.com", chalDNS, core.Authorization{})
	test.Assert(t, prob != nil, "validation succeeded")

	samples := test.CountHistogramSamples(va.metrics.validationTime.With(prometheus.Labels{
		"type":        "dns-01",
		"result":      "invalid",
		"problemType": "unauthorized",
	}))
	if samples != 1 {
		t.Errorf("Wrong number of samples for invalid validation. Expected 1, got %d", samples)
	}
}

func TestDNSValidationEmpty(t *testing.T) {
	va, _ := setup(nil, 0)

	chalDNS := createChallenge(core.ChallengeTypeDNS01)
	_, prob := va.PerformValidation(
		context.Background(),
		"empty-txts.com",
		chalDNS,
		core.Authorization{})
	test.AssertEquals(t, prob.Error(), "unauthorized :: No TXT record found at _acme-challenge.empty-txts.com")

	samples := test.CountHistogramSamples(va.metrics.validationTime.With(prometheus.Labels{
		"type":        "dns-01",
		"result":      "invalid",
		"problemType": "unauthorized",
	}))
	if samples != 1 {
		t.Errorf("Wrong number of samples for invalid validation. Expected 1, got %d", samples)
	}
}

func TestDNSValidationWrong(t *testing.T) {
	va, _ := setup(nil, 0)

	chalDNS := createChallenge(core.ChallengeTypeDNS01)
	_, prob := va.PerformValidation(
		context.Background(),
		"wrong-dns01.com",
		chalDNS,
		core.Authorization{})
	if prob == nil {
		t.Fatalf("Successful DNS validation with wrong TXT record")
	}
	test.AssertEquals(t, prob.Error(), "unauthorized :: Incorrect TXT record \"a\" found at _acme-challenge.wrong-dns01.com")
}

func TestDNSValidationWrongMany(t *testing.T) {
	va, _ := setup(nil, 0)

	chalDNS := createChallenge(core.ChallengeTypeDNS01)
	_, prob := va.PerformValidation(
		context.Background(),
		"wrong-many-dns01.com",
		chalDNS,
		core.Authorization{})
	if prob == nil {
		t.Fatalf("Successful DNS validation with wrong TXT record")
	}
	test.AssertEquals(t, prob.Error(), "unauthorized :: Incorrect TXT record \"a\" (and 4 more) found at _acme-challenge.wrong-many-dns01.com")
}

func TestDNSValidationWrongLong(t *testing.T) {
	va, _ := setup(nil, 0)

	chalDNS := createChallenge(core.ChallengeTypeDNS01)
	_, prob := va.PerformValidation(
		context.Background(),
		"long-dns01.com",
		chalDNS,
		core.Authorization{})
	if prob == nil {
		t.Fatalf("Successful DNS validation with wrong TXT record")
	}
	test.AssertEquals(t, prob.Error(), "unauthorized :: Incorrect TXT record \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa...\" found at _acme-challenge.long-dns01.com")
}

func TestPerformValidationValid(t *testing.T) {
	va, mockLog := setup(nil, 0)

	// create a challenge with well known token
	chalDNS := core.DNSChallenge01("")
	chalDNS.Token = expectedToken
	chalDNS.ProvidedKeyAuthorization = expectedKeyAuthorization
	_, prob := va.PerformValidation(context.Background(), "good-dns01.com", chalDNS, core.Authorization{})
	test.Assert(t, prob == nil, fmt.Sprintf("validation failed: %#v", prob))

	samples := test.CountHistogramSamples(va.metrics.validationTime.With(prometheus.Labels{
		"type":        "dns-01",
		"result":      "valid",
		"problemType": "",
	}))
	if samples != 1 {
		t.Errorf("Wrong number of samples for successful validation. Expected 1, got %d", samples)
	}
	resultLog := mockLog.GetAllMatching(`Validation result`)
	if len(resultLog) != 1 {
		t.Fatalf("Wrong number of matching lines for 'Validation result'")
	}
	if !strings.Contains(resultLog[0], `"Hostname":"good-dns01.com"`) {
		t.Errorf("PerformValidation didn't log validation hostname.")
	}
}

// TestPerformValidationWildcard tests that the VA properly strips the `*.`
// prefix from a wildcard name provided to the PerformValidation function.
func TestPerformValidationWildcard(t *testing.T) {
	va, mockLog := setup(nil, 0)

	// create a challenge with well known token
	chalDNS := core.DNSChallenge01("")
	chalDNS.Token = expectedToken
	chalDNS.ProvidedKeyAuthorization = expectedKeyAuthorization
	// perform a validation for a wildcard name
	_, prob := va.PerformValidation(context.Background(), "*.good-dns01.com", chalDNS, core.Authorization{})
	test.Assert(t, prob == nil, fmt.Sprintf("validation failed: %#v", prob))

	samples := test.CountHistogramSamples(va.metrics.validationTime.With(prometheus.Labels{
		"type":        "dns-01",
		"result":      "valid",
		"problemType": "",
	}))
	if samples != 1 {
		t.Errorf("Wrong number of samples for successful validation. Expected 1, got %d", samples)
	}
	resultLog := mockLog.GetAllMatching(`Validation result`)
	if len(resultLog) != 1 {
		t.Fatalf("Wrong number of matching lines for 'Validation result'")
	}

	// We expect that the top level Hostname reflect the wildcard name
	if !strings.Contains(resultLog[0], `"Hostname":"*.good-dns01.com"`) {
		t.Errorf("PerformValidation didn't log correct validation hostname.")
	}
	// We expect that the ValidationRecord contain the correct non-wildcard
	// hostname that was validated
	if !strings.Contains(resultLog[0], `"hostname":"good-dns01.com"`) {
		t.Errorf("PerformValidation didn't log correct validation record hostname.")
	}
}

func TestDNSValidationFailure(t *testing.T) {
	va, _ := setup(nil, 0)

	chalDNS := createChallenge(core.ChallengeTypeDNS01)

	_, prob := va.validateChallenge(ctx, dnsi("localhost"), chalDNS)

	test.AssertEquals(t, prob.Type, probs.UnauthorizedProblem)
}

func TestDNSValidationInvalid(t *testing.T) {
	var notDNS = core.AcmeIdentifier{
		Type:  core.IdentifierType("iris"),
		Value: "790DB180-A274-47A4-855F-31C428CB1072",
	}

	chalDNS := core.DNSChallenge01("")
	chalDNS.ProvidedKeyAuthorization = expectedKeyAuthorization

	va, _ := setup(nil, 0)

	_, prob := va.validateChallenge(ctx, notDNS, chalDNS)

	test.AssertEquals(t, prob.Type, probs.MalformedProblem)
}

func TestDNSValidationNotSane(t *testing.T) {
	va, _ := setup(nil, 0)

	chal0 := core.DNSChallenge01("")
	chal0.Token = ""

	chal1 := core.DNSChallenge01("")
	chal1.Token = "yfCBb-bRTLz8Wd1C0lTUQK3qlKj3-t2tYGwx5Hj7r_"

	chal2 := core.DNSChallenge01("")
	chal2.ProvidedKeyAuthorization = "a"

	var authz = core.Authorization{
		ID:             core.NewToken(),
		RegistrationID: 1,
		Identifier:     dnsi("localhost"),
		Challenges:     []core.Challenge{chal0, chal1, chal2},
	}

	for i := 0; i < len(authz.Challenges); i++ {
		_, prob := va.validateChallenge(ctx, dnsi("localhost"), authz.Challenges[i])
		if prob.Type != probs.MalformedProblem {
			t.Errorf("Got wrong error type for %d: expected %s, got %s",
				i, prob.Type, probs.MalformedProblem)
		}
		if !strings.Contains(prob.Error(), "Challenge failed consistency check:") {
			t.Errorf("Got wrong error: %s", prob.Error())
		}
	}
}

func TestDNSValidationServFail(t *testing.T) {
	va, _ := setup(nil, 0)

	chalDNS := createChallenge(core.ChallengeTypeDNS01)

	_, prob := va.validateChallenge(ctx, dnsi("servfail.com"), chalDNS)

	test.AssertEquals(t, prob.Type, probs.DNSProblem)
}

func TestDNSValidationNoServer(t *testing.T) {
	va, _ := setup(nil, 0)
	va.dnsClient = bdns.NewTestDNSClientImpl(
		time.Second*5,
		nil,
		metrics.NewNoopScope(),
		clock.Default(),
		1)

	chalDNS := createChallenge(core.ChallengeTypeDNS01)

	_, prob := va.validateChallenge(ctx, dnsi("localhost"), chalDNS)

	test.AssertEquals(t, prob.Type, probs.DNSProblem)
}

func TestDNSValidationOK(t *testing.T) {
	va, _ := setup(nil, 0)

	// create a challenge with well known token
	chalDNS := core.DNSChallenge01("")
	chalDNS.Token = expectedToken
	chalDNS.ProvidedKeyAuthorization = expectedKeyAuthorization

	_, prob := va.validateChallenge(ctx, dnsi("good-dns01.com"), chalDNS)

	test.Assert(t, prob == nil, "Should be valid.")
}

func TestDNSValidationNoAuthorityOK(t *testing.T) {
	va, _ := setup(nil, 0)

	// create a challenge with well known token
	chalDNS := core.DNSChallenge01("")
	chalDNS.Token = expectedToken

	chalDNS.ProvidedKeyAuthorization = expectedKeyAuthorization

	_, prob := va.validateChallenge(ctx, dnsi("no-authority-dns01.com"), chalDNS)

	test.Assert(t, prob == nil, "Should be valid.")
}

func TestLimitedReader(t *testing.T) {
	chall := core.HTTPChallenge01("")
	setChallengeToken(&chall, core.NewToken())

	hs := httpSrv(t, "012345\xff67890123456789012345678901234567890123456789012345678901234567890123456789")
	va, _ := setup(hs, 0)
	defer hs.Close()

	_, prob := va.validateChallenge(ctx, dnsi("localhost"), chall)

	test.AssertEquals(t, prob.Type, probs.UnauthorizedProblem)
	test.Assert(t, strings.HasPrefix(prob.Detail, "Invalid response from "),
		"Expected failure due to truncation")

	if !utf8.ValidString(prob.Detail) {
		t.Errorf("Problem Detail contained an invalid UTF-8 string")
	}
}

func setup(srv *httptest.Server, maxRemoteFailures int) (*ValidationAuthorityImpl, *blog.Mock) {
	logger := blog.NewMock()

	var portConfig cmd.PortConfig
	if srv != nil {
		port := getPort(srv)
		portConfig = cmd.PortConfig{
			HTTPPort: port,
			TLSPort:  port,
		}
	}
	va, err := NewValidationAuthorityImpl(
		// Use the test server's port as both the HTTPPort and the TLSPort for the VA
		&portConfig,
		&bdns.MockDNSClient{},
		nil,
		maxRemoteFailures,
		"user agent 1.0",
		"letsencrypt.org",
		metrics.NewNoopScope(),
		clock.Default(),
		logger,
		accountURIPrefixes)
	if err != nil {
		panic(fmt.Sprintf("Failed to create validation authority: %v", err))
	}
	return va, logger
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

type multiSrv struct {
	*httptest.Server

	mu         sync.Mutex
	allowedUAs map[string]struct{}
}

func httpMultiSrv(t *testing.T, token string, allowedUAs map[string]struct{}) *multiSrv {
	m := http.NewServeMux()

	server := httptest.NewUnstartedServer(m)
	ms := &multiSrv{server, sync.Mutex{}, allowedUAs}

	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.UserAgent() == "slow remote" {
			time.Sleep(time.Second * 5)
		}
		ms.mu.Lock()
		defer ms.mu.Unlock()
		if _, ok := ms.allowedUAs[r.UserAgent()]; ok {
			ch := core.Challenge{Token: token}
			keyAuthz, _ := ch.ExpectedKeyAuthorization(accountKey)
			t.Logf("HTTPSRV: Key Authz = '%s%s'\n", keyAuthz, "\\n\\r \\t")
			fmt.Fprint(w, keyAuthz, "\n\r \t")
		} else {
			fmt.Fprint(w, "???")
		}
	})

	ms.Start()
	return ms
}

// cancelledVA is a mock that always returns context.Canceled for
// PerformValidation calls
type cancelledVA struct{}

func (v cancelledVA) PerformValidation(_ context.Context, _ string, _ core.Challenge, _ core.Authorization) ([]core.ValidationRecord, error) {
	return nil, context.Canceled
}

func TestPerformRemoteValidation(t *testing.T) {
	// Create a new challenge to use for the httpSrv
	chall := core.HTTPChallenge01("")
	setChallengeToken(&chall, core.NewToken())

	// Create an IPv4 test server
	ms := httpMultiSrv(t, chall.Token, map[string]struct{}{"remote 1": {}, "remote 2": {}})
	// defer ms.Close()

	// Create a local test VA and two 'remote' VAs
	localVA, mockLog := setup(ms.Server, 0)
	localVA.userAgent = "local"
	remoteVA1, _ := setup(ms.Server, 0)
	remoteVA1.userAgent = "remote 1"
	remoteVA2, _ := setup(ms.Server, 0)
	remoteVA2.userAgent = "remote 2"
	localVA.remoteVAs = []RemoteVA{
		{remoteVA1, "remote 1"},
		{remoteVA2, "remote 2"},
	}

	// Both remotes working, should succeed
	probCh := make(chan *probs.ProblemDetails, 1)
	localVA.performRemoteValidation(ctx, "localhost", chall, core.Authorization{}, probCh)
	prob := <-probCh
	if prob != nil {
		t.Errorf("performRemoteValidation failed: %s", prob)
	}

	// Only remote 1 working, should fail
	ms.mu.Lock()
	delete(ms.allowedUAs, "remote 1")
	ms.mu.Unlock()
	localVA.performRemoteValidation(ctx, "localhost", chall, core.Authorization{}, probCh)
	prob = <-probCh
	if prob == nil {
		t.Error("performRemoteValidation didn't fail when one 'remote' validation failed")
	}

	ms.mu.Lock()
	ms.allowedUAs["local"] = struct{}{}
	ms.allowedUAs["remote 1"] = struct{}{}
	ms.allowedUAs["remote 2"] = struct{}{}
	ms.mu.Unlock()

	localVA.remoteVAs = []RemoteVA{
		{remoteVA1, "remote 1"},
		{cancelledVA{}, "remote 2"},
	}

	// One remote cancelled, should return no err
	localVA.performRemoteValidation(ctx, "localhost", chall, core.Authorization{}, probCh)
	prob = <-probCh
	if prob != nil {
		t.Errorf("performRemoteValidation returned unexpected err from cancelled context: %s", prob)
	}

	localVA.remoteVAs = []RemoteVA{
		{cancelledVA{}, "remote 1"},
		{cancelledVA{}, "remote 2"},
	}

	// Both remotes cancelled, should return no err
	localVA.performRemoteValidation(ctx, "localhost", chall, core.Authorization{}, probCh)
	prob = <-probCh
	if prob != nil {
		t.Errorf("performRemoteValidation returned unexpected err from cancelled context: %s", prob)
	}

	localVA.remoteVAs = []RemoteVA{
		{remoteVA1, "remote 1"},
		{remoteVA2, "remote 2"},
	}

	// Both local and remotes working, should succeed
	_, err := localVA.PerformValidation(ctx, "localhost", chall, core.Authorization{})
	if err != nil {
		t.Errorf("PerformValidation failed: %s", err)
	}

	// Only remotes working, should fail
	ms.mu.Lock()
	delete(ms.allowedUAs, "local")
	ms.mu.Unlock()
	_, err = localVA.PerformValidation(ctx, "localhost", chall, core.Authorization{})
	if err == nil {
		t.Error("PerformValidation didn't fail when local validation failed")
	}

	// Local and remote 2 working, should fail
	ms.mu.Lock()
	ms.allowedUAs["local"] = struct{}{}
	delete(ms.allowedUAs, "remote 1")
	ms.mu.Unlock()
	_, err = localVA.PerformValidation(ctx, "localhost", chall, core.Authorization{})
	if err == nil {
		t.Error("PerformValidation didn't fail when one 'remote' validation failed")
	}
	failLogs := mockLog.GetAllMatching(`Validation failed due to remote failures`)
	if len(failLogs) == 0 {
		t.Error("Expected log line about failure due to remote failures, didn't get it")
	}
	remoteFailMetric := test.CountCounter(localVA.metrics.remoteValidationFailures)
	if remoteFailMetric != 1 {
		t.Errorf("Expected remote_validation_failures to be incremented, but it wasn't")
	}

	// Local and remote 2 working with maxRemoteFailures == 1, should succeed
	localVA, _ = setup(ms.Server, 1)
	localVA.userAgent = "local"
	localVA.remoteVAs = []RemoteVA{
		{remoteVA1, "remote 1"},
		{remoteVA2, "remote 2"},
	}
	_, err = localVA.PerformValidation(ctx, "localhost", chall, core.Authorization{})
	if err != nil {
		t.Errorf("PerformValidation failed when one 'remote' validation failed but maxRemoteFailures is 1: %s", err)
	}

	// Only local working, should fail
	ms.mu.Lock()
	delete(ms.allowedUAs, "remote 2")
	ms.mu.Unlock()
	_, err = localVA.PerformValidation(ctx, "localhost", chall, core.Authorization{})
	if err == nil {
		t.Error("PerformValidation didn't fail when both 'remote' validations failed")
	}

	// Local and remote 1 working, should succeed and return early
	ms.mu.Lock()
	ms.allowedUAs["remote 1"] = struct{}{}
	ms.mu.Unlock()
	remoteVA2.userAgent = "slow remote"
	s := time.Now()
	_, err = localVA.PerformValidation(ctx, "localhost", chall, core.Authorization{})
	if err != nil {
		t.Errorf("PerformValidation failed when one 'remote' validation failed but maxRemoteFailures is 1: %s", err)
	}
	took := time.Since(s)
	if took >= (time.Second * 5) {
		t.Errorf("PerformValidation didn't return early on success: took %s, expected <5s", took)
	}

	// Only local working, should fail and return early
	ms.mu.Lock()
	delete(ms.allowedUAs, "remote 1")
	ms.mu.Unlock()
	localVA, _ = setup(ms.Server, 0)
	localVA.userAgent = "local"
	localVA.remoteVAs = []RemoteVA{
		{remoteVA1, "remote 1"},
		{remoteVA2, "remote 2"},
	}
	s = time.Now()
	_, err = localVA.PerformValidation(ctx, "localhost", chall, core.Authorization{})
	if err == nil {
		t.Error("PerformValidation didn't fail when two validations failed")
	}
	took = time.Since(s)
	if took >= (time.Second * 5) {
		t.Errorf("PerformValidation didn't return early on failure: took %s, expected <5s", took)
	}
}

// brokenRemoteVA is a mock for the core.ValidationAuthority interface mocked to
// always return errors.
type brokenRemoteVA struct{}

// brokenRemoteVAError is the error returned by a brokenRemoteVA's
// PerformValidation function.
var brokenRemoteVAError = errors.New("brokenRemoteVA is broken")

// PerformValidation returns brokenRemoteVAError unconditionally
func (b *brokenRemoteVA) PerformValidation(
	_ context.Context,
	_ string,
	_ core.Challenge,
	_ core.Authorization) ([]core.ValidationRecord, error) {
	return nil, brokenRemoteVAError
}

func TestPerformRemoteValidationFailure(t *testing.T) {
	// Create a new challenge to use for the httpSrv
	chall := core.HTTPChallenge01("")
	setChallengeToken(&chall, core.NewToken())

	// Create an IPv4 test server
	ms := httpMultiSrv(t, chall.Token, map[string]struct{}{"remote 1": {}, "remote 2": {}})
	defer ms.Close()

	// Create a local test VA configured with a mock logger
	mockLog := blog.NewMock()
	localVA, _ := setup(ms.Server, 0)
	localVA.userAgent = "local"
	localVA.log = mockLog

	// Create a remote test VA
	remoteVA, _ := setup(ms.Server, 0)

	// Create a broken remote test VA
	brokenVA := &brokenRemoteVA{}
	brokenVAAddr := "broken"

	// Set the local VA to use the two remotes
	localVA.remoteVAs = []RemoteVA{
		{remoteVA, "good"},
		{brokenVA, brokenVAAddr},
	}

	// Performing a validation should return a problem on the channel because of
	// the broken remote VA.
	probCh := make(chan *probs.ProblemDetails, 1)
	localVA.performRemoteValidation(
		ctx,
		"localhost",
		chall,
		core.Authorization{},
		probCh)
	prob := <-probCh
	if prob == nil {
		t.Fatalf("performRemoteValidation with a broken remote VA did not " +
			"return a problem")
	}

	expected := probs.ServerInternal("Remote PerformValidation RPCs failed")
	test.AssertDeepEquals(t, prob, expected)

	// The mock logger should have an audit err log line that includes the true
	// underlying error that caused the server internal problem.
	expectedLine := fmt.Sprintf(
		`ERR: \[AUDIT\] Remote VA %q.PerformValidation failed: %s`,
		brokenVAAddr, brokenRemoteVAError.Error())
	failLogs := mockLog.GetAllMatching(expectedLine)
	if len(failLogs) == 0 {
		t.Error("Expected log line with broken remote VA error message. Found none")
	}
}

func TestDetailedError(t *testing.T) {
	cases := []struct {
		err      error
		expected string
	}{
		{
			&net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: &os.SyscallError{
					Syscall: "getsockopt",
					Err:     syscall.ECONNREFUSED,
				},
			},
			"Connection refused",
		},
		{
			&net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: &os.SyscallError{
					Syscall: "getsockopt",
					Err:     syscall.ECONNRESET,
				},
			},
			"Connection reset by peer",
		},
	}
	for _, tc := range cases {
		actual := detailedError(tc.err).Detail
		if actual != tc.expected {
			t.Errorf("Wrong detail for %v. Got %q, expected %q", tc.err, actual, tc.expected)
		}
	}
}
