package main

import (
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/url"
	"path"
	"time"

	"github.com/cactus/go-statsd-client/statsd"
	"github.com/jmhodges/clock"
	"golang.org/x/crypto/ocsp"
	"golang.org/x/net/context"
	gorp "gopkg.in/gorp.v1"

	"github.com/letsencrypt/boulder/akamai"
	"github.com/letsencrypt/boulder/cmd"
	"github.com/letsencrypt/boulder/core"
	bgrpc "github.com/letsencrypt/boulder/grpc"
	blog "github.com/letsencrypt/boulder/log"
	"github.com/letsencrypt/boulder/metrics"
	pubPB "github.com/letsencrypt/boulder/publisher/proto"
	"github.com/letsencrypt/boulder/rpc"
	"github.com/letsencrypt/boulder/sa"
)

// OCSPUpdater contains the useful objects for the Updater
type OCSPUpdater struct {
	stats statsd.Statter
	log   blog.Logger
	clk   clock.Clock

	dbMap *gorp.DbMap

	cac  core.CertificateAuthority
	pubc core.Publisher
	sac  core.StorageAuthority

	// Used  to calculate how far back stale OCSP responses should be looked for
	ocspMinTimeToExpiry time.Duration
	// Used to calculate how far back missing SCT receipts should be looked for
	oldestIssuedSCT time.Duration
	// Number of CT logs we expect to have receipts from
	numLogs int

	loops []*looper

	ccu    *akamai.CachePurgeClient
	issuer *x509.Certificate
}

// This is somewhat gross but can be pared down a bit once the publisher and this
// are fully smooshed together
func newUpdater(
	stats statsd.Statter,
	clk clock.Clock,
	dbMap *gorp.DbMap,
	ca core.CertificateAuthority,
	pub core.Publisher,
	sac core.StorageAuthority,
	config cmd.OCSPUpdaterConfig,
	numLogs int,
	issuerPath string,
) (*OCSPUpdater, error) {
	if config.NewCertificateBatchSize == 0 ||
		config.OldOCSPBatchSize == 0 ||
		config.MissingSCTBatchSize == 0 {
		return nil, fmt.Errorf("Loop batch sizes must be non-zero")
	}
	if config.NewCertificateWindow.Duration == 0 ||
		config.OldOCSPWindow.Duration == 0 ||
		config.MissingSCTWindow.Duration == 0 {
		return nil, fmt.Errorf("Loop window sizes must be non-zero")
	}

	log := blog.Get()

	updater := OCSPUpdater{
		stats:               stats,
		clk:                 clk,
		dbMap:               dbMap,
		cac:                 ca,
		log:                 log,
		sac:                 sac,
		pubc:                pub,
		numLogs:             numLogs,
		ocspMinTimeToExpiry: config.OCSPMinTimeToExpiry.Duration,
		oldestIssuedSCT:     config.OldestIssuedSCT.Duration,
	}

	// Setup loops
	updater.loops = []*looper{
		{
			clk:                  clk,
			stats:                stats,
			batchSize:            config.NewCertificateBatchSize,
			tickDur:              config.NewCertificateWindow.Duration,
			tickFunc:             updater.newCertificateTick,
			name:                 "NewCertificates",
			failureBackoffFactor: config.SignFailureBackoffFactor,
			failureBackoffMax:    config.SignFailureBackoffMax.Duration,
		},
		{
			clk:                  clk,
			stats:                stats,
			batchSize:            config.OldOCSPBatchSize,
			tickDur:              config.OldOCSPWindow.Duration,
			tickFunc:             updater.oldOCSPResponsesTick,
			name:                 "OldOCSPResponses",
			failureBackoffFactor: config.SignFailureBackoffFactor,
			failureBackoffMax:    config.SignFailureBackoffMax.Duration,
		},
		// The missing SCT loop doesn't need to know about failureBackoffFactor or
		// failureBackoffMax as it doesn't make any calls to the CA
		{
			clk:       clk,
			stats:     stats,
			batchSize: config.MissingSCTBatchSize,
			tickDur:   config.MissingSCTWindow.Duration,
			tickFunc:  updater.missingReceiptsTick,
			name:      "MissingSCTReceipts",
		},
	}
	if config.RevokedCertificateBatchSize != 0 &&
		config.RevokedCertificateWindow.Duration != 0 {
		updater.loops = append(updater.loops, &looper{
			clk:                  clk,
			stats:                stats,
			batchSize:            config.RevokedCertificateBatchSize,
			tickDur:              config.RevokedCertificateWindow.Duration,
			tickFunc:             updater.revokedCertificatesTick,
			name:                 "RevokedCertificates",
			failureBackoffFactor: config.SignFailureBackoffFactor,
			failureBackoffMax:    config.SignFailureBackoffMax.Duration,
		})
	}

	// TODO(#1050): Remove this gate and the nil ccu checks below
	if config.AkamaiBaseURL != "" {
		issuer, err := core.LoadCert(issuerPath)
		ccu, err := akamai.NewCachePurgeClient(
			config.AkamaiBaseURL,
			config.AkamaiClientToken,
			config.AkamaiClientSecret,
			config.AkamaiAccessToken,
			config.AkamaiPurgeRetries,
			config.AkamaiPurgeRetryBackoff.Duration,
			log,
			stats,
		)
		if err != nil {
			return nil, err
		}
		updater.ccu = ccu
		updater.issuer = issuer
	}

	return &updater, nil
}

