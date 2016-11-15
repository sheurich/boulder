package sa

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/big"
	"net"
	"reflect"
	"testing"
	"time"

	"golang.org/x/net/context"

	"github.com/jmhodges/clock"
	jose "gopkg.in/square/go-jose.v1"

	"github.com/letsencrypt/boulder/core"
	"github.com/letsencrypt/boulder/features"
	blog "github.com/letsencrypt/boulder/log"
	"github.com/letsencrypt/boulder/revocation"
	"github.com/letsencrypt/boulder/sa/satest"
	"github.com/letsencrypt/boulder/test"
	"github.com/letsencrypt/boulder/test/vars"
)

var log = blog.UseMock()
var ctx = context.Background()

// initSA constructs a SQLStorageAuthority and a clean up function
// that should be defer'ed to the end of the test.
func initSA(t *testing.T) (*SQLStorageAuthority, clock.FakeClock, func()) {
	dbMap, err := NewDbMap(vars.DBConnSA, 0)
	if err != nil {
		t.Fatalf("Failed to create dbMap: %s", err)
	}

	fc := clock.NewFake()
	fc.Set(time.Date(2015, 3, 4, 5, 0, 0, 0, time.UTC))

	sa, err := NewSQLStorageAuthority(dbMap, fc, log)
	if err != nil {
		t.Fatalf("Failed to create SA: %s", err)
	}

	cleanUp := test.ResetSATestDatabase(t)
	return sa, fc, cleanUp
}

var (
	anotherKey = `{
	"kty":"RSA",
	"n": "vd7rZIoTLEe-z1_8G1FcXSw9CQFEJgV4g9V277sER7yx5Qjz_Pkf2YVth6wwwFJEmzc0hoKY-MMYFNwBE4hQHw",
	"e":"AQAB"
}`
)

func TestAddRegistration(t *testing.T) {
	sa, clk, cleanUp := initSA(t)
	defer cleanUp()

	jwk := satest.GoodJWK()

	contact := "mailto:foo@example.com"
	contacts := &[]string{contact}
	reg, err := sa.NewRegistration(ctx, core.Registration{
		Key:       jwk,
		Contact:   contacts,
		InitialIP: net.ParseIP("43.34.43.34"),
	})
	if err != nil {
		t.Fatalf("Couldn't create new registration: %s", err)
	}
	test.Assert(t, reg.ID != 0, "ID shouldn't be 0")
	test.AssertDeepEquals(t, reg.Contact, contacts)

	_, err = sa.GetRegistration(ctx, 0)
	test.AssertError(t, err, "Registration object for ID 0 was returned")

	dbReg, err := sa.GetRegistration(ctx, reg.ID)
	test.AssertNotError(t, err, fmt.Sprintf("Couldn't get registration with ID %v", reg.ID))

	expectedReg := core.Registration{
		ID:        reg.ID,
		Key:       jwk,
		InitialIP: net.ParseIP("43.34.43.34"),
		CreatedAt: clk.Now(),
	}
	test.AssertEquals(t, dbReg.ID, expectedReg.ID)
	test.Assert(t, core.KeyDigestEquals(dbReg.Key, expectedReg.Key), "Stored key != expected")

	newReg := core.Registration{
		ID:        reg.ID,
		Key:       jwk,
		Contact:   &[]string{"test.com"},
		InitialIP: net.ParseIP("72.72.72.72"),
		Agreement: "yes",
	}
	err = sa.UpdateRegistration(ctx, newReg)
	test.AssertNotError(t, err, fmt.Sprintf("Couldn't get registration with ID %v", reg.ID))
	dbReg, err = sa.GetRegistrationByKey(ctx, jwk)
	test.AssertNotError(t, err, "Couldn't get registration by key")

	test.AssertEquals(t, dbReg.ID, newReg.ID)
	test.AssertEquals(t, dbReg.Agreement, newReg.Agreement)

	var anotherJWK jose.JsonWebKey
	err = json.Unmarshal([]byte(anotherKey), &anotherJWK)
	test.AssertNotError(t, err, "couldn't unmarshal anotherJWK")
	_, err = sa.GetRegistrationByKey(ctx, &anotherJWK)
	test.AssertError(t, err, "Registration object for invalid key was returned")
}

func TestNoSuchRegistrationErrors(t *testing.T) {
	sa, _, cleanUp := initSA(t)
	defer cleanUp()

	_, err := sa.GetRegistration(ctx, 100)
	if _, ok := err.(core.NoSuchRegistrationError); !ok {
		t.Errorf("GetRegistration: expected NoSuchRegistrationError, got %T type error (%s)", err, err)
	}

	jwk := satest.GoodJWK()
	_, err = sa.GetRegistrationByKey(ctx, jwk)
	if _, ok := err.(core.NoSuchRegistrationError); !ok {
		t.Errorf("GetRegistrationByKey: expected a NoSuchRegistrationError, got %T type error (%s)", err, err)
	}

	err = sa.UpdateRegistration(ctx, core.Registration{ID: 100, Key: jwk})
	if _, ok := err.(core.NoSuchRegistrationError); !ok {
		t.Errorf("UpdateRegistration: expected a NoSuchRegistrationError, got %T type error (%v)", err, err)
	}
}

