package acme

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// PKIValidation01PathPrefix is the well-known path prefix under which
// pki-validation-01 challenge responses are served. It matches the
// BR §3.2.2.4.18 "Agreed-Upon Change to Website v2" requirement that
// the validation file be located under /.well-known/pki-validation/.
const PKIValidation01PathPrefix = "/.well-known/pki-validation/"

// ErrMalformedKeyAuthorization is returned by PKIValidation01ChallengePath
// when its input is empty.
var ErrMalformedKeyAuthorization = errors.New("acme: malformed key authorization")

// PKIValidation01ChallengePath returns the path at which a pki-validation-01
// challenge response should be served, given an ACME key authorization of the
// form token + "." + base64url(SHA-256(accountJWK)). The returned path is
// PKIValidation01PathPrefix followed by base64url(SHA-256(keyAuthorization)).
//
// Using the hash of the full key authorization (rather than just the
// thumbprint) ensures uniqueness per challenge, since each challenge has a
// unique token. This avoids filename collisions when a single account
// validates the same FQDN concurrently.
//
// The key authorization is also the exact body the server expects at that
// path; see RFC 8555 §8.1 for its construction.
func PKIValidation01ChallengePath(keyAuthorization string) (string, error) {
	if keyAuthorization == "" {
		return "", ErrMalformedKeyAuthorization
	}
	hash := sha256.Sum256([]byte(keyAuthorization))
	filename := base64.RawURLEncoding.EncodeToString(hash[:])
	return PKIValidation01PathPrefix + filename, nil
}
