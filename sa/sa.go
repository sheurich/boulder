package sa

import (
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"

	"github.com/jmhodges/clock"
	"golang.org/x/net/context"
	gorp "gopkg.in/gorp.v1"
	jose "gopkg.in/square/go-jose.v1"

	"github.com/letsencrypt/boulder/core"
	"github.com/letsencrypt/boulder/features"
	blog "github.com/letsencrypt/boulder/log"
	"github.com/letsencrypt/boulder/revocation"
)

// SQLStorageAuthority defines a Storage Authority
type SQLStorageAuthority struct {
	dbMap *gorp.DbMap
	clk   clock.Clock
	log   blog.Logger
}

func digest256(data []byte) []byte {
	d := sha256.New()
	_, _ = d.Write(data) // Never returns an error
	return d.Sum(nil)
}

// Utility models
type pendingauthzModel struct {
	core.Authorization

	LockCol int64
}

type authzModel struct {
	core.Authorization
}

// NewSQLStorageAuthority provides persistence using a SQL backend for
// Boulder. It will modify the given gorp.DbMap by adding relevant tables.
func NewSQLStorageAuthority(dbMap *gorp.DbMap, clk clock.Clock, logger blog.Logger) (*SQLStorageAuthority, error) {
	SetSQLDebug(dbMap, logger)

	ssa := &SQLStorageAuthority{
		dbMap: dbMap,
		clk:   clk,
		log:   logger,
	}

	return ssa, nil
}

func statusIsPending(status core.AcmeStatus) bool {
	return status == core.StatusPending || status == core.StatusProcessing || status == core.StatusUnknown
}

func existingPending(tx *gorp.Transaction, id string) bool {
	var count int64
	_ = tx.SelectOne(&count, "SELECT count(*) FROM pendingAuthorizations WHERE id = :id", map[string]interface{}{"id": id})
	return count > 0
}

func existingFinal(tx *gorp.Transaction, id string) bool {
	var count int64
	_ = tx.SelectOne(&count, "SELECT count(*) FROM authz WHERE id = :id", map[string]interface{}{"id": id})
	return count > 0
}

func existingRegistration(tx *gorp.Transaction, id int64) bool {
	var count int64
	_ = tx.SelectOne(&count, "SELECT count(*) FROM registrations WHERE id = :id", map[string]interface{}{"id": id})
	return count > 0
}

func updateChallenges(authID string, challenges []core.Challenge, tx *gorp.Transaction) error {
	var challs []challModel
	_, err := tx.Select(
		&challs,
		getChallengesQuery,
		map[string]interface{}{"authID": authID},
	)
	if err != nil {
		return err
	}
	if len(challs) != len(challenges) {
		return fmt.Errorf("Invalid number of challenges provided")
	}
	for i, authChall := range challenges {
		chall, err := challengeToModel(&authChall, challs[i].AuthorizationID)
		if err != nil {
			return err
		}
		chall.ID = challs[i].ID
		_, err = tx.Update(chall)
		if err != nil {
			return err
		}
	}
	return nil
}

// GetRegistration obtains a Registration by ID
func (ssa *SQLStorageAuthority) GetRegistration(ctx context.Context, id int64) (core.Registration, error) {
	const query = "WHERE id = ?"
	var model interface{}
	var err error
	if features.Enabled(features.AllowAccountDeactivation) {
		model, err = selectRegistrationv2(ssa.dbMap, query, id)
	} else {
		model, err = selectRegistration(ssa.dbMap, query, id)
	}
	if err == sql.ErrNoRows {
		return core.Registration{}, core.NoSuchRegistrationError(
			fmt.Sprintf("No registrations with ID %d", id),
		)
	}
	if err != nil {
		return core.Registration{}, err
	}
	return modelToRegistration(model)
}

