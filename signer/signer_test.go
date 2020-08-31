package signer

import (
	"crypto"
	"crypto/dsa"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"os"
	"testing"
	"time"

	ct "github.com/google/certificate-transparency-go"
	"github.com/jmhodges/clock"
	"github.com/letsencrypt/boulder/cmd"
	"github.com/letsencrypt/boulder/policyasn1"
	"github.com/letsencrypt/boulder/test"
)

func defaultProfileConfig() ProfileConfig {
	return ProfileConfig{
		AllowECDSAKeys:  true,
		AllowRSAKeys:    true,
		AllowCommonName: true,
		AllowCTPoison:   true,
		AllowSCTList:    true,
		AllowMustStaple: true,
		IssuerURL:       "http://issuer-url",
		OCSPURL:         "http://ocsp-url",
		Policies: []PolicyInformation{
			{OID: "1.2.3"},
		},
		MaxValidityPeriod:   cmd.ConfigDuration{Duration: time.Hour},
		MaxValidityBackdate: cmd.ConfigDuration{Duration: time.Hour},
	}
}

func TestNewProfilePolicies(t *testing.T) {
	config := defaultProfileConfig()
	config.Policies = append(config.Policies, PolicyInformation{
		OID: "1.2.3.4",
		Qualifiers: []PolicyQualifier{
			{
				Type:  "id-qt-cps",
				Value: "cps-url",
			},
		},
	})
	profile, err := newProfile(config)
	test.AssertNotError(t, err, "newProfile failed")
	test.AssertDeepEquals(t, *profile, signingProfile{
		allowRSAKeys:    true,
		allowECDSAKeys:  true,
		allowMustStaple: true,
		allowCTPoison:   true,
		allowSCTList:    true,
		allowCommonName: true,
		issuerURL:       "http://issuer-url",
		ocspURL:         "http://ocsp-url",
		policies: &pkix.Extension{
			Id:    asn1.ObjectIdentifier{2, 5, 29, 32},
			Value: []byte{48, 36, 48, 4, 6, 2, 42, 3, 48, 28, 6, 3, 42, 3, 4, 48, 21, 48, 19, 6, 8, 43, 6, 1, 5, 5, 7, 2, 1, 22, 7, 99, 112, 115, 45, 117, 114, 108},
		},
		maxBackdate: time.Hour,
		maxValidity: time.Hour,
	})
	var policies []policyasn1.PolicyInformation
	_, err = asn1.Unmarshal(profile.policies.Value, &policies)
	test.AssertNotError(t, err, "failed to parse policies extension")
	test.AssertEquals(t, len(policies), 2)
	test.AssertDeepEquals(t, policies[0], policyasn1.PolicyInformation{
		Policy: asn1.ObjectIdentifier{1, 2, 3},
	})
	test.AssertDeepEquals(t, policies[1], policyasn1.PolicyInformation{
		Policy: asn1.ObjectIdentifier{1, 2, 3, 4},
		Qualifiers: []policyasn1.PolicyQualifier{{
			OID:   asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 2, 1},
			Value: "cps-url",
		}},
	})
}

func TestNewProfileNoIssuerURL(t *testing.T) {
	_, err := newProfile(ProfileConfig{})
	test.AssertError(t, err, "newProfile didn't fail with no issuer URL")
	test.AssertEquals(t, err.Error(), "Issuer URL is required")
}

func TestNewProfileNoOCSPURL(t *testing.T) {
	_, err := newProfile(ProfileConfig{IssuerURL: "issuer-url"})
	test.AssertError(t, err, "newProfile didn't fail with no OCSP URL")
	test.AssertEquals(t, err.Error(), "OCSP URL is required")
}

func TestNewProfileInvalidOID(t *testing.T) {
	_, err := newProfile(ProfileConfig{
		IssuerURL: "issuer-url",
		OCSPURL:   "ocsp-url",
		Policies: []PolicyInformation{{
			OID: "a.b.c",
		}},
	})
	test.AssertError(t, err, "newProfile didn't fail with unknown policy qualifier type")
	test.AssertEquals(t, err.Error(), "failed parsing policy OID \"a.b.c\": strconv.Atoi: parsing \"a\": invalid syntax")
}

