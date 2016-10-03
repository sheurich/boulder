package sa

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"time"

	jose "github.com/square/go-jose"

	"github.com/letsencrypt/boulder/core"
	"github.com/letsencrypt/boulder/features"
	"github.com/letsencrypt/boulder/probs"
)

const (
	regV1Fields        string = "id, jwk, jwk_sha256, contact, agreement, initialIP, createdAt, LockCol"
	regV2Fields        string = regV1Fields + ", status"
	pendingAuthzFields string = "id, identifier, registrationID, status, expires, combinations, LockCol"
	authzFields        string = "id, identifier, registrationID, status, expires, combinations"
	sctFields          string = "id, sctVersion, logID, timestamp, extensions, signature, certificateSerial, LockCol"

	// CertificateFields and CertificateStatusFields are also used by cert-checker and ocsp-updater
	CertificateFields       string = "registrationID, serial, digest, der, issued, expires"
	CertificateStatusFields string = "serial, subscriberApproved, status, ocspLastUpdated, revokedDate, revokedReason, lastExpirationNagSent, ocspResponse, LockCol"

	// CertificateStatusFieldsv2 is used when the CertStatusOptimizationsMigrated
	// feature flag is enabled and includes "notAfter" and "isExpired" fields
	CertificateStatusFieldsv2 string = CertificateStatusFields + ", notAfter, isExpired"
)

var mediumBlobSize = int(math.Pow(2, 24))

type issuedNameModel struct {
	ID           int64     `db:"id"`
	ReversedName string    `db:"reversedName"`
	NotBefore    time.Time `db:"notBefore"`
	Serial       string    `db:"serial"`
}

// regModelv1 is the description of a core.Registration in the database before
// sa/_db/migrations/20160818140745_AddRegStatus.sql is applied
type regModelv1 struct {
	ID        int64    `db:"id"`
	Key       []byte   `db:"jwk"`
	KeySHA256 string   `db:"jwk_sha256"`
	Contact   []string `db:"contact"`
	Agreement string   `db:"agreement"`
	// InitialIP is stored as sixteen binary bytes, regardless of whether it
	// represents a v4 or v6 IP address.
	InitialIP []byte    `db:"initialIp"`
	CreatedAt time.Time `db:"createdAt"`
	LockCol   int64
}

// regModelv2 is the description of a core.Registration in the database after
// sa/_db/migrations/20160818140745_AddRegStatus.sql is applied
type regModelv2 struct {
	regModelv1
	Status string `db:"status"`
}

// challModel is the description of a core.Challenge in the database
//
// The Validation field is a stub; the column is only there for backward compatibility.
type challModel struct {
	ID              int64  `db:"id"`
	AuthorizationID string `db:"authorizationID"`

	Type   string          `db:"type"`
	Status core.AcmeStatus `db:"status"`
	Error  []byte          `db:"error"`
	// This field is unused, but is kept temporarily to avoid a database migration.
	// TODO(#1818): remove
	Validated        *time.Time `db:"validated"`
	Token            string     `db:"token"`
	KeyAuthorization string     `db:"keyAuthorization"`
	ValidationRecord []byte     `db:"validationRecord"`

	LockCol int64
}

// getChallengesQuery fetches exactly the fields in challModel from the
// challenges table.
const getChallengesQuery = `
	SELECT id, authorizationID, type, status, error, validated, token,
		keyAuthorization, validationRecord
	FROM challenges WHERE authorizationID = :authID ORDER BY id ASC`

// newReg creates a reg model object from a core.Registration
func registrationToModel(r *core.Registration) (interface{}, error) {
	key, err := json.Marshal(r.Key)
	if err != nil {
		return nil, err
	}

	sha, err := core.KeyDigest(r.Key)
	if err != nil {
		return nil, err
	}
	if r.InitialIP == nil {
		return nil, fmt.Errorf("initialIP was nil")
	}
	if r.Contact == nil {
		r.Contact = &[]string{}
	}
	rm := regModelv1{
		ID:        r.ID,
		Key:       key,
		KeySHA256: sha,
		Contact:   *r.Contact,
		Agreement: r.Agreement,
		InitialIP: []byte(r.InitialIP.To16()),
		CreatedAt: r.CreatedAt,
	}
	if features.Enabled(features.AllowAccountDeactivation) {
		return &regModelv2{
			regModelv1: rm,
			Status:     string(r.Status),
		}, nil
	}
	return &rm, nil
}

func modelToRegistration(ri interface{}) (core.Registration, error) {
	var rm *regModelv1
	if features.Enabled(features.AllowAccountDeactivation) {
		r2 := ri.(*regModelv2)
		rm = &r2.regModelv1
	} else {
		rm = ri.(*regModelv1)
	}
	k := &jose.JsonWebKey{}
	err := json.Unmarshal(rm.Key, k)
	if err != nil {
		err = fmt.Errorf("unable to unmarshal JsonWebKey in db: %s", err)
		return core.Registration{}, err
	}
	var contact *[]string
	// Contact can be nil when the DB contains the literal string "null". We
	// prefer to represent this in memory as a pointer to an empty slice rather
	// than a nil pointer.
	if rm.Contact == nil {
		contact = &[]string{}
	} else {
		contact = &rm.Contact
	}
	r := core.Registration{
		ID:        rm.ID,
		Key:       *k,
		Contact:   contact,
		Agreement: rm.Agreement,
		InitialIP: net.IP(rm.InitialIP),
		CreatedAt: rm.CreatedAt,
	}
	if features.Enabled(features.AllowAccountDeactivation) {
		r2 := ri.(*regModelv2)
		r.Status = core.AcmeStatus(r2.Status)
	}
	return r, nil
}

func challengeToModel(c *core.Challenge, authID string) (*challModel, error) {
	cm := challModel{
		ID:               c.ID,
		AuthorizationID:  authID,
		Type:             c.Type,
		Status:           c.Status,
		Token:            c.Token,
		KeyAuthorization: c.ProvidedKeyAuthorization,
	}
	if c.Error != nil {
		errJSON, err := json.Marshal(c.Error)
		if err != nil {
			return nil, err
		}
		if len(errJSON) > mediumBlobSize {
			return nil, fmt.Errorf("Error object is too large to store in the database")
		}
		cm.Error = errJSON
	}
	if len(c.ValidationRecord) > 0 {
		vrJSON, err := json.Marshal(c.ValidationRecord)
		if err != nil {
			return nil, err
		}
		if len(vrJSON) > mediumBlobSize {
			return nil, fmt.Errorf("Validation Record object is too large to store in the database")
		}
		cm.ValidationRecord = vrJSON
	}
	return &cm, nil
}

func modelToChallenge(cm *challModel) (core.Challenge, error) {
	c := core.Challenge{
		ID:     cm.ID,
		Type:   cm.Type,
		Status: cm.Status,
		Token:  cm.Token,
		ProvidedKeyAuthorization: cm.KeyAuthorization,
	}
	if len(cm.Error) > 0 {
		var problem probs.ProblemDetails
		err := json.Unmarshal(cm.Error, &problem)
		if err != nil {
			return core.Challenge{}, err
		}
		c.Error = &problem
	}
	if len(cm.ValidationRecord) > 0 {
		var vr []core.ValidationRecord
		err := json.Unmarshal(cm.ValidationRecord, &vr)
		if err != nil {
			return core.Challenge{}, err
		}
		c.ValidationRecord = vr
	}
	return c, nil
}