func TestCountPendingAuthorizations(t *testing.T) {
	sa, fc, cleanUp := initSA(t)
	defer cleanUp()

	reg := satest.CreateWorkingRegistration(t, sa)
	expires := fc.Now().Add(time.Hour)
	pendingAuthz := core.Authorization{
		RegistrationID: reg.ID,
		Expires:        &expires,
	}

	pendingAuthz, err := sa.NewPendingAuthorization(ctx, pendingAuthz)
	test.AssertNotError(t, err, "Couldn't create new pending authorization")
	count, err := sa.CountPendingAuthorizations(ctx, reg.ID)
	test.AssertNotError(t, err, "Couldn't count pending authorizations")
	test.AssertEquals(t, count, 0)

	pendingAuthz.Status = core.StatusPending
	pendingAuthz, err = sa.NewPendingAuthorization(ctx, pendingAuthz)
	test.AssertNotError(t, err, "Couldn't create new pending authorization")
	count, err = sa.CountPendingAuthorizations(ctx, reg.ID)
	test.AssertNotError(t, err, "Couldn't count pending authorizations")
	test.AssertEquals(t, count, 1)

	fc.Add(2 * time.Hour)
	count, err = sa.CountPendingAuthorizations(ctx, reg.ID)
	test.AssertNotError(t, err, "Couldn't count pending authorizations")
	test.AssertEquals(t, count, 0)
}

func TestAddAuthorization(t *testing.T) {
	sa, _, cleanUp := initSA(t)
	defer cleanUp()

	reg := satest.CreateWorkingRegistration(t, sa)
	PA := core.Authorization{RegistrationID: reg.ID}

	PA, err := sa.NewPendingAuthorization(ctx, PA)
	test.AssertNotError(t, err, "Couldn't create new pending authorization")
	test.Assert(t, PA.ID != "", "ID shouldn't be blank")

	dbPa, err := sa.GetAuthorization(ctx, PA.ID)
	test.AssertNotError(t, err, "Couldn't get pending authorization with ID "+PA.ID)
	test.AssertMarshaledEquals(t, PA, dbPa)

	expectedPa := core.Authorization{ID: PA.ID}
	test.AssertMarshaledEquals(t, dbPa.ID, expectedPa.ID)

	combos := make([][]int, 1)
	combos[0] = []int{0, 1}

	exp := time.Now().AddDate(0, 0, 1)
	identifier := core.AcmeIdentifier{Type: core.IdentifierDNS, Value: "wut.com"}
	newPa := core.Authorization{ID: PA.ID, Identifier: identifier, RegistrationID: reg.ID, Status: core.StatusPending, Expires: &exp, Combinations: combos}
	err = sa.UpdatePendingAuthorization(ctx, newPa)
	test.AssertNotError(t, err, "Couldn't update pending authorization with ID "+PA.ID)

	newPa.Status = core.StatusValid
	err = sa.FinalizeAuthorization(ctx, newPa)
	test.AssertNotError(t, err, "Couldn't finalize pending authorization with ID "+PA.ID)

	dbPa, err = sa.GetAuthorization(ctx, PA.ID)
	test.AssertNotError(t, err, "Couldn't get authorization with ID "+PA.ID)
}

func CreateDomainAuth(t *testing.T, domainName string, sa *SQLStorageAuthority) (authz core.Authorization) {
	return CreateDomainAuthWithRegID(t, domainName, sa, 42)
}

func CreateDomainAuthWithRegID(t *testing.T, domainName string, sa *SQLStorageAuthority, regID int64) (authz core.Authorization) {

	// create pending auth
	authz, err := sa.NewPendingAuthorization(ctx, core.Authorization{RegistrationID: regID, Challenges: []core.Challenge{{}}})
	if err != nil {
		t.Fatalf("Couldn't create new pending authorization: %s", err)
	}
	test.Assert(t, authz.ID != "", "ID shouldn't be blank")

	// prepare challenge for auth
	chall := core.Challenge{Type: "simpleHttp", Status: core.StatusValid, URI: domainName, Token: "THISWOULDNTBEAGOODTOKEN"}
	combos := make([][]int, 1)
	combos[0] = []int{0, 1}
	exp := time.Now().AddDate(0, 0, 1) // expire in 1 day

	// validate pending auth
	authz.Status = core.StatusPending
	authz.Identifier = core.AcmeIdentifier{Type: core.IdentifierDNS, Value: domainName}
	authz.Expires = &exp
	authz.Challenges = []core.Challenge{chall}
	authz.Combinations = combos

	// save updated auth
	err = sa.UpdatePendingAuthorization(ctx, authz)
	test.AssertNotError(t, err, "Couldn't update pending authorization with ID "+authz.ID)

	return
}