// GetRegistrationByKey obtains a Registration by JWK
func (ssa *SQLStorageAuthority) GetRegistrationByKey(ctx context.Context, key *jose.JsonWebKey) (core.Registration, error) {
	const query = "WHERE jwk_sha256 = ?"
	var model interface{}
	var err error
	if key == nil {
		return core.Registration{}, fmt.Errorf("key argument to GetRegistrationByKey must not be nil")
	}
	sha, err := core.KeyDigest(key.Key)
	if err != nil {
		return core.Registration{}, err
	}
	if features.Enabled(features.AllowAccountDeactivation) {
		model, err = selectRegistrationv2(ssa.dbMap, query, sha)
	} else {
		model, err = selectRegistration(ssa.dbMap, query, sha)
	}
	if err == sql.ErrNoRows {
		msg := fmt.Sprintf("No registrations with public key sha256 %s", sha)
		return core.Registration{}, core.NoSuchRegistrationError(msg)
	}
	if err != nil {
		return core.Registration{}, err
	}

	return modelToRegistration(model)
}

// GetAuthorization obtains an Authorization by ID
func (ssa *SQLStorageAuthority) GetAuthorization(ctx context.Context, id string) (authz core.Authorization, err error) {
	tx, err := ssa.dbMap.Begin()
	if err != nil {
		return
	}

	pa, err := selectPendingAuthz(tx, "WHERE id = ?", id)
	if err != nil && err != sql.ErrNoRows {
		err = Rollback(tx, err)
		return
	}
	if err == sql.ErrNoRows {
		var fa authzModel
		err = tx.SelectOne(&fa, fmt.Sprintf("SELECT %s FROM authz WHERE id = ?", authzFields), id)
		if err == sql.ErrNoRows {
			err = fmt.Errorf("No pendingAuthorization or authz with ID %s", id)
			err = Rollback(tx, err)
			return
		}
		if err != nil {
			err = Rollback(tx, err)
			return
		}
		authz = fa.Authorization
	} else {
		authz = pa.Authorization
	}

	var challObjs []challModel
	_, err = tx.Select(
		&challObjs,
		getChallengesQuery,
		map[string]interface{}{"authID": authz.ID},
	)
	if err != nil {
		err = Rollback(tx, err)
		return
	}
	var challs []core.Challenge
	for _, c := range challObjs {
		chall, err := modelToChallenge(&c)
		if err != nil {
			err = Rollback(tx, err)
			return core.Authorization{}, err
		}
		challs = append(challs, chall)
	}
	authz.Challenges = challs

	err = tx.Commit()
	return
}

// GetValidAuthorizations returns the latest authorization object for all
// domain names from the parameters that the account has authorizations for.
func (ssa *SQLStorageAuthority) GetValidAuthorizations(ctx context.Context, registrationID int64, names []string, now time.Time) (map[string]*core.Authorization, error) {
	if len(names) == 0 {
		return nil, errors.New("GetValidAuthorizations: no names received")
	}

	params := make([]interface{}, len(names))
	qmarks := make([]string, len(names))
	for i, name := range names {
		id := core.AcmeIdentifier{Type: core.IdentifierDNS, Value: name}
		idJSON, err := json.Marshal(id)
		if err != nil {
			return nil, err
		}
		params[i] = string(idJSON)
		qmarks[i] = "?"
	}

	auths, err := selectAuthzs(ssa.dbMap,
		"WHERE registrationID = ? "+
			"AND expires > ? "+
			"AND identifier IN ("+strings.Join(qmarks, ",")+") "+
			"AND status = 'valid'",
		append([]interface{}{registrationID, now}, params...)...)
	if err != nil {
		return nil, err
	}

	byName := make(map[string]*core.Authorization)
	for _, auth := range auths {
		// No real life authorizations should have a nil expires. If we find them,
		// don't consider them valid.
		if auth.Expires == nil {
			continue
		}
		if auth.Identifier.Type != core.IdentifierDNS {
			return nil, fmt.Errorf("unknown identifier type: %q on authz id %q", auth.Identifier.Type, auth.ID)
		}
		existing, present := byName[auth.Identifier.Value]
		if !present || auth.Expires.After(*existing.Expires) {
			byName[auth.Identifier.Value] = auth
		}
	}

	return byName, nil
}

// incrementIP returns a copy of `ip` incremented at a bit index `index`,
// or in other words the first IP of the next highest subnet given a mask of
// length `index`.
// In order to easily account for overflow, we treat ip as a big.Int and add to
// it. If the increment overflows the max size of a net.IP, return the highest
// possible net.IP.
func incrementIP(ip net.IP, index int) net.IP {
	bigInt := new(big.Int)
	bigInt.SetBytes([]byte(ip))
	incr := new(big.Int).Lsh(big.NewInt(1), 128-uint(index))
	bigInt.Add(bigInt, incr)
	// bigInt.Bytes can be shorter than 16 bytes, so stick it into a
	// full-sized net.IP.
	resultBytes := bigInt.Bytes()
	if len(resultBytes) > 16 {
		return net.ParseIP("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff")
	}
	result := make(net.IP, 16)
	copy(result[16-len(resultBytes):], resultBytes)
	return result
}

