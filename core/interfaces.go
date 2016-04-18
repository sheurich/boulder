// Copyright 2014 ISRG.  All rights reserved
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package core

import (
	"crypto/x509"
	"net"
	"net/http"
	"time"

	jose "github.com/square/go-jose"
)

// A WebFrontEnd object supplies methods that can be hooked into
// the Go http module's server functions, principally http.HandleFunc()
//
// It also provides methods to configure the base for authorization and
// certificate URLs.
//
// It is assumed that the ACME server is laid out as follows:
// * One URL for new-authorization -> NewAuthz
// * One URL for new-certificate -> NewCert
// * One path for authorizations -> Authz
// * One path for certificates -> Cert
type WebFrontEnd interface {
	// Set the base URL for authorizations
	SetAuthzBase(path string)

	// Set the base URL for certificates
	SetCertBase(path string)

	// This method represents the ACME new-registration resource
	NewRegistration(response http.ResponseWriter, request *http.Request)

	// This method represents the ACME new-authorization resource
	NewAuthz(response http.ResponseWriter, request *http.Request)

	// This method represents the ACME new-certificate resource
	NewCert(response http.ResponseWriter, request *http.Request)

	// Provide access to requests for registration resources
	Registration(response http.ResponseWriter, request *http.Request)

	// Provide access to requests for authorization resources
	Authz(response http.ResponseWriter, request *http.Request)

	// Provide access to requests for authorization resources
	Cert(response http.ResponseWriter, request *http.Request)
}

// RegistrationAuthority defines the public interface for the Boulder RA
type RegistrationAuthority interface {
	// [WebFrontEnd]
	NewRegistration(Registration) (Registration, error)

	// [WebFrontEnd]
	NewAuthorization(Authorization, int64) (Authorization, error)

	// [WebFrontEnd]
	NewCertificate(CertificateRequest, int64) (Certificate, error)

	// [WebFrontEnd]
	UpdateRegistration(Registration, Registration) (Registration, error)

	// [WebFrontEnd]
	UpdateAuthorization(Authorization, int, Challenge) (Authorization, error)

	// [WebFrontEnd]
	RevokeCertificateWithReg(x509.Certificate, RevocationCode, int64) error

	// [AdminRevoker]
	AdministrativelyRevokeCertificate(x509.Certificate, RevocationCode, string) error

	// [ValidationAuthority]
	OnValidationUpdate(Authorization) error
}

// CertificateAuthority defines the public interface for the Boulder CA
type CertificateAuthority interface {
	// [RegistrationAuthority]
	IssueCertificate(x509.CertificateRequest, int64) (Certificate, error)
	GenerateOCSP(OCSPSigningRequest) ([]byte, error)
}

// PolicyAuthority defines the public interface for the Boulder PA
type PolicyAuthority interface {
	WillingToIssue(id AcmeIdentifier, regID int64) error
	ChallengesFor(AcmeIdentifier, *jose.JsonWebKey) ([]Challenge, [][]int)
}

// StorageGetter are the Boulder SA's read-only methods
type StorageGetter interface {
	GetRegistration(int64) (Registration, error)
	GetRegistrationByKey(jose.JsonWebKey) (Registration, error)
	GetAuthorization(string) (Authorization, error)
	GetLatestValidAuthorization(int64, AcmeIdentifier) (Authorization, error)
	GetValidAuthorizations(int64, []string, time.Time) (map[string]*Authorization, error)
	GetCertificate(string) (Certificate, error)
	GetCertificateStatus(string) (CertificateStatus, error)
	AlreadyDeniedCSR([]string) (bool, error)
	CountCertificatesRange(time.Time, time.Time) (int64, error)
	CountCertificatesByNames([]string, time.Time, time.Time) (map[string]int, error)
	CountRegistrationsByIP(net.IP, time.Time, time.Time) (int, error)
	CountPendingAuthorizations(regID int64) (int, error)
	GetSCTReceipt(string, string) (SignedCertificateTimestamp, error)
	CountFQDNSets(time.Duration, []string) (int64, error)
	FQDNSetExists([]string) (bool, error)
}

// StorageAdder are the Boulder SA's write/update methods
type StorageAdder interface {
	NewRegistration(Registration) (Registration, error)
	UpdateRegistration(Registration) error
	NewPendingAuthorization(Authorization) (Authorization, error)
	UpdatePendingAuthorization(Authorization) error
	FinalizeAuthorization(Authorization) error
	MarkCertificateRevoked(serial string, reasonCode RevocationCode) error
	UpdateOCSP(serial string, ocspResponse []byte) error
	AddCertificate([]byte, int64) (string, error)
	AddSCTReceipt(SignedCertificateTimestamp) error
	RevokeAuthorizationsByDomain(AcmeIdentifier) (int64, int64, error)
}

// StorageAuthority interface represents a simple key/value
// store.  It is divided into StorageGetter and StorageUpdater
// interfaces for privilege separation.
type StorageAuthority interface {
	StorageGetter
	StorageAdder
}

// Publisher defines the public interface for the Boulder Publisher
type Publisher interface {
	SubmitToCT([]byte) error
}