// Ensure we get only valid authorization with correct RegID
func TestGetValidAuthorizationsBasic(t *testing.T) {
	sa, clk, cleanUp := initSA(t)
	defer cleanUp()

	// Attempt to get unauthorized domain.
	authzMap, err := sa.GetValidAuthorizations(ctx, 0, []string{"example.org"}, clk.Now())
	// Should get no results, but not error.
	test.AssertNotError(t, err, "Error getting valid authorizations")
	test.AssertEquals(t, len(authzMap), 0)

	reg := satest.CreateWorkingRegistration(t, sa)

	// authorize "example.org"
	authz := CreateDomainAuthWithRegID(t, "example.org", sa, reg.ID)

	// finalize auth
	authz.Status = core.StatusValid
	err = sa.FinalizeAuthorization(ctx, authz)
	test.AssertNotError(t, err, "Couldn't finalize pending authorization with ID "+authz.ID)

	// attempt to get authorized domain with wrong RegID
	authzMap, err = sa.GetValidAuthorizations(ctx, 0, []string{"example.org"}, clk.Now())
	test.AssertNotError(t, err, "Error getting valid authorizations")
	test.AssertEquals(t, len(authzMap), 0)

	// get authorized domain
	authzMap, err = sa.GetValidAuthorizations(ctx, reg.ID, []string{"example.org"}, clk.Now())
	test.AssertNotError(t, err, "Should have found a valid auth for example.org and regID 42")
	test.AssertEquals(t, len(authzMap), 1)
	result := authzMap["example.org"]
	test.AssertEquals(t, result.Status, core.StatusValid)
	test.AssertEquals(t, result.Identifier.Type, core.IdentifierDNS)
	test.AssertEquals(t, result.Identifier.Value, "example.org")
	test.AssertEquals(t, result.RegistrationID, reg.ID)
}

// Ensure we get the latest valid authorization for an ident
func TestGetValidAuthorizationsDuplicate(t *testing.T) {
	sa, clk, cleanUp := initSA(t)
	defer cleanUp()

	domain := "example.org"
	var err error

	reg := satest.CreateWorkingRegistration(t, sa)

	makeAuthz := func(daysToExpiry int, status core.AcmeStatus) core.Authorization {
		authz := CreateDomainAuthWithRegID(t, domain, sa, reg.ID)
		exp := clk.Now().AddDate(0, 0, daysToExpiry)
		authz.Expires = &exp
		authz.Status = status
		err = sa.FinalizeAuthorization(ctx, authz)
		test.AssertNotError(t, err, "Couldn't finalize pending authorization with ID "+authz.ID)
		return authz
	}

	// create invalid authz
	makeAuthz(10, core.StatusInvalid)

	// should not get the auth
	authzMap, err := sa.GetValidAuthorizations(ctx, reg.ID, []string{domain}, clk.Now())
	test.AssertEquals(t, len(authzMap), 0)

	// create valid auth
	makeAuthz(1, core.StatusValid)

	// should get the valid auth even if it's expire date is lower than the invalid one
	authzMap, err = sa.GetValidAuthorizations(ctx, reg.ID, []string{domain}, clk.Now())
	test.AssertNotError(t, err, "Should have found a valid auth for "+domain)
	test.AssertEquals(t, len(authzMap), 1)
	result1 := authzMap[domain]
	test.AssertEquals(t, result1.Status, core.StatusValid)
	test.AssertEquals(t, result1.Identifier.Type, core.IdentifierDNS)
	test.AssertEquals(t, result1.Identifier.Value, domain)
	test.AssertEquals(t, result1.RegistrationID, reg.ID)

	// create a newer auth
	newAuthz := makeAuthz(2, core.StatusValid)

	authzMap, err = sa.GetValidAuthorizations(ctx, reg.ID, []string{domain}, clk.Now())
	test.AssertNotError(t, err, "Should have found a valid auth for "+domain)
	test.AssertEquals(t, len(authzMap), 1)
	result2 := authzMap[domain]
	test.AssertEquals(t, result2.Status, core.StatusValid)
	test.AssertEquals(t, result2.Identifier.Type, core.IdentifierDNS)
	test.AssertEquals(t, result2.Identifier.Value, domain)
	test.AssertEquals(t, result2.RegistrationID, reg.ID)
	// make sure we got the latest auth
	test.AssertEquals(t, result2.ID, newAuthz.ID)
}

// Fetch multiple authzs at once. Check that
func TestGetValidAuthorizationsMultiple(t *testing.T) {
	sa, clk, cleanUp := initSA(t)
	defer cleanUp()
	var err error

	reg := satest.CreateWorkingRegistration(t, sa)

	makeAuthz := func(daysToExpiry int, status core.AcmeStatus, domain string) core.Authorization {
		authz := CreateDomainAuthWithRegID(t, domain, sa, reg.ID)
		exp := clk.Now().AddDate(0, 0, daysToExpiry)
		authz.Expires = &exp
		authz.Status = status
		err = sa.FinalizeAuthorization(ctx, authz)
		test.AssertNotError(t, err, "Couldn't finalize pending authorization with ID "+authz.ID)
		return authz
	}
	makeAuthz(1, core.StatusValid, "blog.example.com")
	makeAuthz(2, core.StatusInvalid, "blog.example.com")
	makeAuthz(5, core.StatusValid, "www.example.com")
	wwwAuthz := makeAuthz(6, core.StatusValid, "www.example.com")

	authzMap, err := sa.GetValidAuthorizations(ctx, reg.ID,
		[]string{"blog.example.com", "www.example.com", "absent.example.com"}, clk.Now())
	test.AssertNotError(t, err, "Couldn't get authorizations")
	test.AssertEquals(t, len(authzMap), 2)
	blogResult := authzMap["blog.example.com"]
	if blogResult == nil {
		t.Errorf("Didn't find blog.example.com in result")
	}
	if blogResult.Status == core.StatusInvalid {
		t.Errorf("Got invalid blogResult")
	}
	wwwResult := authzMap["www.example.com"]
	if wwwResult == nil {
		t.Errorf("Didn't find www.example.com in result")
	}
	test.AssertEquals(t, wwwResult.ID, wwwAuthz.ID)
}

