package ra

import (
	"crypto/x509"
	"expvar"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jmhodges/clock"
	"github.com/weppos/publicsuffix-go/publicsuffix"
	"golang.org/x/net/context"

	"github.com/letsencrypt/boulder/bdns"
	caPB "github.com/letsencrypt/boulder/ca/proto"
	"github.com/letsencrypt/boulder/core"
	corepb "github.com/letsencrypt/boulder/core/proto"
	"github.com/letsencrypt/boulder/csr"
	csrlib "github.com/letsencrypt/boulder/csr"
	berrors "github.com/letsencrypt/boulder/errors"
	"github.com/letsencrypt/boulder/features"
	"github.com/letsencrypt/boulder/goodkey"
	bgrpc "github.com/letsencrypt/boulder/grpc"
	blog "github.com/letsencrypt/boulder/log"
	"github.com/letsencrypt/boulder/metrics"
	"github.com/letsencrypt/boulder/probs"
	rapb "github.com/letsencrypt/boulder/ra/proto"
	"github.com/letsencrypt/boulder/ratelimit"
	"github.com/letsencrypt/boulder/reloader"
	"github.com/letsencrypt/boulder/revocation"
	sapb "github.com/letsencrypt/boulder/sa/proto"
	vaPB "github.com/letsencrypt/boulder/va/proto"
	grpc "google.golang.org/grpc"
)

// Note: the issuanceExpvar must be a global. If it is a member of the RA, or
// initialized with everything else in NewRegistrationAuthority() then multiple
// invocations of the constructor (e.g from unit tests) will panic with a "Reuse
// of exported var name:" error from the expvar package.
var issuanceExpvar = expvar.NewInt("lastIssuance")

type caaChecker interface {
	IsCAAValid(
		ctx context.Context,
		in *vaPB.IsCAAValidRequest,
		opts ...grpc.CallOption,
	) (*vaPB.IsCAAValidResponse, error)
}

// RegistrationAuthorityImpl defines an RA.
//
// NOTE: All of the fields in RegistrationAuthorityImpl need to be
// populated, or there is a risk of panic.
type RegistrationAuthorityImpl struct {
	CA        core.CertificateAuthority
	VA        core.ValidationAuthority
	SA        core.StorageAuthority
	PA        core.PolicyAuthority
	publisher core.Publisher
	caa       caaChecker

	stats     metrics.Scope
	DNSClient bdns.DNSClient
	clk       clock.Clock
	log       blog.Logger
	keyPolicy goodkey.KeyPolicy
	// How long before a newly created authorization expires.
	authorizationLifetime        time.Duration
	pendingAuthorizationLifetime time.Duration
	rlPolicies                   ratelimit.Limits
	// tiMu protects totalIssuedCount and totalIssuedLastUpdate
	tiMu                  *sync.RWMutex
	totalIssuedCount      int
	totalIssuedLastUpdate time.Time
	maxContactsPerReg     int
	maxNames              int
	forceCNFromSAN        bool
	reuseValidAuthz       bool
	orderLifetime         time.Duration

	regByIPStats         metrics.Scope
	regByIPRangeStats    metrics.Scope
	pendAuthByRegIDStats metrics.Scope
	certsForDomainStats  metrics.Scope
	totalCertsStats      metrics.Scope
}

// NewRegistrationAuthorityImpl constructs a new RA object.
func NewRegistrationAuthorityImpl(
	clk clock.Clock,
	logger blog.Logger,
	stats metrics.Scope,
	maxContactsPerReg int,
	keyPolicy goodkey.KeyPolicy,
	maxNames int,
	forceCNFromSAN bool,
	reuseValidAuthz bool,
	authorizationLifetime time.Duration,
	pendingAuthorizationLifetime time.Duration,
	pubc core.Publisher,
	caaClient caaChecker,
	orderLifetime time.Duration,
) *RegistrationAuthorityImpl {
	ra := &RegistrationAuthorityImpl{
		stats: stats,
		clk:   clk,
		log:   logger,
		authorizationLifetime:        authorizationLifetime,
		pendingAuthorizationLifetime: pendingAuthorizationLifetime,
		rlPolicies:                   ratelimit.New(),
		tiMu:                         new(sync.RWMutex),
		maxContactsPerReg:            maxContactsPerReg,
		keyPolicy:                    keyPolicy,
		maxNames:                     maxNames,
		forceCNFromSAN:               forceCNFromSAN,
		reuseValidAuthz:              reuseValidAuthz,
		regByIPStats:                 stats.NewScope("RateLimit", "RegistrationsByIP"),
		regByIPRangeStats:            stats.NewScope("RateLimit", "RegistrationsByIPRange"),
		pendAuthByRegIDStats:         stats.NewScope("RateLimit", "PendingAuthorizationsByRegID"),
		certsForDomainStats:          stats.NewScope("RateLimit", "CertificatesForDomain"),
		totalCertsStats:              stats.NewScope("RateLimit", "TotalCertificates"),
		publisher:                    pubc,
		caa:                          caaClient,
		orderLifetime:                orderLifetime,
	}
	return ra
}

func (ra *RegistrationAuthorityImpl) SetRateLimitPoliciesFile(filename string) error {
	_, err := reloader.New(filename, ra.rlPolicies.LoadPolicies, ra.rateLimitPoliciesLoadError)
	if err != nil {
		return err
	}

	return nil
}

func (ra *RegistrationAuthorityImpl) rateLimitPoliciesLoadError(err error) {
	ra.log.Err(fmt.Sprintf("error reloading rate limit policy: %s", err))
}

// Run this to continually update the totalIssuedCount field of this
// RA by calling out to the SA. It will run one update before returning, and
// return an error if that update failed.
func (ra *RegistrationAuthorityImpl) UpdateIssuedCountForever() error {
	if err := ra.updateIssuedCount(); err != nil {
		return err
	}
	go func() {
		for {
			_ = ra.updateIssuedCount()
			time.Sleep(1 * time.Minute)
		}
	}()
	return nil
}

func (ra *RegistrationAuthorityImpl) updateIssuedCount() error {
	totalCertLimit := ra.rlPolicies.TotalCertificates()
	if totalCertLimit.Enabled() {
		now := ra.clk.Now()
		// We don't have a Context here, so use the background context. Note that a
		// timeout is still imposed by our RPC layer.
		count, err := ra.SA.CountCertificatesRange(
			context.Background(),
			now.Add(-totalCertLimit.Window.Duration),
			now,
		)
		if err != nil {
			ra.log.AuditErr(fmt.Sprintf("updating total issued count: %s", err))
			return err
		}
		ra.tiMu.Lock()
		ra.totalIssuedCount = int(count)
		ra.totalIssuedLastUpdate = ra.clk.Now()
		ra.tiMu.Unlock()
	}
	return nil
}