func TestNewProfileUnknownQualifierType(t *testing.T) {
	_, err := newProfile(ProfileConfig{
		IssuerURL: "issuer-url",
		OCSPURL:   "ocsp-url",
		Policies: []PolicyInformation{{
			OID: "1.2.3",
			Qualifiers: []PolicyQualifier{{
				Type:  "asd",
				Value: "bad",
			}},
		}},
	})
	test.AssertError(t, err, "newProfile didn't fail with unknown policy qualifier type")
	test.AssertEquals(t, err.Error(), "unknown qualifier type: asd")
}

func TestRequestValid(t *testing.T) {
	fc := clock.NewFake()
	fc.Add(time.Hour * 24)
	tests := []struct {
		name          string
		profile       *signingProfile
		request       *IssuanceRequest
		expectedError string
	}{
		{
			name:          "unsupported key type",
			profile:       &signingProfile{},
			request:       &IssuanceRequest{PublicKey: &dsa.PublicKey{}},
			expectedError: "unsupported public key type",
		},
		{
			name:          "rsa keys not allowed",
			profile:       &signingProfile{},
			request:       &IssuanceRequest{PublicKey: &rsa.PublicKey{}},
			expectedError: "RSA keys not allowed",
		},
		{
			name:          "ecdsa keys not allowed",
			profile:       &signingProfile{},
			request:       &IssuanceRequest{PublicKey: &ecdsa.PublicKey{}},
			expectedError: "ECDSA keys not allowed",
		},
		{
			name: "must staple not allowed",
			profile: &signingProfile{
				allowECDSAKeys: true,
			},
			request: &IssuanceRequest{
				PublicKey:         &ecdsa.PublicKey{},
				IncludeMustStaple: true,
			},
			expectedError: "must-staple extension cannot be included",
		},
		{
			name: "ct poison not allowed",
			profile: &signingProfile{
				allowECDSAKeys: true,
			},
			request: &IssuanceRequest{
				PublicKey:       &ecdsa.PublicKey{},
				IncludeCTPoison: true,
			},
			expectedError: "ct poison extension cannot be included",
		},
		{
			name: "sct list not allowed",
			profile: &signingProfile{
				allowECDSAKeys: true,
			},
			request: &IssuanceRequest{
				PublicKey: &ecdsa.PublicKey{},
				SCTList:   []ct.SignedCertificateTimestamp{},
			},
			expectedError: "sct list extension cannot be included",
		},
		{
			name: "sct list and ct poison not allowed",
			profile: &signingProfile{
				allowECDSAKeys: true,
				allowCTPoison:  true,
				allowSCTList:   true,
			},
			request: &IssuanceRequest{
				PublicKey:       &ecdsa.PublicKey{},
				IncludeCTPoison: true,
				SCTList:         []ct.SignedCertificateTimestamp{},
			},
			expectedError: "cannot include both ct poison and sct list extensions",
		},
		{
			name: "common name not allowed",
			profile: &signingProfile{
				allowECDSAKeys: true,
			},
			request: &IssuanceRequest{
				PublicKey:  &ecdsa.PublicKey{},
				CommonName: "cn",
			},
			expectedError: "common name cannot be included",
		},
		{
			name: "negative validity",
			profile: &signingProfile{
				allowECDSAKeys: true,
			},
			request: &IssuanceRequest{
				PublicKey: &ecdsa.PublicKey{},
				NotBefore: fc.Now().Add(time.Hour),
				NotAfter:  fc.Now(),
			},
			expectedError: "NotAfter must be after NotBefore",
		},
		{
			name: "validity larger than max",
			profile: &signingProfile{
				allowECDSAKeys: true,
				maxValidity:    time.Minute,
			},
			request: &IssuanceRequest{
				PublicKey: &ecdsa.PublicKey{},
				NotBefore: fc.Now(),
				NotAfter:  fc.Now().Add(time.Hour),
			},
			expectedError: "validity period is more than the maximum allowed period (1h0m0s>1m0s)",
		},
		{
			name: "validity backdated more than max",
			profile: &signingProfile{
				allowECDSAKeys: true,
				maxValidity:    time.Hour * 2,
				maxBackdate:    time.Hour,
			},
			request: &IssuanceRequest{
				PublicKey: &ecdsa.PublicKey{},
				NotBefore: fc.Now().Add(-time.Hour * 2),
				NotAfter:  fc.Now().Add(-time.Hour),
			},
			expectedError: "NotBefore is backdated more than the maximum allowed period (2h0m0s>1h0m0s)",
		},
		{
			name: "validity is forward dated",
			profile: &signingProfile{
				allowECDSAKeys: true,
				maxValidity:    time.Hour * 2,
				maxBackdate:    time.Hour,
			},
			request: &IssuanceRequest{
				PublicKey: &ecdsa.PublicKey{},
				NotBefore: fc.Now().Add(time.Hour),
				NotAfter:  fc.Now().Add(time.Hour * 2),
			},
			expectedError: "NotBefore is in the future",
		},
		{
			name: "serial too short",
			profile: &signingProfile{
				allowECDSAKeys: true,
				maxValidity:    time.Hour * 2,
			},
			request: &IssuanceRequest{
				PublicKey: &ecdsa.PublicKey{},
				NotBefore: fc.Now(),
				NotAfter:  fc.Now().Add(time.Hour),
			},
			expectedError: "serial must be between 8 and 20 bytes",
		},
		{
			name: "serial too long",
			profile: &signingProfile{
				allowECDSAKeys: true,
				maxValidity:    time.Hour * 2,
			},
			request: &IssuanceRequest{
				PublicKey: &ecdsa.PublicKey{},
				NotBefore: fc.Now(),
				NotAfter:  fc.Now().Add(time.Hour),
				Serial:    []byte{1, 2, 3, 4, 5, 6, 7, 8, 8, 9, 1, 2, 3, 4, 5, 6, 7, 8, 8, 9, 0},
			},
			expectedError: "serial must be between 8 and 20 bytes",
		},
		{
			name: "good",
			profile: &signingProfile{
				allowECDSAKeys: true,
				maxValidity:    time.Hour * 2,
			},
			request: &IssuanceRequest{
				PublicKey: &ecdsa.PublicKey{},
				NotBefore: fc.Now(),
				NotAfter:  fc.Now().Add(time.Hour),
				Serial:    []byte{1, 2, 3, 4, 5, 6, 7, 8},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.profile.requestValid(fc, tc.request)
			if err != nil {
				if tc.expectedError == "" {
					t.Errorf("failed with unexpected error: %s", err)
				} else if tc.expectedError != err.Error() {
					t.Errorf("failed with unexpected error, wanted: %q, got: %q", tc.expectedError, err.Error())
				}
				return
			} else if tc.expectedError != "" {
				t.Errorf("didn't fail, expected %q", tc.expectedError)
			}
		})
	}
}

