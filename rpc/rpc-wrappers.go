// Copyright 2014 ISRG.  All rights reserved
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package rpc

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	jose "github.com/letsencrypt/boulder/Godeps/_workspace/src/github.com/letsencrypt/go-jose"
	"github.com/letsencrypt/boulder/core"
	blog "github.com/letsencrypt/boulder/log"
)

// This file defines RPC wrappers around the ${ROLE}Impl classes,
// where ROLE covers:
//  * RegistrationAuthority
//  * ValidationAuthority
//  * CertificateAuthority
//  * StorageAuthority
//
// For each one of these, the are ${ROLE}Client and ${ROLE}Server
// types.  ${ROLE}Server is to be run on the server side, as a more
// or less stand-alone component.  ${ROLE}Client is loaded by the
// code making use of the functionality.
//
// The WebFrontEnd role does not expose any functionality over RPC,
// so it doesn't need wrappers.

// These strings are used by the RPC layer to identify function points.
const (
	MethodNewRegistration             = "NewRegistration"             // RA, SA
	MethodNewAuthorization            = "NewAuthorization"            // RA
	MethodNewCertificate              = "NewCertificate"              // RA
	MethodUpdateRegistration          = "UpdateRegistration"          // RA, SA
	MethodUpdateAuthorization         = "UpdateAuthorization"         // RA
	MethodRevokeCertificate           = "RevokeCertificate"           // RA, CA
	MethodOnValidationUpdate          = "OnValidationUpdate"          // RA
	MethodUpdateValidations           = "UpdateValidations"           // VA
	MethodCheckCAARecords             = "CheckCAARecords"             // VA
	MethodIssueCertificate            = "IssueCertificate"            // CA
	MethodGenerateOCSP                = "GenerateOCSP"                // CA
	MethodGetRegistration             = "GetRegistration"             // SA
	MethodGetRegistrationByKey        = "GetRegistrationByKey"        // RA, SA
	MethodGetAuthorization            = "GetAuthorization"            // SA
	MethodGetLatestValidAuthorization = "GetLatestValidAuthorization" // SA
	MethodGetCertificate              = "GetCertificate"              // SA
	MethodGetCertificateByShortSerial = "GetCertificateByShortSerial" // SA
	MethodGetCertificateStatus        = "GetCertificateStatus"        // SA
	MethodMarkCertificateRevoked      = "MarkCertificateRevoked"      // SA
	MethodUpdateOCSP                  = "UpdateOCSP"                  // SA
	MethodNewPendingAuthorization     = "NewPendingAuthorization"     // SA
	MethodUpdatePendingAuthorization  = "UpdatePendingAuthorization"  // SA
	MethodFinalizeAuthorization       = "FinalizeAuthorization"       // SA
	MethodAddCertificate              = "AddCertificate"              // SA
	MethodAlreadyDeniedCSR            = "AlreadyDeniedCSR"            // SA
)

// Request structs
type registrationRequest struct {
	Reg core.Registration
}

type getRegistrationRequest struct {
	ID int64
}

type updateRegistrationRequest struct {
	Base, Update core.Registration
}

type authorizationRequest struct {
	Authz core.Authorization
	RegID int64
}

type updateAuthorizationRequest struct {
	Authz    core.Authorization
	Index    int
	Response core.Challenge
}

type latestValidAuthorizationRequest struct {
	RegID      int64
	Identifier core.AcmeIdentifier
}

type certificateRequest struct {
	Req   core.CertificateRequest
	RegID int64
}

type issueCertificateRequest struct {
	Bytes          []byte
	RegID          int64
	EarliestExpiry time.Time
}

type addCertificateRequest struct {
	Bytes []byte
	RegID int64
}

type revokeCertificateRequest struct {
	Serial     string
	ReasonCode int
}

type markCertificateRevokedRequest struct {
	Serial       string
	OCSPResponse []byte
	ReasonCode   int
}

type caaRequest struct {
	Ident core.AcmeIdentifier
}

type validationRequest struct {
	Authz core.Authorization
	Index int
	Key   jose.JsonWebKey
}

type alreadyDeniedCSRReq struct {
	Names []string
}

type updateOCSPRequest struct {
	Serial       string
	OCSPResponse []byte
}

// Response structs
type caaResponse struct {
	Present bool
	Valid   bool
	Err     error
}

func improperMessage(method string, err error, obj interface{}) {
	log := blog.GetAuditLogger()
	log.Audit(fmt.Sprintf("Improper message. method: %s err: %s data: %+v", method, err, obj))
}
func errorCondition(method string, err error, obj interface{}) {
	log := blog.GetAuditLogger()
	log.Audit(fmt.Sprintf("Error condition. method: %s err: %s data: %+v", method, err, obj))
}

