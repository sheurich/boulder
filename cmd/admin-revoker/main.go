package main

import (
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/user"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/context"

	"github.com/cactus/go-statsd-client/statsd"
	"github.com/codegangsta/cli"
	gorp "gopkg.in/gorp.v1"

	"github.com/letsencrypt/boulder/cmd"
	"github.com/letsencrypt/boulder/core"
	blog "github.com/letsencrypt/boulder/log"
	"github.com/letsencrypt/boulder/metrics"
	"github.com/letsencrypt/boulder/rpc"
	"github.com/letsencrypt/boulder/sa"
)

func loadConfig(c *cli.Context) (config cmd.Config, err error) {
	configFileName := c.GlobalString("config")
	configJSON, err := ioutil.ReadFile(configFileName)
	if err != nil {
		return
	}

	err = json.Unmarshal(configJSON, &config)
	return
}

const clientName = "AdminRevoker"

func setupContext(context *cli.Context) (rpc.RegistrationAuthorityClient, blog.Logger, *gorp.DbMap, rpc.StorageAuthorityClient, statsd.Statter) {
	c, err := loadConfig(context)
	cmd.FailOnError(err, "Failed to load Boulder configuration")

	stats, logger := cmd.StatsAndLogging(c.Statsd, c.Syslog)

	amqpConf := c.Revoker.AMQP
	rac, err := rpc.NewRegistrationAuthorityClient(clientName, amqpConf, stats)
	cmd.FailOnError(err, "Unable to create CA client")

	dbURL, err := c.Revoker.DBConfig.URL()
	cmd.FailOnError(err, "Couldn't load DB URL")
	dbMap, err := sa.NewDbMap(dbURL, c.Revoker.DBConfig.MaxDBConns)
	cmd.FailOnError(err, "Couldn't setup database connection")
	go sa.ReportDbConnCount(dbMap, metrics.NewStatsdScope(stats, "AdminRevoker"))

	sac, err := rpc.NewStorageAuthorityClient(clientName, amqpConf, stats)
	cmd.FailOnError(err, "Failed to create SA client")

	return *rac, logger, dbMap, *sac, stats
}

func addDeniedNames(tx *gorp.Transaction, names []string) (err error) {
	sort.Strings(names)
	deniedCSR := &core.DeniedCSR{Names: strings.ToLower(strings.Join(names, ","))}

	err = tx.Insert(deniedCSR)
	return
}

func revokeBySerial(ctx context.Context, serial string, reasonCode core.RevocationCode, deny bool, rac rpc.RegistrationAuthorityClient, logger blog.Logger, tx *gorp.Transaction) (err error) {
	if reasonCode < 0 || reasonCode == 7 || reasonCode > 10 {
		panic(fmt.Sprintf("Invalid reason code: %d", reasonCode))
	}

	certObj, err := tx.Get(core.Certificate{}, serial)
	if err != nil {
		return
	}
	certificate, ok := certObj.(*core.Certificate)
	if !ok {
		err = fmt.Errorf("Cast failure")
		return
	}
	cert, err := x509.ParseCertificate(certificate.DER)
	if err != nil {
		return
	}
	if deny {
		// Retrieve DNS names associated with serial
		err = addDeniedNames(tx, append(cert.DNSNames, cert.Subject.CommonName))
		if err != nil {
			return
		}
	}

	u, err := user.Current()
	err = rac.AdministrativelyRevokeCertificate(ctx, *cert, reasonCode, u.Username)
	if err != nil {
		return
	}

	logger.Info(fmt.Sprintf("Revoked certificate %s with reason '%s'", serial, core.RevocationReasons[reasonCode]))
	return
}

func revokeByReg(ctx context.Context, regID int64, reasonCode core.RevocationCode, deny bool, rac rpc.RegistrationAuthorityClient, logger blog.Logger, tx *gorp.Transaction) (err error) {
	var certs []core.Certificate
	_, err = tx.Select(&certs, "SELECT serial FROM certificates WHERE registrationID = :regID", map[string]interface{}{"regID": regID})
	if err != nil {
		return
	}

	for _, cert := range certs {
		err = revokeBySerial(ctx, cert.Serial, reasonCode, deny, rac, logger, tx)
		if err != nil {
			return
		}
	}

	return
}

// This abstraction is needed so that we can use sort.Sort below
type revocationCodes []core.RevocationCode

func (rc revocationCodes) Len() int           { return len(rc) }
func (rc revocationCodes) Less(i, j int) bool { return rc[i] < rc[j] }
func (rc revocationCodes) Swap(i, j int)      { rc[i], rc[j] = rc[j], rc[i] }

