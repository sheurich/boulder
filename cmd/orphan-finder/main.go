package main

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/context"

	"github.com/letsencrypt/boulder/cmd"
	"github.com/letsencrypt/boulder/core"
	blog "github.com/letsencrypt/boulder/log"
	"github.com/letsencrypt/boulder/metrics"
	"github.com/letsencrypt/boulder/rpc"
)

var usageString = `
name:
  orphan-finder - Reads orphaned certificates from a boulder-ca log or a der file and adds them to the database

usage:
  orphan-finder parse-ca-log --config <path> --log-file <path>
  orphan-finder parse-der --config <path> --der-file <path> --regID <registration-id>

command descriptions:
  parse-ca-log    Parses boulder-ca logs to add multiple orphaned certificates
  parse-der       Parses a single orphaned DER certificate file and adds it to the database
`

type config struct {
	AMQP   cmd.AMQPConfig
	Statsd cmd.StatsdConfig
	Syslog cmd.SyslogConfig
}

type certificateStorage interface {
	AddCertificate(context.Context, []byte, int64) (string, error)
	GetCertificate(ctx context.Context, serial string) (core.Certificate, error)
}

var (
	b64derOrphan     = regexp.MustCompile(`b64der=\[([a-zA-Z0-9+/=]+)\]`)
	regOrphan        = regexp.MustCompile(`regID=\[(\d+)\]`)
	errAlreadyExists = fmt.Errorf("Certificate already exists in DB")
)

func checkDER(sai certificateStorage, der []byte) error {
	ctx := context.Background()
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return fmt.Errorf("Failed to parse DER: %s", err)
	}
	_, err = sai.GetCertificate(ctx, core.SerialToString(cert.SerialNumber))
	if err == nil {
		return errAlreadyExists
	}
	if _, ok := err.(core.NotFoundError); ok {
		return nil
	}
	return fmt.Errorf("Existing certificate lookup failed: %s", err)
}

func parseLogLine(sa certificateStorage, logger blog.Logger, line string) (found bool, added bool) {
	ctx := context.Background()
	if !strings.Contains(line, "b64der=") || !strings.Contains(line, "orphaning certificate") {
		return false, false
	}
	derStr := b64derOrphan.FindStringSubmatch(line)
	if len(derStr) <= 1 {
		logger.AuditErr(fmt.Sprintf("Didn't match regex for b64der: %s", line))
		return true, false
	}
	der, err := base64.StdEncoding.DecodeString(derStr[1])
	if err != nil {
		logger.AuditErr(fmt.Sprintf("Couldn't decode b64: %s, [%s]", err, line))
		return true, false
	}
	err = checkDER(sa, der)
	if err != nil {
		logFunc := logger.Err
		if err == errAlreadyExists {
			logFunc = logger.Info
		}
		logFunc(fmt.Sprintf("%s, [%s]", err, line))
		return true, false
	}
	// extract the regID
	regStr := regOrphan.FindStringSubmatch(line)
	if len(regStr) <= 1 {
		logger.AuditErr(fmt.Sprintf("regID variable is empty, [%s]", line))
		return true, false
	}
	regID, err := strconv.Atoi(regStr[1])
	if err != nil {
		logger.AuditErr(fmt.Sprintf("Couldn't parse regID: %s, [%s]", err, line))
		return true, false
	}
	_, err = sa.AddCertificate(ctx, der, int64(regID))
	if err != nil {
		logger.AuditErr(fmt.Sprintf("Failed to store certificate: %s, [%s]", err, line))
		return true, false
	}
	return true, true
}

func setup(configFile string) (metrics.Scope, blog.Logger, *rpc.StorageAuthorityClient) {
	configJSON, err := ioutil.ReadFile(configFile)
	cmd.FailOnError(err, "Failed to read config file")
	var conf config
	err = json.Unmarshal(configJSON, &conf)
	cmd.FailOnError(err, "Failed to parse config file")
	stats, logger := cmd.StatsAndLogging(conf.Statsd, conf.Syslog)
	scope := metrics.NewStatsdScope(stats, "OrphanFinder")
	sa, err := rpc.NewStorageAuthorityClient("orphan-finder", &conf.AMQP, scope)
	cmd.FailOnError(err, "Failed to create SA client")
	return scope, logger, sa
}

func main() {
	if len(os.Args) <= 2 {
		fmt.Fprintf(os.Stderr, usageString)
		os.Exit(1)
	}

	command := os.Args[1]
	flagSet := flag.NewFlagSet(command, flag.ContinueOnError)
	configFile := flagSet.String("config", "", "File path to the configuration file for this service")
	logPath := flagSet.String("log-file", "", "Path to boulder-ca log file to parse")
	derPath := flagSet.String("der-file", "", "Path to DER certificate file")
	regID := flagSet.Int("regID", 0, "Registration ID of user who requested the certificate")
	err := flagSet.Parse(os.Args[2:])
	cmd.FailOnError(err, "Error parsing flagset")

	usage := func() {
		fmt.Fprintf(os.Stderr, "%s\nargs:", usageString)
		flagSet.PrintDefaults()
		os.Exit(1)
	}

	if *configFile == "" {
		usage()
	}

	switch command {
	case "parse-ca-log":
		stats, logger, sa := setup(*configFile)
		if *logPath == "" {
			usage()
		}

		logData, err := ioutil.ReadFile(*logPath)
		cmd.FailOnError(err, "Failed to read log file")

		orphansFound := int64(0)
		orphansAdded := int64(0)
		for _, line := range strings.Split(string(logData), "\n") {
			found, added := parseLogLine(sa, logger, line)
			if found {
				orphansFound++
				if added {
					orphansAdded++
				}
			}
		}
		logger.Info(fmt.Sprintf("Found %d orphans and added %d to the database\n", orphansFound, orphansAdded))
		stats.Inc("Found", orphansFound)
		stats.Inc("Added", orphansAdded)
		stats.Inc("AddingFailed", orphansFound-orphansAdded)

	case "parse-der":
		ctx := context.Background()
		_, _, sa := setup(*configFile)
		if *derPath == "" || *regID == 0 {
			usage()
		}
		der, err := ioutil.ReadFile(*derPath)
		cmd.FailOnError(err, "Failed to read DER file")
		err = checkDER(sa, der)
		cmd.FailOnError(err, "Pre-AddCertificate checks failed")
		_, err = sa.AddCertificate(ctx, der, int64(*regID))
		cmd.FailOnError(err, "Failed to add certificate to database")

	default:
		usage()
	}
}
