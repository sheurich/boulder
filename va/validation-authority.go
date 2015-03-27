// Copyright 2014 ISRG.  All rights reserved
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package va

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
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

func NewValidationAuthorityImpl(logger *blog.AuditLogger, tm bool) ValidationAuthorityImpl {
	logger.Notice("Validation Authority Starting")
	return ValidationAuthorityImpl{log: logger, TestMode: tm}
}

// Validation methods

func (va ValidationAuthorityImpl) validateSimpleHTTPS(identifier core.AcmeIdentifier, input core.Challenge) (challenge core.Challenge) {
	challenge = input

	if len(challenge.Path) == 0 {
		challenge.Status = core.StatusInvalid
		return
	}

	url := ""
	if va.TestMode {
		url = fmt.Sprintf("http://localhost:5001/.well-known/acme-challenge/%s", challenge.Path)
	} else {
		url = fmt.Sprintf("https://%s/.well-known/acme-challenge/%s", identifier, challenge.Path)
	}

	httpRequest, err := http.NewRequest("GET", url, nil)
	if err != nil {
		challenge.Status = core.StatusInvalid
		return
	}

	httpRequest.Host = identifier.Value
	client := http.Client{Timeout: 5 * time.Second}
	httpResponse, err := client.Do(httpRequest)

	if err == nil && httpResponse.StatusCode == 200 {
		// Read body & test
		body, err := ioutil.ReadAll(httpResponse.Body)
		if err != nil {
			challenge.Status = core.StatusInvalid
			return
		}

		if subtle.ConstantTimeCompare(body, []byte(challenge.Token)) == 1 {
			challenge.Status = core.StatusValid
			return
		}
	}

	challenge.Status = core.StatusInvalid
	return
}

func (va ValidationAuthorityImpl) validateDvsni(identifier core.AcmeIdentifier, input core.Challenge) (challenge core.Challenge) {
	challenge = input

	const DVSNI_SUFFIX = ".acme.invalid"
	nonceName := challenge.Nonce + DVSNI_SUFFIX

	R, err := core.B64dec(challenge.R)
	if err != nil {
		challenge.Status = core.StatusInvalid
		return
	}
	S, err := core.B64dec(challenge.S)
	if err != nil {
		challenge.Status = core.StatusInvalid
		return
	}
	RS := append(R, S...)

	sha := sha256.New()
	_, _ = sha.Write(RS) // Never returns an error
	z := make([]byte, sha.Size())
	sha.Sum(z)
	zName := hex.EncodeToString(z)

	// Make a connection with SNI = nonceName

	hostPort := ""
	if va.TestMode {
		hostPort = "localhost:5001"
	} else {
		if identifier.Type != "dns" {
			challenge.Status = core.StatusInvalid
			return
		}
		hostPort = identifier.Value + ":443"
	}
	conn, err := tls.Dial("tcp", hostPort, &tls.Config{
		ServerName:         nonceName,
		InsecureSkipVerify: true,
	})

	if err != nil {
		challenge.Status = core.StatusInvalid
		return
	}

	// Check that zName is a dNSName SAN in the server's certificate
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		challenge.Status = core.StatusInvalid
		return
	}
	for _, name := range certs[0].DNSNames {
		if name == zName {
			challenge.Status = core.StatusValid
			return
		}
	}

	challenge.Status = core.StatusInvalid
	return
}

// Overall validation process

func (va ValidationAuthorityImpl) validate(authz core.Authorization) {
	// Select the first supported validation method
	// XXX: Remove the "break" lines to process all supported validations
	for i, challenge := range authz.Challenges {
		switch challenge.Type {
		case core.ChallengeTypeSimpleHTTPS:
			authz.Challenges[i] = va.validateSimpleHTTPS(authz.Identifier, challenge)
			break
		case core.ChallengeTypeDVSNI:
			authz.Challenges[i] = va.validateDvsni(authz.Identifier, challenge)
			break
		}
	}

	va.log.Notice(fmt.Sprintf("Validations: %v", authz))

	va.RA.OnValidationUpdate(authz)
}

func (va ValidationAuthorityImpl) UpdateValidations(authz core.Authorization) error {
	go va.validate(authz)
	return nil
}
