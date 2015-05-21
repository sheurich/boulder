// Copyright 2014 ISRG.  All rights reserved
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package va

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/letsencrypt/boulder/core"
	blog "github.com/letsencrypt/boulder/log"
)

type ValidationAuthorityImpl struct {
	RA       core.RegistrationAuthority
	log      *blog.AuditLogger
	TestMode bool
}

func NewValidationAuthorityImpl(tm bool) ValidationAuthorityImpl {
	logger := blog.GetAuditLogger()
	logger.Notice("Validation Authority Starting")
	return ValidationAuthorityImpl{log: logger, TestMode: tm}
}

// Used for audit logging
type verificationRequestEvent struct {
	ID           string         `json:",omitempty"`
	Requester    int64          `json:",omitempty"`
	Challenge    core.Challenge `json:",omitempty"`
	RequestTime  time.Time      `json:",omitempty"`
	ResponseTime time.Time      `json:",omitempty"`
	Error        string         `json:",omitempty"`
}

// Validation methods

func (va ValidationAuthorityImpl) validateSimpleHTTPS(identifier core.AcmeIdentifier, input core.Challenge) (core.Challenge, error) {
	challenge := input

	if len(challenge.Path) == 0 {
		challenge.Status = core.StatusInvalid
		err := fmt.Errorf("No path provided for SimpleHTTPS challenge.")
		return challenge, err
	}

	if identifier.Type != core.IdentifierDNS {
		challenge.Status = core.StatusInvalid
		err := fmt.Errorf("Identifier type for SimpleHTTPS was not DNS")
		return challenge, err
	}
	hostName := identifier.Value
	protocol := "https"
	if va.TestMode {
		hostName = "localhost:5001"
		protocol = "http"
	}

	url := fmt.Sprintf("%s://%s/.well-known/acme-challenge/%s", protocol, hostName, challenge.Path)

	// AUDIT[ Certificate Requests ] 11917fa4-10ef-4e0d-9105-bacbe7836a3c
	va.log.Audit(fmt.Sprintf("Attempting to validate SimpleHTTPS for %s", url))
	httpRequest, err := http.NewRequest("GET", url, nil)
	if err != nil {
		challenge.Status = core.StatusInvalid
		return challenge, err
	}

	httpRequest.Host = hostName
	tr := &http.Transport{
		// We are talking to a client that does not yet have a certificate,
		// so we accept a temporary, invalid one. TODO: We may want to change this
		// to just be over HTTP.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		// We don't expect to make multiple requests to a client, so close
		// connection immediately.
		DisableKeepAlives: true,
	}
	client := http.Client{
		Transport: tr,
		Timeout:   5 * time.Second,
	}
	httpResponse, err := client.Do(httpRequest)

	if err == nil && httpResponse.StatusCode == 200 {
		// Read body & test
		body, readErr := ioutil.ReadAll(httpResponse.Body)
		if readErr != nil {
			challenge.Status = core.StatusInvalid
			return challenge, readErr
		}

		if subtle.ConstantTimeCompare(body, []byte(challenge.Token)) == 1 {
			challenge.Status = core.StatusValid
		} else {
			err = fmt.Errorf("Incorrect token validating SimpleHTTPS for %s", url)
			challenge.Status = core.StatusInvalid
		}
	} else if err != nil {
		va.log.Debug(fmt.Sprintf("Could not connect to %s: %s", url, err.Error()))
		challenge.Status = core.StatusInvalid
	} else {
		err = fmt.Errorf("Invalid response from %s: %d", url, httpResponse.StatusCode)
		challenge.Status = core.StatusInvalid
	}

	return challenge, err
}

func (va ValidationAuthorityImpl) validateDvsni(identifier core.AcmeIdentifier, input core.Challenge) (core.Challenge, error) {
	challenge := input

	if identifier.Type != "dns" {
		err := fmt.Errorf("Identifier type for DVSNI was not DNS")
		challenge.Status = core.StatusInvalid
		return challenge, err
	}

	const DVSNI_SUFFIX = ".acme.invalid"
	nonceName := challenge.Nonce + DVSNI_SUFFIX

	R, err := core.B64dec(challenge.R)
	if err != nil {
		va.log.Debug("Failed to decode R value from DVSNI challenge")
		challenge.Status = core.StatusInvalid
		return challenge, err
	}
	S, err := core.B64dec(challenge.S)
	if err != nil {
		va.log.Debug("Failed to decode S value from DVSNI challenge")
		challenge.Status = core.StatusInvalid
		return challenge, err
	}
	RS := append(R, S...)

	z := sha256.Sum256(RS)
	zName := fmt.Sprintf("%064x.acme.invalid", z)

	// Make a connection with SNI = nonceName

	hostPort := identifier.Value + ":443"
	if va.TestMode {
		hostPort = "localhost:5001"
	}
	va.log.Notice(fmt.Sprintf("Attempting to validate DVSNI for %s %s %s",
		identifier, hostPort, zName))
	conn, err := tls.Dial("tcp", hostPort, &tls.Config{
		ServerName:         nonceName,
		InsecureSkipVerify: true,
	})

	if err != nil {
		va.log.Debug("Failed to connect to host for DVSNI challenge")
		challenge.Status = core.StatusInvalid
		return challenge, err
	}
	defer conn.Close()

	// Check that zName is a dNSName SAN in the server's certificate
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		err = fmt.Errorf("No certs presented for DVSNI challenge")
		challenge.Status = core.StatusInvalid
		return challenge, err
	}
	for _, name := range certs[0].DNSNames {
		if subtle.ConstantTimeCompare([]byte(name), []byte(zName)) == 1 {
			challenge.Status = core.StatusValid
			return challenge, nil
		}
	}

	err = fmt.Errorf("Correct zName not found for DVSNI challenge")
	challenge.Status = core.StatusInvalid
	return challenge, err
}

// Overall validation process

func (va ValidationAuthorityImpl) validate(authz core.Authorization) {

	// Select the first supported validation method
	// XXX: Remove the "break" lines to process all supported validations
	for i, challenge := range authz.Challenges {
		logEvent := verificationRequestEvent{
			ID:          authz.ID,
			Requester:   authz.RegistrationID,
			RequestTime: time.Now(),
		}

		if !challenge.IsSane(true) {
			challenge.Status = core.StatusInvalid
			logEvent.Error = fmt.Sprintf("Challenge failed sanity check.")
			logEvent.Challenge = challenge
		} else {

			var err error

			switch challenge.Type {
			case core.ChallengeTypeSimpleHTTPS:
				authz.Challenges[i], err = va.validateSimpleHTTPS(authz.Identifier, challenge)
				break
			case core.ChallengeTypeDVSNI:
				authz.Challenges[i], err = va.validateDvsni(authz.Identifier, challenge)
				break
			}

			logEvent.Challenge = authz.Challenges[i]
			if err != nil {
				logEvent.Error = err.Error()
			}
		}

		// AUDIT[ Certificate Requests ] 11917fa4-10ef-4e0d-9105-bacbe7836a3c
		va.log.AuditObject("Validation result", logEvent)
	}

	va.log.Notice(fmt.Sprintf("Validations: %+v", authz))

	va.RA.OnValidationUpdate(authz)
}

func (va ValidationAuthorityImpl) UpdateValidations(authz core.Authorization) error {
	go va.validate(authz)
	return nil
}
