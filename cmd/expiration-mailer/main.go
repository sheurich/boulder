package main

import (
	"bytes"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"math"
	netmail "net/mail"
	"net/url"
	"os"
	"sort"
	"strings"
	"text/template"
	"time"

	"golang.org/x/net/context"

	"github.com/jmhodges/clock"
	"gopkg.in/gorp.v1"

	"github.com/letsencrypt/boulder/cmd"
	"github.com/letsencrypt/boulder/core"
	"github.com/letsencrypt/boulder/features"
	bgrpc "github.com/letsencrypt/boulder/grpc"
	blog "github.com/letsencrypt/boulder/log"
	bmail "github.com/letsencrypt/boulder/mail"
	"github.com/letsencrypt/boulder/metrics"
	"github.com/letsencrypt/boulder/rpc"
	"github.com/letsencrypt/boulder/sa"
	sapb "github.com/letsencrypt/boulder/sa/proto"
)

const defaultNagCheckInterval = 24 * time.Hour

type emailContent struct {
	ExpirationDate   string
	DaysToExpiration int
	DNSNames         string
}

type regStore interface {
	GetRegistration(context.Context, int64) (core.Registration, error)
}

type mailer struct {
	stats         metrics.Scope
	log           blog.Logger
	dbMap         *gorp.DbMap
	rs            regStore
	mailer        bmail.Mailer
	emailTemplate *template.Template
	subject       string
	nagTimes      []time.Duration
	limit         int
	clk           clock.Clock
}

func (m *mailer) sendNags(contacts []string, certs []*x509.Certificate) error {
	if len(contacts) == 0 {
		return nil
	}
	if len(certs) == 0 {
		return errors.New("no certs given to send nags for")
	}
	emails := []string{}
	for _, contact := range contacts {
		parsed, err := url.Parse(contact)
		if err != nil {
			m.log.AuditErr(fmt.Sprintf("parsing contact email %s: %s",
				contact, err))
			continue
		}
		if parsed.Scheme == "mailto" {
			emails = append(emails, parsed.Opaque)
		}
	}
	if len(emails) == 0 {
		return nil
	}

	expiresIn := time.Duration(math.MaxInt64)
	expDate := m.clk.Now()
	domains := []string{}
	serials := []string{}

	// Pick out the expiration date that is closest to being hit.
	for _, cert := range certs {
		domains = append(domains, cert.DNSNames...)
		serials = append(serials, core.SerialToString(cert.SerialNumber))
		possible := cert.NotAfter.Sub(m.clk.Now())
		if possible < expiresIn {
			expiresIn = possible
			expDate = cert.NotAfter
		}
	}
	domains = core.UniqueLowerNames(domains)
	sort.Strings(domains)

	m.log.Debug(fmt.Sprintf("Sending mail for %s (%s)", strings.Join(domains, ", "), strings.Join(serials, ", ")))

	email := emailContent{
		ExpirationDate:   expDate.UTC().Format(time.RFC822Z),
		DaysToExpiration: int(expiresIn.Hours() / 24),
		DNSNames:         strings.Join(domains, "\n"),
	}
	msgBuf := new(bytes.Buffer)
	err := m.emailTemplate.Execute(msgBuf, email)
	if err != nil {
		m.stats.Inc("Errors.SendingNag.TemplateFailure", 1)
		return err
	}
	startSending := m.clk.Now()
	err = m.mailer.SendMail(emails, m.subject, msgBuf.String())
	if err != nil {
		return err
	}
	finishSending := m.clk.Now()
	elapsed := finishSending.Sub(startSending)
	m.stats.TimingDuration("SendLatency", elapsed)
	return nil
}

func (m *mailer) updateCertStatus(serial string) error {
	_, err := m.dbMap.Exec(
		"UPDATE certificateStatus SET lastExpirationNagSent = ?  WHERE serial = ?",
		m.clk.Now(), serial)
	return err
}