// NewRegistrationAuthorityServer constructs an RPC server
func NewRegistrationAuthorityServer(rpc RPCServer, impl core.RegistrationAuthority) error {
	log := blog.GetAuditLogger()

	rpc.Handle(MethodNewRegistration, func(req []byte) (response []byte, err error) {
		var rr registrationRequest
		if err = json.Unmarshal(req, &rr); err != nil {
			// AUDIT[ Improper Messages ] 0786b6f2-91ca-4f48-9883-842a19084c64
			improperMessage(MethodNewRegistration, err, req)
			return
		}

		reg, err := impl.NewRegistration(rr.Reg)
		if err != nil {
			return
		}

		response, err = json.Marshal(reg)
		if err != nil {
			// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
			errorCondition(MethodNewRegistration, err, req)
			return
		}
		return
	})

	rpc.Handle(MethodNewAuthorization, func(req []byte) (response []byte, err error) {
		var ar authorizationRequest
		if err = json.Unmarshal(req, &ar); err != nil {
			// AUDIT[ Improper Messages ] 0786b6f2-91ca-4f48-9883-842a19084c64
			improperMessage(MethodNewAuthorization, err, req)
			return
		}

		authz, err := impl.NewAuthorization(ar.Authz, ar.RegID)
		if err != nil {
			return
		}

		response, err = json.Marshal(authz)
		if err != nil {
			// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
			errorCondition(MethodNewAuthorization, err, req)
			return
		}
		return
	})

	rpc.Handle(MethodNewCertificate, func(req []byte) (response []byte, err error) {
		log.Info(fmt.Sprintf(" [.] Entering MethodNewCertificate"))
		var cr certificateRequest
		if err = json.Unmarshal(req, &cr); err != nil {
			// AUDIT[ Improper Messages ] 0786b6f2-91ca-4f48-9883-842a19084c64
			improperMessage(MethodNewCertificate, err, req)
			return
		}
		log.Info(fmt.Sprintf(" [.] No problem unmarshaling request"))

		cert, err := impl.NewCertificate(cr.Req, cr.RegID)
		if err != nil {
			return
		}
		log.Info(fmt.Sprintf(" [.] No problem issuing new cert"))

		response, err = json.Marshal(cert)
		if err != nil {
			// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
			errorCondition(MethodNewCertificate, err, req)
			return
		}
		return
	})

	rpc.Handle(MethodUpdateRegistration, func(req []byte) (response []byte, err error) {
		var urReq updateRegistrationRequest
		err = json.Unmarshal(req, &urReq)
		if err != nil {
			// AUDIT[ Improper Messages ] 0786b6f2-91ca-4f48-9883-842a19084c64
			improperMessage(MethodUpdateRegistration, err, req)
			return
		}

		reg, err := impl.UpdateRegistration(urReq.Base, urReq.Update)
		if err != nil {
			return
		}

		response, err = json.Marshal(reg)
		if err != nil {
			// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
			errorCondition(MethodUpdateRegistration, err, req)
			return
		}
		return
	})

	rpc.Handle(MethodUpdateAuthorization, func(req []byte) (response []byte, err error) {
		var uaReq updateAuthorizationRequest
		err = json.Unmarshal(req, &uaReq)
		if err != nil {
			// AUDIT[ Improper Messages ] 0786b6f2-91ca-4f48-9883-842a19084c64
			improperMessage(MethodUpdateAuthorization, err, req)
			return
		}

		newAuthz, err := impl.UpdateAuthorization(uaReq.Authz, uaReq.Index, uaReq.Response)
		if err != nil {
			return
		}

		response, err = json.Marshal(newAuthz)
		if err != nil {
			// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
			errorCondition(MethodUpdateAuthorization, err, req)
			return
		}
		return
	})

	rpc.Handle(MethodRevokeCertificate, func(req []byte) (response []byte, err error) {
		certs, err := x509.ParseCertificates(req)
		if err != nil || len(certs) == 0 {
			// AUDIT[ Improper Messages ] 0786b6f2-91ca-4f48-9883-842a19084c64
			improperMessage(MethodRevokeCertificate, err, req)
			return
		}

		err = impl.RevokeCertificate(*certs[0])
		return
	})

	rpc.Handle(MethodOnValidationUpdate, func(req []byte) (response []byte, err error) {
		var authz core.Authorization
		if err = json.Unmarshal(req, &authz); err != nil {
			// AUDIT[ Improper Messages ] 0786b6f2-91ca-4f48-9883-842a19084c64
			improperMessage(MethodOnValidationUpdate, err, req)
			return
		}

		err = impl.OnValidationUpdate(authz)
		return
	})

	return nil
}

