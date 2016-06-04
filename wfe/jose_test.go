package wfe

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"testing"

	"github.com/square/go-jose"
)

func TestRejectsNone(t *testing.T) {
	wfe, _ := setupWFE(t)
	_, _, _, prob := wfe.verifyPOST(ctx, newRequestEvent(), makePostRequest(`
		{
			"header": {
				"alg": "none",
				"jwk": {
					"kty": "RSA",
					"n": "vrjT",
					"e": "AQAB"
				}
			},
			"payload": "aGkK",
			"signature": ""
		}
	`), true, "foo")
	if prob == nil {
		t.Fatalf("verifyPOST did not reject JWS with alg: 'none'")
	}
	if prob.Detail != "algorithm 'none' in JWS header not acceptable" {
		t.Fatalf("verifyPOST rejected JWS with alg: 'none', but for wrong reason: %#v", prob)
	}
}

func TestRejectsHS256(t *testing.T) {
	wfe, _ := setupWFE(t)
	_, _, _, prob := wfe.verifyPOST(ctx, newRequestEvent(), makePostRequest(`
		{
			"header": {
				"alg": "HS256",
				"jwk": {
					"kty": "RSA",
					"n": "vrjT",
					"e": "AQAB"
				}
			},
			"payload": "aGkK",
			"signature": ""
		}
	`), true, "foo")
	if prob == nil {
		t.Fatalf("verifyPOST did not reject JWS with alg: 'HS256'")
	}
	expected := "algorithm 'HS256' in JWS header not acceptable"
	if prob.Detail != expected {
		t.Fatalf("verifyPOST rejected JWS with alg: 'none', but for wrong reason: got '%s', wanted %s", prob, expected)
	}
}

func TestCheckAlgorithm(t *testing.T) {
	testCases := []struct {
		key          jose.JsonWebKey
		jws          jose.JsonWebSignature
		expectedErr  string
		expectedStat string
	}{
		{
			jose.JsonWebKey{
				Algorithm: "HS256",
			},
			jose.JsonWebSignature{},
			"no signature algorithms suitable for given key type",
			"WFE.Errors.NoAlgorithmForKey",
		},
		{
			jose.JsonWebKey{
				Key: &rsa.PublicKey{},
			},
			jose.JsonWebSignature{
				Signatures: []jose.Signature{
					{
						Header: jose.JoseHeader{
							Algorithm: "HS256",
						},
					},
				},
			},
			"algorithm 'HS256' in JWS header not acceptable",
			"WFE.Errors.InvalidJWSAlgorithm",
		},
		{
			jose.JsonWebKey{
				Algorithm: "HS256",
				Key:       &rsa.PublicKey{},
			},
			jose.JsonWebSignature{
				Signatures: []jose.Signature{
					{
						Header: jose.JoseHeader{
							Algorithm: "HS256",
						},
					},
				},
			},
			"algorithm 'HS256' in JWS header not acceptable",
			"WFE.Errors.InvalidJWSAlgorithm",
		},
		{
			jose.JsonWebKey{
				Algorithm: "HS256",
				Key:       &rsa.PublicKey{},
			},
			jose.JsonWebSignature{
				Signatures: []jose.Signature{
					{
						Header: jose.JoseHeader{
							Algorithm: "RS256",
						},
					},
				},
			},
			"algorithm 'HS256' on JWK is unacceptable",
			"WFE.Errors.InvalidAlgorithmOnKey",
		},
	}
	for i, tc := range testCases {
		stat, err := checkAlgorithm(&tc.key, &tc.jws)
		if tc.expectedErr != "" && err.Error() != tc.expectedErr {
			t.Errorf("TestCheckAlgorithm %d: Expected '%s', got '%s'", i, tc.expectedErr, err)
		}
		if tc.expectedStat != "" && stat != tc.expectedStat {
			t.Errorf("TestCheckAlgorithm %d: Expected stat '%s', got '%s'", i, tc.expectedStat, stat)
		}
	}
}

func TestCheckAlgorithmSuccess(t *testing.T) {
	_, err := checkAlgorithm(&jose.JsonWebKey{
		Algorithm: "RS256",
		Key:       &rsa.PublicKey{},
	}, &jose.JsonWebSignature{
		Signatures: []jose.Signature{
			{
				Header: jose.JoseHeader{
					Algorithm: "RS256",
				},
			},
		},
	})
	if err != nil {
		t.Errorf("RS256 key: Expected nil error, got '%s'", err)
	}
	_, err = checkAlgorithm(&jose.JsonWebKey{
		Key: &rsa.PublicKey{},
	}, &jose.JsonWebSignature{
		Signatures: []jose.Signature{
			{
				Header: jose.JoseHeader{
					Algorithm: "RS256",
				},
			},
		},
	})
	if err != nil {
		t.Errorf("RS256 key: Expected nil error, got '%s'", err)
	}

	_, err = checkAlgorithm(&jose.JsonWebKey{
		Algorithm: "ES256",
		Key: &ecdsa.PublicKey{
			Curve: elliptic.P256(),
		},
	}, &jose.JsonWebSignature{
		Signatures: []jose.Signature{
			{
				Header: jose.JoseHeader{
					Algorithm: "ES256",
				},
			},
		},
	})
	if err != nil {
		t.Errorf("ES256 key: Expected nil error, got '%s'", err)
	}

	_, err = checkAlgorithm(&jose.JsonWebKey{
		Key: &ecdsa.PublicKey{
			Curve: elliptic.P256(),
		},
	}, &jose.JsonWebSignature{
		Signatures: []jose.Signature{
			{
				Header: jose.JoseHeader{
					Algorithm: "ES256",
				},
			},
		},
	})
	if err != nil {
		t.Errorf("ES256 key: Expected nil error, got '%s'", err)
	}
}
