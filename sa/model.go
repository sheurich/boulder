package sa

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"time"

	jose "gopkg.in/square/go-jose.v2"

	"github.com/letsencrypt/boulder/core"
	corepb "github.com/letsencrypt/boulder/core/proto"
	"github.com/letsencrypt/boulder/probs"
	"github.com/letsencrypt/boulder/revocation"
)

// By convention, any function that takes a dbOneSelector, dbSelector,
// dbInserter, dbExecer, or dbSelectExecer as as an argument expects
// that a context has already been applied to the relevant DbMap or
// Transaction object.

// A `dbOneSelector` is anything that provides a `SelectOne` function.
type dbOneSelector interface {
	SelectOne(interface{}, string, ...interface{}) error
}

// A `dbSelector` is anything that provides a `Select` function.
type dbSelector interface {
	Select(interface{}, string, ...interface{}) ([]interface{}, error)
}

// a `dbInserter` is anything that provides an `Insert` function
type dbInserter interface {
	Insert(list ...interface{}) error
}

// A `dbExecer` is anything that provides an `Exec` function
type dbExecer interface {
	Exec(string, ...interface{}) (sql.Result, error)
}

// dbSelectExecer offers a subset of gorp.SqlExecutor's methods: Select and
// Exec.
type dbSelectExecer interface {
	dbSelector
	dbExecer
}

const regFields = "id, jwk, jwk_sha256, contact, agreement, initialIP, createdAt, LockCol, status"

// selectRegistration selects all fields of one registration model
func selectRegistration(s dbOneSelector, q string, args ...interface{}) (*regModel, error) {
	var model regModel
	err := s.SelectOne(
		&model,
		"SELECT "+regFields+" FROM registrations "+q,
		args...,
	)
	return &model, err
}

// selectPendingAuthz selects all fields of one pending authorization model
func selectPendingAuthz(s dbOneSelector, q string, args ...interface{}) (*pendingauthzModel, error) {
	var model pendingauthzModel
	err := s.SelectOne(
		&model,
		"SELECT id, identifier, registrationID, status, expires, combinations, LockCol FROM pendingAuthorizations "+q,
		args...,
	)
	return &model, err
}

const authzFields = "id, identifier, registrationID, status, expires, combinations"

// selectAuthz selects all fields of one authorization model
func selectAuthz(s dbOneSelector, q string, args ...interface{}) (*authzModel, error) {
	var model authzModel
	err := s.SelectOne(
		&model,
		"SELECT "+authzFields+" FROM authz "+q,
		args...,
	)
	return &model, err
}

// selectSctReceipt selects all fields of one SignedCertificateTimestamp object
func selectSctReceipt(s dbOneSelector, q string, args ...interface{}) (core.SignedCertificateTimestamp, error) {
	var model core.SignedCertificateTimestamp
	err := s.SelectOne(
		&model,
		"SELECT id, sctVersion, logID, timestamp, extensions, signature, certificateSerial, LockCol FROM sctReceipts "+q,
		args...,
	)
	return model, err
}

const certFields = "registrationID, serial, digest, der, issued, expires"

// SelectCertificate selects all fields of one certificate object
func SelectCertificate(s dbOneSelector, q string, args ...interface{}) (core.Certificate, error) {
	var model core.Certificate
	err := s.SelectOne(
		&model,
		"SELECT "+certFields+" FROM certificates "+q,
		args...,
	)
	return model, err
}

// SelectCertificates selects all fields of multiple certificate objects
func SelectCertificates(s dbSelector, q string, args map[string]interface{}) ([]core.Certificate, error) {
	var models []core.Certificate
	_, err := s.Select(
		&models,
		"SELECT "+certFields+" FROM certificates "+q, args)
	return models, err
}

const certStatusFields = "serial, status, ocspLastUpdated, revokedDate, revokedReason, lastExpirationNagSent, ocspResponse, notAfter, isExpired"

// SelectCertificateStatus selects all fields of one certificate status model
func SelectCertificateStatus(s dbOneSelector, q string, args ...interface{}) (certStatusModel, error) {
	var model certStatusModel
	err := s.SelectOne(
		&model,
		"SELECT "+certStatusFields+" FROM certificateStatus "+q,
		args...,
	)
	return model, err
}

// SelectCertificateStatuses selects all fields of multiple certificate status objects
func SelectCertificateStatuses(s dbSelector, q string, args ...interface{}) ([]core.CertificateStatus, error) {
	var models []core.CertificateStatus
	_, err := s.Select(
		&models,
		"SELECT "+certStatusFields+" FROM certificateStatus "+q,
		args...,
	)
	return models, err
}

var mediumBlobSize = int(math.Pow(2, 24))

type issuedNameModel struct {
	ID           int64     `db:"id"`
	ReversedName string    `db:"reversedName"`
	NotBefore    time.Time `db:"notBefore"`
	Serial       string    `db:"serial"`
}

// regModel is the description of a core.Registration in the database before
type regModel struct {
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
	Status    string `db:"status"`
}