// sendPurge should only be called as a Goroutine as it will block until the purge
// request is successful
func (updater *OCSPUpdater) sendPurge(der []byte) {
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		updater.log.AuditErr(fmt.Errorf("Failed to parse certificate for cache purge: %s", err))
		return
	}

	req, err := ocsp.CreateRequest(cert, updater.issuer, nil)
	if err != nil {
		updater.log.AuditErr(fmt.Errorf("Failed to create OCSP request for cache purge: %s", err))
		return
	}

	// Create a GET style OCSP url for each endpoint in cert.OCSPServer (still waiting
	// on word from Akamai on how to properly purge cached POST requests, for now just
	// do GET)
	urls := []string{}
	for _, ocspServer := range cert.OCSPServer {
		urls = append(
			urls,
			path.Join(ocspServer, url.QueryEscape(base64.StdEncoding.EncodeToString(req))),
		)
	}

	err = updater.ccu.Purge(urls)
	if err != nil {
		updater.log.AuditErr(fmt.Errorf("Failed to purge OCSP response from CDN: %s", err))
	}
}

func (updater *OCSPUpdater) findStaleOCSPResponses(oldestLastUpdatedTime time.Time, batchSize int) ([]core.CertificateStatus, error) {
	var statuses []core.CertificateStatus
	_, err := updater.dbMap.Select(
		&statuses,
		`SELECT cs.*
			 FROM certificateStatus AS cs
			 JOIN certificates AS cert
			 ON cs.serial = cert.serial
			 WHERE cs.ocspLastUpdated < :lastUpdate
			 AND cert.expires > now()
			 ORDER BY cs.ocspLastUpdated ASC
			 LIMIT :limit`,
		map[string]interface{}{
			"lastUpdate": oldestLastUpdatedTime,
			"limit":      batchSize,
		},
	)
	if err == sql.ErrNoRows {
		return statuses, nil
	}
	return statuses, err
}

func (updater *OCSPUpdater) getCertificatesWithMissingResponses(batchSize int) ([]core.CertificateStatus, error) {
	var statuses []core.CertificateStatus
	_, err := updater.dbMap.Select(
		&statuses,
		`SELECT * FROM certificateStatus
			 WHERE ocspLastUpdated = 0
			 LIMIT :limit`,
		map[string]interface{}{
			"limit": batchSize,
		},
	)
	if err == sql.ErrNoRows {
		return statuses, nil
	}
	return statuses, err
}

type responseMeta struct {
	*core.OCSPResponse
	*core.CertificateStatus
}