var (
	unparseableEmailError = berrors.InvalidEmailError("not a valid e-mail address")
	emptyDNSResponseError = berrors.InvalidEmailError(
		"empty DNS response validating email domain - no MX/A records")
	multipleAddressError = berrors.InvalidEmailError("more than one e-mail address")
)

func problemIsTimeout(err error) bool {
	if dnsErr, ok := err.(*bdns.DNSError); ok && dnsErr.Timeout() {
		return true
	}

	return false
}

func validateEmail(ctx context.Context, address string, resolver bdns.DNSClient) error {
	emails, err := mail.ParseAddressList(address)
	if err != nil {
		return unparseableEmailError
	}
	if len(emails) > 1 {
		return multipleAddressError
	}
	splitEmail := strings.SplitN(emails[0].Address, "@", -1)
	domain := strings.ToLower(splitEmail[len(splitEmail)-1])
	var resultMX []string
	var resultA []net.IP
	var errMX, errA error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		resultMX, errMX = resolver.LookupMX(ctx, domain)
		wg.Done()
	}()
	go func() {
		resultA, errA = resolver.LookupHost(ctx, domain)
		wg.Done()
	}()
	wg.Wait()

	// We treat timeouts as non-failures for best-effort email validation
	// See: https://github.com/letsencrypt/boulder/issues/2260
	if problemIsTimeout(errMX) || problemIsTimeout(errA) {
		return nil
	}

	if errMX != nil {
		return berrors.InvalidEmailError(errMX.Error())
	} else if len(resultMX) > 0 {
		return nil
	}
	if errA != nil {
		return berrors.InvalidEmailError(errA.Error())
	} else if len(resultA) > 0 {
		return nil
	}

	return emptyDNSResponseError
}

type certificateRequestEvent struct {
	ID                  string    `json:",omitempty"`
	Requester           int64     `json:",omitempty"`
	SerialNumber        string    `json:",omitempty"`
	RequestMethod       string    `json:",omitempty"`
	VerificationMethods []string  `json:",omitempty"`
	VerifiedFields      []string  `json:",omitempty"`
	CommonName          string    `json:",omitempty"`
	Names               []string  `json:",omitempty"`
	NotBefore           time.Time `json:",omitempty"`
	NotAfter            time.Time `json:",omitempty"`
	RequestTime         time.Time `json:",omitempty"`
	ResponseTime        time.Time `json:",omitempty"`
	Error               string    `json:",omitempty"`
}

// noRegistrationID is used for the regID parameter to GetThreshold when no
// registration-based overrides are necessary.
const noRegistrationID = -1

// registrationCounter is a type to abstract the use of
// ra.SA.CountRegistrationsByIP or ra.SA.CountRegistrationsByIPRange
type registrationCounter func(context.Context, net.IP, time.Time, time.Time) (int, error)

// checkRegistrationIPLimit checks a specific registraton limit by using the
// provided registrationCounter function to determine if the limit has been
// exceeded for a given IP or IP range
func (ra *RegistrationAuthorityImpl) checkRegistrationIPLimit(
	ctx context.Context,
	limit ratelimit.RateLimitPolicy,
	ip net.IP,
	counter registrationCounter) error {

	if !limit.Enabled() {
		return nil
	}

	now := ra.clk.Now()
	windowBegin := limit.WindowBegin(now)
	count, err := counter(ctx, ip, windowBegin, now)
	if err != nil {
		return err
	}

	if count >= limit.GetThreshold(ip.String(), noRegistrationID) {
		return berrors.RateLimitError("too many registrations for this IP")
	}

	return nil
}

// checkRegistrationLimits enforces the RegistrationsPerIP and
// RegistrationsPerIPRange limits
func (ra *RegistrationAuthorityImpl) checkRegistrationLimits(ctx context.Context, ip net.IP) error {
	// Check the registrations per IP limit using the CountRegistrationsByIP SA
	// function that matches IP addresses exactly
	exactRegLimit := ra.rlPolicies.RegistrationsPerIP()
	err := ra.checkRegistrationIPLimit(ctx, exactRegLimit, ip, ra.SA.CountRegistrationsByIP)
	if err != nil {
		ra.regByIPStats.Inc("Exceeded", 1)
		ra.log.Info(fmt.Sprintf("Rate limit exceeded, RegistrationsByIP, IP: %s", ip))
		return err
	}
	ra.regByIPStats.Inc("Pass", 1)

	// We only apply the fuzzy reg limit to IPv6 addresses.
	// Per https://golang.org/pkg/net/#IP.To4 "If ip is not an IPv4 address, To4
	// returns nil"
	if ip.To4() != nil {
		return nil
	}

	// Check the registrations per IP range limit using the
	// CountRegistrationsByIPRange SA function that fuzzy-matches IPv6 addresses
	// within a larger address range
	fuzzyRegLimit := ra.rlPolicies.RegistrationsPerIPRange()
	err = ra.checkRegistrationIPLimit(ctx, fuzzyRegLimit, ip, ra.SA.CountRegistrationsByIPRange)
	if err != nil {
		ra.regByIPRangeStats.Inc("Exceeded", 1)
		ra.log.Info(fmt.Sprintf("Rate limit exceeded, RegistrationsByIPRange, IP: %s", ip))
		// For the fuzzyRegLimit we use a new error message that specifically
		// mentions that the limit being exceeded is applied to a *range* of IPs
		return berrors.RateLimitError("too many registrations for this IP range")
	}
	ra.regByIPRangeStats.Inc("Pass", 1)

	return nil
}

// NewRegistration constructs a new Registration from a request.
func (ra *RegistrationAuthorityImpl) NewRegistration(ctx context.Context, init core.Registration) (core.Registration, error) {
	if err := ra.keyPolicy.GoodKey(init.Key.Key); err != nil {
		return core.Registration{}, berrors.MalformedError("invalid public key: %s", err.Error())
	}
	if err := ra.checkRegistrationLimits(ctx, init.InitialIP); err != nil {
		return core.Registration{}, err
	}

	reg := core.Registration{
		Key:    init.Key,
		Status: core.StatusValid,
	}
	_ = mergeUpdate(&reg, init)

	// This field isn't updatable by the end user, so it isn't copied by
	// MergeUpdate. But we need to fill it in for new registrations.
	reg.InitialIP = init.InitialIP

	if err := ra.validateContacts(ctx, reg.Contact); err != nil {
		return core.Registration{}, err
	}

	// Store the authorization object, then return it
	reg, err := ra.SA.NewRegistration(ctx, reg)
	if err != nil {
		return core.Registration{}, err
	}

	ra.stats.Inc("NewRegistrations", 1)
	return reg, nil
}