func TestAddCertificate(t *testing.T) {
	// Enable the feature for the `CertStatusOptimizationsMigrated` flag so that
	// adding a new certificate will populate the `certificateStatus.NotAfter`
	// field correctly. This will let the unit test assertion for `NotAfter`
	// pass provided everything is working as intended. Note: this must be done
	// **before** the DbMap is created in `initSA()` or the feature flag won't be
	// set correctly at the time the table maps are set up.
	_ = features.Set(map[string]bool{"CertStatusOptimizationsMigrated": true})
	defer features.Reset()

	sa, _, cleanUp := initSA(t)
	defer cleanUp()

	reg := satest.CreateWorkingRegistration(t, sa)

	// An example cert taken from EFF's website
	certDER, err := ioutil.ReadFile("www.eff.org.der")
	test.AssertNotError(t, err, "Couldn't read example cert DER")

	digest, err := sa.AddCertificate(ctx, certDER, reg.ID)
	test.AssertNotError(t, err, "Couldn't add www.eff.org.der")
	test.AssertEquals(t, digest, "qWoItDZmR4P9eFbeYgXXP3SR4ApnkQj8x4LsB_ORKBo")

	retrievedCert, err := sa.GetCertificate(ctx, "000000000000000000000000000000021bd4")
	test.AssertNotError(t, err, "Couldn't get www.eff.org.der by full serial")
	test.AssertByteEquals(t, certDER, retrievedCert.DER)

	certificateStatus, err := sa.GetCertificateStatus(ctx, "000000000000000000000000000000021bd4")
	test.AssertNotError(t, err, "Couldn't get status for www.eff.org.der")
	test.Assert(t, !certificateStatus.SubscriberApproved, "SubscriberApproved should be false")
	test.Assert(t, certificateStatus.Status == core.OCSPStatusGood, "OCSP Status should be good")
	test.Assert(t, certificateStatus.OCSPLastUpdated.IsZero(), "OCSPLastUpdated should be nil")
	test.AssertEquals(t, certificateStatus.NotAfter, retrievedCert.Expires)

	// Test cert generated locally by Boulder / CFSSL, names [example.com,
	// www.example.com, admin.example.com]
	certDER2, err := ioutil.ReadFile("test-cert.der")
	test.AssertNotError(t, err, "Couldn't read example cert DER")
	serial := "ffdd9b8a82126d96f61d378d5ba99a0474f0"

	digest2, err := sa.AddCertificate(ctx, certDER2, reg.ID)
	test.AssertNotError(t, err, "Couldn't add test-cert.der")
	test.AssertEquals(t, digest2, "vrlPN5wIPME1D2PPsCy-fGnTWh8dMyyYQcXPRkjHAQI")

	retrievedCert2, err := sa.GetCertificate(ctx, serial)
	test.AssertNotError(t, err, "Couldn't get test-cert.der")
	test.AssertByteEquals(t, certDER2, retrievedCert2.DER)

	certificateStatus2, err := sa.GetCertificateStatus(ctx, serial)
	test.AssertNotError(t, err, "Couldn't get status for test-cert.der")
	test.Assert(t, !certificateStatus2.SubscriberApproved, "SubscriberApproved should be false")
	test.Assert(t, certificateStatus2.Status == core.OCSPStatusGood, "OCSP Status should be good")
	test.Assert(t, certificateStatus2.OCSPLastUpdated.IsZero(), "OCSPLastUpdated should be nil")
}

