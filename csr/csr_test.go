package csr

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/letsencrypt/boulder/core"
	"github.com/letsencrypt/boulder/goodkey"
	"github.com/letsencrypt/boulder/identifier"
	"github.com/letsencrypt/boulder/test"
)

var testingPolicy = &goodkey.KeyPolicy{
	AllowRSA:           true,
	AllowECDSANISTP256: true,
	AllowECDSANISTP384: true,
}

type mockPA struct{}

func (pa *mockPA) ChallengesFor(identifier identifier.ACMEIdentifier) (challenges []core.Challenge, err error) {
	return
}

func (pa *mockPA) WillingToIssue(id identifier.ACMEIdentifier) error {
	return nil
}

func (pa *mockPA) WillingToIssueWildcard(id identifier.ACMEIdentifier) error {
	if id.Value == "bad-name.com" || id.Value == "other-bad-name.com" {
		return errors.New("")
	}
	return nil
}

func (pa *mockPA) ChallengeTypeEnabled(t string) bool {
	return true
}

func TestVerifyCSR(t *testing.T) {
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	test.AssertNotError(t, err, "error generating test key")
	signedReqBytes, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{PublicKey: private.PublicKey, SignatureAlgorithm: x509.SHA256WithRSA}, private)
	test.AssertNotError(t, err, "error generating test CSR")
	signedReq, err := x509.ParseCertificateRequest(signedReqBytes)
	test.AssertNotError(t, err, "error parsing test CSR")
	brokenSignedReq := new(x509.CertificateRequest)
	*brokenSignedReq = *signedReq
	brokenSignedReq.Signature = []byte{1, 1, 1, 1}
	signedReqWithHosts := new(x509.CertificateRequest)
	*signedReqWithHosts = *signedReq
	signedReqWithHosts.DNSNames = []string{"a.com", "b.com"}
	signedReqWithLongCN := new(x509.CertificateRequest)
	*signedReqWithLongCN = *signedReq
	signedReqWithLongCN.Subject.CommonName = strings.Repeat("a", maxCNLength+1)
	signedReqWithBadNames := new(x509.CertificateRequest)
	*signedReqWithBadNames = *signedReq
	signedReqWithBadNames.DNSNames = []string{"bad-name.com", "other-bad-name.com"}
	signedReqWithEmailAddress := new(x509.CertificateRequest)
	*signedReqWithEmailAddress = *signedReq
	signedReqWithEmailAddress.EmailAddresses = []string{"foo@bar.com"}
	signedReqWithIPAddress := new(x509.CertificateRequest)
	*signedReqWithIPAddress = *signedReq
	signedReqWithIPAddress.IPAddresses = []net.IP{net.IPv4(1, 2, 3, 4)}

	cases := []struct {
		csr           *x509.CertificateRequest
		maxNames      int
		keyPolicy     *goodkey.KeyPolicy
		pa            core.PolicyAuthority
		regID         int64
		expectedError error
	}{
		{
			&x509.CertificateRequest{},
			100,
			testingPolicy,
			&mockPA{},
			0,
			invalidPubKey,
		},
		{
			&x509.CertificateRequest{PublicKey: private.PublicKey},
			100,
			testingPolicy,
			&mockPA{},
			0,
			unsupportedSigAlg,
		},
		{
			brokenSignedReq,
			100,
			testingPolicy,
			&mockPA{},
			0,
			invalidSig,
		},
		{
			signedReq,
			100,
			testingPolicy,
			&mockPA{},
			0,
			invalidNoDNS,
		},
		{
			signedReqWithLongCN,
			100,
			testingPolicy,
			&mockPA{},
			0,
			errors.New("CN was longer than 64 bytes"),
		},
		{
			signedReqWithHosts,
			1,
			testingPolicy,
			&mockPA{},
			0,
			errors.New("CSR contains more than 1 DNS names"),
		},
		{
			signedReqWithBadNames,
			100,
			testingPolicy,
			&mockPA{},
			0,
			errors.New("policy forbids issuing for: \"bad-name.com\", \"other-bad-name.com\""),
		},
		{
			signedReqWithEmailAddress,
			100,
			testingPolicy,
			&mockPA{},
			0,
			invalidEmailPresent,
		},
		{
			signedReqWithIPAddress,
			100,
			testingPolicy,
			&mockPA{},
			0,
			invalidIPPresent,
		},
	}

	for _, c := range cases {
		err := VerifyCSR(c.csr, c.maxNames, c.keyPolicy, c.pa, false, c.regID)
		test.AssertDeepEquals(t, c.expectedError, err)
	}
}

func TestNormalizeCSR(t *testing.T) {
	cases := []struct {
		csr           *x509.CertificateRequest
		forceCN       bool
		expectedCN    string
		expectedNames []string
	}{
		{
			&x509.CertificateRequest{DNSNames: []string{"a.com"}},
			true,
			"a.com",
			[]string{"a.com"},
		},
		{
			&x509.CertificateRequest{Subject: pkix.Name{CommonName: "A.com"}, DNSNames: []string{"a.com"}},
			true,
			"a.com",
			[]string{"a.com"},
		},
		{
			&x509.CertificateRequest{DNSNames: []string{"a.com"}},
			false,
			"",
			[]string{"a.com"},
		},
		{
			&x509.CertificateRequest{DNSNames: []string{"a.com", "a.com"}},
			false,
			"",
			[]string{"a.com"},
		},
		{
			&x509.CertificateRequest{Subject: pkix.Name{CommonName: "A.com"}, DNSNames: []string{"B.com"}},
			false,
			"a.com",
			[]string{"a.com", "b.com"},
		},
	}
	for _, c := range cases {
		normalizeCSR(c.csr, c.forceCN)
		test.AssertEquals(t, c.expectedCN, c.csr.Subject.CommonName)
		test.AssertDeepEquals(t, c.expectedNames, c.csr.DNSNames)
	}
}