func (ra *RegistrationAuthorityImpl) validateContacts(ctx context.Context, contacts *[]string) error {
	if contacts == nil || len(*contacts) == 0 {
		return nil // Nothing to validate
	}
	if ra.maxContactsPerReg > 0 && len(*contacts) > ra.maxContactsPerReg {
		return berrors.MalformedError(
			"too many contacts provided: %d > %d",
			len(*contacts),
			ra.maxContactsPerReg,
		)
	}

	for _, contact := range *contacts {
		if contact == "" {
			return berrors.MalformedError("empty contact")
		}
		parsed, err := url.Parse(contact)
		if err != nil {
			return berrors.MalformedError("invalid contact")
		}
		if parsed.Scheme != "mailto" {
			return berrors.MalformedError("contact method %s is not supported", parsed.Scheme)
		}
		if !core.IsASCII(contact) {
			return berrors.MalformedError(
				"contact email [%s] contains non-ASCII characters",
				contact,
			)
		}

		start := ra.clk.Now()
		ra.stats.Inc("ValidateEmail.Calls", 1)
		err = validateEmail(ctx, parsed.Opaque, ra.DNSClient)
		ra.stats.TimingDuration("ValidateEmail.Latency", ra.clk.Now().Sub(start))
		if err != nil {
			ra.stats.Inc("ValidateEmail.Errors", 1)
			return err
		}
		ra.stats.Inc("ValidateEmail.Successes", 1)
	}

	return nil
}

func (ra *RegistrationAuthorityImpl) checkPendingAuthorizationLimit(ctx context.Context, regID int64) error {
	limit := ra.rlPolicies.PendingAuthorizationsPerAccount()
	if limit.Enabled() {
		count, err := ra.SA.CountPendingAuthorizations(ctx, regID)
		if err != nil {
			return err
		}
		// Most rate limits have a key for overrides, but there is no meaningful key
		// here.
		noKey := ""
		if count >= limit.GetThreshold(noKey, regID) {
			ra.pendAuthByRegIDStats.Inc("Exceeded", 1)
			ra.log.Info(fmt.Sprintf("Rate limit exceeded, PendingAuthorizationsByRegID, regID: %d", regID))
			return berrors.RateLimitError("too many currently pending authorizations")
		}
		ra.pendAuthByRegIDStats.Inc("Pass", 1)
	}
	return nil
}

func (ra *RegistrationAuthorityImpl) checkInvalidAuthorizationLimit(ctx context.Context, regID int64, hostname string) error {
	limit := ra.rlPolicies.InvalidAuthorizationsPerAccount()
	// The SA.CountInvalidAuthorizations method is not implemented on the wrapper
	// interface, because we want to move towards using gRPC interfaces more
	// directly. So we type-assert the wrapper to a gRPC-specific type.
	saGRPC, ok := ra.SA.(*bgrpc.StorageAuthorityClientWrapper)
	if !limit.Enabled() || !ok {
		return nil
	}
	latest := ra.clk.Now().Add(ra.pendingAuthorizationLifetime)
	earliest := latest.Add(-limit.Window.Duration)
	latestNanos := latest.UnixNano()
	earliestNanos := earliest.UnixNano()
	count, err := saGRPC.CountInvalidAuthorizations(ctx, &sapb.CountInvalidAuthorizationsRequest{
		RegistrationID: &regID,
		Hostname:       &hostname,
		Range: &sapb.Range{
			Earliest: &earliestNanos,
			Latest:   &latestNanos,
		},
	})
	if err != nil {
		return err
	}
	if count == nil {
		return fmt.Errorf("nil count")
	}
	// Most rate limits have a key for overrides, but there is no meaningful key
	// here.
	noKey := ""
	if *count.Count >= int64(limit.GetThreshold(noKey, regID)) {
		ra.log.Info(fmt.Sprintf("Rate limit exceeded, InvalidAuthorizationsByRegID, regID: %d", regID))
		return berrors.RateLimitError("Too many invalid authorizations recently.")
	}
	return nil
}

// NewAuthorization constructs a new Authz from a request. Values (domains) in
// request.Identifier will be lowercased before storage.
func (ra *RegistrationAuthorityImpl) NewAuthorization(ctx context.Context, request core.Authorization, regID int64) (core.Authorization, error) {
	identifier := request.Identifier
	identifier.Value = strings.ToLower(identifier.Value)

	// Check that the identifier is present and appropriate
	if err := ra.PA.WillingToIssue(identifier); err != nil {
		return core.Authorization{}, err
	}

	if err := ra.checkPendingAuthorizationLimit(ctx, regID); err != nil {
		return core.Authorization{}, err
	}

	if err := ra.checkInvalidAuthorizationLimit(ctx, regID, identifier.Value); err != nil {
		return core.Authorization{}, err
	}

	if identifier.Type == core.IdentifierDNS {
		isSafeResp, err := ra.VA.IsSafeDomain(ctx, &vaPB.IsSafeDomainRequest{Domain: &identifier.Value})
		if err != nil {
			outErr := berrors.InternalServerError("unable to determine if domain was safe")
			ra.log.Warning(fmt.Sprintf("%s: %s", outErr, err))
			return core.Authorization{}, outErr
		}
		if !isSafeResp.GetIsSafe() {
			return core.Authorization{}, berrors.UnauthorizedError(
				"%q was considered an unsafe domain by a third-party API",
				identifier.Value,
			)
		}
	}

	if ra.reuseValidAuthz {
		auths, err := ra.SA.GetValidAuthorizations(ctx, regID, []string{identifier.Value}, ra.clk.Now())
		if err != nil {
			outErr := berrors.InternalServerError(
				"unable to get existing validations for regID: %d, identifier: %s",
				regID,
				identifier.Value,
			)
			ra.log.Warning(outErr.Error())
			return core.Authorization{}, outErr
		}

		if existingAuthz, ok := auths[identifier.Value]; ok {
			// Use the valid existing authorization's ID to find a fully populated version
			// The results from `GetValidAuthorizations` are most notably missing
			// `Challenge` values that the client expects in the result.
			populatedAuthz, err := ra.SA.GetAuthorization(ctx, existingAuthz.ID)
			if err != nil {
				outErr := berrors.InternalServerError(
					"unable to get existing authorization for auth ID: %s",
					existingAuthz.ID,
				)
				ra.log.Warning(fmt.Sprintf("%s: %s", outErr.Error(), existingAuthz.ID))
				return core.Authorization{}, outErr
			}

			// The existing authorization must not expire within the next 24 hours for
			// it to be OK for reuse
			reuseCutOff := ra.clk.Now().Add(time.Hour * 24)
			if populatedAuthz.Expires.After(reuseCutOff) {
				ra.stats.Inc("ReusedValidAuthz", 1)
				return populatedAuthz, nil
			}
		}
	}
	if features.Enabled(features.ReusePendingAuthz) {
		nowishNano := ra.clk.Now().Add(time.Hour).UnixNano()
		identifierTypeString := string(identifier.Type)
		pendingAuth, err := ra.SA.GetPendingAuthorization(ctx, &sapb.GetPendingAuthorizationRequest{
			RegistrationID:  &regID,
			IdentifierType:  &identifierTypeString,
			IdentifierValue: &identifier.Value,
			ValidUntil:      &nowishNano,
		})
		if err != nil && !berrors.Is(err, berrors.NotFound) {
			return core.Authorization{}, berrors.InternalServerError(
				"unable to get pending authorization for regID: %d, identifier: %s: %s",
				regID,
				identifier.Value,
				err)
		} else if err == nil {
			return *pendingAuth, nil
		}
		// Fall through to normal creation flow.
	}

	// Create challenges. The WFE will  update them with URIs before sending them out.
	challenges, combinations := ra.PA.ChallengesFor(identifier)

	expires := ra.clk.Now().Add(ra.pendingAuthorizationLifetime)

	authz, err := ra.SA.NewPendingAuthorization(ctx, core.Authorization{
		Identifier:     identifier,
		RegistrationID: regID,
		Status:         core.StatusPending,
		Combinations:   combinations,
		Challenges:     challenges,
		Expires:        &expires,
	})
	if err != nil {
		// berrors.InternalServerError since the user-data was validated before being
		// passed to the SA.
		err = berrors.InternalServerError("invalid authorization request: %s", err)
		return core.Authorization{}, err
	}

	// Check each challenge for sanity.
	for _, challenge := range authz.Challenges {
		if err := challenge.CheckConsistencyForClientOffer(); err != nil {
			// berrors.InternalServerError because we generated these challenges, they should
			// be OK.
			err = berrors.InternalServerError("challenge didn't pass sanity check: %+v", challenge)
			return core.Authorization{}, err
		}
	}

	return authz, err
}