// ipRange returns a range of IP addresses suitable for querying MySQL for the
// purpose of rate limiting using a range that is inclusive on the lower end and
// exclusive at the higher end. If ip is an IPv4 address, it returns that address,
// plus the one immediately higher than it. If ip is an IPv6 address, it applies
// a /48 mask to it and returns the lowest IP in the resulting network, and the
// first IP outside of the resulting network.
func ipRange(ip net.IP) (net.IP, net.IP) {
	ip = ip.To16()
	// For IPv6, match on a certain subnet range, since one person can commonly
	// have an entire /48 to themselves.
	maskLength := 48
	// For IPv4 addresses, do a match on exact address, so begin = ip and end =
	// next higher IP.
	if ip.To4() != nil {
		maskLength = 128
	}

	mask := net.CIDRMask(maskLength, 128)
	begin := ip.Mask(mask)
	end := incrementIP(begin, maskLength)

	return begin, end
}

// CountRegistrationsByIP returns the number of registrations created in the
// time range in an IP range. For IPv4 addresses, that range is limited to the
// single IP. For IPv6 addresses, that range is a /48, since it's not uncommon
// for one person to have a /48 to themselves.
func (ssa *SQLStorageAuthority) CountRegistrationsByIP(ctx context.Context, ip net.IP, earliest time.Time, latest time.Time) (int, error) {
	var count int64
	beginIP, endIP := ipRange(ip)
	err := ssa.dbMap.SelectOne(
		&count,
		`SELECT COUNT(1) FROM registrations
		 WHERE
		 :beginIP <= initialIP AND
		 initialIP < :endIP AND
		 :earliest < createdAt AND
		 createdAt <= :latest`,
		map[string]interface{}{
			"ip":       ip.String(),
			"earliest": earliest,
			"latest":   latest,
			"beginIP":  []byte(beginIP),
			"endIP":    []byte(endIP),
		})
	if err != nil {
		return -1, err
	}
	return int(count), nil
}

// TooManyCertificatesError indicates that the number of certificates returned by
// CountCertificates exceeded the hard-coded limit of 10,000 certificates.
type TooManyCertificatesError string

func (t TooManyCertificatesError) Error() string {
	return string(t)
}

// CountCertificatesByNames counts, for each input domain, the number of
// certificates issued in the given time range for that domain and its
// subdomains. It returns a map from domains to counts, which is guaranteed to
// contain an entry for each input domain, so long as err is nil.
// The highest count this function can return is 10,000. If there are more
// certificates than that matching one of the provided domain names, it will return
// TooManyCertificatesError.
func (ssa *SQLStorageAuthority) CountCertificatesByNames(ctx context.Context, domains []string, earliest, latest time.Time) (map[string]int, error) {
	ret := make(map[string]int, len(domains))
	for _, domain := range domains {
		currentCount, err := ssa.countCertificatesByName(domain, earliest, latest)
		if err != nil {
			return ret, err
		}
		ret[domain] = currentCount
	}
	return ret, nil
}

// countCertificatesByNames returns, for a single domain, the count of
// certificates issued in the given time range for that domain and its
// subdomains.
// The highest count this function can return is 10,000. If there are more
// certificates than that matching one of the provided domain names, it will return
// TooManyCertificatesError.
func (ssa *SQLStorageAuthority) countCertificatesByName(domain string, earliest, latest time.Time) (int, error) {
	var count int64
	const max = 10000
	var serials []struct {
		Serial string
	}
	_, err := ssa.dbMap.Select(
		&serials,
		`SELECT serial from issuedNames
		 WHERE (reversedName = :reversedDomain OR
			      reversedName LIKE CONCAT(:reversedDomain, ".%"))
		 AND notBefore > :earliest AND notBefore <= :latest
		 LIMIT :limit;`,
		map[string]interface{}{
			"reversedDomain": core.ReverseName(domain),
			"earliest":       earliest,
			"latest":         latest,
			"limit":          max + 1,
		})
	if err == sql.ErrNoRows {
		return 0, nil
	} else if err != nil {
		return -1, err
	} else if count > max {
		return max, TooManyCertificatesError(fmt.Sprintf("More than %d issuedName entries for %s.", max, domain))
	}
	serialMap := make(map[string]struct{}, len(serials))
	for _, s := range serials {
		serialMap[s.Serial] = struct{}{}
	}

	return len(serialMap), nil
}