func TestCountCertificatesByNames(t *testing.T) {
	sa, clk, cleanUp := initSA(t)
	defer cleanUp()
	// Test cert generated locally by Boulder / CFSSL, names [example.com,
	// www.example.com, admin.example.com]
	certDER, err := ioutil.ReadFile("test-cert.der")
	test.AssertNotError(t, err, "Couldn't read example cert DER")

	cert, err := x509.ParseCertificate(certDER)
	test.AssertNotError(t, err, "Couldn't parse example cert DER")

	// Set the test clock's time to the time from the test certificate
	clk.Add(-clk.Now().Sub(cert.NotBefore))
	now := clk.Now()
	yesterday := clk.Now().Add(-24 * time.Hour)
	twoDaysAgo := clk.Now().Add(-48 * time.Hour)
	tomorrow := clk.Now().Add(24 * time.Hour)

	// Count for a name that doesn't have any certs
	counts, err := sa.CountCertificatesByNames(ctx, []string{"example.com"}, yesterday, now)
	test.AssertNotError(t, err, "Error counting certs.")
	test.AssertEquals(t, len(counts), 1)
	test.AssertEquals(t, counts["example.com"], 0)

	// Add the test cert and query for its names.
	reg := satest.CreateWorkingRegistration(t, sa)
	_, err = sa.AddCertificate(ctx, certDER, reg.ID)
	test.AssertNotError(t, err, "Couldn't add test-cert.der")

	// Time range including now should find the cert
	counts, err = sa.CountCertificatesByNames(ctx, []string{"example.com"}, yesterday, now)
	test.AssertEquals(t, len(counts), 1)
	test.AssertEquals(t, counts["example.com"], 1)

	// Time range between two days ago and yesterday should not.
	counts, err = sa.CountCertificatesByNames(ctx, []string{"example.com"}, twoDaysAgo, yesterday)
	test.AssertNotError(t, err, "Error counting certs.")
	test.AssertEquals(t, len(counts), 1)
	test.AssertEquals(t, counts["example.com"], 0)

	// Time range between now and tomorrow also should not (time ranges are
	// inclusive at the tail end, but not the beginning end).
	counts, err = sa.CountCertificatesByNames(ctx, []string{"example.com"}, now, tomorrow)
	test.AssertNotError(t, err, "Error counting certs.")
	test.AssertEquals(t, len(counts), 1)
	test.AssertEquals(t, counts["example.com"], 0)

	// Add a second test cert (for example.co.bn) and query for multiple names.
	certDER2, err := ioutil.ReadFile("test-cert2.der")
	test.AssertNotError(t, err, "Couldn't read test-cert2.der")
	_, err = sa.AddCertificate(ctx, certDER2, reg.ID)
	test.AssertNotError(t, err, "Couldn't add test-cert2.der")
	counts, err = sa.CountCertificatesByNames(ctx, []string{"example.com", "foo.com", "example.co.bn"}, yesterday, now.Add(10000*time.Hour))
	test.AssertNotError(t, err, "Error counting certs.")
	test.AssertEquals(t, len(counts), 3)
	test.AssertEquals(t, counts["foo.com"], 0)
	test.AssertEquals(t, counts["example.com"], 1)
	test.AssertEquals(t, counts["example.co.bn"], 1)
}

const (
	sctVersion    = 0
	sctTimestamp  = 1435787268907
	sctLogID      = "aPaY+B9kgr46jO65KB1M/HFRXWeT1ETRCmesu09P+8Q="
	sctSignature  = "BAMASDBGAiEA/4kz9wQq3NhvZ6VlOmjq2Z9MVHGrUjF8uxUG9n1uRc4CIQD2FYnnszKXrR9AP5kBWmTgh3fXy+VlHK8HZXfbzdFf7g=="
	sctCertSerial = "ff000000000000012607e11a78ac01f9"
)

func TestAddSCTReceipt(t *testing.T) {
	sigBytes, err := base64.StdEncoding.DecodeString(sctSignature)
	test.AssertNotError(t, err, "Failed to decode SCT signature")
	sct := core.SignedCertificateTimestamp{
		SCTVersion:        sctVersion,
		LogID:             sctLogID,
		Timestamp:         sctTimestamp,
		Signature:         sigBytes,
		CertificateSerial: sctCertSerial,
	}
	sa, _, cleanup := initSA(t)
	defer cleanup()
	err = sa.AddSCTReceipt(ctx, sct)
	test.AssertNotError(t, err, "Failed to add SCT receipt")
	// Append only and unique on signature and across LogID and CertificateSerial
	err = sa.AddSCTReceipt(ctx, sct)
	test.AssertNotError(t, err, "Incorrectly returned error on duplicate SCT receipt")
}

func TestGetSCTReceipt(t *testing.T) {
	sigBytes, err := base64.StdEncoding.DecodeString(sctSignature)
	test.AssertNotError(t, err, "Failed to decode SCT signature")
	sct := core.SignedCertificateTimestamp{
		SCTVersion:        sctVersion,
		LogID:             sctLogID,
		Timestamp:         sctTimestamp,
		Signature:         sigBytes,
		CertificateSerial: sctCertSerial,
	}
	sa, _, cleanup := initSA(t)
	defer cleanup()
	err = sa.AddSCTReceipt(ctx, sct)
	test.AssertNotError(t, err, "Failed to add SCT receipt")

	sqlSCT, err := sa.GetSCTReceipt(ctx, sctCertSerial, sctLogID)
	test.AssertNotError(t, err, "Failed to get existing SCT receipt")
	test.Assert(t, sqlSCT.SCTVersion == sct.SCTVersion, "Invalid SCT version")
	test.Assert(t, sqlSCT.LogID == sct.LogID, "Invalid log ID")
	test.Assert(t, sqlSCT.Timestamp == sct.Timestamp, "Invalid timestamp")
	test.Assert(t, bytes.Compare(sqlSCT.Signature, sct.Signature) == 0, "Invalid signature")
	test.Assert(t, sqlSCT.CertificateSerial == sct.CertificateSerial, "Invalid certificate serial")
}

