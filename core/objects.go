// Copyright 2014 ISRG.  All rights reserved
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package core

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/letsencrypt/boulder/Godeps/_workspace/src/github.com/letsencrypt/go-jose"
)

// AcmeStatus defines the state of a given authorization
type AcmeStatus string

// AcmeResource values identify different types of ACME resources
type AcmeResource string

// Buffer is a variable-length collection of bytes
type Buffer []byte

// IdentifierType defines the available identification mechanisms for domains
type IdentifierType string

// OCSPStatus defines the state of OCSP for a domain
type OCSPStatus string

// ProblemType defines the error types in the ACME protocol
type ProblemType string

// ProblemDetails objects represent problem documents
// https://tools.ietf.org/html/draft-ietf-appsawg-http-problem-00
type ProblemDetails struct {
	Type   ProblemType `json:"type,omitempty"`
	Detail string      `json:"detail,omitempty"`
}

// These statuses are the states of authorizations
const (
	StatusUnknown    = AcmeStatus("unknown")    // Unknown status; the default
	StatusPending    = AcmeStatus("pending")    // In process; client has next action
	StatusProcessing = AcmeStatus("processing") // In process; server has next action
	StatusValid      = AcmeStatus("valid")      // Validation succeeded
	StatusInvalid    = AcmeStatus("invalid")    // Validation failed
	StatusRevoked    = AcmeStatus("revoked")    // Object no longer valid
)

// These types are the available identification mechanisms
const (
	IdentifierDNS = IdentifierType("dns")
)

// The types of ACME resources
const (
	ResourceNewReg       = AcmeResource("new-reg")
	ResourceNewAuthz     = AcmeResource("new-authz")
	ResourceNewCert      = AcmeResource("new-cert")
	ResourceRevokeCert   = AcmeResource("revoke-cert")
	ResourceRegistration = AcmeResource("reg")
	ResourceChallenge    = AcmeResource("challenge")
)

// These status are the states of OCSP
const (
	OCSPStatusGood    = OCSPStatus("good")
	OCSPStatusRevoked = OCSPStatus("revoked")
)

// Error types that can be used in ACME payloads
const (
	ConnectionProblem     = ProblemType("urn:acme:error:connection")
	MalformedProblem      = ProblemType("urn:acme:error:malformed")
	ServerInternalProblem = ProblemType("urn:acme:error:serverInternal")
	TLSProblem            = ProblemType("urn:acme:error:tls")
	UnauthorizedProblem   = ProblemType("urn:acme:error:unauthorized")
	UnknownHostProblem    = ProblemType("urn:acme:error:unknownHost")
)

// These types are the available challenges
const (
	ChallengeTypeSimpleHTTP = "simpleHttp"
	ChallengeTypeDVSNI      = "dvsni"
	ChallengeTypeDNS        = "dns"
)

// The suffix appended to pseudo-domain names in DVSNI challenges
const DVSNISuffix = "acme.invalid"

// The label attached to DNS names in DNS challenges
const DNSPrefix = "_acme-challenge"

func (pd *ProblemDetails) Error() string {
	return fmt.Sprintf("%s :: %s", pd.Type, pd.Detail)
}

func cmpStrSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cmpExtKeyUsageSlice(a, b []x509.ExtKeyUsage) bool {
	if len(a) != len(b) {
		return false
	}
	testMap := make(map[int]bool, len(a))
	for i := range a {
		testMap[int(a[i])] = true
	}
	for i := range b {
		if !testMap[int(b[i])] {
			return false
		}
	}
	return true
}

func cmpIPSlice(a, b []net.IP) bool {
	if len(a) != len(b) {
		return false
	}
	testMap := make(map[string]bool, len(a))
	for i := range a {
		testMap[a[i].String()] = true
	}
	for i := range b {
		if !testMap[b[i].String()] {
			return false
		}
	}
	return true
}