func (m *mailer) certIsRenewed(serial string) (renewed bool, err error) {
	present, err := m.dbMap.SelectInt(`
		SELECT b.serial IS NOT NULL
		FROM fqdnSets a
		LEFT OUTER JOIN fqdnSets b
			ON a.setHash = b.setHash
			AND a.issued < b.issued
		WHERE a.serial = :serial
		LIMIT 1`,
		map[string]interface{}{"serial": serial},
	)
	if present == 1 {
		m.log.Debug(fmt.Sprintf("Cert %s is already renewed", serial))
	}
	return present == 1, err
}

func (m *mailer) processCerts(allCerts []core.Certificate) {
	ctx := context.Background()

	regIDToCerts := make(map[int64][]core.Certificate)

	for _, cert := range allCerts {
		cs := regIDToCerts[cert.RegistrationID]
		cs = append(cs, cert)
		regIDToCerts[cert.RegistrationID] = cs
	}

	err := m.mailer.Connect()
	if err != nil {
		m.log.AuditErr(fmt.Sprintf("Error connecting to send nag emails: %s", err))
		return
	}
	defer func() {
		_ = m.mailer.Close()
	}()

	for regID, certs := range regIDToCerts {
		reg, err := m.rs.GetRegistration(ctx, regID)
		if err != nil {
			m.log.AuditErr(fmt.Sprintf("Error fetching registration %d: %s", regID, err))
			m.stats.Inc("Errors.GetRegistration", 1)
			continue
		}

		parsedCerts := []*x509.Certificate{}
		for _, cert := range certs {
			parsedCert, err := x509.ParseCertificate(cert.DER)
			if err != nil {
				// TODO(#1420): tell registration about this error
				m.log.AuditErr(fmt.Sprintf("Error parsing certificate %s: %s", cert.Serial, err))
				m.stats.Inc("Errors.ParseCertificate", 1)
				continue
			}

			renewed, err := m.certIsRenewed(cert.Serial)
			if err != nil {
				m.log.AuditErr(fmt.Sprintf("expiration-mailer: error fetching renewal state: %v", err))
				// assume not renewed
			} else if renewed {
				m.stats.Inc("Renewed", 1)
				if err := m.updateCertStatus(cert.Serial); err != nil {
					m.log.AuditErr(fmt.Sprintf("Error updating certificate status for %s: %s", cert.Serial, err))
					m.stats.Inc("Errors.UpdateCertificateStatus", 1)
				}
				continue
			}

			parsedCerts = append(parsedCerts, parsedCert)
		}

		if len(parsedCerts) == 0 {
			// all certificates are renewed
			continue
		}

		if reg.Contact == nil {
			continue
		}

		err = m.sendNags(*reg.Contact, parsedCerts)
		if err != nil {
			m.log.AuditErr(fmt.Sprintf("Error sending nag emails: %s", err))
			continue
		}
		for _, cert := range parsedCerts {
			serial := core.SerialToString(cert.SerialNumber)
			err = m.updateCertStatus(serial)
			if err != nil {
				m.log.AuditErr(fmt.Sprintf("Error updating certificate status for %s: %s", serial, err))
				m.stats.Inc("Errors.UpdateCertificateStatus", 1)
				continue
			}
		}
	}
	return
}

