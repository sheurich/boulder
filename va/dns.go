package va

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/letsencrypt/boulder/bdns"
	"github.com/letsencrypt/boulder/core"
	berrors "github.com/letsencrypt/boulder/errors"
	"github.com/letsencrypt/boulder/features"
	"github.com/letsencrypt/boulder/identifier"
)

// getAddr will query for all A/AAAA records associated with hostname and return
// the preferred address, the first net.IP in the addrs slice, and all addresses
// resolved. This is the same choice made by the Go internal resolution library
// used by net/http. If there is an error resolving the hostname, or if no
// usable IP addresses are available then a berrors.DNSError instance is
// returned with a nil net.IP slice.
func (va ValidationAuthorityImpl) getAddrs(ctx context.Context, hostname string) ([]net.IP, bdns.ResolverAddrs, error) {
	addrs, resolvers, err := va.dnsClient.LookupHost(ctx, hostname)
	if err != nil {
		return nil, resolvers, berrors.DNSError("%v", err)
	}

	if len(addrs) == 0 {
		// This should be unreachable, as no valid IP addresses being found results
		// in an error being returned from LookupHost.
		return nil, resolvers, berrors.DNSError("No valid IP addresses found for %s", hostname)
	}
	va.log.Debugf("Resolved addresses for %s: %s", hostname, addrs)
	return addrs, resolvers, nil
}

// availableAddresses takes a ValidationRecord and splits the AddressesResolved
// into a list of IPv4 and IPv6 addresses.
func availableAddresses(allAddrs []net.IP) (v4 []net.IP, v6 []net.IP) {
	for _, addr := range allAddrs {
		if addr.To4() != nil {
			v4 = append(v4, addr)
		} else {
			v6 = append(v6, addr)
		}
	}
	return
}

// calculateDNSAccount01Label calculates the label used in DNS-ACCOUNT-01 challenges.
//
// The DNS-ACCOUNT-01 challenge type is defined in draft-ietf-acme-dns-account-label-00
// and differs from the standard DNS-01 challenge by using a label derived from the
// account URI in the DNS record format.
//
// The label is calculated by:
// 1. Taking the SHA-256 hash of the account URI
// 2. Using the first 10 bytes of the hash
// 3. Encoding those bytes using standard base32 encoding
// 4. Prepending '_' (underscore)
//
// This function validates that the accountURL is non-empty, syntactically valid,
// and uses the HTTPS scheme before calculation. It returns the calculated label
// and a nil error on success, or an empty string and a non-nil error on failure.
func (va *ValidationAuthorityImpl) calculateDNSAccount01Label(accountURI string, accountURIPrefixes []string) (string, error) {

	// If the accounturi is not formatted according to RFC 3986, reject it.
	_, err := url.Parse(accountURI)
	if err != nil {
		return "", berrors.MalformedError("Invalid Account URI syntax %q: %v", accountURI, err)
	}

	// Ensure accountURI matches a valid prefix
	var found bool
	for _, prefix := range accountURIPrefixes {
		if strings.HasPrefix(accountURI, prefix) {
			found = true
			break
		}
	}
	if !found {
		return "", berrors.UnauthorizedError("Invalid Account URI prefix: %s", accountURI)
	}

	h := sha256.Sum256([]byte(accountURI))
	// Use ToLower as specified in the draft examples implicitly
	label := fmt.Sprintf("_%s",
		strings.ToLower(base32.StdEncoding.EncodeToString(h[:10])))

	return label, nil
}

