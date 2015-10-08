// Copyright 2014 ISRG.  All rights reserved
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package core

import (
	"crypto/subtle"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
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
	RateLimitedProblem    = ProblemType("urn:acme:error:rateLimited")
)

// These types are the available challenges
const (
	ChallengeTypeSimpleHTTP = "simpleHttp"
	ChallengeTypeDVSNI      = "dvsni"
	ChallengeTypeHTTP01     = "http-01"
	ChallengeTypeTLSSNI01   = "tls-sni-01"
	ChallengeTypeDNS01      = "dns-01"
)

// The suffix appended to pseudo-domain names in DVSNI challenges
const TLSSNISuffix = "acme.invalid"

// The label attached to DNS names in DNS challenges
const DNSPrefix = "_acme-challenge"

func (pd *ProblemDetails) Error() string {
	return fmt.Sprintf("%s :: %s", pd.Type, pd.Detail)
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
	Key jose.JsonWebKey `json:"key"`

	// Contact URIs
	Contact []*AcmeURL `json:"contact,omitempty"`

	// Agreement with terms of service
	Agreement string `json:"agreement,omitempty"`

	// InitialIP is the IP address from which the registration was created
	InitialIP net.IP `json:"initialIp"`

	// CreatedAt is the time the registration was created.
	CreatedAt time.Time `json:"createdAt"`
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

// ValidationRecord represents a validation attempt against a specific URL/hostname
// and the IP addresses that were resolved and used
type ValidationRecord struct {
	// SimpleHTTP only
	URL string `json:"url,omitempty"`

	// Shared
	Hostname          string   `json:"hostname"`
	Port              string   `json:"port"`
	AddressesResolved []net.IP `json:"addressesResolved"`
	AddressUsed       net.IP   `json:"addressUsed"`
}

// KeyAuthorization represents a domain holder's authorization for a
// specific account key to satisfy a specific challenge.
type KeyAuthorization struct {
	Token      string
	Thumbprint string
}

// NewKeyAuthorization computes the thumbprint and assembles the object
func NewKeyAuthorization(token string, key *jose.JsonWebKey) (KeyAuthorization, error) {
	if key == nil {
		return KeyAuthorization{}, fmt.Errorf("Cannot authorize a nil key")
	}

	thumbprint, err := Thumbprint(key)
	if err != nil {
		return KeyAuthorization{}, err
	}

	return KeyAuthorization{
		Token:      token,
		Thumbprint: thumbprint,
	}, nil
}

// NewKeyAuthorizationFromString parses the string and composes a key authorization struct
func NewKeyAuthorizationFromString(input string) (ka KeyAuthorization, err error) {
	parts := strings.Split(input, ".")
	if len(parts) != 2 {
		err = fmt.Errorf("Invalid key authorization: %d parts", len(parts))
		return
	} else if !LooksLikeAToken(parts[0]) {
		err = fmt.Errorf("Invalid key authorization: malformed token")
		return
	} else if !LooksLikeAToken(parts[1]) {
		// Thumbprints have the same syntax as tokens in boulder
		// Both are base64-encoded and 32 octets
		err = fmt.Errorf("Invalid key authorization: malformed key thumbprint")
		return
	}

	ka = KeyAuthorization{
		Token:      parts[0],
		Thumbprint: parts[1],
	}
	return
}

// String produces the string representation of a key authorization
func (ka KeyAuthorization) String() string {
	return ka.Token + "." + ka.Thumbprint
}

// Match determines whether this KeyAuthorization matches the given token and key
func (ka KeyAuthorization) Match(token string, key *jose.JsonWebKey) bool {
	if key == nil {
		return false
	}

	thumbprint, err := Thumbprint(key)
	if err != nil {
		return false
	}

	tokensEqual := subtle.ConstantTimeCompare([]byte(token), []byte(ka.Token))
	thumbprintsEqual := subtle.ConstantTimeCompare([]byte(thumbprint), []byte(ka.Thumbprint))

	return tokensEqual == 1 && thumbprintsEqual == 1
}

// MarshalJSON packs a key authorization into its string representation
func (ka KeyAuthorization) MarshalJSON() (result []byte, err error) {
	return json.Marshal(ka.String())
}

// UnmarshalJSON unpacks a key authorization from a string
func (ka *KeyAuthorization) UnmarshalJSON(data []byte) (err error) {
	var str string
	err = json.Unmarshal(data, &str)
	if err != nil {
		return err
	}

	parsed, err := NewKeyAuthorizationFromString(str)
	if err != nil {
		return err
	}

	*ka = parsed
	return
}

// Challenge is an aggregate of all data needed for any challenges.
//
// Rather than define individual types for different types of
// challenge, we just throw all the elements into one bucket,
// together with the common metadata elements.
type Challenge struct {
	ID int64 `json:"id,omitempty"`

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
	URI string `json:"uri"`

	// Used by simpleHttp, http-00, tls-sni-00, and dns-00 challenges
	Token string `json:"token,omitempty"`

	// Used by simpleHttp challenges
	TLS *bool `json:"tls,omitempty"`

	// Used by dvsni challenges
	Validation *jose.JsonWebSignature `json:"validation,omitempty"`

	// Used by http-00, tls-sni-00, and dns-00 challenges
	KeyAuthorization *KeyAuthorization `json:"keyAuthorization,omitempty"`

	// Contains information about URLs used or redirected to and IPs resolved and
	// used
	ValidationRecord []ValidationRecord `json:"validationRecord,omitempty"`

	// The account key used to create this challenge.  This is not part of the
	// spec, but clients are required to ignore unknown fields, so it's harmless
	// to include.
	//
	// Boulder needs to remember what key was used to create a challenge in order
	// to prevent an attacker from re-using a validation signature with a different,
	// unauthorized key. See:
	//   https://mailarchive.ietf.org/arch/msg/acme/F71iz6qq1o_QPVhJCV4dqWf-4Yc
	AccountKey *jose.JsonWebKey `json:"accountKey,omitempty"`
}

// RecordsSane checks the sanity of a ValidationRecord object before sending it
// back to the RA to be stored.
func (ch Challenge) RecordsSane() bool {
	if ch.ValidationRecord == nil || len(ch.ValidationRecord) == 0 {
		return false
	}

	switch ch.Type {
	case ChallengeTypeSimpleHTTP:
		// TODO(https://github.com/letsencrypt/boulder/issues/894): Remove this case
		fallthrough
	case ChallengeTypeHTTP01:
		for _, rec := range ch.ValidationRecord {
			if rec.URL == "" || rec.Hostname == "" || rec.Port == "" || rec.AddressUsed == nil ||
				len(rec.AddressesResolved) == 0 {
				return false
			}
		}
	case ChallengeTypeDVSNI:
		// TODO(https://github.com/letsencrypt/boulder/issues/894): Remove this case
		fallthrough
	case ChallengeTypeTLSSNI01:
		if len(ch.ValidationRecord) > 1 {
			return false
		}
		if ch.ValidationRecord[0].URL != "" {
			return false
		}
		if ch.ValidationRecord[0].Hostname == "" || ch.ValidationRecord[0].Port == "" ||
			ch.ValidationRecord[0].AddressUsed == nil || len(ch.ValidationRecord[0].AddressesResolved) == 0 {
			return false
		}
	case ChallengeTypeDNS01:
		// Nothing for now
	}

	return true
}

// isLegacy returns true if the challenge is of a legacy type (i.e., one defined
// before draft-ietf-acme-acme-00)
// TODO(https://github.com/letsencrypt/boulder/issues/894): Delete this method
func (ch Challenge) isLegacy() bool {
	return (ch.Type == ChallengeTypeSimpleHTTP) ||
		(ch.Type == ChallengeTypeDVSNI)
}

// legacyIsSane performs sanity checks for legacy challenge types, which have
// a different structure / logic than current challenges.
// TODO(https://github.com/letsencrypt/boulder/issues/894): Delete this method
func (ch Challenge) legacyIsSane(completed bool) bool {
	if ch.Status != StatusPending {
		return false
	}

	if ch.AccountKey == nil {
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

// legacyMergeResponse copies a subset of client-provided data to the current Challenge.
// Note: This method does not update the challenge on the left side of the '.'
// TODO(https://github.com/letsencrypt/boulder/issues/894): Delete this method
func (ch Challenge) legacyMergeResponse(resp Challenge) Challenge {
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
		// For dvsni and dns, only "validation" is client-provided
		if resp.Validation != nil {
			ch.Validation = resp.Validation
		}
	}

	return ch
}

// IsSane checks the sanity of a challenge object before issued to the client
// (completed = false) and before validation (completed = true).
func (ch Challenge) IsSane(completed bool) bool {
	// TODO(https://github.com/letsencrypt/boulder/issues/894): Delete this branch
	if ch.isLegacy() {
		return ch.legacyIsSane(completed)
	}

	if ch.Status != StatusPending {
		return false
	}

	// There always needs to be an account key and token
	if ch.AccountKey == nil || !LooksLikeAToken(ch.Token) {
		return false
	}

	// Before completion, the key authorization field should be empty
	if !completed && ch.KeyAuthorization != nil {
		return false
	}

	// If the challenge is completed, then there should be a key authorization,
	// and it should match the challenge.
	if completed {
		if ch.KeyAuthorization == nil {
			return false
		}

		if !ch.KeyAuthorization.Match(ch.Token, ch.AccountKey) {
			return false
		}
	}

	return true
}

// MergeResponse copies a subset of client-provided data to the current Challenge.
// Note: This method does not update the challenge on the left side of the '.'
func (ch Challenge) MergeResponse(resp Challenge) Challenge {
	// TODO(https://github.com/letsencrypt/boulder/issues/894): Delete this branch
	if ch.isLegacy() {
		return ch.legacyMergeResponse(resp)
	}

	// The only client-provided field is the key authorization, and all current
	// challenge types use it.
	switch ch.Type {
	case ChallengeTypeHTTP01:
		fallthrough
	case ChallengeTypeTLSSNI01:
		fallthrough
	case ChallengeTypeDNS01:
		ch.KeyAuthorization = resp.KeyAuthorization
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
	// longer be considered valid. Note: a certificate may be issued even on the
	// last day of an authorization's lifetime. The last day for which someone can
	// hold a valid certificate based on an authorization is authorization
	// lifetime + certificate lifetime.
	Expires *time.Time `json:"expires,omitempty" db:"expires"`

	// An array of challenges objects used to validate the
	// applicant's control of the identifier.  For authorizations
	// in process, these are challenges to be fulfilled; for
	// final authorizations, they describe the evidence that
	// the server used in support of granting the authorization.
	Challenges []Challenge `json:"challenges,omitempty" db:"-"`

	// The server may suggest combinations of challenges if it
	// requires more than one challenge to be completed.
	Combinations [][]int `json:"combinations,omitempty" db:"combinations"`
}

// FindChallenge will look for the given challenge inside this authorization. If
// found, it will return the index of that challenge within the Authorization's
// Challenges array. Otherwise it will return -1.
func (authz *Authorization) FindChallenge(challengeID int64) int {
	for i, c := range authz.Challenges {
		if c.ID == challengeID {
			return i
		}
	}
	return -1
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

	Serial  string    `db:"serial"`
	Digest  string    `db:"digest"`
	DER     []byte    `db:"der"`
	Issued  time.Time `db:"issued"`
	Expires time.Time `db:"expires"`
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
	RevokedReason RevocationCode `db:"revokedReason"`

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
	Reason    RevocationCode
	RevokedAt time.Time
}

// SignedCertificateTimestamp represents objects used by Certificate Transparency
// to demonstrate that a certificate was submitted to a CT log. See RFC 6962.
type SignedCertificateTimestamp struct {
	ID int `db:"id"`
	// The version of the protocol to which the SCT conforms
	SCTVersion uint8 `db:"sctVersion"`
	// the SHA-256 hash of the log's public key, calculated over
	// the DER encoding of the key represented as SubjectPublicKeyInfo.
	LogID string `db:"logID"`
	// Timestamp (in ms since unix epoc) at which the SCT was issued
	Timestamp uint64 `db:"timestamp"`
	// For future extensions to the protocol
	Extensions []byte `db:"extensions"`
	// The Log's signature for this SCT
	Signature []byte `db:"signature"`

	// The serial of the certificate this SCT is for
	CertificateSerial string `db:"certificateSerial"`

	LockCol int64
}

type rawSignedCertificateTimestamp struct {
	Version    uint8  `json:"sct_version"`
	LogID      string `json:"id"`
	Timestamp  uint64 `json:"timestamp"`
	Signature  string `json:"signature"`
	Extensions string `json:"extensions"`
}

// UnmarshalJSON parses the add-chain response from a CT log. It fills all of
// the fields in the SignedCertificateTimestamp struct except for ID and
// CertificateSerial, which are used for local recordkeeping in the Boulder DB.
func (sct *SignedCertificateTimestamp) UnmarshalJSON(data []byte) error {
	var err error
	var rawSCT rawSignedCertificateTimestamp
	if err = json.Unmarshal(data, &rawSCT); err != nil {
		return fmt.Errorf("Failed to unmarshal SCT receipt, %s", err)
	}
	sct.LogID = rawSCT.LogID
	if err != nil {
		return fmt.Errorf("Failed to decode log ID, %s", err)
	}
	sct.Signature, err = base64.StdEncoding.DecodeString(rawSCT.Signature)
	if err != nil {
		return fmt.Errorf("Failed to decode SCT signature, %s", err)
	}
	sct.Extensions, err = base64.StdEncoding.DecodeString(rawSCT.Extensions)
	if err != nil {
		return fmt.Errorf("Failed to decode SCT extensions, %s", err)
	}
	sct.SCTVersion = rawSCT.Version
	sct.Timestamp = rawSCT.Timestamp
	return nil
}

const (
	sctHashSHA256 = 4
	sctSigECDSA   = 3
)

// CheckSignature validates that the returned SCT signature is a valid SHA256 +
// ECDSA signature but does not verify that a specific public key signed it.
func (sct *SignedCertificateTimestamp) CheckSignature() error {
	if len(sct.Signature) < 4 {
		return errors.New("SCT signature is truncated")
	}
	// Since all of the known logs currently only use SHA256 hashes and ECDSA
	// keys, only allow those
	if sct.Signature[0] != sctHashSHA256 {
		return fmt.Errorf("Unsupported SCT hash function [%d]", sct.Signature[0])
	}
	if sct.Signature[1] != sctSigECDSA {
		return fmt.Errorf("Unsupported SCT signature algorithm [%d]", sct.Signature[1])
	}

	var ecdsaSig struct {
		R, S *big.Int
	}
	// Ignore the two length bytes and attempt to unmarshal the signature directly
	signatureBytes := sct.Signature[4:]
	signatureBytes, err := asn1.Unmarshal(signatureBytes, &ecdsaSig)
	if err != nil {
		return fmt.Errorf("Failed to parse SCT signature, %s", err)
	}
	if len(signatureBytes) > 0 {
		return fmt.Errorf("Trailing garbage after signature")
	}

	return nil
}

// RevocationCode is used to specify a certificate revocation reason
type RevocationCode int

// RevocationReasons provides a map from reason code to string explaining the
// code
var RevocationReasons = map[RevocationCode]string{
	0: "unspecified",
	1: "keyCompromise",
	2: "cACompromise",
	3: "affiliationChanged",
	4: "superseded",
	5: "cessationOfOperation",
	6: "certificateHold",
	// 7 is unused
	8:  "removeFromCRL", // needed?
	9:  "privilegeWithdrawn",
	10: "aAcompromise",
}
