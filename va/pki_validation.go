package va

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/letsencrypt/boulder/core"
	berrors "github.com/letsencrypt/boulder/errors"
	"github.com/letsencrypt/boulder/identifier"
)

// validatePKIValidation01 performs domain validation using the
// pki-validation-01 challenge type, implementing BR §3.2.2.4.18
// "Agreed-Upon Change to Website v2" semantics.
//
// The validation URL is /.well-known/pki-validation/<thumbprint>
// where <thumbprint> is the second half of the key authorization
// (base64url-encoded SHA-256 of the account JWK). The response body
// must equal the full key authorization. Transport rules, redirect
// policy, IPv4 fallback, MPIC, and response size limit are inherited
// from processHTTPValidation (shared with HTTP-01).
func (va *ValidationAuthorityImpl) validatePKIValidation01(
	ctx context.Context,
	ident identifier.ACMEIdentifier,
	token string,
	keyAuthorization string,
) ([]core.ValidationRecord, error) {
	if ident.Type != identifier.TypeDNS {
		va.log.Errf("Identifier type for pki-validation-01 challenge was not DNS: %s", ident)
		return nil, berrors.MalformedError("Identifier type for pki-validation-01 challenge was not DNS")
	}

	// The thumbprint is the second half of the key authorization. The RA
	// computes keyAuthorization as token + "." + base64url(SHA-256(JWK)),
	// so splitting gives us the authoritative thumbprint without an extra
	// gRPC field and without independent verification (the body check
	// below provides all cryptographic binding to the account key).
	parts := strings.SplitN(keyAuthorization, ".", 2)
	if len(parts) != 2 || parts[1] == "" {
		return nil, berrors.MalformedError(
			"malformed key authorization for pki-validation-01: missing thumbprint")
	}
	thumbprint := parts[1]

	path := fmt.Sprintf("/.well-known/pki-validation/%s", thumbprint)
	body, records, err := va.processHTTPValidation(ctx, ident, path, core.ChallengeTypePKIValidation01)
	if err != nil {
		return records, err
	}
	payload := strings.TrimRightFunc(string(body), unicode.IsSpace)

	if payload != keyAuthorization {
		problem := berrors.UnauthorizedError(
			"The key authorization file from the server did not match this challenge. "+
				"Expected %q (got %q)",
			keyAuthorization, payload)
		va.log.Infof("%s for %s", problem, ident)
		return records, problem
	}

	return records, nil
}