// RegistrationAuthorityClient represents an RA RPC client
type RegistrationAuthorityClient struct {
	rpc RPCClient
}

// NewRegistrationAuthorityClient constructs an RPC client
func NewRegistrationAuthorityClient(client RPCClient) (rac RegistrationAuthorityClient, err error) {
	rac = RegistrationAuthorityClient{rpc: client}
	return
}

// NewRegistration sends a New Registration request
func (rac RegistrationAuthorityClient) NewRegistration(reg core.Registration) (newReg core.Registration, err error) {
	data, err := json.Marshal(registrationRequest{reg})
	if err != nil {
		return
	}

	newRegData, err := rac.rpc.DispatchSync(MethodNewRegistration, data)
	if err != nil {
		return
	}

	err = json.Unmarshal(newRegData, &newReg)
	return
}

// NewAuthorization sends a New Authorization request
func (rac RegistrationAuthorityClient) NewAuthorization(authz core.Authorization, regID int64) (newAuthz core.Authorization, err error) {
	data, err := json.Marshal(authorizationRequest{authz, regID})
	if err != nil {
		return
	}

	newAuthzData, err := rac.rpc.DispatchSync(MethodNewAuthorization, data)
	if err != nil {
		return
	}

	err = json.Unmarshal(newAuthzData, &newAuthz)
	return
}

// NewCertificate sends a New Certificate request
func (rac RegistrationAuthorityClient) NewCertificate(cr core.CertificateRequest, regID int64) (cert core.Certificate, err error) {
	data, err := json.Marshal(certificateRequest{cr, regID})
	if err != nil {
		return
	}

	certData, err := rac.rpc.DispatchSync(MethodNewCertificate, data)
	if err != nil {
		return
	}

	err = json.Unmarshal(certData, &cert)
	return
}

// UpdateRegistration sends an Update Registration request
func (rac RegistrationAuthorityClient) UpdateRegistration(base core.Registration, update core.Registration) (newReg core.Registration, err error) {
	var urReq updateRegistrationRequest
	urReq.Base = base
	urReq.Update = update

	data, err := json.Marshal(urReq)
	if err != nil {
		return
	}

	newRegData, err := rac.rpc.DispatchSync(MethodUpdateRegistration, data)
	if err != nil {
		return
	}

	err = json.Unmarshal(newRegData, &newReg)
	return
}

// UpdateAuthorization sends an Update Authorization request
func (rac RegistrationAuthorityClient) UpdateAuthorization(authz core.Authorization, index int, response core.Challenge) (newAuthz core.Authorization, err error) {
	var uaReq updateAuthorizationRequest
	uaReq.Authz = authz
	uaReq.Index = index
	uaReq.Response = response

	data, err := json.Marshal(uaReq)
	if err != nil {
		return
	}

	newAuthzData, err := rac.rpc.DispatchSync(MethodUpdateAuthorization, data)
	if err != nil {
		return
	}

	err = json.Unmarshal(newAuthzData, &newAuthz)
	return
}

// RevokeCertificate sends a Revoke Certificate request
func (rac RegistrationAuthorityClient) RevokeCertificate(cert x509.Certificate) (err error) {
	_, err = rac.rpc.DispatchSync(MethodRevokeCertificate, cert.Raw)
	return
}

// OnValidationUpdate senda a notice that a validation has updated
func (rac RegistrationAuthorityClient) OnValidationUpdate(authz core.Authorization) (err error) {
	data, err := json.Marshal(authz)
	if err != nil {
		return
	}

	_, err = rac.rpc.DispatchSync(MethodOnValidationUpdate, data)
	return
}

// NewValidationAuthorityServer constructs an RPC server
//
// ValidationAuthorityClient / Server
//  -> UpdateValidations
func NewValidationAuthorityServer(rpc RPCServer, impl core.ValidationAuthority) (err error) {
	rpc.Handle(MethodUpdateValidations, func(req []byte) (response []byte, err error) {
		var vaReq validationRequest
		if err = json.Unmarshal(req, &vaReq); err != nil {
			// AUDIT[ Improper Messages ] 0786b6f2-91ca-4f48-9883-842a19084c64
			improperMessage(MethodUpdateValidations, err, req)
			return
		}

		err = impl.UpdateValidations(vaReq.Authz, vaReq.Index, vaReq.Key)
		return
	})

	rpc.Handle(MethodCheckCAARecords, func(req []byte) (response []byte, err error) {
		var caaReq caaRequest
		if err = json.Unmarshal(req, &caaReq); err != nil {
			// AUDIT[ Improper Messages ] 0786b6f2-91ca-4f48-9883-842a19084c64
			improperMessage(MethodCheckCAARecords, err, req)
			return
		}

		present, valid, err := impl.CheckCAARecords(caaReq.Ident)
		if err != nil {
			return
		}

		var caaResp caaResponse
		caaResp.Present = present
		caaResp.Valid = valid
		caaResp.Err = err
		response, err = json.Marshal(caaResp)
		if err != nil {
			// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
			errorCondition(MethodCheckCAARecords, err, caaReq)
			return
		}
		return
	})

	return nil
}