// GetCertificate takes a serial number and returns the corresponding
// certificate, or error if it does not exist.
func (ssa *SQLStorageAuthority) GetCertificate(ctx context.Context, serial string) (core.Certificate, error) {
	if !core.ValidSerial(serial) {
		err := fmt.Errorf("Invalid certificate serial %s", serial)
		return core.Certificate{}, err
	}

	cert, err := SelectCertificate(ssa.dbMap, "WHERE serial = ?", serial)
	if err == sql.ErrNoRows {
		return core.Certificate{}, core.NotFoundError(fmt.Sprintf("No certificate found for %s", serial))
	}
	if err != nil {
		return core.Certificate{}, err
	}
	return cert, err
}

// GetCertificateStatus takes a hexadecimal string representing the full 128-bit serial
// number of a certificate and returns data about that certificate's current
// validity.
func (ssa *SQLStorageAuthority) GetCertificateStatus(ctx context.Context, serial string) (core.CertificateStatus, error) {
	if !core.ValidSerial(serial) {
		err := fmt.Errorf("Invalid certificate serial %s", serial)
		return core.CertificateStatus{}, err
	}

	var status core.CertificateStatus
	if features.Enabled(features.CertStatusOptimizationsMigrated) {
		statusObj, err := ssa.dbMap.Get(certStatusModelv2{}, serial)
		if err != nil {
			return status, err
		}
		if statusObj == nil {
			return status, nil
		}
		statusModel := statusObj.(*certStatusModelv2)
		status = core.CertificateStatus{
			Serial:                statusModel.Serial,
			SubscriberApproved:    statusModel.SubscriberApproved,
			Status:                statusModel.Status,
			OCSPLastUpdated:       statusModel.OCSPLastUpdated,
			RevokedDate:           statusModel.RevokedDate,
			RevokedReason:         statusModel.RevokedReason,
			LastExpirationNagSent: statusModel.LastExpirationNagSent,
			OCSPResponse:          statusModel.OCSPResponse,
			NotAfter:              statusModel.NotAfter,
			IsExpired:             statusModel.IsExpired,
			LockCol:               statusModel.LockCol,
		}
	} else {
		statusObj, err := ssa.dbMap.Get(certStatusModelv1{}, serial)
		if err != nil {
			return status, err
		}
		if statusObj == nil {
			return status, nil
		}
		statusModel := statusObj.(*certStatusModelv1)
		status = core.CertificateStatus{
			Serial:                statusModel.Serial,
			SubscriberApproved:    statusModel.SubscriberApproved,
			Status:                statusModel.Status,
			OCSPLastUpdated:       statusModel.OCSPLastUpdated,
			RevokedDate:           statusModel.RevokedDate,
			RevokedReason:         statusModel.RevokedReason,
			LastExpirationNagSent: statusModel.LastExpirationNagSent,
			OCSPResponse:          statusModel.OCSPResponse,
			LockCol:               statusModel.LockCol,
		}
	}

	return status, nil
}

// NewRegistration stores a new Registration
func (ssa *SQLStorageAuthority) NewRegistration(ctx context.Context, reg core.Registration) (core.Registration, error) {
	reg.CreatedAt = ssa.clk.Now()
	rm, err := registrationToModel(&reg)
	if err != nil {
		return reg, err
	}
	err = ssa.dbMap.Insert(rm)
	if err != nil {
		return reg, err
	}
	return modelToRegistration(rm)
}