type certStatusModel struct {
	Serial                string            `db:"serial"`
	Status                core.OCSPStatus   `db:"status"`
	OCSPLastUpdated       time.Time         `db:"ocspLastUpdated"`
	RevokedDate           time.Time         `db:"revokedDate"`
	RevokedReason         revocation.Reason `db:"revokedReason"`
	LastExpirationNagSent time.Time         `db:"lastExpirationNagSent"`
	OCSPResponse          []byte            `db:"ocspResponse"`
	NotAfter              time.Time         `db:"notAfter"`
	IsExpired             bool              `db:"isExpired"`

	// TODO(#856, #873): Deprecated, remove once #2882 has been deployed
	// to production
	SubscribedApproved bool `db:"subscriberApproved"`
	LockCol            int
}

// challModel is the description of a core.Challenge in the database
//
// The Validation field is a stub; the column is only there for backward compatibility.
type challModel struct {
	ID              int64  `db:"id"`
	AuthorizationID string `db:"authorizationID"`

	Type             string          `db:"type"`
	Status           core.AcmeStatus `db:"status"`
	Error            []byte          `db:"error"`
	Token            string          `db:"token"`
	KeyAuthorization string          `db:"keyAuthorization"`
	ValidationRecord []byte          `db:"validationRecord"`

	// TODO(#1818): Remove, this field is unused, but is kept temporarily to avoid a database migration.
	Validated bool `db:"validated"`

	LockCol int64
}

// getChallengesQuery fetches exactly the fields in challModel from the
// challenges table.
const getChallengesQuery = `
	SELECT id, authorizationID, type, status, error, token,
		keyAuthorization, validationRecord
	FROM challenges WHERE authorizationID = :authID ORDER BY id ASC`

// newReg creates a reg model object from a core.Registration
func registrationToModel(r *core.Registration) (*regModel, error) {
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
	rm := regModel{
		ID:        r.ID,
		Key:       key,
		KeySHA256: sha,
		Contact:   *r.Contact,
		Agreement: r.Agreement,
		InitialIP: []byte(r.InitialIP.To16()),
		CreatedAt: r.CreatedAt,
		Status:    string(r.Status),
	}

	return &rm, nil
}

func modelToRegistration(reg *regModel) (core.Registration, error) {
	k := &jose.JSONWebKey{}
	err := json.Unmarshal(reg.Key, k)
	if err != nil {
		err = fmt.Errorf("unable to unmarshal JSONWebKey in db: %s", err)
		return core.Registration{}, err
	}
	var contact *[]string
	// Contact can be nil when the DB contains the literal string "null". We
	// prefer to represent this in memory as a pointer to an empty slice rather
	// than a nil pointer.
	if reg.Contact == nil {
		contact = &[]string{}
	} else {
		contact = &reg.Contact
	}
	r := core.Registration{
		ID:        reg.ID,
		Key:       k,
		Contact:   contact,
		Agreement: reg.Agreement,
		InitialIP: net.IP(reg.InitialIP),
		CreatedAt: reg.CreatedAt,
		Status:    core.AcmeStatus(reg.Status),
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
		ID:                       cm.ID,
		Type:                     cm.Type,
		Status:                   cm.Status,
		Token:                    cm.Token,
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

type orderModel struct {
	ID                int64
	RegistrationID    int64
	Expires           time.Time
	Created           time.Time
	Error             []byte
	CertificateSerial string
	BeganProcessing   bool
}

type requestedNameModel struct {
	ID           int64
	OrderID      int64
	ReversedName string
}

type orderToAuthzModel struct {
	OrderID int64
	AuthzID string
}

func orderToModel(order *corepb.Order) (*orderModel, error) {
	om := &orderModel{
		ID:              *order.Id,
		RegistrationID:  *order.RegistrationID,
		Expires:         time.Unix(0, *order.Expires),
		Created:         time.Unix(0, *order.Created),
		BeganProcessing: *order.BeganProcessing,
	}
	if order.CertificateSerial != nil {
		om.CertificateSerial = *order.CertificateSerial
	}

	if order.Error != nil {
		errJSON, err := json.Marshal(order.Error)
		if err != nil {
			return nil, err
		}
		if len(errJSON) > mediumBlobSize {
			return nil, fmt.Errorf("Error object is too large to store in the database")
		}
		om.Error = errJSON
	}
	return om, nil
}

func modelToOrder(om *orderModel) (*corepb.Order, error) {
	expires := om.Expires.UnixNano()
	created := om.Created.UnixNano()
	order := &corepb.Order{
		Id:                &om.ID,
		RegistrationID:    &om.RegistrationID,
		Expires:           &expires,
		Created:           &created,
		CertificateSerial: &om.CertificateSerial,
		BeganProcessing:   &om.BeganProcessing,
	}
	if len(om.Error) > 0 {
		var problem corepb.ProblemDetails
		err := json.Unmarshal(om.Error, &problem)
		if err != nil {
			return &corepb.Order{}, err
		}
		order.Error = &problem
	}
	return order, nil
}