func TestMarkCertificateRevoked(t *testing.T) {
	sa, fc, cleanUp := initSA(t)
	defer cleanUp()

	reg := satest.CreateWorkingRegistration(t, sa)
	// Add a cert to the DB to test with.
	certDER, err := ioutil.ReadFile("www.eff.org.der")
	test.AssertNotError(t, err, "Couldn't read example cert DER")
	_, err = sa.AddCertificate(ctx, certDER, reg.ID)
	test.AssertNotError(t, err, "Couldn't add www.eff.org.der")

	serial := "000000000000000000000000000000021bd4"
	const ocspResponse = "this is a fake OCSP response"

	certificateStatusObj, err := sa.GetCertificateStatus(ctx, serial)
	test.AssertEquals(t, certificateStatusObj.Status, core.OCSPStatusGood)

	fc.Add(1 * time.Hour)

	err = sa.MarkCertificateRevoked(ctx, serial, revocation.KeyCompromise)
	test.AssertNotError(t, err, "MarkCertificateRevoked failed")

	certificateStatusObj, err = sa.GetCertificateStatus(ctx, serial)
	test.AssertNotError(t, err, "Failed to fetch certificate status")

	if revocation.KeyCompromise != certificateStatusObj.RevokedReason {
		t.Errorf("RevokedReasons, expected %v, got %v", revocation.KeyCompromise, certificateStatusObj.RevokedReason)
	}
	if !fc.Now().Equal(certificateStatusObj.RevokedDate) {
		t.Errorf("RevokedData, expected %s, got %s", fc.Now(), certificateStatusObj.RevokedDate)
	}
}

func TestCountCertificates(t *testing.T) {
	sa, fc, cleanUp := initSA(t)
	defer cleanUp()
	fc.Add(time.Hour * 24)
	now := fc.Now()
	count, err := sa.CountCertificatesRange(ctx, now.Add(-24*time.Hour), now)
	test.AssertNotError(t, err, "Couldn't get certificate count for the last 24hrs")
	test.AssertEquals(t, count, int64(0))

	reg := satest.CreateWorkingRegistration(t, sa)
	// Add a cert to the DB to test with.
	certDER, err := ioutil.ReadFile("www.eff.org.der")
	test.AssertNotError(t, err, "Couldn't read example cert DER")
	_, err = sa.AddCertificate(ctx, certDER, reg.ID)
	test.AssertNotError(t, err, "Couldn't add www.eff.org.der")

	fc.Add(2 * time.Hour)
	now = fc.Now()
	count, err = sa.CountCertificatesRange(ctx, now.Add(-24*time.Hour), now)
	test.AssertNotError(t, err, "Couldn't get certificate count for the last 24hrs")
	test.AssertEquals(t, count, int64(1))

	fc.Add(24 * time.Hour)
	now = fc.Now()
	count, err = sa.CountCertificatesRange(ctx, now.Add(-24*time.Hour), now)
	test.AssertNotError(t, err, "Couldn't get certificate count for the last 24hrs")
	test.AssertEquals(t, count, int64(0))
}

func TestCountRegistrationsByIP(t *testing.T) {
	sa, fc, cleanUp := initSA(t)
	defer cleanUp()

	contact := "mailto:foo@example.com"

	_, err := sa.NewRegistration(ctx, core.Registration{
		Key:       &jose.JsonWebKey{Key: &rsa.PublicKey{N: big.NewInt(1), E: 1}},
		Contact:   &[]string{contact},
		InitialIP: net.ParseIP("43.34.43.34"),
	})
	test.AssertNotError(t, err, "Couldn't insert registration")
	_, err = sa.NewRegistration(ctx, core.Registration{
		Key:       &jose.JsonWebKey{Key: &rsa.PublicKey{N: big.NewInt(2), E: 1}},
		Contact:   &[]string{contact},
		InitialIP: net.ParseIP("2001:cdba:1234:5678:9101:1121:3257:9652"),
	})
	test.AssertNotError(t, err, "Couldn't insert registration")
	_, err = sa.NewRegistration(ctx, core.Registration{
		Key:       &jose.JsonWebKey{Key: &rsa.PublicKey{N: big.NewInt(3), E: 1}},
		Contact:   &[]string{contact},
		InitialIP: net.ParseIP("2001:cdba:1234:5678:9101:1121:3257:9653"),
	})
	test.AssertNotError(t, err, "Couldn't insert registration")

	earliest := fc.Now().Add(-time.Hour * 24)
	latest := fc.Now()

	count, err := sa.CountRegistrationsByIP(ctx, net.ParseIP("1.1.1.1"), earliest, latest)
	test.AssertNotError(t, err, "Failed to count registrations")
	test.AssertEquals(t, count, 0)
	count, err = sa.CountRegistrationsByIP(ctx, net.ParseIP("43.34.43.34"), earliest, latest)
	test.AssertNotError(t, err, "Failed to count registrations")
	test.AssertEquals(t, count, 1)
	count, err = sa.CountRegistrationsByIP(ctx, net.ParseIP("2001:cdba:1234:5678:9101:1121:3257:9652"), earliest, latest)
	test.AssertNotError(t, err, "Failed to count registrations")
	test.AssertEquals(t, count, 2)
	count, err = sa.CountRegistrationsByIP(ctx, net.ParseIP("2001:cdba:1234:0000:0000:0000:0000:0000"), earliest, latest)
	test.AssertNotError(t, err, "Failed to count registrations")
	test.AssertEquals(t, count, 2)
}