// MatchesCSR tests the contents of a generated certificate to make sure
// that the PublicKey, CommonName, and DNSNames match those provided in
// the CSR that was used to generate the certificate. It also checks the
// following fields for:
//		* notBefore is not more than 24 hours ago
//		* BasicConstraintsValid is true
//		* IsCA is false
//		* ExtKeyUsage only contains ExtKeyUsageServerAuth & ExtKeyUsageClientAuth
//		* Subject only contains CommonName & Names
func (ra *RegistrationAuthorityImpl) MatchesCSR(parsedCertificate *x509.Certificate, csr *x509.CertificateRequest) error {
	// Check issued certificate matches what was expected from the CSR
	hostNames := make([]string, len(csr.DNSNames))
	copy(hostNames, csr.DNSNames)
	if len(csr.Subject.CommonName) > 0 {
		hostNames = append(hostNames, csr.Subject.CommonName)
	}
	hostNames = core.UniqueLowerNames(hostNames)

	if !core.KeyDigestEquals(parsedCertificate.PublicKey, csr.PublicKey) {
		return berrors.InternalServerError("generated certificate public key doesn't match CSR public key")
	}
	if !ra.forceCNFromSAN && len(csr.Subject.CommonName) > 0 &&
		parsedCertificate.Subject.CommonName != strings.ToLower(csr.Subject.CommonName) {
		return berrors.InternalServerError("generated certificate CommonName doesn't match CSR CommonName")
	}
	// Sort both slices of names before comparison.
	parsedNames := parsedCertificate.DNSNames
	sort.Strings(parsedNames)
	sort.Strings(hostNames)
	if !reflect.DeepEqual(parsedNames, hostNames) {
		return berrors.InternalServerError("generated certificate DNSNames don't match CSR DNSNames")
	}
	if !reflect.DeepEqual(parsedCertificate.IPAddresses, csr.IPAddresses) {
		return berrors.InternalServerError("generated certificate IPAddresses don't match CSR IPAddresses")
	}
	if !reflect.DeepEqual(parsedCertificate.EmailAddresses, csr.EmailAddresses) {
		return berrors.InternalServerError("generated certificate EmailAddresses don't match CSR EmailAddresses")
	}
	if len(parsedCertificate.Subject.Country) > 0 || len(parsedCertificate.Subject.Organization) > 0 ||
		len(parsedCertificate.Subject.OrganizationalUnit) > 0 || len(parsedCertificate.Subject.Locality) > 0 ||
		len(parsedCertificate.Subject.Province) > 0 || len(parsedCertificate.Subject.StreetAddress) > 0 ||
		len(parsedCertificate.Subject.PostalCode) > 0 {
		return berrors.InternalServerError("generated certificate Subject contains fields other than CommonName, or SerialNumber")
	}
	now := ra.clk.Now()
	if now.Sub(parsedCertificate.NotBefore) > time.Hour*24 {
		return berrors.InternalServerError("generated certificate is back dated %s", now.Sub(parsedCertificate.NotBefore))
	}
	if !parsedCertificate.BasicConstraintsValid {
		return berrors.InternalServerError("generated certificate doesn't have basic constraints set")
	}
	if parsedCertificate.IsCA {
		return berrors.InternalServerError("generated certificate can sign other certificates")
	}
	if !reflect.DeepEqual(parsedCertificate.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}) {
		return berrors.InternalServerError("generated certificate doesn't have correct key usage extensions")
	}

	return nil
}

// checkAuthorizations checks that each requested name has a valid authorization
// that won't expire before the certificate expires. Returns an error otherwise.
func (ra *RegistrationAuthorityImpl) checkAuthorizations(ctx context.Context, names []string, regID int64) error {
	now := ra.clk.Now()
	var badNames, recheckNames []string
	for i := range names {
		names[i] = strings.ToLower(names[i])
	}
	// Per Baseline Requirements, CAA must be checked within 8 hours of issuance.
	// CAA is checked when an authorization is validated, so as long as that was
	// less than 8 hours ago, we're fine. If it was more than 8 hours ago
	// we have to recheck. Since we don't record the validation time for
	// authorizations, we instead look at the expiration time and subtract out the
	// expected authorization lifetime. Note: If we adjust the authorization
	// lifetime in the future we will need to tweak this correspondingly so it
	// works correctly during the switchover.
	caaRecheckTime := now.Add(ra.authorizationLifetime).Add(-8 * time.Hour)
	auths, err := ra.SA.GetValidAuthorizations(ctx, regID, names, now)
	if err != nil {
		return err
	}
	for _, name := range names {
		authz := auths[name]
		if authz == nil {
			badNames = append(badNames, name)
		} else if authz.Expires == nil {
			return berrors.InternalServerError("found an authorization with a nil Expires field: id %s", authz.ID)
		} else if authz.Expires.Before(now) {
			badNames = append(badNames, name)
		} else if authz.Expires.Before(caaRecheckTime) {
			recheckNames = append(recheckNames, name)
		}
	}

	if features.Enabled(features.RecheckCAA) {
		if err = ra.recheckCAA(ctx, recheckNames); err != nil {
			return err
		}
	}

	if len(badNames) > 0 {
		return berrors.UnauthorizedError(
			"authorizations for these names not found or expired: %s",
			strings.Join(badNames, ", "),
		)
	}

	return nil
}

