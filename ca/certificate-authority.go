// Copyright 2014 ISRG.  All rights reserved
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ca

import (
	"crypto"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/ioutil"
	"time"

	"github.com/letsencrypt/boulder/core"
	blog "github.com/letsencrypt/boulder/log"
	"github.com/letsencrypt/boulder/policy"

	cfsslConfig "github.com/letsencrypt/boulder/Godeps/_workspace/src/github.com/cloudflare/cfssl/config"
	"github.com/letsencrypt/boulder/Godeps/_workspace/src/github.com/cloudflare/cfssl/crypto/pkcs11key"
	"github.com/letsencrypt/boulder/Godeps/_workspace/src/github.com/cloudflare/cfssl/helpers"
	"github.com/letsencrypt/boulder/Godeps/_workspace/src/github.com/cloudflare/cfssl/ocsp"
	"github.com/letsencrypt/boulder/Godeps/_workspace/src/github.com/cloudflare/cfssl/signer"
	"github.com/letsencrypt/boulder/Godeps/_workspace/src/github.com/cloudflare/cfssl/signer/local"
)

// Config defines the JSON configuration file schema
type Config struct {
	Profile      string
	TestMode     bool
	DBDriver     string
	DBConnect    string
	SerialPrefix int
	Key          KeyConfig
	// LifespanOCSP is how long OCSP responses are valid for; It should be longer
	// than the minTimeToExpiry field for the OCSP Updater.
	LifespanOCSP string
	// How long issued certificates are valid for, should match expiry field
	// in cfssl config.
	Expiry string
	// The maximum number of subjectAltNames in a single certificate
	MaxNames int
	CFSSL    cfsslConfig.Config

	// DebugAddr is the address to run the /debug handlers on.
	DebugAddr string
}

// KeyConfig should contain either a File path to a PEM-format private key,
// or a PKCS11Config defining how to load a module for an HSM.
type KeyConfig struct {
	File   string
	PKCS11 PKCS11Config
}

// PKCS11Config defines how to load a module for an HSM.
type PKCS11Config struct {
	Module string
	Token  string
	PIN    string
	Label  string
}

// This map is used to detect algorithms in crypto/x509 that
// are no longer considered sufficiently strong.
// * No MD2, MD5, or SHA-1
// * No DSA
//
// SHA1WithRSA is allowed because there's still a fair bit of it
// out there, but we should try to remove it soon.
var badSignatureAlgorithms = map[x509.SignatureAlgorithm]bool{
	x509.UnknownSignatureAlgorithm: true,
	x509.MD2WithRSA:                true,
	x509.MD5WithRSA:                true,
	x509.DSAWithSHA1:               true,
	x509.DSAWithSHA256:             true,
	x509.ECDSAWithSHA1:             true,
}

// CertificateAuthorityImpl represents a CA that signs certificates, CRLs, and
// OCSP responses.
type CertificateAuthorityImpl struct {
	profile        string
	Signer         signer.Signer
	OCSPSigner     ocsp.Signer
	SA             core.StorageAuthority
	PA             core.PolicyAuthority
	DB             core.CertificateAuthorityDatabase
	log            *blog.AuditLogger
	Prefix         int // Prepended to the serial number
	ValidityPeriod time.Duration
	NotAfter       time.Time
	MaxNames       int
	MaxKeySize     int
}