func TestGenerateTemplate(t *testing.T) {
	tests := []struct {
		name             string
		profile          *signingProfile
		expectedTemplate *x509.Certificate
	}{
		{
			name: "crl url",
			profile: &signingProfile{
				crlURL: "crl-url",
				sigAlg: x509.SHA256WithRSA,
			},
			expectedTemplate: &x509.Certificate{
				BasicConstraintsValid: true,
				SignatureAlgorithm:    x509.SHA256WithRSA,
				ExtKeyUsage:           defaultEKU,
				IssuingCertificateURL: []string{""},
				OCSPServer:            []string{""},
				CRLDistributionPoints: []string{"crl-url"},
			},
		},
		{
			name: "include policies",
			profile: &signingProfile{
				sigAlg: x509.SHA256WithRSA,
				policies: &pkix.Extension{
					Id:    asn1.ObjectIdentifier{1, 2, 3},
					Value: []byte{4, 5, 6},
				},
			},
			expectedTemplate: &x509.Certificate{
				BasicConstraintsValid: true,
				SignatureAlgorithm:    x509.SHA256WithRSA,
				ExtKeyUsage:           defaultEKU,
				IssuingCertificateURL: []string{""},
				OCSPServer:            []string{""},
				ExtraExtensions: []pkix.Extension{
					{
						Id:    asn1.ObjectIdentifier{1, 2, 3},
						Value: []byte{4, 5, 6},
					},
				},
			},
		},
	}
	fc := clock.NewFake()
	fc.Set(time.Time{}.Add(time.Hour))
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			template := tc.profile.generateTemplate(fc)
			test.AssertDeepEquals(t, *template, *tc.expectedTemplate)
		})
	}
}