func (ra *RegistrationAuthorityImpl) recheckCAA(ctx context.Context, names []string) error {
	ra.stats.Inc("recheck_caa", 1)
	ra.stats.Inc("recheck_caa_names", int64(len(names)))
	wg := sync.WaitGroup{}
	ch := make(chan *probs.ProblemDetails, len(names))
	for _, name := range names {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			resp, err := ra.caa.IsCAAValid(ctx, &vaPB.IsCAAValidRequest{
				Domain: &name,
			})
			if err != nil {
				ra.log.AuditErr(fmt.Sprintf("Rechecking CAA: %s", err))
				ch <- probs.ServerInternal("Internal error rechecking CAA for " + name)
			} else if resp.Problem != nil {
				ch <- &probs.ProblemDetails{
					Type:   probs.ProblemType(*resp.Problem.ProblemType),
					Detail: *resp.Problem.Detail,
				}
			}
		}(name)
	}
	wg.Wait()
	close(ch)
	var fails []*probs.ProblemDetails
	for err := range ch {
		if err != nil {
			fails = append(fails, err)
		}
	}
	if len(fails) > 0 {
		message := "Rechecking CAA: "
		for i, pd := range fails {
			if i > 0 {
				message = message + ", "
			}
			message = message + pd.Detail
		}
		return berrors.CAAError(message)
	}
	return nil
}

// NewCertificate requests the issuance of a certificate.
func (ra *RegistrationAuthorityImpl) NewCertificate(ctx context.Context, req core.CertificateRequest, regID int64) (core.Certificate, error) {
	emptyCert := core.Certificate{}
	var logEventResult string

	// Assume the worst
	logEventResult = "error"

	// Construct the log event
	logEvent := certificateRequestEvent{
		ID:            core.NewToken(),
		Requester:     regID,
		RequestMethod: "online",
		RequestTime:   ra.clk.Now(),
	}

	// No matter what, log the request
	defer func() {
		ra.log.AuditObject(fmt.Sprintf("Certificate request - %s", logEventResult), logEvent)
	}()

	if regID <= 0 {
		return emptyCert, berrors.MalformedError("invalid registration ID: %d", regID)
	}

	registration, err := ra.SA.GetRegistration(ctx, regID)
	if err != nil {
		logEvent.Error = err.Error()
		return emptyCert, err
	}

	// Verify the CSR
	csr := req.CSR
	if err := csrlib.VerifyCSR(csr, ra.maxNames, &ra.keyPolicy, ra.PA, ra.forceCNFromSAN, regID); err != nil {
		return emptyCert, berrors.MalformedError(err.Error())
	}

	logEvent.CommonName = csr.Subject.CommonName
	logEvent.Names = csr.DNSNames

	// Validate that authorization key is authorized for all domains
	names := make([]string, len(csr.DNSNames))
	copy(names, csr.DNSNames)

	if len(names) == 0 {
		err = berrors.UnauthorizedError("CSR has no names in it")
		logEvent.Error = err.Error()
		return emptyCert, err
	}

	if core.KeyDigestEquals(csr.PublicKey, registration.Key) {
		err = berrors.MalformedError("certificate public key must be different than account key")
		return emptyCert, err
	}

	// Check rate limits before checking authorizations. If someone is unable to
	// issue a cert due to rate limiting, we don't want to tell them to go get the
	// necessary authorizations, only to later fail the rate limit check.
	err = ra.checkLimits(ctx, names, registration.ID)
	if err != nil {
		logEvent.Error = err.Error()
		return emptyCert, err
	}

	err = ra.checkAuthorizations(ctx, names, registration.ID)
	if err != nil {
		logEvent.Error = err.Error()
		return emptyCert, err
	}

	// Mark that we verified the CN and SANs
	logEvent.VerifiedFields = []string{"subject.commonName", "subjectAltName"}

	// Create the certificate and log the result
	issueReq := &caPB.IssueCertificateRequest{
		Csr:            csr.Raw,
		RegistrationID: &regID,
	}
	cert, err := ra.CA.IssueCertificate(ctx, issueReq)
	if err != nil {
		logEvent.Error = err.Error()
		return emptyCert, err
	}

	if ra.publisher != nil {
		go func() {
			// Since we don't want this method to be canceled if the parent context
			// expires, pass a background context to it and run it in a goroutine.
			_ = ra.publisher.SubmitToCT(context.Background(), cert.DER)
		}()
	}

	parsedCertificate, err := x509.ParseCertificate([]byte(cert.DER))
	if err != nil {
		// berrors.InternalServerError because the certificate from the CA should be
		// parseable.
		err = berrors.InternalServerError("failed to parse certificate: %s", err.Error())
		logEvent.Error = err.Error()
		return emptyCert, err
	}

	err = ra.MatchesCSR(parsedCertificate, csr)
	if err != nil {
		logEvent.Error = err.Error()
		return emptyCert, err
	}

	now := ra.clk.Now()
	logEvent.SerialNumber = core.SerialToString(parsedCertificate.SerialNumber)
	logEvent.CommonName = parsedCertificate.Subject.CommonName
	logEvent.NotBefore = parsedCertificate.NotBefore
	logEvent.NotAfter = parsedCertificate.NotAfter
	logEvent.ResponseTime = now

	logEventResult = "successful"

	issuanceExpvar.Set(now.Unix())
	ra.stats.Inc("NewCertificates", 1)
	return cert, nil
}

// domainsForRateLimiting transforms a list of FQDNs into a list of eTLD+1's
// for the purpose of rate limiting. It also de-duplicates the output
// domains. Exact public suffix matches are not included.
func domainsForRateLimiting(names []string) ([]string, error) {
	var domains []string
	for _, name := range names {
		domain, err := publicsuffix.Domain(name)
		if err != nil {
			// The only possible errors are:
			// (1) publicsuffix.Domain is giving garbage values
			// (2) the public suffix is the domain itself
			// We assume 2 and do not include it in the result.
			continue
		}
		domains = append(domains, domain)
	}
	return core.UniqueLowerNames(domains), nil
}