// An AcmeIdentifier encodes an identifier that can
// be validated by ACME.  The protocol allows for different
// types of identifier to be supported (DNS names, IP
// addresses, etc.), but currently we only support
// domain names.
type AcmeIdentifier struct {
	Type  IdentifierType `json:"type"`  // The type of identifier being encoded
	Value string         `json:"value"` // The identifier itself
}

// CertificateRequest is just a CSR
//
// This data is unmarshalled from JSON by way of rawCertificateRequest, which
// represents the actual structure received from the client.
type CertificateRequest struct {
	CSR   *x509.CertificateRequest // The CSR
	Bytes []byte                   // The original bytes of the CSR, for logging.
}

type rawCertificateRequest struct {
	CSR JSONBuffer `json:"csr"` // The encoded CSR
}

// UnmarshalJSON provides an implementation for decoding CertificateRequest objects.
func (cr *CertificateRequest) UnmarshalJSON(data []byte) error {
	var raw rawCertificateRequest
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	csr, err := x509.ParseCertificateRequest(raw.CSR)
	if err != nil {
		return err
	}

	cr.CSR = csr
	cr.Bytes = raw.CSR
	return nil
}

// MarshalJSON provides an implementation for encoding CertificateRequest objects.
func (cr CertificateRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(rawCertificateRequest{
		CSR: cr.CSR.Raw,
	})
}

// Registration objects represent non-public metadata attached
// to account keys.
type Registration struct {
	// Unique identifier
	ID int64 `json:"id" db:"id"`

	// Account key to which the details are attached
	Key jose.JsonWebKey `json:"key" db:"jwk"`

	// Contact URIs
	Contact []*AcmeURL `json:"contact,omitempty" db:"contact"`

	// Agreement with terms of service
	Agreement string `json:"agreement,omitempty" db:"agreement"`
}

// MergeUpdate copies a subset of information from the input Registration
// into this one.
func (r *Registration) MergeUpdate(input Registration) {
	if len(input.Contact) > 0 {
		r.Contact = input.Contact
	}

	if len(input.Agreement) > 0 {
		r.Agreement = input.Agreement
	}
}

// Challenge is an aggregate of all data needed for any challenges.
//
// Rather than define individual types for different types of
// challenge, we just throw all the elements into one bucket,
// together with the common metadata elements.
type Challenge struct {
	// The type of challenge
	Type string `json:"type"`

	// The status of this challenge
	Status AcmeStatus `json:"status,omitempty"`

	// Contains the error that occured during challenge validation, if any
	Error *ProblemDetails `json:"error,omitempty"`

	// If successful, the time at which this challenge
	// was completed by the server.
	Validated *time.Time `json:"validated,omitempty"`

	// A URI to which a response can be POSTed
	URI *AcmeURL `json:"uri"`

	// Used by simpleHttp, dvsni, and dns challenges
	Token string `json:"token,omitempty"`

	// Used by simpleHTTP challenges
	TLS *bool `json:"tls,omitempty"`

	// Used by dns and dvsni challenges
	Validation *jose.JsonWebSignature `json:"validation,omitempty"`
}

// IsSane checks the sanity of a challenge object before issued to the client
// (completed = false) and before validation (completed = true).
func (ch Challenge) IsSane(completed bool) bool {
	if ch.Status != StatusPending {
		return false
	}

	switch ch.Type {
	case ChallengeTypeSimpleHTTP:
		// check extra fields aren't used
		if ch.Validation != nil {
			return false
		}

		if completed && ch.TLS == nil {
			return false
		}

		// check token is present, corrent length, and contains b64 encoded string
		if ch.Token == "" || len(ch.Token) != 43 {
			return false
		}
		if _, err := B64dec(ch.Token); err != nil {
			return false
		}
	case ChallengeTypeDVSNI:
		// Same as DNS
		fallthrough
	case ChallengeTypeDNS:
		// check extra fields aren't used
		if ch.TLS != nil {
			return false
		}

		// check token is present, corrent length, and contains b64 encoded string
		if ch.Token == "" || len(ch.Token) != 43 {
			return false
		}
		if _, err := B64dec(ch.Token); err != nil {
			return false
		}

		// If completed, check that there's a validation object
		if completed && ch.Validation == nil {
			return false
		}

	default:
		return false
	}

	return true
}

