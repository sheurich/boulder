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
)

type ValidationAuthorityImpl struct {
	RA core.RegistrationAuthority
}

func NewValidationAuthorityImpl() ValidationAuthorityImpl {
	return ValidationAuthorityImpl{}
}

// Validation methods

func (va ValidationAuthorityImpl) validateSimpleHTTPS(authz core.Authorization) (challenge core.Challenge) {
	identifier := authz.Identifier.Value

	challenge, ok := authz.Challenges[core.ChallengeTypeSimpleHTTPS]
	if !ok {
		challenge.Status = core.StatusInvalid
		return
	}

	if len(challenge.Path) == 0 {
		challenge.Status = core.StatusInvalid
		return
	}

	// XXX: Local version; uncomment for real version
	url := fmt.Sprintf("http://localhost:5001/.well-known/acme-challenge/%s", challenge.Path)
	//url := fmt.Sprintf("https://%s/.well-known/acme-challenge/%s", identifier, challenge.Path)

	httpRequest, err := http.NewRequest("GET", url, nil)
	if err != nil {
		challenge.Status = core.StatusInvalid
		return
	}

	httpRequest.Host = identifier
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

func (va ValidationAuthorityImpl) validateDvsni(authz core.Authorization) (challenge core.Challenge) {
	// identifier := authz.Identifier.Value // XXX: Local version; uncomment for real version
	challenge, ok := authz.Challenges[core.ChallengeTypeDVSNI]

	if !ok {
		challenge.Status = core.StatusInvalid
		return
	}

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
	hostPort := "localhost:5001"
	//hostPort := identifier + ":443" // XXX: Local version; uncomment for real version
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
	for i := range authz.Challenges {
		switch i {
		case "simpleHTTPS":
			authz.Challenges[i] = va.validateSimpleHTTPS(authz)
			break
		case "dvsni":
			authz.Challenges[i] = va.validateDvsni(authz)
			break
		}
	}

	va.RA.OnValidationUpdate(authz)
}

func (va ValidationAuthorityImpl) UpdateValidations(authz core.Authorization) error {
	go va.validate(authz)
	return nil
}