func TestRevokeAuthorizationsByDomain(t *testing.T) {
	sa, _, cleanUp := initSA(t)
	defer cleanUp()

	reg := satest.CreateWorkingRegistration(t, sa)
	PA1 := CreateDomainAuthWithRegID(t, "a.com", sa, reg.ID)
	PA2 := CreateDomainAuthWithRegID(t, "a.com", sa, reg.ID)

	PA2.Status = core.StatusValid
	err := sa.FinalizeAuthorization(ctx, PA2)
	test.AssertNotError(t, err, "Failed to finalize authorization")

	ident := core.AcmeIdentifier{Value: "a.com", Type: core.IdentifierDNS}
	ar, par, err := sa.RevokeAuthorizationsByDomain(ctx, ident)
	test.AssertNotError(t, err, "Failed to revoke authorizations for a.com")
	test.AssertEquals(t, ar, int64(1))
	test.AssertEquals(t, par, int64(1))

	PA, err := sa.GetAuthorization(ctx, PA1.ID)
	test.AssertNotError(t, err, "Failed to retrieve pending authorization")
	FA, err := sa.GetAuthorization(ctx, PA2.ID)
	test.AssertNotError(t, err, "Failed to retrieve finalized authorization")

	test.AssertEquals(t, PA.Status, core.StatusRevoked)
	test.AssertEquals(t, FA.Status, core.StatusRevoked)
}

func TestFQDNSets(t *testing.T) {
	sa, fc, cleanUp := initSA(t)
	defer cleanUp()

	tx, err := sa.dbMap.Begin()
	test.AssertNotError(t, err, "Failed to open transaction")
	names := []string{"a.example.com", "B.example.com"}
	expires := fc.Now().Add(time.Hour * 2).UTC()
	issued := fc.Now()
	err = addFQDNSet(tx, names, "serial", issued, expires)
	test.AssertNotError(t, err, "Failed to add name set")
	test.AssertNotError(t, tx.Commit(), "Failed to commit transaction")

	// only one valid
	threeHours := time.Hour * 3
	count, err := sa.CountFQDNSets(ctx, threeHours, names)
	test.AssertNotError(t, err, "Failed to count name sets")
	test.AssertEquals(t, count, int64(1))

	// check hash isn't affected by changing name order/casing
	count, err = sa.CountFQDNSets(ctx, threeHours, []string{"b.example.com", "A.example.COM"})
	test.AssertNotError(t, err, "Failed to count name sets")
	test.AssertEquals(t, count, int64(1))

	// add another valid set
	tx, err = sa.dbMap.Begin()
	test.AssertNotError(t, err, "Failed to open transaction")
	err = addFQDNSet(tx, names, "anotherSerial", issued, expires)
	test.AssertNotError(t, err, "Failed to add name set")
	test.AssertNotError(t, tx.Commit(), "Failed to commit transaction")

	// only two valid
	count, err = sa.CountFQDNSets(ctx, threeHours, names)
	test.AssertNotError(t, err, "Failed to count name sets")
	test.AssertEquals(t, count, int64(2))

	// add an expired set
	tx, err = sa.dbMap.Begin()
	test.AssertNotError(t, err, "Failed to open transaction")
	err = addFQDNSet(
		tx,
		names,
		"yetAnotherSerial",
		issued.Add(-threeHours),
		expires.Add(-threeHours),
	)
	test.AssertNotError(t, err, "Failed to add name set")
	test.AssertNotError(t, tx.Commit(), "Failed to commit transaction")

	// only two valid
	count, err = sa.CountFQDNSets(ctx, threeHours, names)
	test.AssertNotError(t, err, "Failed to count name sets")
	test.AssertEquals(t, count, int64(2))
}

func TestFQDNSetsExists(t *testing.T) {
	sa, fc, cleanUp := initSA(t)
	defer cleanUp()

	names := []string{"a.example.com", "B.example.com"}
	exists, err := sa.FQDNSetExists(ctx, names)
	test.AssertNotError(t, err, "Failed to check FQDN set existence")
	test.Assert(t, !exists, "FQDN set shouldn't exist")

	tx, err := sa.dbMap.Begin()
	test.AssertNotError(t, err, "Failed to open transaction")
	expires := fc.Now().Add(time.Hour * 2).UTC()
	issued := fc.Now()
	err = addFQDNSet(tx, names, "serial", issued, expires)
	test.AssertNotError(t, err, "Failed to add name set")
	test.AssertNotError(t, tx.Commit(), "Failed to commit transaction")

	exists, err = sa.FQDNSetExists(ctx, names)
	test.AssertNotError(t, err, "Failed to check FQDN set existence")
	test.Assert(t, exists, "FQDN set does exist")
}