// MarkCertificateRevoked stores the fact that a certificate is revoked, along
// with a timestamp and a reason.
func (ssa *SQLStorageAuthority) MarkCertificateRevoked(ctx context.Context, serial string, reasonCode revocation.Reason) error {
	var err error
	if _, err = ssa.GetCertificate(ctx, serial); err != nil {
		return fmt.Errorf(
			"Unable to mark certificate %s revoked: cert not found.", serial)
	}

	if _, err = ssa.GetCertificateStatus(ctx, serial); err != nil {
		return fmt.Errorf(
			"Unable to mark certificate %s revoked: cert status not found.", serial)
	}

	tx, err := ssa.dbMap.Begin()
	if err != nil {
		return err
	}

	const statusQuery = "WHERE serial = ?"
	var statusObj interface{}

	if features.Enabled(features.CertStatusOptimizationsMigrated) {
		statusObj, err = SelectCertificateStatusv2(tx, statusQuery, serial)
	} else {
		statusObj, err = SelectCertificateStatus(tx, statusQuery, serial)
	}
	if err == sql.ErrNoRows {
		err = fmt.Errorf("No certificate with serial %s", serial)
		err = Rollback(tx, err)
		return err
	}
	if err != nil {
		err = Rollback(tx, err)
		return err
	}

	var n int64
	now := ssa.clk.Now()
	if features.Enabled(features.CertStatusOptimizationsMigrated) {
		status := statusObj.(certStatusModelv2)
		status.Status = core.OCSPStatusRevoked
		status.RevokedDate = now
		status.RevokedReason = reasonCode
		n, err = tx.Update(&status)
	} else {
		status := statusObj.(certStatusModelv1)
		status.Status = core.OCSPStatusRevoked
		status.RevokedDate = now
		status.RevokedReason = reasonCode
		n, err = tx.Update(&status)
	}
	if err != nil {
		err = Rollback(tx, err)
		return err
	}
	if n == 0 {
		err = errors.New("No certificate updated. Maybe the lock column was off?")
		err = Rollback(tx, err)
		return err
	}

	return tx.Commit()
}

// UpdateRegistration stores an updated Registration
func (ssa *SQLStorageAuthority) UpdateRegistration(ctx context.Context, reg core.Registration) error {
	const query = "WHERE id = ?"
	var model interface{}
	var err error
	if features.Enabled(features.AllowAccountDeactivation) {
		model, err = selectRegistrationv2(ssa.dbMap, query, reg.ID)
	} else {
		model, err = selectRegistration(ssa.dbMap, query, reg.ID)
	}
	if err == sql.ErrNoRows {
		msg := fmt.Sprintf("No registrations with ID %d", reg.ID)
		return core.NoSuchRegistrationError(msg)
	}

	updatedRegModel, err := registrationToModel(&reg)
	if err != nil {
		return err
	}

	// Since registrationToModel has to return an interface so that we can use either model
	// version we need to cast both the updated and existing model to their proper types
	// so that we can copy over the LockCol from one to the other. Once we have copied
	// that field we reassign to the interface so gorp can properly update it.
	if features.Enabled(features.AllowAccountDeactivation) {
		erm := model.(*regModelv2)
		urm := updatedRegModel.(*regModelv2)
		urm.LockCol = erm.LockCol
		updatedRegModel = urm
	} else {
		erm := model.(*regModelv1)
		urm := updatedRegModel.(*regModelv1)
		urm.LockCol = erm.LockCol
		updatedRegModel = urm
	}

	n, err := ssa.dbMap.Update(updatedRegModel)
	if err != nil {
		return err
	}
	if n == 0 {
		msg := fmt.Sprintf("Requested registration not found %d", reg.ID)
		return core.NoSuchRegistrationError(msg)
	}

	return nil
}

// NewPendingAuthorization stores a new Pending Authorization
func (ssa *SQLStorageAuthority) NewPendingAuthorization(ctx context.Context, authz core.Authorization) (core.Authorization, error) {
	var output core.Authorization
	tx, err := ssa.dbMap.Begin()
	if err != nil {
		return output, err
	}

	// Check that it doesn't exist already
	authz.ID = core.NewToken()
	for existingPending(tx, authz.ID) || existingFinal(tx, authz.ID) {
		authz.ID = core.NewToken()
	}

	// Insert a stub row in pending
	pendingAuthz := pendingauthzModel{Authorization: authz}
	err = tx.Insert(&pendingAuthz)
	if err != nil {
		err = Rollback(tx, err)
		return output, err
	}

	for i, c := range authz.Challenges {
		challModel, err := challengeToModel(&c, pendingAuthz.ID)
		if err != nil {
			err = Rollback(tx, err)
			return output, err
		}
		// Magic happens here: Gorp will modify challModel, setting challModel.ID
		// to the auto-increment primary key. This is important because we want
		// the challenge objects inside the Authorization we return to know their
		// IDs, so they can have proper URLs.
		// See https://godoc.org/github.com/coopernurse/gorp#DbMap.Insert
		err = tx.Insert(challModel)
		if err != nil {
			err = Rollback(tx, err)
			return output, err
		}
		challenge, err := modelToChallenge(challModel)
		if err != nil {
			err = Rollback(tx, err)
			return output, err
		}
		authz.Challenges[i] = challenge
	}

	err = tx.Commit()
	output = pendingAuthz.Authorization
	output.Challenges = authz.Challenges
	return output, nil
}

