package va

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
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
// The validation URL is /.well-known/pki-validation/<hash> where
// <hash> is base64url(SHA-256(keyAuthorization)). Using the hash of
// the full key authorization (rather than just the thumbprint) ensures
// uniqueness per challenge, since each challenge has a unique token.
// This avoids filename collisions when a single account validates the
// same FQDN concurrently.
//
// The response body must equal the full key authorization. Transport
// rules, redirect policy, IPv4 fallback, MPIC, and response size limit
// are inherited from processHTTPValidation (shared with HTTP-01).
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

	if keyAuthorization == "" {
		return nil, berrors.MalformedError(
			"malformed key authorization for pki-validation-01: empty")
	}

	// Defense-in-depth: warn on structurally unexpected keyAuthorization.
	// The RA always produces "token.thumbprint" but a future bug might
	// deliver garbage; this log aids diagnosis without rejecting the input.
	if !strings.Contains(keyAuthorization, ".") {
		va.log.Warningf("pki-validation-01: keyAuthorization %q has no dot separator", keyAuthorization)
	}

	// Derive filename from the full key authorization. The token component
	// provides per-challenge uniqueness; hashing ensures the entire Request
	// Token does not appear in the request used to retrieve the file
	// (BR §3.2.2.4.18 line 947).
	hash := sha256.Sum256([]byte(keyAuthorization))
	filename := base64.RawURLEncoding.EncodeToString(hash[:])

	path := fmt.Sprintf("/.well-known/pki-validation/%s", filename)
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