// suffixesForRateLimiting returns the unique subset of input names that are
// exactly equal to a public suffix.
func suffixesForRateLimiting(names []string) ([]string, error) {
	var suffixMatches []string
	for _, name := range names {
		_, err := publicsuffix.Domain(name)
		if err != nil {
			// Like `domainsForRateLimiting`, the only possible errors here are:
			// (1) publicsuffix.Domain is giving garbage values
			// (2) the public suffix is the domain itself
			// We assume 2 and collect it into the result
			suffixMatches = append(suffixMatches, name)
		}
	}
	return core.UniqueLowerNames(suffixMatches), nil
}

// certCountRPC abstracts the choice of the SA.CountCertificatesByExactNames or
// the SA.CountCertificatesByNames RPC.
type certCountRPC func(ctx context.Context, names []string, earliest, lastest time.Time) ([]*sapb.CountByNames_MapElement, error)

// enforceNameCounts uses the provided count RPC to find a count of certificates
// for each of the names. If the count for any of the names exceeds the limit
// for the given registration then the names out of policy are returned to be
// used for a rate limit error.
func (ra *RegistrationAuthorityImpl) enforceNameCounts(
	ctx context.Context,
	names []string,
	limit ratelimit.RateLimitPolicy,
	regID int64,
	countFunc certCountRPC) ([]string, error) {

	now := ra.clk.Now()
	windowBegin := limit.WindowBegin(now)
	counts, err := countFunc(ctx, names, windowBegin, now)
	if err != nil {
		return nil, err
	}

	var badNames []string
	for _, entry := range counts {
		// Should not happen, but be defensive.
		if entry.Count == nil || entry.Name == nil {
			return nil, fmt.Errorf("CountByNames_MapElement had nil Count or Name")
		}
		if int(*entry.Count) >= limit.GetThreshold(*entry.Name, regID) {
			badNames = append(badNames, *entry.Name)
		}
	}
	return badNames, nil
}

func (ra *RegistrationAuthorityImpl) checkCertificatesPerNameLimit(ctx context.Context, names []string, limit ratelimit.RateLimitPolicy, regID int64) error {
	tldNames, err := domainsForRateLimiting(names)
	if err != nil {
		return err
	}
	exactPublicSuffixes, err := suffixesForRateLimiting(names)
	if err != nil {
		return err
	}

	var badNames []string
	// If the CountCertificatesExact feature is enabled then treat exact public
	// suffic domains differently by enforcing the limit against only exact
	// matches to the names, not matches to subdomains as well.
	if features.Enabled(features.CountCertificatesExact) && len(exactPublicSuffixes) > 0 {
		psNamesOutOfLimit, err := ra.enforceNameCounts(ctx, exactPublicSuffixes, limit, regID, ra.SA.CountCertificatesByExactNames)
		if err != nil {
			return err
		}
		badNames = append(badNames, psNamesOutOfLimit...)
	} else {
		// When the CountCertificatesExact feature is *not* enabled we maintain the
		// historic behaviour of treating exact public suffix matches the same as
		// any other domain for rate limiting by combining the exactPublicSuffixes
		// with the tldNames.
		tldNames = append(tldNames, exactPublicSuffixes...)
	}

	// If there are any tldNames, enforce the certificate count rate limit against
	// them and any subdomains.
	if len(tldNames) > 0 {
		namesOutOfLimit, err := ra.enforceNameCounts(ctx, tldNames, limit, regID, ra.SA.CountCertificatesByNames)
		if err != nil {
			return err
		}
		badNames = append(badNames, namesOutOfLimit...)
	}

	if len(badNames) > 0 {
		// check if there is already a existing certificate for
		// the exact name set we are issuing for. If so bypass the
		// the certificatesPerName limit.
		exists, err := ra.SA.FQDNSetExists(ctx, names)
		if err != nil {
			return err
		}
		if exists {
			ra.certsForDomainStats.Inc("FQDNSetBypass", 1)
			return nil
		}
		domains := strings.Join(badNames, ", ")
		ra.certsForDomainStats.Inc("Exceeded", 1)
		ra.log.Info(fmt.Sprintf("Rate limit exceeded, CertificatesForDomain, regID: %d, domains: %s", regID, domains))
		return berrors.RateLimitError(
			"too many certificates already issued for: %s",
			domains,
		)
	}
	ra.certsForDomainStats.Inc("Pass", 1)

	return nil
}

func (ra *RegistrationAuthorityImpl) checkCertificatesPerFQDNSetLimit(ctx context.Context, names []string, limit ratelimit.RateLimitPolicy, regID int64) error {
	count, err := ra.SA.CountFQDNSets(ctx, limit.Window.Duration, names)
	if err != nil {
		return err
	}
	names = core.UniqueLowerNames(names)
	if int(count) > limit.GetThreshold(strings.Join(names, ","), regID) {
		return berrors.RateLimitError(
			"too many certificates already issued for exact set of domains: %s",
			strings.Join(names, ","),
		)
	}
	return nil
}

func (ra *RegistrationAuthorityImpl) checkTotalCertificatesLimit() error {
	totalCertLimits := ra.rlPolicies.TotalCertificates()
	ra.tiMu.RLock()
	defer ra.tiMu.RUnlock()
	// If last update of the total issued count was more than five minutes ago,
	// or not yet updated, fail.
	if ra.clk.Now().After(ra.totalIssuedLastUpdate.Add(5*time.Minute)) ||
		ra.totalIssuedLastUpdate.IsZero() {
		return berrors.InternalServerError(
			"Total certificate count out of date: updated %s",
			ra.totalIssuedLastUpdate,
		)
	}
	if ra.totalIssuedCount >= totalCertLimits.Threshold {
		ra.totalCertsStats.Inc("Exceeded", 1)
		ra.log.Info(fmt.Sprintf("Rate limit exceeded, TotalCertificates, totalIssued: %d, lastUpdated %s", ra.totalIssuedCount, ra.totalIssuedLastUpdate))
		return berrors.RateLimitError("global certificate issuance limit reached. Try again in an hour")
	}
	ra.totalCertsStats.Inc("Pass", 1)
	return nil
}

func (ra *RegistrationAuthorityImpl) checkLimits(ctx context.Context, names []string, regID int64) error {
	totalCertLimits := ra.rlPolicies.TotalCertificates()
	if totalCertLimits.Enabled() {
		err := ra.checkTotalCertificatesLimit()
		if err != nil {
			return err
		}
	}

	certNameLimits := ra.rlPolicies.CertificatesPerName()
	if certNameLimits.Enabled() {
		err := ra.checkCertificatesPerNameLimit(ctx, names, certNameLimits, regID)
		if err != nil {
			return err
		}
	}

	fqdnLimits := ra.rlPolicies.CertificatesPerFQDNSet()
	if fqdnLimits.Enabled() {
		err := ra.checkCertificatesPerFQDNSetLimit(ctx, names, fqdnLimits, regID)
		if err != nil {
			return err
		}
	}
	return nil
}