func TestNewSignerUnsupportedKeyType(t *testing.T) {
	_, err := NewSigner(Config{
		Profile: defaultProfileConfig(),
		Issuer: &x509.Certificate{
			PublicKey: &ed25519.PublicKey{},
		},
		Signer: &ed25519.PrivateKey{},
	})
	test.AssertError(t, err, "NewSigner didn't fail")
	test.AssertEquals(t, err.Error(), "unsupported issuer key type")
}

func TestNewSignerRSAKey(t *testing.T) {
	mod, ok := big.NewInt(0).SetString("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", 16)
	test.Assert(t, ok, "failed to set mod")
	signer, err := NewSigner(Config{
		Profile: defaultProfileConfig(),
		Issuer: &x509.Certificate{
			PublicKey: &rsa.PublicKey{
				N: mod,
			},
		},
		Signer: &rsa.PrivateKey{
			PublicKey: rsa.PublicKey{
				N: mod,
			},
		},
	})
	test.AssertNotError(t, err, "NewSigner failed")
	_, ok = signer.lintKey.(*rsa.PrivateKey)
	test.Assert(t, ok, "lint key is not RSA")
}

func TestNewSignerECDSAKey(t *testing.T) {
	signer, err := NewSigner(Config{
		Profile: defaultProfileConfig(),
		Issuer: &x509.Certificate{
			PublicKey: &ecdsa.PublicKey{
				Curve: elliptic.P256(),
			},
		},
		Signer: &ecdsa.PrivateKey{
			PublicKey: ecdsa.PublicKey{
				Curve: elliptic.P256(),
			},
		},
	})
	test.AssertNotError(t, err, "NewSigner failed")
	_, ok := signer.lintKey.(*ecdsa.PrivateKey)
	test.Assert(t, ok, "lint key is not ECDSA")
}

var issuerCert *x509.Certificate
var issuerSigner *ecdsa.PrivateKey

func TestMain(m *testing.M) {
	tk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cmd.FailOnError(err, "failed to generate test key")
	issuerSigner = tk
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(123),
		PublicKey:             tk.Public(),
		BasicConstraintsValid: true,
		IsCA:                  true,
		Subject: pkix.Name{
			CommonName: "big ca",
		},
		KeyUsage:     x509.KeyUsageCertSign,
		SubjectKeyId: []byte{1, 2, 3, 4, 5, 6, 7, 8},
	}
	issuer, err := x509.CreateCertificate(rand.Reader, template, template, tk.Public(), tk)
	cmd.FailOnError(err, "failed to generate test issuer")
	issuerCert, err = x509.ParseCertificate(issuer)
	cmd.FailOnError(err, "failed to parse test issuer")
	os.Exit(m.Run())
}

func TestIssue(t *testing.T) {
	for _, tc := range []struct {
		name         string
		generateFunc func() (crypto.Signer, error)
		ku           x509.KeyUsage
	}{
		{
			name: "RSA",
			generateFunc: func() (crypto.Signer, error) {
				return rsa.GenerateKey(rand.Reader, 2048)
			},
			ku: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		},
		{
			name: "ECDSA",
			generateFunc: func() (crypto.Signer, error) {
				return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			},
			ku: x509.KeyUsageDigitalSignature,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := clock.NewFake()
			fc.Set(time.Now())
			signer, err := NewSigner(Config{
				Issuer:       issuerCert,
				Signer:       issuerSigner,
				Clk:          fc,
				Profile:      defaultProfileConfig(),
				IgnoredLints: []string{"w_ct_sct_policy_count_unsatisfied", "n_subject_common_name_included"},
			})
			test.AssertNotError(t, err, "NewSigner failed")
			pk, err := tc.generateFunc()
			test.AssertNotError(t, err, "failed to generate test key")
			certBytes, err := signer.Issue(&IssuanceRequest{
				PublicKey:  pk.Public(),
				Serial:     []byte{1, 2, 3, 4, 5, 6, 7, 8},
				CommonName: "example.com",
				DNSNames:   []string{"example.com"},
				NotBefore:  fc.Now(),
				NotAfter:   fc.Now().Add(time.Hour),
			})
			test.AssertNotError(t, err, "Issue failed")
			cert, err := x509.ParseCertificate(certBytes)
			test.AssertNotError(t, err, "failed to parse certificate")
			err = cert.CheckSignatureFrom(issuerCert)
			test.AssertNotError(t, err, "signature validation failed")
			test.AssertDeepEquals(t, cert.DNSNames, []string{"example.com"})
			test.AssertEquals(t, cert.Subject.CommonName, "example.com")
			test.AssertByteEquals(t, cert.SerialNumber.Bytes(), []byte{1, 2, 3, 4, 5, 6, 7, 8})
			test.AssertDeepEquals(t, cert.PublicKey, pk.Public())
			test.AssertEquals(t, len(cert.Extensions), 8) // Constraints, KU, EKU, SKID, AKID, AIA, SAN, Policies
			test.AssertEquals(t, cert.KeyUsage, tc.ku)
		})
	}
}

