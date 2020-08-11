package main

import (
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
	"time"
)

type policyInfoConfig struct {
	OID    string
	CPSURI string `yaml:"cps-uri"`
}

// certProfile contains the information required to generate a certificate
type certProfile struct {
	// SignatureAlgorithm should contain one of the allowed signature algorithms
	// in AllowedSigAlgs
	SignatureAlgorithm string `yaml:"signature-algorithm"`

	// CommonName should contain the requested subject common name
	CommonName string `yaml:"common-name"`
	// Organization should contain the requested subject organization
	Organization string `yaml:"organization"`
	// Country should contain the requested subject country code
	Country string `yaml:"country"`

	// NotBefore should contain the requested NotBefore date for the
	// certificate in the format "2006-01-02 15:04:05". Dates will
	// always be UTC.
	NotBefore string `yaml:"not-before"`
	// NotAfter should contain the requested NotAfter date for the
	// certificate in the format "2006-01-02 15:04:05". Dates will
	// always be UTC.
	NotAfter string `yaml:"not-after"`

	// OCSPURL should contain the URL at which a OCSP responder that
	// can respond to OCSP requests for this certificate operates
	OCSPURL string `yaml:"ocsp-url"`
	// CRLURL should contain the URL at which CRLs for this certificate
	// can be found
	CRLURL string `yaml:"crl-url"`
	// IssuerURL should contain the URL at which the issuing certificate
	// can be found, this is only required if generating an intermediate
	// certificate
	IssuerURL string `yaml:"issuer-url"`

	// PolicyOIDs should contain any OIDs to be inserted in a certificate
	// policies extension. If the CPSURI field of a policyInfoConfig element
	// is set it will result in a policyInformation structure containing a
	// single id-qt-cps type qualifier indicating the CPS URI.
	Policies []policyInfoConfig `yaml:"policies"`

	// KeyUsages should contain the set of key usage bits to set
	KeyUsages []string `yaml:"key-usages"`
}

// AllowedSigAlgs contains the allowed signature algorithms
var AllowedSigAlgs = map[string]x509.SignatureAlgorithm{
	"SHA256WithRSA":   x509.SHA256WithRSA,
	"SHA384WithRSA":   x509.SHA384WithRSA,
	"SHA512WithRSA":   x509.SHA512WithRSA,
	"ECDSAWithSHA256": x509.ECDSAWithSHA256,
	"ECDSAWithSHA384": x509.ECDSAWithSHA384,
	"ECDSAWithSHA512": x509.ECDSAWithSHA512,
}

type certType int

const (
	rootCert certType = iota
	intermediateCert
	ocspCert
	crlCert
)

func (profile *certProfile) verifyProfile(ct certType) error {
	if profile.NotBefore == "" {
		return errors.New("not-before is required")
	}
	if profile.NotAfter == "" {
		return errors.New("not-after is required")
	}
	if profile.SignatureAlgorithm == "" {
		return errors.New("signature-algorithm is required")
	}
	if profile.CommonName == "" {
		return errors.New("common-name is required")
	}
	if profile.Organization == "" {
		return errors.New("organization is required")
	}
	if profile.Country == "" {
		return errors.New("country is required")
	}

	if ct == intermediateCert {
		if profile.CRLURL == "" {
			return errors.New("crl-url is required for intermediates")
		}
		if profile.IssuerURL == "" {
			return errors.New("issuer-url is required for intermediates")
		}
	}

	if ct == ocspCert || ct == crlCert {
		if len(profile.KeyUsages) != 0 {
			return errors.New("key-usages cannot be set for a delegated signer")
		}
		if profile.CRLURL != "" {
			return errors.New("crl-url cannot be set for a delegated signer")
		}
		if profile.OCSPURL != "" {
			return errors.New("ocsp-url cannot be set for a delegated signer")
		}
	}
	return nil
}

func parseOID(oidStr string) (asn1.ObjectIdentifier, error) {
	var oid asn1.ObjectIdentifier
	for _, a := range strings.Split(oidStr, ".") {
		i, err := strconv.Atoi(a)
		if err != nil {
			return nil, err
		}
		oid = append(oid, i)
	}
	return oid, nil
}

var stringToKeyUsage = map[string]x509.KeyUsage{
	"Digital Signature": x509.KeyUsageDigitalSignature,
	"CRL Sign":          x509.KeyUsageCRLSign,
	"Cert Sign":         x509.KeyUsageCertSign,
}

type policyQualifier struct {
	Id    asn1.ObjectIdentifier
	Value string `asn1:"tag:optional,ia5"`
}

type policyInformation struct {
	Policy     asn1.ObjectIdentifier
	Qualifiers []policyQualifier `asn1:"tag:optional,omitempty"`
}

var (
	oidExtensionCertificatePolicies = asn1.ObjectIdentifier{2, 5, 29, 32}
	oidCPSQualifier                 = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 2, 1}

	oidOCSPNoCheck = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 48, 1, 5}
)