// NewCertificateAuthorityImpl creates a CA that talks to a remote CFSSL
// instance.  (To use a local signer, simply instantiate CertificateAuthorityImpl
// directly.)  Communications with the CA are authenticated with MACs,
// using CFSSL's authenticated signature scheme.  A CA created in this way
// issues for a single profile on the remote signer, which is indicated
// by name in this constructor.
func NewCertificateAuthorityImpl(cadb core.CertificateAuthorityDatabase, config Config, issuerCert string) (*CertificateAuthorityImpl, error) {
	var ca *CertificateAuthorityImpl
	var err error
	logger := blog.GetAuditLogger()
	logger.Notice("Certificate Authority Starting")

	if config.SerialPrefix <= 0 || config.SerialPrefix >= 256 {
		err = errors.New("Must have a positive non-zero serial prefix less than 256 for CA.")
		return nil, err
	}

	// CFSSL requires processing JSON configs through its own LoadConfig, so we
	// serialize and then deserialize.
	cfsslJSON, err := json.Marshal(config.CFSSL)
	if err != nil {
		return nil, err
	}
	cfsslConfigObj, err := cfsslConfig.LoadConfig(cfsslJSON)
	if err != nil {
		return nil, err
	}

	// Load the private key, which can be a file or a PKCS#11 key.
	priv, err := loadKey(config.Key)
	if err != nil {
		return nil, err
	}

	issuer, err := loadIssuer(issuerCert)
	if err != nil {
		return nil, err
	}

	signer, err := local.NewSigner(priv, issuer, x509.SHA256WithRSA, cfsslConfigObj.Signing)
	if err != nil {
		return nil, err
	}

	if config.LifespanOCSP == "" {
		return nil, errors.New("Config must specify an OCSP lifespan period.")
	}
	lifespanOCSP, err := time.ParseDuration(config.LifespanOCSP)
	if err != nil {
		return nil, err
	}

	// Set up our OCSP signer. Note this calls for both the issuer cert and the
	// OCSP signing cert, which are the same in our case.
	ocspSigner, err := ocsp.NewSigner(issuer, issuer, priv, lifespanOCSP)
	if err != nil {
		return nil, err
	}

	pa := policy.NewPolicyAuthorityImpl()

	ca = &CertificateAuthorityImpl{
		Signer:     signer,
		OCSPSigner: ocspSigner,
		profile:    config.Profile,
		PA:         pa,
		DB:         cadb,
		Prefix:     config.SerialPrefix,
		log:        logger,
		NotAfter:   issuer.NotAfter,
	}

	if config.Expiry == "" {
		return nil, errors.New("Config must specify an expiry period.")
	}
	ca.ValidityPeriod, err = time.ParseDuration(config.Expiry)
	if err != nil {
		return nil, err
	}

	ca.MaxNames = config.MaxNames

	return ca, nil
}

func loadKey(keyConfig KeyConfig) (priv crypto.Signer, err error) {
	if keyConfig.File != "" {
		var keyBytes []byte
		keyBytes, err = ioutil.ReadFile(keyConfig.File)
		if err != nil {
			return nil, fmt.Errorf("Could not read key file %s", keyConfig.File)
		}

		priv, err = helpers.ParsePrivateKeyPEM(keyBytes)
		return
	}

	pkcs11Config := keyConfig.PKCS11
	priv, err = pkcs11key.New(pkcs11Config.Module,
		pkcs11Config.Token, pkcs11Config.PIN, pkcs11Config.Label)
	return
}

func loadIssuer(filename string) (issuerCert *x509.Certificate, err error) {
	if filename == "" {
		err = errors.New("Issuer certificate was not provided in config.")
		return
	}
	issuerCertPEM, err := ioutil.ReadFile(filename)
	if err != nil {
		return
	}
	issuerCert, err = helpers.ParseCertificatePEM(issuerCertPEM)
	return
}

func loadIssuerKey(filename string) (issuerKey crypto.Signer, err error) {
	if filename == "" {
		err = errors.New("IssuerKey must be provided in test mode.")
		return
	}

	pem, err := ioutil.ReadFile(filename)
	if err != nil {
		return
	}
	issuerKey, err = helpers.ParsePrivateKeyPEM(pem)
	return
}

// GenerateOCSP produces a new OCSP response and returns it
func (ca *CertificateAuthorityImpl) GenerateOCSP(xferObj core.OCSPSigningRequest) ([]byte, error) {
	cert, err := x509.ParseCertificate(xferObj.CertDER)
	if err != nil {
		// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
		ca.log.AuditErr(err)
		return nil, err
	}

	signRequest := ocsp.SignRequest{
		Certificate: cert,
		Status:      xferObj.Status,
		Reason:      xferObj.Reason,
		RevokedAt:   xferObj.RevokedAt,
	}

	ocspResponse, err := ca.OCSPSigner.Sign(signRequest)
	return ocspResponse, err
}