func (m *mailer) findExpiringCertificates() error {
	now := m.clk.Now()
	// E.g. m.nagTimes = [2, 4, 8, 15] days from expiration
	for i, expiresIn := range m.nagTimes {
		left := now
		if i > 0 {
			left = left.Add(m.nagTimes[i-1])
		}
		right := now.Add(expiresIn)

		m.log.Info(fmt.Sprintf("expiration-mailer: Searching for certificates that expire between %s and %s and had last nag >%s before expiry",
			left.UTC(), right.UTC(), expiresIn))

		// If CertStatusOptimizationsMigrated is enabled then we can do this query
		// in a slightly more efficient way by looking at `cs.NotAfter` instead of
		// `cert.expires` in the WHERE clause.
		var certs []core.Certificate
		var err error
		if features.Enabled(features.CertStatusOptimizationsMigrated) {
			_, err = m.dbMap.Select(
				&certs,
				`SELECT cert.* FROM certificates AS cert
				 JOIN certificateStatus AS cs
				 ON cs.serial = cert.serial
				 AND cs.notAfter > :cutoffA
				 AND cs.notAfter <= :cutoffB
				 AND cs.status != "revoked"
				 AND COALESCE(TIMESTAMPDIFF(SECOND, cs.lastExpirationNagSent, cert.expires) > :nagCutoff, 1)
				 ORDER BY cert.expires ASC
				 LIMIT :limit`,
				map[string]interface{}{
					"cutoffA":   left,
					"cutoffB":   right,
					"nagCutoff": expiresIn.Seconds(),
					"limit":     m.limit,
				},
			)
		} else {
			_, err = m.dbMap.Select(
				&certs,
				`SELECT cert.* FROM certificates AS cert
						 JOIN certificateStatus AS cs
						 ON cs.serial = cert.serial
						 AND cert.expires > :cutoffA
						 AND cert.expires <= :cutoffB
						 AND cs.status != "revoked"
						 AND COALESCE(TIMESTAMPDIFF(SECOND, cs.lastExpirationNagSent, cert.expires) > :nagCutoff, 1)
						 ORDER BY cert.expires ASC
						 LIMIT :limit`,
				map[string]interface{}{
					"cutoffA":   left,
					"cutoffB":   right,
					"nagCutoff": expiresIn.Seconds(),
					"limit":     m.limit,
				},
			)
		}
		if err != nil {
			m.log.AuditErr(fmt.Sprintf("expiration-mailer: Error loading certificates: %s", err))
			return err // fatal
		}
		m.log.Info(fmt.Sprintf("Found %d certificates expiring between %s and %s", len(certs),
			left.Format("2006-01-02 03:04"), right.Format("2006-01-02 03:04")))

		if len(certs) == 0 {
			continue // nothing to do
		}

		// If the `certs` result was exactly `m.limit` rows we need to increment
		// a stat indicating that this nag group is at capacity based on the
		// configured cert limit. If this condition continually occurs across mailer
		// runs then we will not catch up, resulting in under-sending expiration
		// mails. The effects of this were initially described in issue #2002[0].
		//
		// 0: https://github.com/letsencrypt/boulder/issues/2002
		if len(certs) == m.limit {
			m.log.Info(fmt.Sprintf(
				"nag group %s expiring certificates at configured capacity (cert limit %d)\n",
				expiresIn.String(),
				m.limit))
			statName := fmt.Sprintf("Errors.Nag-%s.AtCapacity", expiresIn.String())
			m.stats.Inc(statName, 1)
		}

		processingStarted := m.clk.Now()
		m.processCerts(certs)
		processingEnded := m.clk.Now()
		elapsed := processingEnded.Sub(processingStarted)
		m.stats.TimingDuration("ProcessingCertificatesLatency", elapsed)
	}

	return nil
}

type durationSlice []time.Duration

func (ds durationSlice) Len() int {
	return len(ds)
}

func (ds durationSlice) Less(a, b int) bool {
	return ds[a] < ds[b]
}

func (ds durationSlice) Swap(a, b int) {
	ds[a], ds[b] = ds[b], ds[a]
}

const clientName = "ExpirationMailer"

type config struct {
	Mailer struct {
		cmd.ServiceConfig
		cmd.DBConfig
		cmd.SMTPConfig

		From    string
		Subject string

		CertLimit int
		NagTimes  []string
		// How much earlier (than configured nag intervals) to
		// send reminders, to account for the expected delay
		// before the next expiration-mailer invocation.
		NagCheckInterval string
		// Path to a text/template email template
		EmailTemplate string

		SAService *cmd.GRPCClientConfig
	}

	Statsd cmd.StatsdConfig

	Syslog cmd.SyslogConfig
}