type execRecorder struct {
	query string
	args  []interface{}
}

func (e *execRecorder) Exec(query string, args ...interface{}) (sql.Result, error) {
	e.query = query
	e.args = args
	return nil, nil
}

func TestAddIssuedNames(t *testing.T) {
	var e execRecorder
	err := addIssuedNames(&e, &x509.Certificate{
		DNSNames: []string{
			"example.co.uk",
			"example.xyz",
		},
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Date(2015, 3, 4, 5, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := "INSERT INTO issuedNames (reversedName, serial, notBefore) VALUES (?, ?, ?), (?, ?, ?);"
	if e.query != expected {
		t.Errorf("Wrong query: got %q, expected %q", e.query, expected)
	}
	expectedArgs := []interface{}{
		"uk.co.example",
		"000000000000000000000000000000000001",
		time.Date(2015, 3, 4, 5, 0, 0, 0, time.UTC),
		"xyz.example",
		"000000000000000000000000000000000001",
		time.Date(2015, 3, 4, 5, 0, 0, 0, time.UTC),
	}
	if !reflect.DeepEqual(e.args, expectedArgs) {
		t.Errorf("Wrong args: got\n%#v, expected\n%#v", e.args, expectedArgs)
	}
}

func TestDeactivateAuthorization(t *testing.T) {
	sa, _, cleanUp := initSA(t)
	defer cleanUp()

	reg := satest.CreateWorkingRegistration(t, sa)
	PA := core.Authorization{RegistrationID: reg.ID}

	PA, err := sa.NewPendingAuthorization(ctx, PA)
	test.AssertNotError(t, err, "Couldn't create new pending authorization")
	test.Assert(t, PA.ID != "", "ID shouldn't be blank")

	dbPa, err := sa.GetAuthorization(ctx, PA.ID)
	test.AssertNotError(t, err, "Couldn't get pending authorization with ID "+PA.ID)
	test.AssertMarshaledEquals(t, PA, dbPa)

	expectedPa := core.Authorization{ID: PA.ID}
	test.AssertMarshaledEquals(t, dbPa.ID, expectedPa.ID)

	combos := make([][]int, 1)
	combos[0] = []int{0, 1}

	exp := time.Now().AddDate(0, 0, 1)
	identifier := core.AcmeIdentifier{Type: core.IdentifierDNS, Value: "wut.com"}
	newPa := core.Authorization{
		ID:             PA.ID,
		Identifier:     identifier,
		RegistrationID: reg.ID,
		Status:         core.StatusPending,
		Expires:        &exp,
		Combinations:   combos,
	}
	err = sa.UpdatePendingAuthorization(ctx, newPa)
	test.AssertNotError(t, err, "Couldn't update pending authorization with ID "+PA.ID)

	newPa.Status = core.StatusValid
	err = sa.FinalizeAuthorization(ctx, newPa)
	test.AssertNotError(t, err, "Couldn't finalize pending authorization with ID "+PA.ID)

	dbPa, err = sa.GetAuthorization(ctx, PA.ID)
	test.AssertNotError(t, err, "Couldn't get authorization with ID "+PA.ID)

	err = sa.DeactivateAuthorization(ctx, dbPa.ID)
	test.AssertNotError(t, err, "Couldn't deactivate valid authorization with ID "+PA.ID)

	dbPa, err = sa.GetAuthorization(ctx, PA.ID)
	test.AssertNotError(t, err, "Couldn't get authorization with ID "+PA.ID)
	test.AssertEquals(t, dbPa.Status, core.StatusDeactivated)

	PA, err = sa.NewPendingAuthorization(ctx, PA)
	test.AssertNotError(t, err, "Couldn't create new pending authorization")
	test.Assert(t, PA.ID != "", "ID shouldn't be blank")
	PA.Status = core.StatusPending
	err = sa.UpdatePendingAuthorization(ctx, PA)
	test.AssertNotError(t, err, "Couldn't update pending authorization with ID "+PA.ID)

	err = sa.DeactivateAuthorization(ctx, PA.ID)
	test.AssertNotError(t, err, "Couldn't deactivate pending authorization with ID "+PA.ID)

	dbPa, err = sa.GetAuthorization(ctx, PA.ID)
	test.AssertNotError(t, err, "Couldn't get authorization with ID "+PA.ID)
	test.AssertEquals(t, dbPa.Status, core.StatusDeactivated)
}

func TestDeactivateAccount(t *testing.T) {
	_ = features.Set(map[string]bool{"AllowAccountDeactivation": true})
	defer features.Reset()
	sa, _, cleanUp := initSA(t)
	defer cleanUp()

	reg := satest.CreateWorkingRegistration(t, sa)

	err := sa.DeactivateRegistration(context.Background(), reg.ID)
	test.AssertNotError(t, err, "DeactivateRegistration failed")

	dbReg, err := sa.GetRegistration(context.Background(), reg.ID)
	test.AssertNotError(t, err, "GetRegistration failed")
	test.AssertEquals(t, dbReg.Status, core.StatusDeactivated)
}