// ValidationAuthorityClient represents an RPC client for the VA
type ValidationAuthorityClient struct {
	rpc RPCClient
}

// NewValidationAuthorityClient constructs an RPC client
func NewValidationAuthorityClient(client RPCClient) (vac ValidationAuthorityClient, err error) {
	vac = ValidationAuthorityClient{rpc: client}
	return
}

// UpdateValidations sends an Update Validations request
func (vac ValidationAuthorityClient) UpdateValidations(authz core.Authorization, index int, key jose.JsonWebKey) error {
	vaReq := validationRequest{
		Authz: authz,
		Index: index,
		Key:   key,
	}
	data, err := json.Marshal(vaReq)
	if err != nil {
		return err
	}

	_, err = vac.rpc.DispatchSync(MethodUpdateValidations, data)
	return nil
}

// CheckCAARecords sends a request to check CAA records
func (vac ValidationAuthorityClient) CheckCAARecords(ident core.AcmeIdentifier) (present bool, valid bool, err error) {
	var caaReq caaRequest
	caaReq.Ident = ident
	data, err := json.Marshal(caaReq)
	if err != nil {
		return
	}

	jsonResp, err := vac.rpc.DispatchSync(MethodCheckCAARecords, data)
	if err != nil {
		return
	}

	var caaResp caaResponse

	err = json.Unmarshal(jsonResp, &caaResp)
	if err != nil {
		return
	}
	present = caaResp.Present
	valid = caaResp.Valid
	return
}

// NewCertificateAuthorityServer constructs an RPC server
//
// CertificateAuthorityClient / Server
//  -> IssueCertificate
func NewCertificateAuthorityServer(rpc RPCServer, impl core.CertificateAuthority) (err error) {
	rpc.Handle(MethodIssueCertificate, func(req []byte) (response []byte, err error) {
		var icReq issueCertificateRequest
		err = json.Unmarshal(req, &icReq)
		if err != nil {
			// AUDIT[ Improper Messages ] 0786b6f2-91ca-4f48-9883-842a19084c64
			improperMessage(MethodIssueCertificate, err, req)
			return
		}

		csr, err := x509.ParseCertificateRequest(icReq.Bytes)
		if err != nil {
			// AUDIT[ Improper Messages ] 0786b6f2-91ca-4f48-9883-842a19084c64
			improperMessage(MethodIssueCertificate, err, req)
			return
		}

		cert, err := impl.IssueCertificate(*csr, icReq.RegID, icReq.EarliestExpiry)
		if err != nil {
			return
		}

		response, err = json.Marshal(cert)
		if err != nil {
			// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
			errorCondition(MethodGetRegistration, err, req)
			return
		}

		return
	})

	rpc.Handle(MethodRevokeCertificate, func(req []byte) (response []byte, err error) {
		var revokeReq revokeCertificateRequest
		err = json.Unmarshal(req, &revokeReq)
		if err != nil {
			// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
			errorCondition(MethodRevokeCertificate, err, req)
			return
		}

		err = impl.RevokeCertificate(revokeReq.Serial, revokeReq.ReasonCode)
		return
	})

	rpc.Handle(MethodGenerateOCSP, func(req []byte) (response []byte, err error) {
		var xferObj core.OCSPSigningRequest
		err = json.Unmarshal(req, &xferObj)
		if err != nil {
			// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
			errorCondition(MethodGenerateOCSP, err, req)
			return
		}

		response, err = impl.GenerateOCSP(xferObj)
		if err != nil {
			return
		}

		return
	})

	return nil
}

// CertificateAuthorityClient is a client to communicate with the CA.
type CertificateAuthorityClient struct {
	rpc RPCClient
}

// NewCertificateAuthorityClient constructs an RPC client
func NewCertificateAuthorityClient(client RPCClient) (cac CertificateAuthorityClient, err error) {
	cac = CertificateAuthorityClient{rpc: client}
	return
}