// UpdatePendingAuthorization updates a Pending Authorization
func (ssa *SQLStorageAuthority) UpdatePendingAuthorization(ctx context.Context, authz core.Authorization) error {
	tx, err := ssa.dbMap.Begin()
	if err != nil {
		return err
	}

	if !statusIsPending(authz.Status) {
		err = errors.New("Use FinalizeAuthorization() to update to a final status")
		return Rollback(tx, err)
	}

	if existingFinal(tx, authz.ID) {
		err = errors.New("Cannot update a final authorization")
		return Rollback(tx, err)
	}

	if !existingPending(tx, authz.ID) {
		err = errors.New("Requested authorization not found " + authz.ID)
		return Rollback(tx, err)
	}

	pa, err := selectPendingAuthz(tx, "WHERE id = ?", authz.ID)
	if err == sql.ErrNoRows {
		return Rollback(tx, fmt.Errorf("No pending authorization with ID %s", authz.ID))
	}
	if err != nil {
		return Rollback(tx, err)
	}
	pa.Authorization = authz
	_, err = tx.Update(pa)
	if err != nil {
		return Rollback(tx, err)
	}

	err = updateChallenges(authz.ID, authz.Challenges, tx)
	if err != nil {
		return Rollback(tx, err)
	}

	return tx.Commit()
}

// FinalizeAuthorization converts a Pending Authorization to a final one
func (ssa *SQLStorageAuthority) FinalizeAuthorization(ctx context.Context, authz core.Authorization) error {
	tx, err := ssa.dbMap.Begin()
	if err != nil {
		return err
	}

	// Check that a pending authz exists
	if !existingPending(tx, authz.ID) {
		err = errors.New("Cannot finalize an authorization that is not pending")
		return Rollback(tx, err)
	}
	if statusIsPending(authz.Status) {
		err = errors.New("Cannot finalize to a non-final status")
		return Rollback(tx, err)
	}

	auth := &authzModel{authz}
	pa, err := selectPendingAuthz(tx, "WHERE id = ?", authz.ID)
	if err == sql.ErrNoRows {
		return Rollback(tx, fmt.Errorf("No pending authorization with ID %s", authz.ID))
	}
	if err != nil {
		return Rollback(tx, err)
	}

	err = tx.Insert(auth)
	if err != nil {
		return Rollback(tx, err)
	}

	_, err = tx.Delete(pa)
	if err != nil {
		return Rollback(tx, err)
	}

	err = updateChallenges(authz.ID, authz.Challenges, tx)
	if err != nil {
		return Rollback(tx, err)
	}

	return tx.Commit()
}

// RevokeAuthorizationsByDomain invalidates all pending or finalized authorizations
// for a specific domain
func (ssa *SQLStorageAuthority) RevokeAuthorizationsByDomain(ctx context.Context, ident core.AcmeIdentifier) (int64, int64, error) {
	identifierJSON, err := json.Marshal(ident)
	if err != nil {
		return 0, 0, err
	}
	identifier := string(identifierJSON)
	results := []int64{0, 0}

	now := ssa.clk.Now()
	for i, table := range []string{"authz", "pendingAuthorizations"} {
		for {
			authz, err := getAuthorizationIDsByDomain(ssa.dbMap, table, identifier, now)
			if err != nil {
				return results[0], results[1], err
			}
			numAuthz := len(authz)
			if numAuthz == 0 {
				break
			}

			numRevoked, err := revokeAuthorizations(ssa.dbMap, table, authz)
			if err != nil {
				return results[0], results[1], err
			}
			results[i] += numRevoked
			if numRevoked < int64(numAuthz) {
				return results[0], results[1], fmt.Errorf("Didn't revoke all found authorizations")
			}
		}
	}

	return results[0], results[1], nil
}