func TestIssueRSA(t *testing.T) {
	fc := clock.NewFake()
	fc.Set(time.Now())
	signer, err := NewSigner(Config{
		Issuer:       issuerCert,
		Signer:       issuerSigner,
		Clk:          fc,
		Profile:      defaultProfileConfig(),
		IgnoredLints: []string{"w_ct_sct_policy_count_unsatisfied"},
	})
	test.AssertNotError(t, err, "NewSigner failed")
	pk, err := rsa.GenerateKey(rand.Reader, 2048)
	test.AssertNotError(t, err, "failed to generate test key")
	certBytes, err := signer.Issue(&IssuanceRequest{
		PublicKey: pk.Public(),
		Serial:    []byte{1, 2, 3, 4, 5, 6, 7, 8},
		DNSNames:  []string{"example.com"},
		NotBefore: fc.Now(),
		NotAfter:  fc.Now().Add(time.Hour),
	})
	test.AssertNotError(t, err, "Issue failed")
	cert, err := x509.ParseCertificate(certBytes)
	test.AssertNotError(t, err, "failed to parse certificate")
	err = cert.CheckSignatureFrom(issuerCert)
	test.AssertNotError(t, err, "signature validation failed")
	test.AssertByteEquals(t, cert.SerialNumber.Bytes(), []byte{1, 2, 3, 4, 5, 6, 7, 8})
	test.AssertDeepEquals(t, cert.PublicKey, pk.Public())
	test.AssertEquals(t, len(cert.Extensions), 8) // Constraints, KU, EKU, SKID, AKID, AIA, SAN, Policies
	test.AssertEquals(t, cert.KeyUsage, x509.KeyUsageDigitalSignature|x509.KeyUsageKeyEncipherment)
}

func TestIssueCTPoison(t *testing.T) {
	fc := clock.NewFake()
	fc.Set(time.Now())
	signer, err := NewSigner(Config{
		Issuer:       issuerCert,
		Signer:       issuerSigner,
		Clk:          fc,
		Profile:      defaultProfileConfig(),
		IgnoredLints: []string{"w_ct_sct_policy_count_unsatisfied"},
	})
	test.AssertNotError(t, err, "NewSigner failed")
	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	test.AssertNotError(t, err, "failed to generate test key")
	certBytes, err := signer.Issue(&IssuanceRequest{
		PublicKey:       pk.Public(),
		Serial:          []byte{1, 2, 3, 4, 5, 6, 7, 8},
		DNSNames:        []string{"example.com"},
		IncludeCTPoison: true,
		NotBefore:       fc.Now(),
		NotAfter:        fc.Now().Add(time.Hour),
	})
	test.AssertNotError(t, err, "Issue failed")
	cert, err := x509.ParseCertificate(certBytes)
	test.AssertNotError(t, err, "failed to parse certificate")
	err = cert.CheckSignatureFrom(issuerCert)
	test.AssertNotError(t, err, "signature validation failed")
	test.AssertByteEquals(t, cert.SerialNumber.Bytes(), []byte{1, 2, 3, 4, 5, 6, 7, 8})
	test.AssertDeepEquals(t, cert.PublicKey, pk.Public())
	test.AssertEquals(t, len(cert.Extensions), 9) // Constraints, KU, EKU, SKID, AKID, AIA, SAN, Policies, CT Poison
	test.AssertDeepEquals(t, cert.Extensions[8], ctPoisonExt)
}

