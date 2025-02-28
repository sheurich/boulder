package va

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"net"

	"github.com/letsencrypt/boulder/bdns"
	"github.com/letsencrypt/boulder/core"
	berrors "github.com/letsencrypt/boulder/errors"
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

// computeAccountLabel computes the account label for dns-account-01 challenge
// as specified in the IETF draft. It takes the first 10 bytes of the SHA-256 digest
// of the account URI and encodes them using base32 (RFC 4648). This creates a unique
// but deterministic label for each ACME account, allowing multiple independent ACME
// clients to perform domain validation concurrently.
func computeAccountLabel(accountURI string) string {
	x := sha256.Sum256([]byte(accountURI))
	// Take the first 10 bytes of the digest and encode using base32 (RFC 4648)
	return base32.StdEncoding.EncodeToString(x[0:10])
}

func (va *ValidationAuthorityImpl) validateDNS01(ctx context.Context, ident identifier.ACMEIdentifier, keyAuthorization string) ([]core.ValidationRecord, error) {
	if ident.Type != identifier.TypeDNS {
		va.log.Infof("Identifier type for DNS challenge was not DNS: %s", ident)
		return nil, berrors.MalformedError("Identifier type for DNS was not itself DNS")
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

// validateDNSAccount01 validates a dns-account-01 challenge as specified in the IETF draft
// https://www.ietf.org/archive/id/draft-ietf-acme-dns-account-label-00.txt
// This challenge type allows multiple independent ACME clients to perform domain validation
// concurrently through account-specific subdomain prefixes.
func (va *ValidationAuthorityImpl) validateDNSAccount01(ctx context.Context, ident identifier.ACMEIdentifier, keyAuthorization string, accountURI string) ([]core.ValidationRecord, error) {
	if ident.Type != identifier.TypeDNS {
		va.log.Infof("Identifier type for DNS-Account challenge was not DNS: %s", ident)
		return nil, berrors.MalformedError("Identifier type for DNS-Account was not itself DNS")
	}

	// Compute the digest of the key authorization file
	h := sha256.New()
	h.Write([]byte(keyAuthorization))
	authorizedKeysDigest := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	// Compute the account label
	accountLabel := computeAccountLabel(accountURI)

	// Look for the required record in the DNS
	challengeSubdomain := fmt.Sprintf("%s%s.%s.%s", core.DNSAccountPrefix, accountLabel, core.DNSPrefix, ident.Value)
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