// AddCertificate stores an issued certificate and returns the digest as
// a string, or an error if any occured.
func (ssa *SQLStorageAuthority) AddCertificate(ctx context.Context, certDER []byte, regID int64) (string, error) {
	parsedCertificate, err := x509.ParseCertificate(certDER)
	if err != nil {
		return "", err
	}
	digest := core.Fingerprint256(certDER)
	serial := core.SerialToString(parsedCertificate.SerialNumber)

	cert := &core.Certificate{
		RegistrationID: regID,
		Serial:         serial,
		Digest:         digest,
		DER:            certDER,
		Issued:         ssa.clk.Now(),
		Expires:        parsedCertificate.NotAfter,
	}

	var certStatusOb interface{}
	if features.Enabled(features.CertStatusOptimizationsMigrated) {
		certStatusOb = &certStatusModelv2{
			certStatusModelv1: certStatusModelv1{
				SubscriberApproved: false,
				Status:             core.OCSPStatus("good"),
				OCSPLastUpdated:    time.Time{},
				OCSPResponse:       []byte{},
				Serial:             serial,
				RevokedDate:        time.Time{},
				RevokedReason:      0,
				LockCol:            0,
			},
			NotAfter: parsedCertificate.NotAfter,
		}
	} else {
		certStatusOb = &certStatusModelv1{
			SubscriberApproved: false,
			Status:             core.OCSPStatus("good"),
			OCSPLastUpdated:    time.Time{},
			OCSPResponse:       []byte{},
			Serial:             serial,
			RevokedDate:        time.Time{},
			RevokedReason:      0,
			LockCol:            0,
		}
	}

	tx, err := ssa.dbMap.Begin()
	if err != nil {
		return "", err
	}

	// Note: will fail on duplicate serials. Extremely unlikely to happen and soon
	// to be fixed by redesign. Reference issue
	// https://github.com/letsencrypt/boulder/issues/2265 for more
	err = tx.Insert(cert)
	if err != nil {
		return "", Rollback(tx, err)
	}

	err = tx.Insert(certStatusOb)
	if err != nil {
		return "", Rollback(tx, err)
	}

	err = addIssuedNames(tx, parsedCertificate)
	if err != nil {
		return "", Rollback(tx, err)
	}

	err = addFQDNSet(
		tx,
		parsedCertificate.DNSNames,
		serial,
		parsedCertificate.NotBefore,
		parsedCertificate.NotAfter,
	)
	if err != nil {
		return "", Rollback(tx, err)
	}

	return digest, tx.Commit()
}

// CountCertificatesRange returns the number of certificates issued in a specific
// date range
func (ssa *SQLStorageAuthority) CountCertificatesRange(ctx context.Context, start, end time.Time) (int64, error) {
	var count int64
	err := ssa.dbMap.SelectOne(
		&count,
		`SELECT COUNT(1) FROM certificates
		WHERE issued >= :windowLeft
		AND issued < :windowRight`,
		map[string]interface{}{
			"windowLeft":  start,
			"windowRight": end,
		},
	)
	return count, err
}

// CountPendingAuthorizations returns the number of pending, unexpired
// authorizations for the give registration.
func (ssa *SQLStorageAuthority) CountPendingAuthorizations(ctx context.Context, regID int64) (count int, err error) {
	err = ssa.dbMap.SelectOne(&count,
		`SELECT count(1) FROM pendingAuthorizations
		WHERE registrationID = :regID AND
		expires > :now AND
		status = :pending`,
		map[string]interface{}{
			"regID":   regID,
			"now":     ssa.clk.Now(),
			"pending": string(core.StatusPending),
		})
	return
}

// ErrNoReceipt is an error type for non-existent SCT receipt
type ErrNoReceipt string

func (e ErrNoReceipt) Error() string {
	return string(e)
}

