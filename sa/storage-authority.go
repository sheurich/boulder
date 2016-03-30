// Copyright 2014 ISRG.  All rights reserved
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

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
	"sort"
	"strings"
	"time"

	"github.com/letsencrypt/boulder/Godeps/_workspace/src/github.com/jmhodges/clock"
	jose "github.com/letsencrypt/boulder/Godeps/_workspace/src/github.com/square/go-jose"
	gorp "github.com/letsencrypt/boulder/Godeps/_workspace/src/gopkg.in/gorp.v1"

	"github.com/letsencrypt/boulder/core"
	blog "github.com/letsencrypt/boulder/log"
)

const getChallengesQuery = "SELECT * FROM challenges WHERE authorizationID = :authID ORDER BY id ASC"

// SQLStorageAuthority defines a Storage Authority
type SQLStorageAuthority struct {
	dbMap *gorp.DbMap
	clk   clock.Clock
	log   *blog.AuditLogger
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
func NewSQLStorageAuthority(dbMap *gorp.DbMap, clk clock.Clock) (*SQLStorageAuthority, error) {
	logger := blog.GetAuditLogger()

	logger.Notice("Storage Authority Starting")
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
func (ssa *SQLStorageAuthority) GetRegistration(id int64) (core.Registration, error) {
	regObj, err := ssa.dbMap.Get(regModel{}, id)
	if err != nil {
		return core.Registration{}, err
	}
	if regObj == nil {
		msg := fmt.Sprintf("No registrations with ID %d", id)
		return core.Registration{}, core.NoSuchRegistrationError(msg)
	}
	regPtr, ok := regObj.(*regModel)
	if !ok {
		return core.Registration{}, fmt.Errorf("Invalid cast to reg model object")
	}
	return modelToRegistration(regPtr)
}

// GetRegistrationByKey obtains a Registration by JWK
func (ssa *SQLStorageAuthority) GetRegistrationByKey(key jose.JsonWebKey) (core.Registration, error) {
	reg := &regModel{}
	sha, err := core.KeyDigest(key.Key)
	if err != nil {
		return core.Registration{}, err
	}
	err = ssa.dbMap.SelectOne(reg, "SELECT * FROM registrations WHERE jwk_sha256 = :key", map[string]interface{}{"key": sha})

	if err == sql.ErrNoRows {
		msg := fmt.Sprintf("No registrations with public key sha256 %s", sha)
		return core.Registration{}, core.NoSuchRegistrationError(msg)
	}
	if err != nil {
		return core.Registration{}, err
	}

	return modelToRegistration(reg)
}

// GetAuthorization obtains an Authorization by ID
func (ssa *SQLStorageAuthority) GetAuthorization(id string) (authz core.Authorization, err error) {
	tx, err := ssa.dbMap.Begin()
	if err != nil {
		return
	}

	authObj, err := tx.Get(pendingauthzModel{}, id)
	if err != nil {
		tx.Rollback()
		return
	}
	if authObj != nil {
		authD := *authObj.(*pendingauthzModel)
		authz = authD.Authorization
	} else {
		authObj, err = tx.Get(authzModel{}, id)
		if err != nil {
			tx.Rollback()
			return
		}
		if authObj == nil {
			err = fmt.Errorf("No pendingAuthorization or authz with ID %s", id)
			tx.Rollback()
			return
		}
		authD := authObj.(*authzModel)
		authz = authD.Authorization
	}

	var challObjs []challModel
	_, err = tx.Select(
		&challObjs,
		getChallengesQuery,
		map[string]interface{}{"authID": authz.ID},
	)
	if err != nil {
		tx.Rollback()
		return
	}
	var challs []core.Challenge
	for _, c := range challObjs {
		chall, err := modelToChallenge(&c)
		if err != nil {
			tx.Rollback()
			return core.Authorization{}, err
		}
		challs = append(challs, chall)
	}
	authz.Challenges = challs

	err = tx.Commit()
	return
}

// GetLatestValidAuthorization gets the valid authorization with biggest expire date for a given domain and registrationId
func (ssa *SQLStorageAuthority) GetLatestValidAuthorization(registrationID int64, identifier core.AcmeIdentifier) (authz core.Authorization, err error) {
	ident, err := json.Marshal(identifier)
	if err != nil {
		return
	}
	var auth core.Authorization
	err = ssa.dbMap.SelectOne(&auth, "SELECT id FROM authz "+
		"WHERE identifier = :identifier AND registrationID = :registrationID AND status = 'valid' "+
		"ORDER BY expires DESC LIMIT 1",
		map[string]interface{}{"identifier": string(ident), "registrationID": registrationID})
	if err != nil {
		return
	}

	return ssa.GetAuthorization(auth.ID)
}

// GetValidAuthorizations returns the latest authorization object for all
// domain names from the parameters that the account has authorizations for.
func (ssa *SQLStorageAuthority) GetValidAuthorizations(registrationID int64, names []string, now time.Time) (latest map[string]*core.Authorization, err error) {
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

	var auths []*core.Authorization
	_, err = ssa.dbMap.Select(&auths, `
		SELECT * FROM authz
		WHERE registrationID = ?
		AND expires > ?
		AND identifier IN (`+strings.Join(qmarks, ",")+`)
		AND status = 'valid'
		ORDER BY expires ASC
		`, append([]interface{}{registrationID, now}, params...)...)
	if err != nil {
		return nil, err
	}

	byName := make(map[string]*core.Authorization)
	for _, auth := range auths {
		if auth.Identifier.Type != core.IdentifierDNS {
			return nil, fmt.Errorf("unknown identifier type: %q on authz id %q", auth.Identifier.Type, auth.ID)
		}
		// Due to ORDER BY expires, this results in the latest value
		// for each name being used.
		byName[auth.Identifier.Value] = auth
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
func (ssa *SQLStorageAuthority) CountRegistrationsByIP(ip net.IP, earliest time.Time, latest time.Time) (int, error) {
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
func (ssa *SQLStorageAuthority) CountCertificatesByNames(domains []string, earliest, latest time.Time) (map[string]int, error) {
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
func (ssa *SQLStorageAuthority) GetCertificate(serial string) (core.Certificate, error) {
	if !core.ValidSerial(serial) {
		err := fmt.Errorf("Invalid certificate serial %s", serial)
		return core.Certificate{}, err
	}

	certObj, err := ssa.dbMap.Get(core.Certificate{}, serial)
	if err != nil {
		return core.Certificate{}, err
	}
	if certObj == nil {
		ssa.log.Debug(fmt.Sprintf("Nil cert for %s", serial))
		return core.Certificate{}, core.NotFoundError(fmt.Sprintf("No certificate found for %s", serial))
	}

	certPtr, ok := certObj.(*core.Certificate)
	if !ok {
		ssa.log.Debug("Failed to convert cert")
		return core.Certificate{}, fmt.Errorf("Error converting certificate response for %s", serial)
	}
	return *certPtr, err
}

// GetCertificateStatus takes a hexadecimal string representing the full 128-bit serial
// number of a certificate and returns data about that certificate's current
// validity.
func (ssa *SQLStorageAuthority) GetCertificateStatus(serial string) (status core.CertificateStatus, err error) {
	if !core.ValidSerial(serial) {
		err := fmt.Errorf("Invalid certificate serial %s", serial)
		return core.CertificateStatus{}, err
	}

	certificateStats, err := ssa.dbMap.Get(core.CertificateStatus{}, serial)
	if err != nil {
		return
	}
	status = *certificateStats.(*core.CertificateStatus)
	return
}

// NewRegistration stores a new Registration
func (ssa *SQLStorageAuthority) NewRegistration(reg core.Registration) (core.Registration, error) {
	rm, err := registrationToModel(&reg)
	if err != nil {
		return reg, err
	}
	rm.CreatedAt = ssa.clk.Now()
	err = ssa.dbMap.Insert(rm)
	if err != nil {
		return reg, err
	}
	return modelToRegistration(rm)
}

// UpdateOCSP stores an updated OCSP response.
func (ssa *SQLStorageAuthority) UpdateOCSP(serial string, ocspResponse []byte) (err error) {
	status, err := ssa.GetCertificateStatus(serial)
	if err != nil {
		return fmt.Errorf(
			"Unable to update OCSP for certificate %s: cert status not found.", serial)
	}

	status.OCSPResponse = ocspResponse
	status.OCSPLastUpdated = ssa.clk.Now()
	_, err = ssa.dbMap.Update(&status)
	return err
}

// MarkCertificateRevoked stores the fact that a certificate is revoked, along
// with a timestamp and a reason.
func (ssa *SQLStorageAuthority) MarkCertificateRevoked(serial string, reasonCode core.RevocationCode) (err error) {
	if _, err = ssa.GetCertificate(serial); err != nil {
		return fmt.Errorf(
			"Unable to mark certificate %s revoked: cert not found.", serial)
	}

	if _, err = ssa.GetCertificateStatus(serial); err != nil {
		return fmt.Errorf(
			"Unable to mark certificate %s revoked: cert status not found.", serial)
	}

	tx, err := ssa.dbMap.Begin()
	if err != nil {
		return
	}

	statusObj, err := tx.Get(core.CertificateStatus{}, serial)
	if err != nil {
		tx.Rollback()
		return
	}
	if statusObj == nil {
		err = fmt.Errorf("No certificate with serial %s", serial)
		tx.Rollback()
		return
	}
	now := ssa.clk.Now()
	status := statusObj.(*core.CertificateStatus)
	status.Status = core.OCSPStatusRevoked
	status.RevokedDate = now
	status.RevokedReason = reasonCode

	n, err := tx.Update(status)
	if err != nil {
		tx.Rollback()
		return
	}
	if n == 0 {
		tx.Rollback()
		err = errors.New("No certificate updated. Maybe the lock column was off?")
		return
	}
	err = tx.Commit()

	return
}

// UpdateRegistration stores an updated Registration
func (ssa *SQLStorageAuthority) UpdateRegistration(reg core.Registration) error {
	lookupResult, err := ssa.dbMap.Get(regModel{}, reg.ID)
	if err != nil {
		return err
	}
	if lookupResult == nil {
		msg := fmt.Sprintf("No registrations with ID %d", reg.ID)
		return core.NoSuchRegistrationError(msg)
	}
	existingRegModel, ok := lookupResult.(*regModel)
	if !ok {
		// Shouldn't happen
		return fmt.Errorf("Incorrect type returned from registration lookup")
	}

	updatedRegModel, err := registrationToModel(&reg)
	if err != nil {
		return err
	}
	updatedRegModel.LockCol = existingRegModel.LockCol

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
func (ssa *SQLStorageAuthority) NewPendingAuthorization(authz core.Authorization) (output core.Authorization, err error) {
	tx, err := ssa.dbMap.Begin()
	if err != nil {
		return
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
		tx.Rollback()
		return
	}

	for i, c := range authz.Challenges {
		challModel, err := challengeToModel(&c, pendingAuthz.ID)
		if err != nil {
			tx.Rollback()
			return core.Authorization{}, err
		}
		// Magic happens here: Gorp will modify challModel, setting challModel.ID
		// to the auto-increment primary key. This is important because we want
		// the challenge objects inside the Authorization we return to know their
		// IDs, so they can have proper URLs.
		// See https://godoc.org/github.com/coopernurse/gorp#DbMap.Insert
		err = tx.Insert(challModel)
		if err != nil {
			tx.Rollback()
			return core.Authorization{}, err
		}
		challenge, err := modelToChallenge(challModel)
		if err != nil {
			tx.Rollback()
			return core.Authorization{}, err
		}
		authz.Challenges[i] = challenge
	}

	err = tx.Commit()
	output = pendingAuthz.Authorization
	output.Challenges = authz.Challenges
	return
}

// UpdatePendingAuthorization updates a Pending Authorization
func (ssa *SQLStorageAuthority) UpdatePendingAuthorization(authz core.Authorization) (err error) {
	tx, err := ssa.dbMap.Begin()
	if err != nil {
		return
	}

	if !statusIsPending(authz.Status) {
		err = errors.New("Use FinalizeAuthorization() to update to a final status")
		tx.Rollback()
		return
	}

	if existingFinal(tx, authz.ID) {
		err = errors.New("Cannot update a final authorization")
		tx.Rollback()
		return
	}

	if !existingPending(tx, authz.ID) {
		err = errors.New("Requested authorization not found " + authz.ID)
		tx.Rollback()
		return
	}

	authObj, err := tx.Get(pendingauthzModel{}, authz.ID)
	if err != nil {
		tx.Rollback()
		return
	}
	auth := authObj.(*pendingauthzModel)
	auth.Authorization = authz
	_, err = tx.Update(auth)
	if err != nil {
		tx.Rollback()
		return
	}

	err = updateChallenges(authz.ID, authz.Challenges, tx)
	if err != nil {
		tx.Rollback()
		return
	}

	err = tx.Commit()
	return
}

// FinalizeAuthorization converts a Pending Authorization to a final one
func (ssa *SQLStorageAuthority) FinalizeAuthorization(authz core.Authorization) (err error) {
	tx, err := ssa.dbMap.Begin()
	if err != nil {
		return
	}

	// Check that a pending authz exists
	if !existingPending(tx, authz.ID) {
		err = errors.New("Cannot finalize an authorization that is not pending")
		tx.Rollback()
		return
	}
	if statusIsPending(authz.Status) {
		err = errors.New("Cannot finalize to a non-final status")
		tx.Rollback()
		return
	}

	auth := &authzModel{authz}
	authObj, err := tx.Get(pendingauthzModel{}, authz.ID)
	if err != nil {
		tx.Rollback()
		return
	}
	oldAuth := authObj.(*pendingauthzModel)

	err = tx.Insert(auth)
	if err != nil {
		tx.Rollback()
		return
	}

	_, err = tx.Delete(oldAuth)
	if err != nil {
		tx.Rollback()
		return
	}

	err = updateChallenges(authz.ID, authz.Challenges, tx)
	if err != nil {
		tx.Rollback()
		return
	}

	err = tx.Commit()
	return
}

// RevokeAuthorizationsByDomain invalidates all pending or finalized authorizations
// for a specific domain
func (ssa *SQLStorageAuthority) RevokeAuthorizationsByDomain(ident core.AcmeIdentifier) (int64, int64, error) {
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

// AddCertificate stores an issued certificate.
func (ssa *SQLStorageAuthority) AddCertificate(certDER []byte, regID int64) (digest string, err error) {
	var parsedCertificate *x509.Certificate
	parsedCertificate, err = x509.ParseCertificate(certDER)
	if err != nil {
		return
	}
	digest = core.Fingerprint256(certDER)
	serial := core.SerialToString(parsedCertificate.SerialNumber)

	cert := &core.Certificate{
		RegistrationID: regID,
		Serial:         serial,
		Digest:         digest,
		DER:            certDER,
		Issued:         ssa.clk.Now(),
		Expires:        parsedCertificate.NotAfter,
	}
	certStatus := &core.CertificateStatus{
		SubscriberApproved: false,
		Status:             core.OCSPStatus("good"),
		OCSPLastUpdated:    time.Time{},
		OCSPResponse:       []byte{},
		Serial:             serial,
		RevokedDate:        time.Time{},
		RevokedReason:      0,
		LockCol:            0,
	}

	tx, err := ssa.dbMap.Begin()
	if err != nil {
		return
	}

	// TODO Verify that the serial number doesn't yet exist
	err = tx.Insert(cert)
	if err != nil {
		tx.Rollback()
		return
	}

	err = tx.Insert(certStatus)
	if err != nil {
		tx.Rollback()
		return
	}

	err = addIssuedNames(tx, parsedCertificate)
	if err != nil {
		tx.Rollback()
		return
	}

	err = addFQDNSet(
		tx,
		parsedCertificate.DNSNames,
		serial,
		parsedCertificate.NotBefore,
		parsedCertificate.NotAfter,
	)
	if err != nil {
		tx.Rollback()
		return
	}

	err = tx.Commit()
	return
}

// AlreadyDeniedCSR queries to find if the name list has already been denied.
func (ssa *SQLStorageAuthority) AlreadyDeniedCSR(names []string) (already bool, err error) {
	sort.Strings(names)

	var denied int64
	err = ssa.dbMap.SelectOne(
		&denied,
		"SELECT count(*) FROM deniedCSRs WHERE names = :names",
		map[string]interface{}{"names": strings.ToLower(strings.Join(names, ","))},
	)
	if err != nil {
		return
	}
	if denied > 0 {
		already = true
	}

	return
}

// CountCertificatesRange returns the number of certificates issued in a specific
// date range
func (ssa *SQLStorageAuthority) CountCertificatesRange(start, end time.Time) (count int64, err error) {
	err = ssa.dbMap.SelectOne(
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
func (ssa *SQLStorageAuthority) CountPendingAuthorizations(regID int64) (count int, err error) {
	err = ssa.dbMap.SelectOne(&count,
		`SELECT count(1) FROM pendingAuthorizations
		 WHERE registrationID = :regID AND
				expires > :now`,
		map[string]interface{}{
			"regID": regID,
			"now":   ssa.clk.Now(),
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
func (ssa *SQLStorageAuthority) GetSCTReceipt(serial string, logID string) (receipt core.SignedCertificateTimestamp, err error) {
	err = ssa.dbMap.SelectOne(
		&receipt,
		"SELECT * FROM sctReceipts WHERE certificateSerial = :serial AND logID = :logID",
		map[string]interface{}{
			"serial": serial,
			"logID":  logID,
		},
	)

	if err == sql.ErrNoRows {
		err = ErrNoReceipt(err.Error())
		return
	}

	return
}

// ErrDuplicateReceipt is an error type for duplicate SCT receipts
type ErrDuplicateReceipt string

func (e ErrDuplicateReceipt) Error() string {
	return string(e)
}

// AddSCTReceipt adds a new SCT receipt to the (append-only) sctReceipts table
func (ssa *SQLStorageAuthority) AddSCTReceipt(sct core.SignedCertificateTimestamp) error {
	err := ssa.dbMap.Insert(&sct)
	if err != nil && strings.HasPrefix(err.Error(), "Error 1062: Duplicate entry") {
		err = ErrDuplicateReceipt(err.Error())
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
func (ssa *SQLStorageAuthority) CountFQDNSets(window time.Duration, names []string) (int64, error) {
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
func (ssa *SQLStorageAuthority) FQDNSetExists(names []string) (bool, error) {
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