func main() {
	ctx := context.Background()
	app := cli.NewApp()
	app.Name = "admin-revoker"
	app.Usage = "Revokes issued certificates"
	app.Version = cmd.Version()
	app.Author = "Boulder contributors"
	app.Email = "ca-dev@letsencrypt.org"

	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:   "config",
			Value:  "config.json",
			EnvVar: "BOULDER_CONFIG",
			Usage:  "Path to Boulder JSON configuration file",
		},
		cli.BoolFlag{
			Name:  "deny",
			Usage: "Add certificate DNS names to the denied list",
		},
	}
	app.Commands = []cli.Command{
		{
			Name:  "serial-revoke",
			Usage: "Revoke a single certificate by the hex serial number",
			Action: func(c *cli.Context) {
				// 1: serial,  2: reasonCode (3: deny flag)
				serial := c.Args().First()
				reasonCode, err := strconv.Atoi(c.Args().Get(1))
				cmd.FailOnError(err, "Reason code argument must be an integer")
				deny := c.GlobalBool("deny")

				cac, logger, dbMap, _, _ := setupContext(c)

				tx, err := dbMap.Begin()
				if err != nil {
					cmd.FailOnError(sa.Rollback(tx, err), "Couldn't begin transaction")
				}

				err = revokeBySerial(ctx, serial, core.RevocationCode(reasonCode), deny, cac, logger, tx)
				if err != nil {
					cmd.FailOnError(sa.Rollback(tx, err), "Couldn't revoke certificate")
				}

				err = tx.Commit()
				cmd.FailOnError(err, "Couldn't cleanly close transaction")
			},
		},
		{
			Name:  "reg-revoke",
			Usage: "Revoke all certificates associated with a registration ID",
			Action: func(c *cli.Context) {
				// 1: registration ID,  2: reasonCode (3: deny flag)
				regID, err := strconv.ParseInt(c.Args().First(), 10, 64)
				cmd.FailOnError(err, "Registration ID argument must be an integer")
				reasonCode, err := strconv.Atoi(c.Args().Get(1))
				cmd.FailOnError(err, "Reason code argument must be an integer")
				deny := c.GlobalBool("deny")

				cac, logger, dbMap, sac, _ := setupContext(c)
				// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
				defer logger.AuditPanic()

				tx, err := dbMap.Begin()
				if err != nil {
					cmd.FailOnError(sa.Rollback(tx, err), "Couldn't begin transaction")
				}

				_, err = sac.GetRegistration(ctx, regID)
				if err != nil {
					cmd.FailOnError(err, "Couldn't fetch registration")
				}

				err = revokeByReg(ctx, regID, core.RevocationCode(reasonCode), deny, cac, logger, tx)
				if err != nil {
					cmd.FailOnError(sa.Rollback(tx, err), "Couldn't revoke certificate")
				}

				err = tx.Commit()
				cmd.FailOnError(err, "Couldn't cleanly close transaction")
			},
		},
		{
			Name:  "list-reasons",
			Usage: "List all revocation reason codes",
			Action: func(c *cli.Context) {
				var codes revocationCodes
				for k := range core.RevocationReasons {
					codes = append(codes, k)
				}
				sort.Sort(codes)
				fmt.Printf("Revocation reason codes\n-----------------------\n\n")
				for _, k := range codes {
					fmt.Printf("%d: %s\n", k, core.RevocationReasons[k])
				}
			},
		},
		{
			Name:  "auth-revoke",
			Usage: "Revoke all pending/valid authorizations for a domain",
			Action: func(c *cli.Context) {
				domain := c.Args().First()
				_, logger, _, sac, stats := setupContext(c)
				ident := core.AcmeIdentifier{Value: domain, Type: core.IdentifierDNS}
				authsRevoked, pendingAuthsRevoked, err := sac.RevokeAuthorizationsByDomain(ctx, ident)
				cmd.FailOnError(err, fmt.Sprintf("Failed to revoke authorizations for %s", ident.Value))
				logger.Info(fmt.Sprintf(
					"Revoked %d pending authorizations and %d final authorizations\n",
					authsRevoked,
					pendingAuthsRevoked,
				))
				stats.Inc("admin-revoker.revokedAuthorizations", authsRevoked, 1.0)
				stats.Inc("admin-revoker.revokedPendingAuthorizations", pendingAuthsRevoked, 1.0)
			},
		},
	}

	err := app.Run(os.Args)
	cmd.FailOnError(err, "Failed to run application")
}