// GetSCTReceipt gets a specific SCT receipt for a given certificate serial and
// CT log ID
func (ssa *SQLStorageAuthority) GetSCTReceipt(ctx context.Context, serial string, logID string) (core.SignedCertificateTimestamp, error) {
	receipt, err := selectSctReceipt(ssa.dbMap, "WHERE certificateSerial = ? AND logID = ?", serial, logID)
	if err == sql.ErrNoRows {
		return receipt, ErrNoReceipt(err.Error())
	}
	return receipt, err
}

// AddSCTReceipt adds a new SCT receipt to the (append-only) sctReceipts table
func (ssa *SQLStorageAuthority) AddSCTReceipt(ctx context.Context, sct core.SignedCertificateTimestamp) error {
	err := ssa.dbMap.Insert(&sct)
	// For AddSCTReceipt, duplicates are explicitly OK, so don't return errors
	// based on duplicates, especially because we currently retry all submissions
	// for a certificate if even one of them fails. Once https://github.com/letsencrypt/boulder/issues/891
	// is fixed, we may want to start returning this as an error, or logging it.
	if err != nil && strings.HasPrefix(err.Error(), "Error 1062: Duplicate entry") {
		return nil
	}
	return err
}

func hashNames(names []string) []byte {
	names = core.UniqueLowerNames(names)
	hash := sha256.Sum256([]byte(strings.Join(names, ",")))
	return hash[:]
}

func addFQDNSet(tx *gorp.Transaction, names []string, serial string, issued time.Time, expires time.Time) error {
	return tx.Insert(&core.FQDNSet{
		SetHash: hashNames(names),
		Serial:  serial,
		Issued:  issued,
		Expires: expires,
	})
}

type execable interface {
	Exec(string, ...interface{}) (sql.Result, error)
}

func addIssuedNames(tx execable, cert *x509.Certificate) error {
	var qmarks []string
	var values []interface{}
	for _, name := range cert.DNSNames {
		values = append(values,
			core.ReverseName(name),
			core.SerialToString(cert.SerialNumber),
			cert.NotBefore)
		qmarks = append(qmarks, "(?, ?, ?)")
	}
	query := `INSERT INTO issuedNames (reversedName, serial, notBefore) VALUES ` + strings.Join(qmarks, ", ") + `;`
	_, err := tx.Exec(query, values...)
	return err
}

// CountFQDNSets returns the number of sets with hash |setHash| within the window
// |window|
func (ssa *SQLStorageAuthority) CountFQDNSets(ctx context.Context, window time.Duration, names []string) (int64, error) {
	var count int64
	err := ssa.dbMap.SelectOne(
		&count,
		`SELECT COUNT(1) FROM fqdnSets
		WHERE setHash = ?
		AND issued > ?`,
		hashNames(names),
		ssa.clk.Now().Add(-window),
	)
	return count, err
}

// FQDNSetExists returns a bool indicating if one or more FQDN sets |names|
// exists in the database
func (ssa *SQLStorageAuthority) FQDNSetExists(ctx context.Context, names []string) (bool, error) {
	var count int64
	err := ssa.dbMap.SelectOne(
		&count,
		`SELECT COUNT(1) FROM fqdnSets
		WHERE setHash = ?
		LIMIT 1`,
		hashNames(names),
	)
	return count > 0, err
}

// DeactivateRegistration deactivates a currently valid registration
func (ssa *SQLStorageAuthority) DeactivateRegistration(ctx context.Context, id int64) error {
	_, err := ssa.dbMap.Exec(
		"UPDATE registrations SET status = ? WHERE status = ? AND id = ?",
		string(core.StatusDeactivated),
		string(core.StatusValid),
		id,
	)
	return err
}

// DeactivateAuthorization deactivates a currently valid or pending authorization
func (ssa *SQLStorageAuthority) DeactivateAuthorization(ctx context.Context, id string) error {
	tx, err := ssa.dbMap.Begin()
	if err != nil {
		return err
	}
	table := "authz"
	oldStatus := core.StatusValid
	if existingPending(tx, id) {
		table = "pendingAuthorizations"
		oldStatus = core.StatusPending
	}

	_, err = tx.Exec(
		fmt.Sprintf(`UPDATE %s SET status = ? WHERE id = ? and status = ?`, table),
		string(core.StatusDeactivated),
		id,
		string(oldStatus),
	)
	if err != nil {
		err = Rollback(tx, err)
		return err
	}
	return tx.Commit()
}