// IssueCertificate sends a request to issue a certificate
func (cac CertificateAuthorityClient) IssueCertificate(csr x509.CertificateRequest, regID int64, earliestExpiry time.Time) (cert core.Certificate, err error) {
	var icReq issueCertificateRequest
	icReq.Bytes = csr.Raw
	icReq.RegID = regID
	data, err := json.Marshal(icReq)
	if err != nil {
		return
	}

	jsonResponse, err := cac.rpc.DispatchSync(MethodIssueCertificate, data)
	if err != nil {
		return
	}

	err = json.Unmarshal(jsonResponse, &cert)
	return
}

// RevokeCertificate sends a request to revoke a certificate
func (cac CertificateAuthorityClient) RevokeCertificate(serial string, reasonCode int) (err error) {
	var revokeReq revokeCertificateRequest
	revokeReq.Serial = serial
	revokeReq.ReasonCode = reasonCode

	data, err := json.Marshal(revokeReq)
	if err != nil {
		// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
		errorCondition(MethodRevokeCertificate, err, revokeReq)
		return
	}

	_, err = cac.rpc.DispatchSync(MethodRevokeCertificate, data)
	return
}

// GenerateOCSP sends a request to generate an OCSP response
func (cac CertificateAuthorityClient) GenerateOCSP(signRequest core.OCSPSigningRequest) (resp []byte, err error) {
	data, err := json.Marshal(signRequest)
	if err != nil {
		// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
		errorCondition(MethodGenerateOCSP, err, signRequest)
		return
	}

	resp, err = cac.rpc.DispatchSync(MethodGenerateOCSP, data)
	if err != nil {
		return
	}
	if len(resp) < 1 {
		err = fmt.Errorf("Failure at Signer")
		return
	}
	return
}

