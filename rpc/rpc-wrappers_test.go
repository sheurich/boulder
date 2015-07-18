// Copyright 2015 ISRG.  All rights reserved
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package rpc

import (
	"encoding/json"
	"testing"
	"time"

	jose "github.com/letsencrypt/boulder/Godeps/_workspace/src/github.com/square/go-jose"

	"github.com/letsencrypt/boulder/core"
	"github.com/letsencrypt/boulder/test"
)

const JWK1JSON = `{
  "kty": "RSA",
  "n": "vuc785P8lBj3fUxyZchF_uZw6WtbxcorqgTyq-qapF5lrO1U82Tp93rpXlmctj6fyFHBVVB5aXnUHJ7LZeVPod7Wnfl8p5OyhlHQHC8BnzdzCqCMKmWZNX5DtETDId0qzU7dPzh0LP0idt5buU7L9QNaabChw3nnaL47iu_1Di5Wp264p2TwACeedv2hfRDjDlJmaQXuS8Rtv9GnRWyC9JBu7XmGvGDziumnJH7Hyzh3VNu-kSPQD3vuAFgMZS6uUzOztCkT0fpOalZI6hqxtWLvXUMj-crXrn-Maavz8qRhpAyp5kcYk3jiHGgQIi7QSK2JIdRJ8APyX9HlmTN5AQ",
  "e": "AAEAAQ"
}`

type MockRPCClient struct {
	LastMethod string
	LastBody   []byte
	NextResp   []byte
	NextErr    error
}

func (rpc *MockRPCClient) SetTimeout(ttl time.Duration) {
}

func (rpc *MockRPCClient) Dispatch(method string, body []byte) chan []byte {
	rpc.LastMethod = method
	rpc.LastBody = body

	rsp := make(chan []byte)
	rsp <- body
	return rsp
}

func (rpc *MockRPCClient) DispatchSync(method string, body []byte) (response []byte, err error) {
	rpc.LastMethod = method
	rpc.LastBody = body
	response = body

	if rpc.NextResp != nil {
		response = rpc.NextResp
		rpc.NextResp = nil
	}

	if rpc.NextErr != nil {
		err = rpc.NextErr
		rpc.NextErr = nil
	}

	return
}

func TestRANewRegistration(t *testing.T) {
	mock := &MockRPCClient{}
	client, err := NewRegistrationAuthorityClient(mock)
	test.AssertNotError(t, err, "Client construction")
	test.AssertNotNil(t, client, "Client construction")

	var jwk jose.JsonWebKey
	json.Unmarshal([]byte(JWK1JSON), &jwk)

	reg := core.Registration{
		ID:  1,
		Key: jwk,
	}

	_, err = client.NewRegistration(reg)
	test.AssertNotError(t, err, "Updated Registration")
	test.Assert(t, len(mock.LastBody) > 0, "Didn't send Registration")
	test.AssertEquals(t, "NewRegistration", mock.LastMethod)

	t.Logf("LastMethod: %v", mock.LastMethod)
	t.Logf("LastBody: %v", mock.LastBody)
}

func TestGenerateOCSP(t *testing.T) {
	mock := &MockRPCClient{}

	client, err := NewCertificateAuthorityClient(mock)
	test.AssertNotError(t, err, "Client construction")
	test.AssertNotNil(t, client, "Client construction")

	req := core.OCSPSigningRequest{
	// nope
	}

	mock.NextResp = []byte{}
	_, err = client.GenerateOCSP(req)
	test.AssertError(t, err, "Should have failed at signer")
}