// MergeResponse copies a subset of client-provided data to the current Challenge.
// Note: This method does not update the challenge on the left side of the '.'
func (ch Challenge) MergeResponse(resp Challenge) Challenge {
	switch ch.Type {
	case ChallengeTypeSimpleHTTP:
		// For simpleHttp, only "tls" is client-provided
		// If "tls" is not provided, default to "true"
		if resp.TLS != nil {
			ch.TLS = resp.TLS
		} else {
			ch.TLS = new(bool)
			*ch.TLS = true
		}

	case ChallengeTypeDVSNI:
		fallthrough
	case ChallengeTypeDNS:
		// For dvsni and dns, only "validation" is client-provided
		if resp.Validation != nil {
			ch.Validation = resp.Validation
		}
	}

	return ch
}

// Authorization represents the authorization of an account key holder
// to act on behalf of a domain.  This struct is intended to be used both
// internally and for JSON marshaling on the wire.  Any fields that should be
// suppressed on the wire (e.g., ID, regID) must be made empty before marshaling.
type Authorization struct {
	// An identifier for this authorization, unique across
	// authorizations and certificates within this instance.
	ID string `json:"id,omitempty" db:"id"`

	// The identifier for which authorization is being given
	Identifier AcmeIdentifier `json:"identifier,omitempty" db:"identifier"`

	// The registration ID associated with the authorization
	RegistrationID int64 `json:"regId,omitempty" db:"registrationID"`

	// The status of the validation of this authorization
	Status AcmeStatus `json:"status,omitempty" db:"status"`

	// The date after which this authorization will be no
	// longer be considered valid
	Expires *time.Time `json:"expires,omitempty" db:"expires"`

	// An array of challenges objects used to validate the
	// applicant's control of the identifier.  For authorizations
	// in process, these are challenges to be fulfilled; for
	// final authorizations, they describe the evidence that
	// the server used in support of granting the authorization.
	Challenges []Challenge `json:"challenges,omitempty" db:"challenges"`

	// The server may suggest combinations of challenges if it
	// requires more than one challenge to be completed.
	Combinations [][]int `json:"combinations,omitempty" db:"combinations"`
}

// JSONBuffer fields get encoded and decoded JOSE-style, in base64url encoding
// with stripped padding.
type JSONBuffer []byte

// URL-safe base64 encode that strips padding
func base64URLEncode(data []byte) string {
	var result = base64.URLEncoding.EncodeToString(data)
	return strings.TrimRight(result, "=")
}

// URL-safe base64 decoder that adds padding
func base64URLDecode(data string) ([]byte, error) {
	var missing = (4 - len(data)%4) % 4
	data += strings.Repeat("=", missing)
	return base64.URLEncoding.DecodeString(data)
}

// MarshalJSON encodes a JSONBuffer for transmission.
func (jb JSONBuffer) MarshalJSON() (result []byte, err error) {
	return json.Marshal(base64URLEncode(jb))
}

// UnmarshalJSON decodes a JSONBuffer to an object.
func (jb *JSONBuffer) UnmarshalJSON(data []byte) (err error) {
	var str string
	err = json.Unmarshal(data, &str)
	if err != nil {
		return err
	}
	*jb, err = base64URLDecode(str)
	return
}

// Certificate objects are entirely internal to the server.  The only
// thing exposed on the wire is the certificate itself.
type Certificate struct {
	RegistrationID int64 `db:"registrationID"`

	// The revocation status of the certificate.
	// * "valid" - not revoked
	// * "revoked" - revoked
	Status AcmeStatus `db:"status"`

	Serial  string    `db:"serial"`
	Digest  string    `db:"digest"`
	DER     []byte    `db:"der"`
	Issued  time.Time `db:"issued"`
	Expires time.Time `db:"expires"`
}