// UpdateRegistration updates an existing Registration with new values. Caller
// is responsible for making sure that update.Key is only different from base.Key
// if it is being called from the WFE key change endpoint.
func (ra *RegistrationAuthorityImpl) UpdateRegistration(ctx context.Context, base core.Registration, update core.Registration) (core.Registration, error) {
	if changed := mergeUpdate(&base, update); !changed {
		// If merging the update didn't actually change the base then our work is
		// done, we can return before calling ra.SA.UpdateRegistration since theres
		// nothing for the SA to do
		return base, nil
	}

	err := ra.validateContacts(ctx, base.Contact)
	if err != nil {
		return core.Registration{}, err
	}

	err = ra.SA.UpdateRegistration(ctx, base)
	if err != nil {
		// berrors.InternalServerError since the user-data was validated before being
		// passed to the SA.
		err = berrors.InternalServerError("Could not update registration: %s", err)
		return core.Registration{}, err
	}

	ra.stats.Inc("UpdatedRegistrations", 1)
	return base, nil
}

func contactsEqual(r *core.Registration, other core.Registration) bool {
	// If there is no existing contact slice, or the contact slice lengths
	// differ, then the other contact is not equal
	if r.Contact == nil || len(*other.Contact) != len(*r.Contact) {
		return false
	}

	// If there is an existing contact slice and it has the same length as the
	// new contact slice we need to look at each contact to determine if there
	// is a change being made. Use `sort.Strings` here to ensure a consistent
	// comparison
	a := *other.Contact
	b := *r.Contact
	sort.Strings(a)
	sort.Strings(b)
	for i := 0; i < len(a); i++ {
		// If the contact's string representation differs at any index they aren't
		// equal
		if a[i] != b[i] {
			return false
		}
	}

	// They are equal!
	return true
}

// MergeUpdate copies a subset of information from the input Registration
// into the Registration r. It returns true if an update was performed and the base object
// was changed, and false if no change was made.
func mergeUpdate(r *core.Registration, input core.Registration) bool {
	var changed bool

	// Note: we allow input.Contact to overwrite r.Contact even if the former is
	// empty in order to allow users to remove the contact associated with
	// a registration. Since the field type is a pointer to slice of pointers we
	// can perform a nil check to differentiate between an empty value and a nil
	// (e.g. not provided) value
	if input.Contact != nil && !contactsEqual(r, input) {
		r.Contact = input.Contact
		changed = true
	}

	// If there is an agreement in the input and it's not the same as the base,
	// then we update the base
	if len(input.Agreement) > 0 && input.Agreement != r.Agreement {
		r.Agreement = input.Agreement
		changed = true
	}

	if input.Key != nil {
		if r.Key != nil {
			sameKey, _ := core.PublicKeysEqual(r.Key.Key, input.Key.Key)
			if !sameKey {
				r.Key = input.Key
				changed = true
			}
		}
	}

	return changed
}

// UpdateAuthorization updates an authorization with new values.
func (ra *RegistrationAuthorityImpl) UpdateAuthorization(ctx context.Context, base core.Authorization, challengeIndex int, response core.Challenge) (core.Authorization, error) {
	// Refuse to update expired authorizations
	if base.Expires == nil || base.Expires.Before(ra.clk.Now()) {
		return core.Authorization{}, berrors.MalformedError("expired authorization")
	}

	authz := base
	if challengeIndex >= len(authz.Challenges) {
		return core.Authorization{}, berrors.MalformedError("invalid challenge index '%d'", challengeIndex)
	}

	ch := &authz.Challenges[challengeIndex]

	if response.Type != "" && ch.Type != response.Type {
		// TODO(riking): Check the rate on this, uncomment error return if negligible
		ra.stats.Inc("StartChallengeWrongType", 1)
		// return authz, berrors.MalformedError(
		// 	"invalid challenge update: provided type was %s but actual type is %s",
		// 	response.Type,
		// 	ch.Type,
		// )
	}

	// When configured with `reuseValidAuthz` we can expect some clients to try
	// and update a challenge for an authorization that is already valid. In this
	// case we don't need to process the challenge update. It wouldn't be helpful,
	// the overall authorization is already good! We increment a stat for this
	// case and return early.
	if ra.reuseValidAuthz && authz.Status == core.StatusValid {
		ra.stats.Inc("ReusedValidAuthzChallenge", 1)
		return authz, nil
	}

	// Look up the account key for this authorization
	reg, err := ra.SA.GetRegistration(ctx, authz.RegistrationID)
	if err != nil {
		return core.Authorization{}, berrors.InternalServerError(err.Error())
	}

	// Recompute the key authorization field provided by the client and
	// check it against the value provided
	expectedKeyAuthorization, err := ch.ExpectedKeyAuthorization(reg.Key)
	if err != nil {
		return core.Authorization{}, berrors.InternalServerError("could not compute expected key authorization value")
	}
	if expectedKeyAuthorization != response.ProvidedKeyAuthorization {
		return core.Authorization{}, berrors.MalformedError("provided key authorization was incorrect")
	}

	// Copy information over that the client is allowed to supply
	ch.ProvidedKeyAuthorization = response.ProvidedKeyAuthorization

	// Double check before sending to VA
	if cErr := ch.CheckConsistencyForValidation(); cErr != nil {
		return core.Authorization{}, berrors.MalformedError(cErr.Error())
	}

	// Store the updated version
	if err = ra.SA.UpdatePendingAuthorization(ctx, authz); err != nil {
		ra.log.Warning(fmt.Sprintf(
			"Error calling ra.SA.UpdatePendingAuthorization: %s\n", err.Error()))
		return core.Authorization{}, err
	}
	ra.stats.Inc("NewPendingAuthorizations", 1)

	// Dispatch to the VA for service

	vaCtx := context.Background()
	go func() {
		records, err := ra.VA.PerformValidation(vaCtx, authz.Identifier.Value, authz.Challenges[challengeIndex], authz)
		var prob *probs.ProblemDetails
		if p, ok := err.(*probs.ProblemDetails); ok {
			prob = p
		} else if err != nil {
			prob = probs.ServerInternal("Could not communicate with VA")
			ra.log.AuditErr(fmt.Sprintf("Could not communicate with VA: %s", err))
		}

		// Save the updated records
		challenge := &authz.Challenges[challengeIndex]
		challenge.ValidationRecord = records

		if !challenge.RecordsSane() && prob == nil {
			prob = probs.ServerInternal("Records for validation failed sanity check")
		}

		if prob != nil {
			challenge.Status = core.StatusInvalid
			challenge.Error = prob
		} else {
			challenge.Status = core.StatusValid
		}
		authz.Challenges[challengeIndex] = *challenge

		err = ra.onValidationUpdate(vaCtx, authz)
		if err != nil {
			ra.log.AuditErr(fmt.Sprintf(
				"Could not record updated validation: err=[%s] regID=[%d] authzID=[%s]",
				err, authz.RegistrationID, authz.ID))
		}
	}()
	ra.stats.Inc("UpdatedPendingAuthorizations", 1)
	return authz, nil
}