func (updater *OCSPUpdater) generateResponse(ctx context.Context, status core.CertificateStatus) (*core.CertificateStatus, error) {
	var cert core.Certificate
	err := updater.dbMap.SelectOne(
		&cert,
		"SELECT * FROM certificates WHERE serial = :serial",
		map[string]interface{}{"serial": status.Serial},
	)
	if err != nil {
		return nil, err
	}

	_, err = x509.ParseCertificate(cert.DER)
	if err != nil {
		return nil, err
	}

	signRequest := core.OCSPSigningRequest{
		CertDER:   cert.DER,
		Reason:    status.RevokedReason,
		Status:    string(status.Status),
		RevokedAt: status.RevokedDate,
	}

	ocspResponse, err := updater.cac.GenerateOCSP(ctx, signRequest)
	if err != nil {
		return nil, err
	}

	status.OCSPLastUpdated = updater.clk.Now()
	status.OCSPResponse = ocspResponse

	// Purge OCSP response from CDN, gated on client having been initialized
	if updater.ccu != nil {
		go updater.sendPurge(cert.DER)
	}

	return &status, nil
}

func (updater *OCSPUpdater) generateRevokedResponse(ctx context.Context, status core.CertificateStatus) (*core.CertificateStatus, error) {
	cert, err := updater.sac.GetCertificate(ctx, status.Serial)
	if err != nil {
		return nil, err
	}

	signRequest := core.OCSPSigningRequest{
		CertDER:   cert.DER,
		Status:    string(core.OCSPStatusRevoked),
		Reason:    status.RevokedReason,
		RevokedAt: status.RevokedDate,
	}

	ocspResponse, err := updater.cac.GenerateOCSP(ctx, signRequest)
	if err != nil {
		return nil, err
	}

	now := updater.clk.Now()
	status.OCSPLastUpdated = now
	status.OCSPResponse = ocspResponse

	// Purge OCSP response from CDN, gated on client having been initialized
	if updater.ccu != nil {
		go updater.sendPurge(cert.DER)
	}

	return &status, nil
}

func (updater *OCSPUpdater) storeResponse(status *core.CertificateStatus) error {
	// Update the certificateStatus table with the new OCSP response, the status
	// WHERE is used make sure we don't overwrite a revoked response with a one
	// containing a 'good' status and that we don't do the inverse when the OCSP
	// status should be 'good'.
	_, err := updater.dbMap.Exec(
		`UPDATE certificateStatus
		 SET ocspResponse=?,ocspLastUpdated=?
		 WHERE serial=?
		 AND status=?`,
		status.OCSPResponse,
		status.OCSPLastUpdated,
		status.Serial,
		string(status.Status),
	)
	return err
}

// newCertificateTick checks for certificates issued since the last tick and
// generates and stores OCSP responses for these certs
func (updater *OCSPUpdater) newCertificateTick(ctx context.Context, batchSize int) error {
	// Check for anything issued between now and previous tick and generate first
	// OCSP responses
	statuses, err := updater.getCertificatesWithMissingResponses(batchSize)
	if err != nil {
		updater.stats.Inc("OCSP.Errors.FindMissingResponses", 1, 1.0)
		updater.log.AuditErr(fmt.Errorf("Failed to find certificates with missing OCSP responses: %s", err))
		return err
	}

	return updater.generateOCSPResponses(ctx, statuses)
}

func (updater *OCSPUpdater) findRevokedCertificatesToUpdate(batchSize int) ([]core.CertificateStatus, error) {
	var statuses []core.CertificateStatus
	_, err := updater.dbMap.Select(
		&statuses,
		`SELECT * FROM certificateStatus
		 WHERE status = :revoked
		 AND ocspLastUpdated <= revokedDate
		 LIMIT :limit`,
		map[string]interface{}{
			"revoked": string(core.OCSPStatusRevoked),
			"limit":   batchSize,
		},
	)
	return statuses, err
}

func (updater *OCSPUpdater) revokedCertificatesTick(ctx context.Context, batchSize int) error {
	statuses, err := updater.findRevokedCertificatesToUpdate(batchSize)
	if err != nil {
		updater.stats.Inc("OCSP.Errors.FindRevokedCertificates", 1, 1.0)
		updater.log.AuditErr(fmt.Errorf("Failed to find revoked certificates: %s", err))
		return err
	}

	for _, status := range statuses {
		meta, err := updater.generateRevokedResponse(ctx, status)
		if err != nil {
			updater.log.AuditErr(fmt.Errorf("Failed to generate revoked OCSP response: %s", err))
			updater.stats.Inc("OCSP.Errors.RevokedResponseGeneration", 1, 1.0)
			return err
		}
		err = updater.storeResponse(meta)
		if err != nil {
			updater.stats.Inc("OCSP.Errors.StoreRevokedResponse", 1, 1.0)
			updater.log.AuditErr(fmt.Errorf("Failed to store OCSP response: %s", err))
			continue
		}
	}
	return nil
}

