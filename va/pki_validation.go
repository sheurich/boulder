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
//
// Redirect policy rationale: BR §3.2.2.4.18 does not independently
// specify redirect constraints. The transport rules from §3.2.2.4.19
// (max 10 redirects, HTTP/HTTPS scheme only, ports 80/443, TLS ≥1.2)
// are inherited as the general HTTP validation transport requirements
// that all HTTP-based BR methods share. This is the same interpretation
// applied by other production CAs implementing HTTP-based methods.
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

	// Defense-in-depth: reject thumbprints containing path separators.
	// Legitimate thumbprints are base64url-encoded SHA-256 hashes and can
	// only contain [A-Za-z0-9_-]. This guard protects against future
	// refactors that might change how keyAuthorization reaches the VA.
	if strings.ContainsAny(thumbprint, "/\\") {
		return nil, berrors.MalformedError(
			"malformed thumbprint for pki-validation-01: contains path separator")
	}

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