// NewStorageAuthorityServer constructs an RPC server
func NewStorageAuthorityServer(rpc RPCServer, impl core.StorageAuthority) error {
	rpc.Handle(MethodUpdateRegistration, func(req []byte) (response []byte, err error) {
		var reg core.Registration
		if err = json.Unmarshal(req, &reg); err != nil {
			// AUDIT[ Improper Messages ] 0786b6f2-91ca-4f48-9883-842a19084c64
			improperMessage(MethodUpdateRegistration, err, req)
			return
		}

		err = impl.UpdateRegistration(reg)
		return
	})

	rpc.Handle(MethodGetRegistration, func(req []byte) (response []byte, err error) {
		var grReq getRegistrationRequest
		err = json.Unmarshal(req, &grReq)
		if err != nil {
			// AUDIT[ Improper Messages ] 0786b6f2-91ca-4f48-9883-842a19084c64
			improperMessage(MethodGetRegistration, err, req)
			return
		}

		reg, err := impl.GetRegistration(grReq.ID)
		if err != nil {
			return
		}

		response, err = json.Marshal(reg)
		if err != nil {
			// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
			errorCondition(MethodGetRegistration, err, req)
			return
		}
		return
	})

	rpc.Handle(MethodGetRegistrationByKey, func(req []byte) (response []byte, err error) {
		var jwk jose.JsonWebKey
		if err = json.Unmarshal(req, &jwk); err != nil {
			// AUDIT[ Improper Messages ] 0786b6f2-91ca-4f48-9883-842a19084c64
			improperMessage(MethodGetRegistrationByKey, err, req)
			return
		}

		reg, err := impl.GetRegistrationByKey(jwk)
		if err != nil {
			return
		}

		response, err = json.Marshal(reg)
		if err != nil {
			// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
			errorCondition(MethodGetRegistrationByKey, err, req)
			return
		}
		return
	})

	rpc.Handle(MethodGetAuthorization, func(req []byte) (response []byte, err error) {
		authz, err := impl.GetAuthorization(string(req))
		if err != nil {
			return
		}

		response, err = json.Marshal(authz)
		if err != nil {
			// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
			errorCondition(MethodGetAuthorization, err, req)
			return
		}
		return
	})

	rpc.Handle(MethodGetLatestValidAuthorization, func(req []byte) (response []byte, err error) {
		var lvar latestValidAuthorizationRequest
		if err = json.Unmarshal(req, &lvar); err != nil {
			// AUDIT[ Improper Messages ] 0786b6f2-91ca-4f48-9883-842a19084c64
			improperMessage(MethodNewAuthorization, err, req)
			return
		}

		authz, err := impl.GetLatestValidAuthorization(lvar.RegID, lvar.Identifier)
		if err != nil {
			return
		}

		response, err = json.Marshal(authz)
		if err != nil {
			// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
			errorCondition(MethodGetLatestValidAuthorization, err, req)
			return
		}
		return
	})

	rpc.Handle(MethodAddCertificate, func(req []byte) (response []byte, err error) {
		var acReq addCertificateRequest
		err = json.Unmarshal(req, &acReq)
		if err != nil {
			// AUDIT[ Improper Messages ] 0786b6f2-91ca-4f48-9883-842a19084c64
			improperMessage(MethodAddCertificate, err, req)
			return
		}

		id, err := impl.AddCertificate(acReq.Bytes, acReq.RegID)
		if err != nil {
			return
		}
		response = []byte(id)
		return
	})

	rpc.Handle(MethodNewRegistration, func(req []byte) (response []byte, err error) {
		var registration core.Registration
		err = json.Unmarshal(req, &registration)
		if err != nil {
			// AUDIT[ Improper Messages ] 0786b6f2-91ca-4f48-9883-842a19084c64
			improperMessage(MethodNewRegistration, err, req)
			return
		}

		output, err := impl.NewRegistration(registration)
		if err != nil {
			return
		}

		response, err = json.Marshal(output)
		if err != nil {
			// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
			errorCondition(MethodNewRegistration, err, req)
			return
		}
		return
	})

	rpc.Handle(MethodNewPendingAuthorization, func(req []byte) (response []byte, err error) {
		var authz core.Authorization
		if err = json.Unmarshal(req, &authz); err != nil {
			// AUDIT[ Improper Messages ] 0786b6f2-91ca-4f48-9883-842a19084c64
			improperMessage(MethodNewPendingAuthorization, err, req)
			return
		}

		output, err := impl.NewPendingAuthorization(authz)
		if err != nil {
			return
		}

		response, err = json.Marshal(output)
		if err != nil {
			// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
			errorCondition(MethodNewPendingAuthorization, err, req)
			return
		}
		return
	})

	rpc.Handle(MethodUpdatePendingAuthorization, func(req []byte) (response []byte, err error) {
		var authz core.Authorization
		if err = json.Unmarshal(req, &authz); err != nil {
			// AUDIT[ Improper Messages ] 0786b6f2-91ca-4f48-9883-842a19084c64
			improperMessage(MethodUpdatePendingAuthorization, err, req)
			return
		}

		err = impl.UpdatePendingAuthorization(authz)
		return
	})

	rpc.Handle(MethodFinalizeAuthorization, func(req []byte) (response []byte, err error) {
		var authz core.Authorization
		if err = json.Unmarshal(req, &authz); err != nil {
			// AUDIT[ Improper Messages ] 0786b6f2-91ca-4f48-9883-842a19084c64
			improperMessage(MethodFinalizeAuthorization, err, req)
			return
		}

		err = impl.FinalizeAuthorization(authz)
		return
	})

	rpc.Handle(MethodGetCertificate, func(req []byte) (response []byte, err error) {
		cert, err := impl.GetCertificate(string(req))
		if err != nil {
			return
		}

		jsonResponse, err := json.Marshal(cert)
		if err != nil {
			// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
			errorCondition(MethodGetCertificate, err, req)
			return
		}

		return jsonResponse, nil
	})

	rpc.Handle(MethodGetCertificateByShortSerial, func(req []byte) (response []byte, err error) {
		cert, err := impl.GetCertificateByShortSerial(string(req))
		if err != nil {
			return
		}

		jsonResponse, err := json.Marshal(cert)
		if err != nil {
			// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
			errorCondition(MethodGetCertificateByShortSerial, err, req)
			return
		}

		return jsonResponse, nil
	})

	rpc.Handle(MethodGetCertificateStatus, func(req []byte) (response []byte, err error) {
		status, err := impl.GetCertificateStatus(string(req))
		if err != nil {
			return
		}

		response, err = json.Marshal(status)
		if err != nil {
			// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
			errorCondition(MethodGetCertificateStatus, err, req)
			return
		}
		return
	})

	rpc.Handle(MethodMarkCertificateRevoked, func(req []byte) (response []byte, err error) {
		var mcrReq markCertificateRevokedRequest

		if err = json.Unmarshal(req, &mcrReq); err != nil {
			// AUDIT[ Improper Messages ] 0786b6f2-91ca-4f48-9883-842a19084c64
			improperMessage(MethodMarkCertificateRevoked, err, req)
			return
		}

		err = impl.MarkCertificateRevoked(mcrReq.Serial, mcrReq.OCSPResponse, mcrReq.ReasonCode)
		return
	})

	rpc.Handle(MethodUpdateOCSP, func(req []byte) (response []byte, err error) {
		var updateOCSPReq updateOCSPRequest

		if err = json.Unmarshal(req, &updateOCSPReq); err != nil {
			// AUDIT[ Improper Messages ] 0786b6f2-91ca-4f48-9883-842a19084c64
			improperMessage(MethodUpdateOCSP, err, req)
			return
		}

		err = impl.UpdateOCSP(updateOCSPReq.Serial, updateOCSPReq.OCSPResponse)
		return
	})

	rpc.Handle(MethodAlreadyDeniedCSR, func(req []byte) (response []byte, err error) {
		var adcReq alreadyDeniedCSRReq

		err = json.Unmarshal(req, &adcReq)
		if err != nil {
			// AUDIT[ Improper Messages ] 0786b6f2-91ca-4f48-9883-842a19084c64
			improperMessage(MethodAlreadyDeniedCSR, err, req)
			return
		}

		exists, err := impl.AlreadyDeniedCSR(adcReq.Names)
		if err != nil {
			return
		}

		if exists {
			response = []byte{1}
		} else {
			response = []byte{0}
		}
		return
	})

	return nil
}