// validateDNSAccount01 validates the DNS-ACCOUNT-01 challenge type.
//
// This challenge type is similar to DNS-01 but uses a DNS record name that includes
// a label derived from the account URI, binding the challenge to a specific ACME account.
//
// The DNS record format is: {accountLabel}._acme-challenge.{domain}
//
// Where {accountLabel} is produced using calculateDNSAccount01Label and
// {domain} is the domain being validated. The TXT record value is the same as
// for DNS-01: a base64url encoded SHA-256 digest of the key authorization.
func (va *ValidationAuthorityImpl) validateDNSAccount01(ctx context.Context, ident identifier.ACMEIdentifier, keyAuthorization string, accountURL string) ([]core.ValidationRecord, error) {
	if !features.Get().DNSAccount01Enabled {
		va.log.Infof("Got a dns-account-01 validation request but dns-account-01 challenge type is disabled")
		return nil, berrors.UnauthorizedError("dns-account-01 challenge type disabled")
	}

	if ident.Type != identifier.TypeDNS {
		va.log.Infof("Identifier type for DNS-ACCOUNT-01 challenge was not DNS: %s", ident)
		return nil, berrors.MalformedError("Identifier type for DNS-ACCOUNT-01 challenge was not DNS")
	}

	// Compute the digest of the key authorization file
	h := sha256.New()
	h.Write([]byte(keyAuthorization))
	authorizedKeysDigest := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	// Compute the DNS-ACCOUNT-01 record
	label, err := va.calculateDNSAccount01Label(accountURL, va.accountURIPrefixes)
	if err != nil {
		return nil, berrors.MalformedError("dns-account-01 label calculation failed: %s", err)
	}

	// Look for the required record in the DNS
	challengeSubdomain := fmt.Sprintf("%s.%s.%s", label, core.DNSPrefix, ident.Value)
	txts, resolvers, err := va.dnsClient.LookupTXT(ctx, challengeSubdomain)
	if err != nil {
		return nil, berrors.DNSError("%s", err)
	}

	// If there weren't any TXT records return a distinct error message to allow
	// troubleshooters to differentiate between no TXT records and
	// invalid/incorrect TXT records.
	if len(txts) == 0 {
		return nil, berrors.UnauthorizedError("No TXT record found at %s", challengeSubdomain)
	}

	for _, element := range txts {
		if subtle.ConstantTimeCompare([]byte(element), []byte(authorizedKeysDigest)) == 1 {
			// Successful challenge validation
			return []core.ValidationRecord{{DnsName: ident.Value, ResolverAddrs: resolvers}}, nil
		}
	}

	invalidRecord := txts[0]
	if len(invalidRecord) > 100 {
		invalidRecord = invalidRecord[0:100] + "..."
	}
	var andMore string
	if len(txts) > 1 {
		andMore = fmt.Sprintf(" (and %d more)", len(txts)-1)
	}
	return nil, berrors.UnauthorizedError("Incorrect TXT record %q%s found at %s",
		invalidRecord, andMore, challengeSubdomain)
}

func (va *ValidationAuthorityImpl) validateDNS01(ctx context.Context, ident identifier.ACMEIdentifier, keyAuthorization string) ([]core.ValidationRecord, error) {
	if ident.Type != identifier.TypeDNS {
		va.log.Infof("Identifier type for DNS challenge was not DNS: %s", ident)
		return nil, berrors.MalformedError("Identifier type for DNS challenge was not DNS")
	}

	// Compute the digest of the key authorization file
	h := sha256.New()
	h.Write([]byte(keyAuthorization))
	authorizedKeysDigest := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	// Look for the required record in the DNS
	challengeSubdomain := fmt.Sprintf("%s.%s", core.DNSPrefix, ident.Value)
	txts, resolvers, err := va.dnsClient.LookupTXT(ctx, challengeSubdomain)
	if err != nil {
		return nil, berrors.DNSError("%s", err)
	}

	// If there weren't any TXT records return a distinct error message to allow
	// troubleshooters to differentiate between no TXT records and
	// invalid/incorrect TXT records.
	if len(txts) == 0 {
		return nil, berrors.UnauthorizedError("No TXT record found at %s", challengeSubdomain)
	}

	for _, element := range txts {
		if subtle.ConstantTimeCompare([]byte(element), []byte(authorizedKeysDigest)) == 1 {
			// Successful challenge validation
			return []core.ValidationRecord{{DnsName: ident.Value, ResolverAddrs: resolvers}}, nil
		}
	}

	invalidRecord := txts[0]
	if len(invalidRecord) > 100 {
		invalidRecord = invalidRecord[0:100] + "..."
	}
	var andMore string
	if len(txts) > 1 {
		andMore = fmt.Sprintf(" (and %d more)", len(txts)-1)
	}
	return nil, berrors.UnauthorizedError("Incorrect TXT record %q%s found at %s",
		invalidRecord, andMore, challengeSubdomain)
}