func main() {
	configFile := flag.String("config", "", "File path to the configuration file for this service")
	certLimit := flag.Int("cert_limit", 0, "Count of certificates to process per expiration period")
	reconnBase := flag.Duration("reconnectBase", 1*time.Second, "Base sleep duration between reconnect attempts")
	reconnMax := flag.Duration("reconnectMax", 5*60*time.Second, "Max sleep duration between reconnect attempts after exponential backoff")

	flag.Parse()

	if *configFile == "" {
		flag.Usage()
		os.Exit(1)
	}

	var c config
	err := cmd.ReadConfigFile(*configFile, &c)
	cmd.FailOnError(err, "Reading JSON config file into config structure")

	stats, logger := cmd.StatsAndLogging(c.Statsd, c.Syslog)
	scope := metrics.NewStatsdScope(stats, "Expiration")
	defer logger.AuditPanic()
	logger.Info(cmd.VersionString(clientName))

	if *certLimit > 0 {
		c.Mailer.CertLimit = *certLimit
	}
	// Default to 100 if no certLimit is set
	if c.Mailer.CertLimit == 0 {
		c.Mailer.CertLimit = 100
	}

	// Configure DB
	dbURL, err := c.Mailer.DBConfig.URL()
	cmd.FailOnError(err, "Couldn't load DB URL")
	dbMap, err := sa.NewDbMap(dbURL, c.Mailer.DBConfig.MaxDBConns)
	sa.SetSQLDebug(dbMap, logger)
	cmd.FailOnError(err, "Could not connect to database")
	go sa.ReportDbConnCount(dbMap, scope)

	var sac core.StorageAuthority
	if c.Mailer.SAService != nil {
		conn, err := bgrpc.ClientSetup(c.Mailer.SAService, scope)
		cmd.FailOnError(err, "Failed to load credentials and create gRPC connection to SA")
		sac = bgrpc.NewStorageAuthorityClient(sapb.NewStorageAuthorityClient(conn))
	} else {
		sac, err = rpc.NewStorageAuthorityClient(clientName, c.Mailer.AMQP, scope)
		cmd.FailOnError(err, "Failed to create SA client")
	}

	// Load email template
	emailTmpl, err := ioutil.ReadFile(c.Mailer.EmailTemplate)
	cmd.FailOnError(err, fmt.Sprintf("Could not read email template file [%s]", c.Mailer.EmailTemplate))
	tmpl, err := template.New("expiry-email").Parse(string(emailTmpl))
	cmd.FailOnError(err, "Could not parse email template")

	fromAddress, err := netmail.ParseAddress(c.Mailer.From)
	cmd.FailOnError(err, fmt.Sprintf("Could not parse from address: %s", c.Mailer.From))

	smtpPassword, err := c.Mailer.PasswordConfig.Pass()
	cmd.FailOnError(err, "Failed to load SMTP password")
	mailClient := bmail.New(
		c.Mailer.Server,
		c.Mailer.Port,
		c.Mailer.Username,
		smtpPassword,
		*fromAddress,
		logger,
		scope,
		*reconnBase,
		*reconnMax)

	nagCheckInterval := defaultNagCheckInterval
	if s := c.Mailer.NagCheckInterval; s != "" {
		nagCheckInterval, err = time.ParseDuration(s)
		if err != nil {
			logger.AuditErr(fmt.Sprintf("Failed to parse NagCheckInterval string %q: %s", s, err))
			return
		}
	}

	var nags durationSlice
	for _, nagDuration := range c.Mailer.NagTimes {
		dur, err := time.ParseDuration(nagDuration)
		if err != nil {
			logger.AuditErr(fmt.Sprintf("Failed to parse nag duration string [%s]: %s", nagDuration, err))
			return
		}
		nags = append(nags, dur+nagCheckInterval)
	}
	// Make sure durations are sorted in increasing order
	sort.Sort(nags)

	subject := "Certificate expiration notice"
	if c.Mailer.Subject != "" {
		subject = c.Mailer.Subject
	}
	m := mailer{
		stats:         scope,
		subject:       subject,
		log:           logger,
		dbMap:         dbMap,
		rs:            sac,
		mailer:        mailClient,
		emailTemplate: tmpl,
		nagTimes:      nags,
		limit:         c.Mailer.CertLimit,
		clk:           cmd.Clock(),
	}

	go cmd.DebugServer(c.Mailer.DebugAddr)

	err = m.findExpiringCertificates()
	cmd.FailOnError(err, "expiration-mailer has failed")
}