// StorageAuthorityClient is a client to communicate with the Storage Authority
type StorageAuthorityClient struct {
	rpc RPCClient
}

// NewStorageAuthorityClient constructs an RPC client
func NewStorageAuthorityClient(client RPCClient) (sac StorageAuthorityClient, err error) {
	sac = StorageAuthorityClient{rpc: client}
	return
}

// GetRegistration sends a request to get a registration by ID
func (cac StorageAuthorityClient) GetRegistration(id int64) (reg core.Registration, err error) {
	var grReq getRegistrationRequest
	grReq.ID = id

	data, err := json.Marshal(grReq)
	if err != nil {
		return
	}

	jsonReg, err := cac.rpc.DispatchSync(MethodGetRegistration, data)
	if err != nil {
		return
	}

	err = json.Unmarshal(jsonReg, &reg)
	return
}

// GetRegistrationByKey sends a request to get a registration by JWK
func (cac StorageAuthorityClient) GetRegistrationByKey(key jose.JsonWebKey) (reg core.Registration, err error) {
	jsonKey, err := key.MarshalJSON()
	if err != nil {
		return
	}

	jsonReg, err := cac.rpc.DispatchSync(MethodGetRegistrationByKey, jsonKey)
	if err != nil {
		return
	}

	err = json.Unmarshal(jsonReg, &reg)
	return
}

// GetAuthorization sends a request to get an Authorization by ID
func (cac StorageAuthorityClient) GetAuthorization(id string) (authz core.Authorization, err error) {
	jsonAuthz, err := cac.rpc.DispatchSync(MethodGetAuthorization, []byte(id))
	if err != nil {
		return
	}

	err = json.Unmarshal(jsonAuthz, &authz)
	return
}

// GetLatestValidAuthorization sends a request to get an Authorization by RegID, Identifier
func (cac StorageAuthorityClient) GetLatestValidAuthorization(registrationId int64, identifier core.AcmeIdentifier) (authz core.Authorization, err error) {

	var lvar latestValidAuthorizationRequest
	lvar.RegID = registrationId
	lvar.Identifier = identifier

	data, err := json.Marshal(lvar)
	if err != nil {
		return
	}

	jsonAuthz, err := cac.rpc.DispatchSync(MethodGetLatestValidAuthorization, data)
	if err != nil {
		return
	}

	err = json.Unmarshal(jsonAuthz, &authz)
	return
}

// GetCertificate sends a request to get a Certificate by ID
func (cac StorageAuthorityClient) GetCertificate(id string) (cert core.Certificate, err error) {
	jsonCert, err := cac.rpc.DispatchSync(MethodGetCertificate, []byte(id))
	if err != nil {
		return
	}

	err = json.Unmarshal(jsonCert, &cert)
	return
}

// GetCertificateByShortSerial sends a request to search for a certificate by
// the predictable portion of its serial number.
func (cac StorageAuthorityClient) GetCertificateByShortSerial(id string) (cert core.Certificate, err error) {
	jsonCert, err := cac.rpc.DispatchSync(MethodGetCertificateByShortSerial, []byte(id))
	if err != nil {
		return
	}

	err = json.Unmarshal(jsonCert, &cert)
	return
}

// GetCertificateStatus sends a request to obtain the current status of a
// certificate by ID
func (cac StorageAuthorityClient) GetCertificateStatus(id string) (status core.CertificateStatus, err error) {
	jsonStatus, err := cac.rpc.DispatchSync(MethodGetCertificateStatus, []byte(id))
	if err != nil {
		return
	}

	err = json.Unmarshal(jsonStatus, &status)
	return
}