func (updater *OCSPUpdater) generateOCSPResponses(ctx context.Context, statuses []core.CertificateStatus) error {
	for _, status := range statuses {
		meta, err := updater.generateResponse(ctx, status)
		if err != nil {
			updater.log.AuditErr(fmt.Errorf("Failed to generate OCSP response: %s", err))
			updater.stats.Inc("OCSP.Errors.ResponseGeneration", 1, 1.0)
			return err
		}
		updater.stats.Inc("OCSP.GeneratedResponses", 1, 1.0)
		err = updater.storeResponse(meta)
		if err != nil {
			updater.log.AuditErr(fmt.Errorf("Failed to store OCSP response: %s", err))
			updater.stats.Inc("OCSP.Errors.StoreResponse", 1, 1.0)
			continue
		}
		updater.stats.Inc("OCSP.StoredResponses", 1, 1.0)
	}
	return nil
}

// oldOCSPResponsesTick looks for certificates with stale OCSP responses and
// generates/stores new ones
func (updater *OCSPUpdater) oldOCSPResponsesTick(ctx context.Context, batchSize int) error {
	now := time.Now()
	statuses, err := updater.findStaleOCSPResponses(now.Add(-updater.ocspMinTimeToExpiry), batchSize)
	if err != nil {
		updater.stats.Inc("OCSP.Errors.FindStaleResponses", 1, 1.0)
		updater.log.AuditErr(fmt.Errorf("Failed to find stale OCSP responses: %s", err))
		return err
	}

	return updater.generateOCSPResponses(ctx, statuses)
}

func (updater *OCSPUpdater) getSerialsIssuedSince(since time.Time, batchSize int) ([]string, error) {
	var allSerials []string
	for {
		serials := []string{}
		_, err := updater.dbMap.Select(
			&serials,
			`SELECT serial FROM certificates
			 WHERE issued > :since
			 ORDER BY issued ASC
			 LIMIT :limit OFFSET :offset`,
			map[string]interface{}{
				"since":  since,
				"limit":  batchSize,
				"offset": len(allSerials),
			},
		)
		if err == sql.ErrNoRows || len(serials) == 0 {
			break
		}
		if err != nil {
			return nil, err
		}
		allSerials = append(allSerials, serials...)
	}
	return allSerials, nil
}

func (updater *OCSPUpdater) getNumberOfReceipts(serial string) (int, error) {
	var count int
	err := updater.dbMap.SelectOne(
		&count,
		"SELECT COUNT(id) FROM sctReceipts WHERE certificateSerial = :serial",
		map[string]interface{}{"serial": serial},
	)
	return count, err
}

// missingReceiptsTick looks for certificates without the correct number of SCT
// receipts and retrieves them
func (updater *OCSPUpdater) missingReceiptsTick(ctx context.Context, batchSize int) error {
	now := updater.clk.Now()
	since := now.Add(-updater.oldestIssuedSCT)
	serials, err := updater.getSerialsIssuedSince(since, batchSize)
	if err != nil {
		updater.log.AuditErr(fmt.Errorf("Failed to get certificate serials: %s", err))
		return err
	}

	for _, serial := range serials {
		count, err := updater.getNumberOfReceipts(serial)
		if err != nil {
			updater.log.AuditErr(fmt.Errorf("Failed to get number of SCT receipts for certificate: %s", err))
			continue
		}
		if count >= updater.numLogs {
			continue
		}
		cert, err := updater.sac.GetCertificate(ctx, serial)
		if err != nil {
			updater.log.AuditErr(fmt.Errorf("Failed to get certificate: %s", err))
			continue
		}
		_ = updater.pubc.SubmitToCT(ctx, cert.DER)
	}
	return nil
}

type looper struct {
	clk                  clock.Clock
	stats                statsd.Statter
	batchSize            int
	tickDur              time.Duration
	tickFunc             func(context.Context, int) error
	name                 string
	failureBackoffFactor float64
	failureBackoffMax    time.Duration
	failures             int
}

