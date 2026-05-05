package acme

import (
	"errors"
	"fmt"
	"strings"
)

// PKIValidation01PathPrefix is the well-known path prefix under which
// pki-validation-01 challenge responses are served. It matches the
// BR §3.2.2.4.18 "Agreed-Upon Change to Website v2" requirement that
// the validation file be located under /.well-known/pki-validation/.
const PKIValidation01PathPrefix = "/.well-known/pki-validation/"

// ErrMalformedKeyAuthorization is returned by PKIValidation01ChallengePath
// when its input is not a well-formed ACME key authorization of the form
// token.thumbprint with both halves non-empty.
var ErrMalformedKeyAuthorization = errors.New("acme: malformed key authorization")

// PKIValidation01ChallengePath returns the path at which a pki-validation-01
// challenge response should be served, given an ACME key authorization of the
// form token + "." + base64url(SHA-256(accountJWK)). The returned path is
// PKIValidation01PathPrefix followed by the thumbprint half of the key
// authorization.
//
// The key authorization is also the exact body the server expects at that
// path; see RFC 8555 §8.1 for its construction.
func PKIValidation01ChallengePath(keyAuthorization string) (string, error) {
	parts := strings.SplitN(keyAuthorization, ".", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("%w: missing separator", ErrMalformedKeyAuthorization)
	}
	token, thumbprint := parts[0], parts[1]
	if token == "" {
		return "", fmt.Errorf("%w: empty token", ErrMalformedKeyAuthorization)
	}
	if thumbprint == "" {
		return "", fmt.Errorf("%w: empty thumbprint", ErrMalformedKeyAuthorization)
	}
	return PKIValidation01PathPrefix + thumbprint, nil
}