type IssuedCertIdentifierData struct {
	ReversedName string
	Serial       string
}

// IdentifierData holds information about what certificates are known for a
// given identifier. This is used to present Proof of Posession challenges in
// the case where a certificate already exists. The DB table holding
// IdentifierData rows contains information about certs issued by Boulder and
// also information about certs observed from third parties.
type IdentifierData struct {
	ReversedName string `db:"reversedName"` // The label-wise reverse of an identifier, e.g. com.example or com.example.*
	CertSHA1     string `db:"certSHA1"`     // The hex encoding of the SHA-1 hash of a cert containing the identifier
}

// ExternalCert holds information about certificates issued by other CAs,
// obtained through Certificate Transparency, the SSL Observatory, or scans.io.
type ExternalCert struct {
	SHA1     string    `db:"sha1"`       // The hex encoding of the SHA-1 hash of this cert
	Issuer   string    `db:"issuer"`     // The Issuer field of this cert
	Subject  string    `db:"subject"`    // The Subject field of this cert
	NotAfter time.Time `db:"notAfter"`   // Date after which this cert should be considered invalid
	SPKI     []byte    `db:"spki"`       // The hex encoding of the certificate's SubjectPublicKeyInfo in DER form
	Valid    bool      `db:"valid"`      // Whether this certificate was valid at LastUpdated time
	EV       bool      `db:"ev"`         // Whether this cert was EV valid
	CertDER  []byte    `db:"rawDERCert"` // DER (binary) encoding of the raw certificate
}

// MatchesCSR tests the contents of a generated certificate to make sure
// that the PublicKey, CommonName, and DNSNames match those provided in
// the CSR that was used to generate the certificate. It also checks the
// following fields for:
//		* notAfter is after earliestExpiry
//		* notBefore is not more than 24 hours ago
//		* BasicConstraintsValid is true
//		* IsCA is false
//		* ExtKeyUsage only contains ExtKeyUsageServerAuth & ExtKeyUsageClientAuth
//		* Subject only contains CommonName & Names
func (cert Certificate) MatchesCSR(csr *x509.CertificateRequest, earliestExpiry time.Time) (err error) {
	parsedCertificate, err := x509.ParseCertificate([]byte(cert.DER))
	if err != nil {
		return
	}

	// Check issued certificate matches what was expected from the CSR
	hostNames := make([]string, len(csr.DNSNames))
	copy(hostNames, csr.DNSNames)
	if len(csr.Subject.CommonName) > 0 {
		hostNames = append(hostNames, csr.Subject.CommonName)
	}
	hostNames = UniqueNames(hostNames)

	if !KeyDigestEquals(parsedCertificate.PublicKey, csr.PublicKey) {
		err = InternalServerError("Generated certificate public key doesn't match CSR public key")
		return
	}
	if len(csr.Subject.CommonName) > 0 && parsedCertificate.Subject.CommonName != csr.Subject.CommonName {
		err = InternalServerError("Generated certificate CommonName doesn't match CSR CommonName")
		return
	}
	if !cmpStrSlice(parsedCertificate.DNSNames, hostNames) {
		err = InternalServerError("Generated certificate DNSNames don't match CSR DNSNames")
		return
	}
	if !cmpIPSlice(parsedCertificate.IPAddresses, csr.IPAddresses) {
		err = InternalServerError("Generated certificate IPAddresses don't match CSR IPAddresses")
		return
	}
	if !cmpStrSlice(parsedCertificate.EmailAddresses, csr.EmailAddresses) {
		err = InternalServerError("Generated certificate EmailAddresses don't match CSR EmailAddresses")
		return
	}
	if len(parsedCertificate.Subject.Country) > 0 || len(parsedCertificate.Subject.Organization) > 0 ||
		len(parsedCertificate.Subject.OrganizationalUnit) > 0 || len(parsedCertificate.Subject.Locality) > 0 ||
		len(parsedCertificate.Subject.Province) > 0 || len(parsedCertificate.Subject.StreetAddress) > 0 ||
		len(parsedCertificate.Subject.PostalCode) > 0 || len(parsedCertificate.Subject.SerialNumber) > 0 {
		err = InternalServerError("Generated certificate Subject contains fields other than CommonName or Names")
		return
	}
	if parsedCertificate.NotAfter.After(earliestExpiry) {
		err = InternalServerError("Generated certificate expires before earliest expiration")
		return
	}
	now := time.Now()
	if now.Sub(parsedCertificate.NotBefore) > time.Hour*24 {
		err = InternalServerError(fmt.Sprintf("Generated certificate is back dated %s", now.Sub(parsedCertificate.NotBefore)))
		return
	}
	if !parsedCertificate.BasicConstraintsValid {
		err = InternalServerError("Generated certificate doesn't have basic constraints set")
		return
	}
	if parsedCertificate.IsCA {
		err = InternalServerError("Generated certificate can sign other certificates")
		return
	}
	if !cmpExtKeyUsageSlice(parsedCertificate.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}) {
		err = InternalServerError("Generated certificate doesn't have correct key usage extensions")
		return
	}

	return
}