func (l *looper) tick() {
	tickStart := l.clk.Now()
	ctx := context.TODO()
	err := l.tickFunc(ctx, l.batchSize)
	l.stats.TimingDuration(fmt.Sprintf("OCSP.%s.TickDuration", l.name), time.Since(tickStart), 1.0)
	l.stats.Inc(fmt.Sprintf("OCSP.%s.Ticks", l.name), 1, 1.0)
	tickEnd := tickStart.Add(time.Since(tickStart))
	expectedTickEnd := tickStart.Add(l.tickDur)
	if tickEnd.After(expectedTickEnd) {
		l.stats.Inc(fmt.Sprintf("OCSP.%s.LongTicks", l.name), 1, 1.0)
	}

	// After we have all the stats stuff out of the way let's check if the tick
	// function failed, if the reason is the HSM is dead increase the length of
	// sleepDur using the exponentially increasing duration returned by core.RetryBackoff.
	sleepDur := expectedTickEnd.Sub(tickEnd)
	if err != nil {
		l.stats.Inc(fmt.Sprintf("OCSP.%s.FailedTicks", l.name), 1, 1.0)
		l.failures++
		sleepDur = core.RetryBackoff(l.failures, l.tickDur, l.failureBackoffMax, l.failureBackoffFactor)
	} else if l.failures > 0 {
		// If the tick was successful but previously there were failures reset
		// counter to 0
		l.failures = 0
	}

	// Sleep for the remaining tick period or for the backoff time
	l.clk.Sleep(sleepDur)
}

func (l *looper) loop() error {
	if l.batchSize == 0 || l.tickDur == 0 {
		return fmt.Errorf("Both batch size and tick duration are required, not running '%s' loop", l.name)
	}
	for {
		l.tick()
	}
}

const clientName = "OCSP"

func setupClients(c cmd.OCSPUpdaterConfig, stats metrics.Statter) (
	core.CertificateAuthority,
	core.Publisher,
	core.StorageAuthority,
) {
	amqpConf := c.AMQP
	cac, err := rpc.NewCertificateAuthorityClient(clientName, amqpConf, stats)
	cmd.FailOnError(err, "Unable to create CA client")

	var pubc core.Publisher
	if c.Publisher != nil {
		conn, err := bgrpc.ClientSetup(c.Publisher)
		cmd.FailOnError(err, "Failed to load credentials and create connection to service")
		pubc = bgrpc.NewPublisherClientWrapper(pubPB.NewPublisherClient(conn), c.Publisher.Timeout.Duration)
	} else {
		pubc, err = rpc.NewPublisherClient(clientName, amqpConf, stats)
		cmd.FailOnError(err, "Unable to create Publisher client")
	}
	sac, err := rpc.NewStorageAuthorityClient(clientName, amqpConf, stats)
	cmd.FailOnError(err, "Unable to create SA client")
	return cac, pubc, sac
}

func main() {
	app := cmd.NewAppShell("ocsp-updater", "Generates and updates OCSP responses")

	app.Action = func(c cmd.Config, stats metrics.Statter, auditlogger blog.Logger) {
		conf := c.OCSPUpdater
		go cmd.DebugServer(conf.DebugAddr)
		go cmd.ProfileCmd("OCSP-Updater", stats)

		// Configure DB
		dbURL, err := conf.DBConfig.URL()
		cmd.FailOnError(err, "Couldn't load DB URL")
		dbMap, err := sa.NewDbMap(dbURL, conf.DBConfig.MaxDBConns)
		cmd.FailOnError(err, "Could not connect to database")
		go sa.ReportDbConnCount(dbMap, metrics.NewStatsdScope(stats, "OCSPUpdater"))

		cac, pubc, sac := setupClients(conf, stats)

		updater, err := newUpdater(
			stats,
			clock.Default(),
			dbMap,
			cac,
			pubc,
			sac,
			// Necessary evil for now
			conf,
			len(c.Common.CT.Logs),
			c.Common.IssuerCert,
		)

		cmd.FailOnError(err, "Failed to create updater")

		for _, l := range updater.loops {
			go func(loop *looper) {
				err = loop.loop()
				if err != nil {
					auditlogger.AuditErr(err)
				}
			}(l)
		}

		// Sleep forever (until signaled)
		select {}
	}

	app.Run()
}