func TestIssueSCTList(t *testing.T) {
	fc := clock.NewFake()
	fc.Set(time.Now())
	signer, err := NewSigner(Config{
		Issuer:       issuerCert,
		Signer:       issuerSigner,
		Clk:          fc,
		Profile:      defaultProfileConfig(),
		IgnoredLints: []string{"w_ct_sct_policy_count_unsatisfied"},
	})
	test.AssertNotError(t, err, "NewSigner failed")
	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	test.AssertNotError(t, err, "failed to generate test key")
	certBytes, err := signer.Issue(&IssuanceRequest{
		PublicKey: pk.Public(),
		Serial:    []byte{1, 2, 3, 4, 5, 6, 7, 8},
		DNSNames:  []string{"example.com"},
		SCTList: []ct.SignedCertificateTimestamp{
			{},
		},
		NotBefore: fc.Now(),
		NotAfter:  fc.Now().Add(time.Hour),
	})
	test.AssertNotError(t, err, "Issue failed")
	cert, err := x509.ParseCertificate(certBytes)
	test.AssertNotError(t, err, "failed to parse certificate")
	err = cert.CheckSignatureFrom(issuerCert)
	test.AssertNotError(t, err, "signature validation failed")
	test.AssertByteEquals(t, cert.SerialNumber.Bytes(), []byte{1, 2, 3, 4, 5, 6, 7, 8})
	test.AssertDeepEquals(t, cert.PublicKey, pk.Public())
	test.AssertEquals(t, len(cert.Extensions), 9) // Constraints, KU, EKU, SKID, AKID, AIA, SAN, Policies, SCT list
	test.AssertDeepEquals(t, cert.Extensions[8], pkix.Extension{
		Id:    sctListOID,
		Value: []byte{4, 51, 0, 49, 0, 47, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	})
}

func TestIssueMustStaple(t *testing.T) {
	fc := clock.NewFake()
	fc.Set(time.Now())
	signer, err := NewSigner(Config{
		Issuer:       issuerCert,
		Signer:       issuerSigner,
		Clk:          fc,
		Profile:      defaultProfileConfig(),
		IgnoredLints: []string{"w_ct_sct_policy_count_unsatisfied"},
	})
	test.AssertNotError(t, err, "NewSigner failed")
	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	test.AssertNotError(t, err, "failed to generate test key")
	certBytes, err := signer.Issue(&IssuanceRequest{
		PublicKey:         pk.Public(),
		Serial:            []byte{1, 2, 3, 4, 5, 6, 7, 8},
		DNSNames:          []string{"example.com"},
		IncludeMustStaple: true,
		NotBefore:         fc.Now(),
		NotAfter:          fc.Now().Add(time.Hour),
	})
	test.AssertNotError(t, err, "Issue failed")
	cert, err := x509.ParseCertificate(certBytes)
	test.AssertNotError(t, err, "failed to parse certificate")
	err = cert.CheckSignatureFrom(issuerCert)
	test.AssertNotError(t, err, "signature validation failed")
	test.AssertByteEquals(t, cert.SerialNumber.Bytes(), []byte{1, 2, 3, 4, 5, 6, 7, 8})
	test.AssertDeepEquals(t, cert.PublicKey, pk.Public())
	test.AssertEquals(t, len(cert.Extensions), 9) // Constraints, KU, EKU, SKID, AKID, AIA, SAN, Policies, Must-Staple
	test.AssertDeepEquals(t, cert.Extensions[8], mustStapleExt)
}

func TestIssueBadLint(t *testing.T) {
	fc := clock.NewFake()
	fc.Set(time.Now())
	signer, err := NewSigner(Config{
		Issuer:  issuerCert,
		Signer:  issuerSigner,
		Clk:     fc,
		Profile: defaultProfileConfig(),
	})
	test.AssertNotError(t, err, "NewSigner failed")
	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	test.AssertNotError(t, err, "failed to generate test key")
	_, err = signer.Issue(&IssuanceRequest{
		PublicKey: pk.Public(),
		Serial:    []byte{1, 2, 3, 4, 5, 6, 7, 8},
		DNSNames:  []string{"example.com"},
		NotBefore: fc.Now(),
		NotAfter:  fc.Now().Add(time.Hour),
	})
	test.AssertError(t, err, "Issue didn't fail")
	test.AssertEquals(t, err.Error(), "tbsCertificate linting failed: failed lints: w_ct_sct_policy_count_unsatisfied")
}