// RevokeCertificate revokes the trust of the Cert referred to by the provided Serial.
func (ca *CertificateAuthorityImpl) RevokeCertificate(serial string, reasonCode int) (err error) {
	coreCert, err := ca.SA.GetCertificate(serial)
	if err != nil {
		// AUDIT[ Revocation Requests ] 4e85d791-09c0-4ab3-a837-d3d67e945134
		ca.log.AuditErr(err)
		return err
	}
	cert, err := x509.ParseCertificate(coreCert.DER)
	if err != nil {
		// AUDIT[ Revocation Requests ] 4e85d791-09c0-4ab3-a837-d3d67e945134
		ca.log.AuditErr(err)
		return err
	}

	signRequest := ocsp.SignRequest{
		Certificate: cert,
		Status:      string(core.OCSPStatusRevoked),
		Reason:      reasonCode,
		RevokedAt:   time.Now(),
	}
	ocspResponse, err := ca.OCSPSigner.Sign(signRequest)
	if err != nil {
		// AUDIT[ Revocation Requests ] 4e85d791-09c0-4ab3-a837-d3d67e945134
		ca.log.AuditErr(err)
		return err
	}
	err = ca.SA.MarkCertificateRevoked(serial, ocspResponse, reasonCode)
	return err
}

// IssueCertificate attempts to convert a CSR into a signed Certificate, while
// enforcing all policies.
func (ca *CertificateAuthorityImpl) IssueCertificate(csr x509.CertificateRequest, regID int64, earliestExpiry time.Time) (core.Certificate, error) {
	emptyCert := core.Certificate{}
	var err error
	key, ok := csr.PublicKey.(crypto.PublicKey)
	if !ok {
		err = fmt.Errorf("Invalid public key in CSR.")
		// AUDIT[ Certificate Requests ] 11917fa4-10ef-4e0d-9105-bacbe7836a3c
		ca.log.AuditErr(err)
		return emptyCert, err
	}
	if err = core.GoodKey(key, ca.MaxKeySize); err != nil {
		err = fmt.Errorf("Invalid public key in CSR: %s", err.Error())
		// AUDIT[ Certificate Requests ] 11917fa4-10ef-4e0d-9105-bacbe7836a3c
		ca.log.AuditErr(err)
		return emptyCert, err
	}
	if badSignatureAlgorithms[csr.SignatureAlgorithm] {
		err = fmt.Errorf("Invalid signature algorithm in CSR")
		// AUDIT[ Certificate Requests ] 11917fa4-10ef-4e0d-9105-bacbe7836a3c
		ca.log.AuditErr(err)
		return emptyCert, err
	}

	// Pull hostnames from CSR
	// Authorization is checked by the RA
	commonName := ""
	hostNames := make([]string, len(csr.DNSNames))
	copy(hostNames, csr.DNSNames)
	if len(csr.Subject.CommonName) > 0 {
		commonName = csr.Subject.CommonName
		hostNames = append(hostNames, csr.Subject.CommonName)
	} else if len(hostNames) > 0 {
		commonName = hostNames[0]
	} else {
		err = fmt.Errorf("Cannot issue a certificate without a hostname.")
		// AUDIT[ Certificate Requests ] 11917fa4-10ef-4e0d-9105-bacbe7836a3c
		ca.log.AuditErr(err)
		return emptyCert, err
	}

	// Collapse any duplicate names.  Note that this operation may re-order the names
	hostNames = core.UniqueNames(hostNames)
	if ca.MaxNames > 0 && len(hostNames) > ca.MaxNames {
		err = fmt.Errorf("Certificate request has %d > %d names", len(hostNames), ca.MaxNames)
		ca.log.WarningErr(err)
		return emptyCert, err
	}

	// Verify that names are allowed by policy
	identifier := core.AcmeIdentifier{Type: core.IdentifierDNS, Value: commonName}
	if err = ca.PA.WillingToIssue(identifier); err != nil {
		err = fmt.Errorf("Policy forbids issuing for name %s", commonName)
		// AUDIT[ Certificate Requests ] 11917fa4-10ef-4e0d-9105-bacbe7836a3c
		ca.log.AuditErr(err)
		return emptyCert, err
	}
	for _, name := range hostNames {
		identifier = core.AcmeIdentifier{Type: core.IdentifierDNS, Value: name}
		if err = ca.PA.WillingToIssue(identifier); err != nil {
			err = fmt.Errorf("Policy forbids issuing for name %s", name)
			// AUDIT[ Certificate Requests ] 11917fa4-10ef-4e0d-9105-bacbe7836a3c
			ca.log.AuditErr(err)
			return emptyCert, err
		}
	}

	notAfter := time.Now().Add(ca.ValidityPeriod)

	if ca.NotAfter.Before(notAfter) {
		// AUDIT[ Certificate Requests ] 11917fa4-10ef-4e0d-9105-bacbe7836a3c
		err = errors.New("Cannot issue a certificate that expires after the intermediate certificate.")
		ca.log.AuditErr(err)
		return emptyCert, err
	}

	// Note: We do not current enforce that certificate lifetimes match
	// authorization lifetimes, because it was breaking integration tests.
	if earliestExpiry.Before(notAfter) {
		message := fmt.Sprintf("Issuing a certificate that expires after the shortest underlying authorization. [%v] [%v]", earliestExpiry, notAfter)
		ca.log.Notice(message)
	}

	// Convert the CSR to PEM
	csrPEM := string(pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: csr.Raw,
	}))

	// Get the next serial number
	tx, err := ca.DB.Begin()
	if err != nil {
		// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
		ca.log.AuditErr(err)
		return emptyCert, err
	}

	serialDec, err := ca.DB.IncrementAndGetSerial(tx)
	if err != nil {
		// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
		ca.log.Audit(fmt.Sprintf("Serial increment failed, rolling back: err=[%v]", err))
		tx.Rollback()
		return emptyCert, err
	}
	serialHex := fmt.Sprintf("%02X%014X", ca.Prefix, serialDec)

	// Send the cert off for signing
	req := signer.SignRequest{
		Request: csrPEM,
		Profile: ca.profile,
		Hosts:   hostNames,
		Subject: &signer.Subject{
			CN: commonName,
		},
		SerialSeq: serialHex,
	}

	certPEM, err := ca.Signer.Sign(req)
	if err != nil {
		// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
		ca.log.Audit(fmt.Sprintf("Signer failed, rolling back: serial=[%s] err=[%v]", serialHex, err))
		tx.Rollback()
		return emptyCert, err
	}

	if len(certPEM) == 0 {
		err = fmt.Errorf("No certificate returned by server")
		// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
		ca.log.Audit(fmt.Sprintf("PEM empty from Signer, rolling back: serial=[%s] err=[%v]", serialHex, err))
		tx.Rollback()
		return emptyCert, err
	}

	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		err = fmt.Errorf("Invalid certificate value returned")

		// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
		ca.log.Audit(fmt.Sprintf("PEM decode error, aborting and rolling back issuance: pem=[%s] err=[%v]", certPEM, err))
		tx.Rollback()
		return emptyCert, err
	}
	certDER := block.Bytes

	cert := core.Certificate{
		DER:    certDER,
		Status: core.StatusValid,
	}

	// This is one last check for uncaught errors
	if err != nil {
		// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
		ca.log.Audit(fmt.Sprintf("Uncaught error, aborting and rolling back issuance: pem=[%s] err=[%v]", certPEM, err))
		tx.Rollback()
		return emptyCert, err
	}

	// Store the cert with the certificate authority, if provided
	_, err = ca.SA.AddCertificate(certDER, regID)
	if err != nil {
		// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
		ca.log.Audit(fmt.Sprintf("Failed RPC to store at SA, orphaning certificate: pem=[%s] err=[%v]", certPEM, err))
		tx.Rollback()
		return emptyCert, err
	}

	if err = tx.Commit(); err != nil {
		// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
		ca.log.Audit(fmt.Sprintf("Failed to commit, orphaning certificate: pem=[%s] err=[%v]", certPEM, err))
		return emptyCert, err
	}

	// Attempt to generate the OCSP Response now. If this raises an error, it is
	// logged but is not returned to the caller, as an error at this point does
	// not constitute an issuance failure.

	certObj, err := x509.ParseCertificate(certDER)
	if err != nil {
		ca.log.Warning(fmt.Sprintf("Post-Issuance OCSP failed parsing Certificate: %s", err))
		return cert, nil
	}

	serial := core.SerialToString(certObj.SerialNumber)

	signRequest := ocsp.SignRequest{
		Certificate: certObj,
		Status:      string(core.OCSPStatusGood),
	}

	ocspResponse, err := ca.OCSPSigner.Sign(signRequest)
	if err != nil {
		ca.log.Warning(fmt.Sprintf("Post-Issuance OCSP failed signing: %s", err))
		return cert, nil
	}

	err = ca.SA.UpdateOCSP(serial, ocspResponse)
	if err != nil {
		ca.log.Warning(fmt.Sprintf("Post-Issuance OCSP failed storing: %s", err))
		return cert, nil
	}

	// Do not return an err at this point; caller must know that the Certificate
	// was issued. (Also, it should be impossible for err to be non-nil here)
	return cert, nil
}