func revokeEvent(state, serial, cn string, names []string, revocationCode revocation.Reason) string {
	return fmt.Sprintf(
		"Revocation - State: %s, Serial: %s, CN: %s, DNS Names: %s, Reason: %s",
		state,
		serial,
		cn,
		names,
		revocation.ReasonToString[revocationCode],
	)
}

// RevokeCertificateWithReg terminates trust in the certificate provided.
func (ra *RegistrationAuthorityImpl) RevokeCertificateWithReg(ctx context.Context, cert x509.Certificate, revocationCode revocation.Reason, regID int64) error {
	serialString := core.SerialToString(cert.SerialNumber)
	err := ra.SA.MarkCertificateRevoked(ctx, serialString, revocationCode)

	state := "Failure"
	defer func() {
		// Needed:
		//   Serial
		//   CN
		//   DNS names
		//   Revocation reason
		//   Registration ID of requester
		//   Error (if there was one)
		ra.log.AuditInfo(fmt.Sprintf(
			"%s, Request by registration ID: %d",
			revokeEvent(state, serialString, cert.Subject.CommonName, cert.DNSNames, revocationCode),
			regID,
		))
	}()

	if err != nil {
		state = fmt.Sprintf("Failure -- %s", err)
		return err
	}

	state = "Success"
	return nil
}

// AdministrativelyRevokeCertificate terminates trust in the certificate provided and
// does not require the registration ID of the requester since this method is only
// called from the admin-revoker tool.
func (ra *RegistrationAuthorityImpl) AdministrativelyRevokeCertificate(ctx context.Context, cert x509.Certificate, revocationCode revocation.Reason, user string) error {
	serialString := core.SerialToString(cert.SerialNumber)
	err := ra.SA.MarkCertificateRevoked(ctx, serialString, revocationCode)

	state := "Failure"
	defer func() {
		// Needed:
		//   Serial
		//   CN
		//   DNS names
		//   Revocation reason
		//   Name of admin-revoker user
		//   Error (if there was one)
		ra.log.AuditInfo(fmt.Sprintf(
			"%s, admin-revoker user: %s",
			revokeEvent(state, serialString, cert.Subject.CommonName, cert.DNSNames, revocationCode),
			user,
		))
	}()

	if err != nil {
		state = fmt.Sprintf("Failure -- %s", err)
		return err
	}

	state = "Success"
	ra.stats.Inc("RevokedCertificates", 1)
	return nil
}

// onValidationUpdate saves a validation's new status after receiving an
// authorization back from the VA.
func (ra *RegistrationAuthorityImpl) onValidationUpdate(ctx context.Context, authz core.Authorization) error {
	// Consider validation successful if any of the combinations
	// specified in the authorization has been fulfilled
	validated := map[int]bool{}
	for i, ch := range authz.Challenges {
		if ch.Status == core.StatusValid {
			validated[i] = true
		}
	}
	for _, combo := range authz.Combinations {
		comboValid := true
		for _, i := range combo {
			if !validated[i] {
				comboValid = false
				break
			}
		}
		if comboValid {
			authz.Status = core.StatusValid
		}
	}

	// If no validation succeeded, then the authorization is invalid
	// NOTE: This only works because we only ever do one validation
	if authz.Status != core.StatusValid {
		authz.Status = core.StatusInvalid
	} else {
		exp := ra.clk.Now().Add(ra.authorizationLifetime)
		authz.Expires = &exp
	}

	// Finalize the authorization
	err := ra.SA.FinalizeAuthorization(ctx, authz)
	if err != nil {
		return err
	}

	ra.stats.Inc("FinalizedAuthorizations", 1)
	return nil
}

// DeactivateRegistration deactivates a valid registration
func (ra *RegistrationAuthorityImpl) DeactivateRegistration(ctx context.Context, reg core.Registration) error {
	if reg.Status != core.StatusValid {
		return berrors.MalformedError("only valid registrations can be deactivated")
	}
	err := ra.SA.DeactivateRegistration(ctx, reg.ID)
	if err != nil {
		return berrors.InternalServerError(err.Error())
	}
	return nil
}

// DeactivateAuthorization deactivates a currently valid authorization
func (ra *RegistrationAuthorityImpl) DeactivateAuthorization(ctx context.Context, auth core.Authorization) error {
	if auth.Status != core.StatusValid && auth.Status != core.StatusPending {
		return berrors.MalformedError("only valid and pending authorizations can be deactivated")
	}
	err := ra.SA.DeactivateAuthorization(ctx, auth.ID)
	if err != nil {
		return berrors.InternalServerError(err.Error())
	}
	return nil
}

// NewOrder creates a new order object
func (ra *RegistrationAuthorityImpl) NewOrder(ctx context.Context, req *rapb.NewOrderRequest) (*corepb.Order, error) {
	expires := ra.clk.Now().Add(ra.orderLifetime).UnixNano()
	status := string(core.StatusPending)
	order := &corepb.Order{
		RegistrationID: req.RegistrationID,
		Expires:        &expires,
		Csr:            req.Csr,
		Status:         &status,
	}
	parsedCSR, err := x509.ParseCertificateRequest(req.Csr)
	if err != nil {
		return nil, err
	}

	err = csr.VerifyCSR(parsedCSR, ra.maxNames, &ra.keyPolicy, ra.PA, ra.forceCNFromSAN, *req.RegistrationID)
	if err != nil {
		return nil, err
	}

	// TODO(#2955): Replace this with the batched methods
	for _, name := range parsedCSR.DNSNames {
		authz, err := ra.NewAuthorization(ctx, core.Authorization{
			Identifier: core.AcmeIdentifier{
				Type:  core.IdentifierDNS,
				Value: name,
			},
		}, *req.RegistrationID)
		if err != nil {
			return nil, err
		}
		order.Authorizations = append(order.Authorizations, authz.ID)
	}

	storedOrder, err := ra.SA.NewOrder(ctx, order)
	if err != nil {
		return nil, err
	}

	return storedOrder, nil
}
