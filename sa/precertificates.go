package sa

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/letsencrypt/boulder/core"
	corepb "github.com/letsencrypt/boulder/core/proto"
	"github.com/letsencrypt/boulder/db"
	berrors "github.com/letsencrypt/boulder/errors"
	"github.com/letsencrypt/boulder/features"
	bgrpc "github.com/letsencrypt/boulder/grpc"
	sapb "github.com/letsencrypt/boulder/sa/proto"
)

var errIncompleteRequest = errors.New("Incomplete gRPC request message")

// AddSerial writes a record of a serial number generation to the DB.
func (ssa *SQLStorageAuthority) AddSerial(ctx context.Context, req *sapb.AddSerialRequest) (*corepb.Empty, error) {
	if req == nil || req.Created == nil || req.Expires == nil || req.Serial == nil || req.RegID == nil {
		return nil, errIncompleteRequest
	}
	created := time.Unix(0, *req.Created)
	expires := time.Unix(0, *req.Expires)
	err := ssa.dbMap.WithContext(ctx).Insert(&recordedSerialModel{
		Serial:         *req.Serial,
		RegistrationID: *req.RegID,
		Created:        created,
		Expires:        expires,
	})
	if err != nil {
		return nil, err
	}
	return &corepb.Empty{}, nil
}

// AddPrecertificate writes a record of a precertificate generation to the DB.
func (ssa *SQLStorageAuthority) AddPrecertificate(ctx context.Context, req *sapb.AddCertificateRequest) (*corepb.Empty, error) {
	if req == nil || req.Der == nil || req.Issued == nil || req.RegID == nil {
		return nil, errIncompleteRequest
	}
	parsed, err := x509.ParseCertificate(req.Der)
	if err != nil {
		return nil, err
	}
	issued := time.Unix(0, *req.Issued)
	serialHex := core.SerialToString(parsed.SerialNumber)

	preCertModel := &precertificateModel{
		Serial:         serialHex,
		RegistrationID: *req.RegID,
		DER:            req.Der,
		Issued:         issued,
		Expires:        parsed.NotAfter,
	}

	_, overallError := db.WithTransaction(ctx, ssa.dbMap, func(txWithCtx db.Executor) (interface{}, error) {
		if err := txWithCtx.Insert(preCertModel); err != nil {
			if db.IsDuplicate(err) {
				return nil, berrors.DuplicateError("cannot add a duplicate precertificate")
			}
			return nil, err
		}

		// With feature.StoreIssuerInfo we've added a new field to certStatusModel
		// so when we try and use dbMap.Insert it will always try to insert that field.
		// That will break when the relevant migration hasn't been applied so we need
		// to use an explicit INSERT statement that we can manipulate to include the
		// field only when the feature is enabled (and as such the migration has been
		// applied).
		csFields := certStatusFields
		if features.Enabled(features.StoreIssuerInfo) && req.IssuerID != nil {
			csFields += ", issuerID"
		}
		qmarks := []string{}
		for range strings.Split(csFields, ",") {
			qmarks = append(qmarks, "?")
		}
		args := []interface{}{
			serialHex,                   // serial
			string(core.OCSPStatusGood), // status
			ssa.clk.Now(),               // ocspLastUpdated
			time.Time{},                 // revokedDate
			0,                           // revokedReason
			time.Time{},                 // lastExpirationNagSent
			req.Ocsp,                    // ocspResponse
			parsed.NotAfter,             // notAfter
			false,                       // isExpired
		}
		if features.Enabled(features.StoreIssuerInfo) && req.IssuerID != nil {
			args = append(args, req.IssuerID)
		}

		_, err = txWithCtx.Exec(fmt.Sprintf(
			"INSERT INTO certificateStatus (%s) VALUES (%s)",
			csFields,
			strings.Join(qmarks, ","),
		), args...)
		if err != nil {
			return nil, err
		}

		// NOTE(@cpu): When we collect up names to check if an FQDN set exists (e.g.
		// that it is a renewal) we use just the DNSNames from the certificate and
		// ignore the Subject Common Name (if any). This is a safe assumption because
		// if a certificate we issued were to have a Subj. CN not present as a SAN it
		// would be a misissuance and miscalculating whether the cert is a renewal or
		// not for the purpose of rate limiting is the least of our troubles.
		isRenewal, err := ssa.checkFQDNSetExists(
			txWithCtx.SelectOne,
			parsed.DNSNames)
		if err != nil {
			return nil, err
		}
		if err := addIssuedNames(txWithCtx, parsed, isRenewal); err != nil {
			return nil, err
		}
		if features.Enabled(features.StoreKeyHashes) {
			if err := addKeyHash(txWithCtx, parsed); err != nil {
				return nil, err
			}
		}

		return nil, nil
	})
	if overallError != nil {
		return nil, overallError
	}
	return &corepb.Empty{}, nil
}

// GetPrecertificate takes a serial number and returns the corresponding
// precertificate, or error if it does not exist.
func (ssa *SQLStorageAuthority) GetPrecertificate(ctx context.Context, reqSerial *sapb.Serial) (*corepb.Certificate, error) {
	if !core.ValidSerial(*reqSerial.Serial) {
		return nil,
			fmt.Errorf("Invalid precertificate serial %q", *reqSerial.Serial)
	}
	cert, err := SelectPrecertificate(ssa.dbMap.WithContext(ctx), *reqSerial.Serial)
	if err != nil {
		if db.IsNoRows(err) {
			return nil, berrors.NotFoundError(
				"precertificate with serial %q not found",
				*reqSerial.Serial)
		}
		return nil, err
	}

	return bgrpc.CertToPB(cert), nil
}