func buildPolicies(policies []policyInfoConfig) (pkix.Extension, error) {
	policyExt := pkix.Extension{Id: oidExtensionCertificatePolicies}
	var policyInfo []policyInformation
	for _, p := range policies {
		oid, err := parseOID(p.OID)
		if err != nil {
			return pkix.Extension{}, err
		}
		pi := policyInformation{Policy: oid}
		if p.CPSURI != "" {
			pi.Qualifiers = []policyQualifier{{Id: oidCPSQualifier, Value: p.CPSURI}}
		}
		policyInfo = append(policyInfo, pi)
	}
	v, err := asn1.Marshal(policyInfo)
	if err != nil {
		return pkix.Extension{}, err
	}
	policyExt.Value = v
	return policyExt, nil
}

func generateSKID(pk []byte) ([]byte, error) {
	var pkixPublicKey struct {
		Algo      pkix.AlgorithmIdentifier
		BitString asn1.BitString
	}
	if _, err := asn1.Unmarshal(pk, &pkixPublicKey); err != nil {
		return nil, err
	}
	skid := sha1.Sum(pkixPublicKey.BitString.Bytes)
	return skid[:], nil
}

// makeTemplate generates the certificate template for use in x509.CreateCertificate
func makeTemplate(randReader io.Reader, profile *certProfile, pubKey []byte, ct certType) (*x509.Certificate, error) {
	notBefore, err := time.Parse(configDateLayout, profile.NotBefore)
	if err != nil {
		return nil, err
	}
	notAfter, err := time.Parse(configDateLayout, profile.NotAfter)
	if err != nil {
		return nil, err
	}

	var ocspServer []string
	if profile.OCSPURL != "" {
		ocspServer = []string{profile.OCSPURL}
	}
	var crlDistributionPoints []string
	if profile.CRLURL != "" {
		crlDistributionPoints = []string{profile.CRLURL}
	}
	var issuingCertificateURL []string
	if profile.IssuerURL != "" {
		issuingCertificateURL = []string{profile.IssuerURL}
	}

	sigAlg, ok := AllowedSigAlgs[profile.SignatureAlgorithm]
	if !ok {
		return nil, fmt.Errorf("unsupported signature algorithm %q", profile.SignatureAlgorithm)
	}

	subjectKeyID, err := generateSKID(pubKey)
	if err != nil {
		return nil, err
	}

	serial := make([]byte, 16)
	_, err = randReader.Read(serial)
	if err != nil {
		return nil, fmt.Errorf("failed to generate serial number: %s", err)
	}

	var ku x509.KeyUsage
	for _, kuStr := range profile.KeyUsages {
		kuBit, ok := stringToKeyUsage[kuStr]
		if !ok {
			return nil, fmt.Errorf("unknown key usage %q", kuStr)
		}
		ku |= kuBit
	}
	if ct == ocspCert {
		ku = x509.KeyUsageDigitalSignature
	} else if ct == crlCert {
		ku = x509.KeyUsageCRLSign
	}
	if ku == 0 {
		return nil, errors.New("at least one key usage must be set")
	}

	cert := &x509.Certificate{
		SignatureAlgorithm:    sigAlg,
		SerialNumber:          big.NewInt(0).SetBytes(serial),
		BasicConstraintsValid: true,
		IsCA:                  true,
		Subject: pkix.Name{
			CommonName:   profile.CommonName,
			Organization: []string{profile.Organization},
			Country:      []string{profile.Country},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		OCSPServer:            ocspServer,
		CRLDistributionPoints: crlDistributionPoints,
		IssuingCertificateURL: issuingCertificateURL,
		KeyUsage:              ku,
		SubjectKeyId:          subjectKeyID,
	}

	if ct == ocspCert {
		cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageOCSPSigning}
		// ASN.1 NULL is 0x05, 0x00
		ocspNoCheckExt := pkix.Extension{Id: oidOCSPNoCheck, Value: []byte{5, 0}}
		cert.ExtraExtensions = append(cert.ExtraExtensions, ocspNoCheckExt)
		cert.IsCA = false
	} else if ct == crlCert {
		cert.IsCA = false
	} else if ct == intermediateCert {
		cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}
		cert.MaxPathLenZero = true
	}

	if len(profile.Policies) > 0 {
		policyExt, err := buildPolicies(profile.Policies)
		if err != nil {
			return nil, err
		}
		cert.ExtraExtensions = append(cert.ExtraExtensions, policyExt)
	}

	return cert, nil
}

// failReader exists to be passed to x509.CreateCertificate which requires
// a source of randomness for signing methods that require a source of
// randomness. Since HSM based signing will generate its own randomness
// we don't need a real reader. Instead of passing a nil reader we use one
// that always returns errors in case the internal usage of this reader
// changes.
type failReader struct{}

func (fr *failReader) Read([]byte) (int, error) {
	return 0, errors.New("Empty reader used by x509.CreateCertificate")
}