// CertificateStatus structs are internal to the server. They represent the
// latest data about the status of the certificate, required for OCSP updating
// and for validating that the subscriber has accepted the certificate.
type CertificateStatus struct {
	Serial string `db:"serial"`

	// subscriberApproved: true iff the subscriber has posted back to the server
	//   that they accept the certificate, otherwise 0.
	SubscriberApproved bool `db:"subscriberApproved"`

	// status: 'good' or 'revoked'. Note that good, expired certificates remain
	//   with status 'good' but don't necessarily get fresh OCSP responses.
	Status OCSPStatus `db:"status"`

	// ocspLastUpdated: The date and time of the last time we generated an OCSP
	//   response. If we have never generated one, this has the zero value of
	//   time.Time, i.e. Jan 1 1970.
	OCSPLastUpdated time.Time `db:"ocspLastUpdated"`

	// revokedDate: If status is 'revoked', this is the date and time it was
	//   revoked. Otherwise it has the zero value of time.Time, i.e. Jan 1 1970.
	RevokedDate time.Time `db:"revokedDate"`

	// revokedReason: If status is 'revoked', this is the reason code for the
	//   revocation. Otherwise it is zero (which happens to be the reason
	//   code for 'unspecified').
	RevokedReason int `db:"revokedReason"`

	LastExpirationNagSent time.Time `db:"lastExpirationNagSent"`

	LockCol int64 `json:"-"`
}

// OCSPResponse is a (large) table of OCSP responses. This contains all
// historical OCSP responses we've signed, is append-only, and is likely to get
// quite large.
// It must be administratively truncated outside of Boulder.
type OCSPResponse struct {
	ID int `db:"id"`

	// serial: Same as certificate serial.
	Serial string `db:"serial"`

	// createdAt: The date the response was signed.
	CreatedAt time.Time `db:"createdAt"`

	// response: The encoded and signed CRL.
	Response []byte `db:"response"`
}

// CRL is a large table of signed CRLs. This contains all historical CRLs
// we've signed, is append-only, and is likely to get quite large.
// It must be administratively truncated outside of Boulder.
type CRL struct {
	// serial: Same as certificate serial.
	Serial string `db:"serial"`

	// createdAt: The date the CRL was signed.
	CreatedAt time.Time `db:"createdAt"`

	// crl: The encoded and signed CRL.
	CRL string `db:"crl"`
}

// DeniedCSR is a list of names we deny issuing.
type DeniedCSR struct {
	ID int `db:"id"`

	Names string `db:"names"`
}

// OCSPSigningRequest is a transfer object representing an OCSP Signing Request
type OCSPSigningRequest struct {
	CertDER   []byte
	Status    string
	Reason    int
	RevokedAt time.Time
}