// MarkCertificateRevoked sends a request to mark a certificate as revoked
func (cac StorageAuthorityClient) MarkCertificateRevoked(serial string, ocspResponse []byte, reasonCode int) (err error) {
	var mcrReq markCertificateRevokedRequest

	mcrReq.Serial = serial
	mcrReq.OCSPResponse = ocspResponse
	mcrReq.ReasonCode = reasonCode

	data, err := json.Marshal(mcrReq)
	if err != nil {
		return
	}

	_, err = cac.rpc.DispatchSync(MethodMarkCertificateRevoked, data)
	return
}

// UpdateOCSP sends a request to store an updated OCSP response
func (cac StorageAuthorityClient) UpdateOCSP(serial string, ocspResponse []byte) (err error) {
	var updateOCSPReq updateOCSPRequest

	updateOCSPReq.Serial = serial
	updateOCSPReq.OCSPResponse = ocspResponse

	data, err := json.Marshal(updateOCSPReq)
	if err != nil {
		return
	}

	_, err = cac.rpc.DispatchSync(MethodUpdateOCSP, data)
	return
}

// UpdateRegistration sends a request to store an updated registration
func (cac StorageAuthorityClient) UpdateRegistration(reg core.Registration) (err error) {
	jsonReg, err := json.Marshal(reg)
	if err != nil {
		return
	}

	_, err = cac.rpc.DispatchSync(MethodUpdateRegistration, jsonReg)
	return
}

// NewRegistration sends a request to store a new registration
func (cac StorageAuthorityClient) NewRegistration(reg core.Registration) (output core.Registration, err error) {
	jsonReg, err := json.Marshal(reg)
	if err != nil {
		err = errors.New("NewRegistration RPC failed")
		return
	}
	response, err := cac.rpc.DispatchSync(MethodNewRegistration, jsonReg)
	if err != nil {
		return
	}
	err = json.Unmarshal(response, &output)
	if err != nil {
		err = errors.New("NewRegistration RPC failed")
		return
	}
	return output, nil
}

// NewPendingAuthorization sends a request to store a pending authorization
func (cac StorageAuthorityClient) NewPendingAuthorization(authz core.Authorization) (output core.Authorization, err error) {
	jsonAuthz, err := json.Marshal(authz)
	if err != nil {
		return
	}
	response, err := cac.rpc.DispatchSync(MethodNewPendingAuthorization, jsonAuthz)
	if err != nil {
		return
	}
	err = json.Unmarshal(response, &output)
	if err != nil {
		err = errors.New("NewRegistration RPC failed")
		return
	}
	return
}

// UpdatePendingAuthorization sends a request to update the data in a pending
// authorization
func (cac StorageAuthorityClient) UpdatePendingAuthorization(authz core.Authorization) (err error) {
	jsonAuthz, err := json.Marshal(authz)
	if err != nil {
		return
	}

	_, err = cac.rpc.DispatchSync(MethodUpdatePendingAuthorization, jsonAuthz)
	return
}

// FinalizeAuthorization sends a request to finalize an authorization (convert
// from pending)
func (cac StorageAuthorityClient) FinalizeAuthorization(authz core.Authorization) (err error) {
	jsonAuthz, err := json.Marshal(authz)
	if err != nil {
		return
	}

	_, err = cac.rpc.DispatchSync(MethodFinalizeAuthorization, jsonAuthz)
	return
}

// AddCertificate sends a request to record the issuance of a certificate
func (cac StorageAuthorityClient) AddCertificate(cert []byte, regID int64) (id string, err error) {
	var acReq addCertificateRequest
	acReq.Bytes = cert
	acReq.RegID = regID
	data, err := json.Marshal(acReq)
	if err != nil {
		return
	}

	response, err := cac.rpc.DispatchSync(MethodAddCertificate, data)
	if err != nil {
		return
	}
	id = string(response)
	return
}

// AlreadyDeniedCSR sends a request to search for denied names
func (cac StorageAuthorityClient) AlreadyDeniedCSR(names []string) (exists bool, err error) {
	var adcReq alreadyDeniedCSRReq
	adcReq.Names = names

	data, err := json.Marshal(adcReq)
	if err != nil {
		return
	}

	response, err := cac.rpc.DispatchSync(MethodAlreadyDeniedCSR, data)
	if err != nil {
		return
	}

	switch response[0] {
	case 0:
		exists = false
	case 1:
		exists = true
	}
	return
}
