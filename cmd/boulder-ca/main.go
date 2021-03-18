package main

import (
	"flag"
	"fmt"
	"os"
	"sync"

	"github.com/beeker1121/goque"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/letsencrypt/boulder/ca"
	capb "github.com/letsencrypt/boulder/ca/proto"
	"github.com/letsencrypt/boulder/cmd"
	"github.com/letsencrypt/boulder/features"
	"github.com/letsencrypt/boulder/goodkey"
	bgrpc "github.com/letsencrypt/boulder/grpc"
	"github.com/letsencrypt/boulder/issuance"
	"github.com/letsencrypt/boulder/lint"
	"github.com/letsencrypt/boulder/policy"
	sapb "github.com/letsencrypt/boulder/sa/proto"
)

type config struct {
	CA struct {
		cmd.ServiceConfig

		// TODO(#5275): Refactor to named field once all configs in dev,
		// staging and prod have been updated to contain `dbconfig`
		// field
		cmd.DBConfig
		cmd.HostnamePolicyConfig

		GRPCCA            *cmd.GRPCServerConfig
		GRPCOCSPGenerator *cmd.GRPCServerConfig

		SAService *cmd.GRPCClientConfig

		// Issuance contains all information necessary to load and initialize issuers.
		Issuance struct {
			Profile      issuance.ProfileConfig
			Issuers      []issuance.IssuerConfig
			IgnoredLints []string
		}

		// How long issued certificates are valid for.
		Expiry cmd.ConfigDuration

		// How far back certificates should be backdated.
		Backdate cmd.ConfigDuration

		// What digits we should prepend to serials after randomly generating them.
		SerialPrefix int

		// The maximum number of subjectAltNames in a single certificate
		MaxNames int

		// LifespanOCSP is how long OCSP responses are valid for; It should be longer
		// than the minTimeToExpiry field for the OCSP Updater.
		LifespanOCSP cmd.ConfigDuration

		// WeakKeyFile is the path to a JSON file containing truncated RSA modulus
		// hashes of known easily enumerable keys.
		WeakKeyFile string

		// BlockedKeyFile is the path to a YAML file containing Base64 encoded
		// SHA256 hashes of SubjectPublicKeyInfo's that should be considered
		// administratively blocked.
		BlockedKeyFile string

		// Path to directory holding orphan queue files, if not provided an orphan queue
		// is not used.
		OrphanQueueDir string

		// Maximum length (in bytes) of a line accumulating OCSP audit log entries.
		// Recommended to be around 4000. If this is 0, do not perform OCSP audit
		// logging.
		OCSPLogMaxLength int

		// Maximum period (in Go duration format) to wait to accumulate a max-length
		// OCSP audit log line. We will emit a log line at least once per period,
		// if there is anything to be logged. Keeping this low minimizes the risk
		// of losing logs during a catastrophic failure. Making it too high
		// means logging more often than necessary, which is inefficient in terms
		// of bytes and log system resources.
		// Recommended to be around 500ms.
		OCSPLogPeriod cmd.ConfigDuration

		// List of Registration IDs for which ECDSA issuance is allowed. If an
		// account is in this allowlist *and* requests issuance for an ECDSA key
		// *and* an ECDSA issuer is configured in the CA, then the certificate
		// will be issued from that ECDSA issuer. If this list is empty, then
		// ECDSA issuance is allowed for all accounts.
		// This is temporary, and will be used for testing and slow roll-out of
		// ECDSA issuance, but will then be removed.
		ECDSAAllowedAccounts []int64

		Features map[string]bool
	}

	PA cmd.PAConfig

	Syslog cmd.SyslogConfig
}

func loadBoulderIssuers(profileConfig issuance.ProfileConfig, issuerConfigs []issuance.IssuerConfig, ignoredLints []string) ([]*issuance.Issuer, error) {
	issuers := make([]*issuance.Issuer, 0, len(issuerConfigs))
	for _, issuerConfig := range issuerConfigs {
		profile, err := issuance.NewProfile(profileConfig, issuerConfig)
		if err != nil {
			return nil, err
		}

		cert, signer, err := issuance.LoadIssuer(issuerConfig.Location)
		if err != nil {
			return nil, err
		}

		linter, err := lint.NewLinter(signer, ignoredLints)
		if err != nil {
			return nil, err
		}

		issuer, err := issuance.NewIssuer(cert, signer, profile, linter, cmd.Clock())
		if err != nil {
			return nil, err
		}

		issuers = append(issuers, issuer)
	}
	return issuers, nil
}

func main() {
	caAddr := flag.String("ca-addr", "", "CA gRPC listen address override")
	ocspAddr := flag.String("ocsp-addr", "", "OCSP gRPC listen address override")
	debugAddr := flag.String("debug-addr", "", "Debug server address override")
	configFile := flag.String("config", "", "File path to the configuration file for this service")
	flag.Parse()
	if *configFile == "" {
		flag.Usage()
		os.Exit(1)
	}

	var c config
	err := cmd.ReadConfigFile(*configFile, &c)
	cmd.FailOnError(err, "Reading JSON config file into config structure")

	err = features.Set(c.CA.Features)
	cmd.FailOnError(err, "Failed to set feature flags")

	if *caAddr != "" {
		c.CA.GRPCCA.Address = *caAddr
	}
	if *ocspAddr != "" {
		c.CA.GRPCOCSPGenerator.Address = *ocspAddr
	}
	if *debugAddr != "" {
		c.CA.DebugAddr = *debugAddr
	}

	if c.CA.MaxNames == 0 {
		cmd.Fail("Error in CA config: MaxNames must not be 0")
	}

	scope, logger := cmd.StatsAndLogging(c.Syslog, c.CA.DebugAddr)
	defer logger.AuditPanic()
	logger.Info(cmd.VersionString())

	cmd.FailOnError(c.PA.CheckChallenges(), "Invalid PA configuration")

	pa, err := policy.New(c.PA.Challenges)
	cmd.FailOnError(err, "Couldn't create PA")

	if c.CA.HostnamePolicyFile == "" {
		cmd.FailOnError(fmt.Errorf("HostnamePolicyFile was empty."), "")
	}
	err = pa.SetHostnamePolicyFile(c.CA.HostnamePolicyFile)
	cmd.FailOnError(err, "Couldn't load hostname policy file")

	var boulderIssuers []*issuance.Issuer
	boulderIssuers, err = loadBoulderIssuers(c.CA.Issuance.Profile, c.CA.Issuance.Issuers, c.CA.Issuance.IgnoredLints)
	cmd.FailOnError(err, "Couldn't load issuers")

	tlsConfig, err := c.CA.TLS.Load()
	cmd.FailOnError(err, "TLS config")

	clk := cmd.Clock()

	clientMetrics := bgrpc.NewClientMetrics(scope)
	conn, err := bgrpc.ClientSetup(c.CA.SAService, tlsConfig, clientMetrics, clk)
	cmd.FailOnError(err, "Failed to load credentials and create gRPC connection to SA")
	sa := bgrpc.NewStorageAuthorityClient(sapb.NewStorageAuthorityClient(conn))

	kp, err := goodkey.NewKeyPolicy(c.CA.WeakKeyFile, c.CA.BlockedKeyFile, sa.KeyBlocked)
	cmd.FailOnError(err, "Unable to create key policy")

	var orphanQueue *goque.Queue
	if c.CA.OrphanQueueDir != "" {
		orphanQueue, err = goque.OpenQueue(c.CA.OrphanQueueDir)
		cmd.FailOnError(err, "Failed to open orphaned certificate queue")
		defer func() { _ = orphanQueue.Close() }()
	}

	cai, err := ca.NewCertificateAuthorityImpl(
		sa,
		pa,
		boulderIssuers,
		c.CA.ECDSAAllowedAccounts,
		c.CA.Expiry.Duration,
		c.CA.Backdate.Duration,
		c.CA.SerialPrefix,
		c.CA.MaxNames,
		c.CA.LifespanOCSP.Duration,
		kp,
		orphanQueue,
		c.CA.OCSPLogMaxLength,
		c.CA.OCSPLogPeriod.Duration,
		logger,
		scope,
		clk)
	cmd.FailOnError(err, "Failed to create CA impl")

	if orphanQueue != nil {
		go cai.OrphanIntegrationLoop()
	}
	go cai.LogOCSPLoop()

	serverMetrics := bgrpc.NewServerMetrics(scope)
	var wg sync.WaitGroup

	caSrv, caListener, err := bgrpc.NewServer(c.CA.GRPCCA, tlsConfig, serverMetrics, clk)
	cmd.FailOnError(err, "Unable to setup CA gRPC server")
	capb.RegisterCertificateAuthorityServer(caSrv, cai)
	caHealth := health.NewServer()
	healthpb.RegisterHealthServer(caSrv, caHealth)
	wg.Add(1)
	go func() {
		cmd.FailOnError(cmd.FilterShutdownErrors(caSrv.Serve(caListener)), "CA gRPC service failed")
		wg.Done()
	}()

	ocspSrv, ocspListener, err := bgrpc.NewServer(c.CA.GRPCOCSPGenerator, tlsConfig, serverMetrics, clk)
	cmd.FailOnError(err, "Unable to setup CA gRPC server")
	capb.RegisterOCSPGeneratorServer(ocspSrv, cai)
	ocspHealth := health.NewServer()
	healthpb.RegisterHealthServer(ocspSrv, ocspHealth)
	wg.Add(1)
	go func() {
		cmd.FailOnError(cmd.FilterShutdownErrors(ocspSrv.Serve(ocspListener)),
			"OCSPGenerator gRPC service failed")
		wg.Done()
	}()

	go cmd.CatchSignals(logger, func() {
		caHealth.Shutdown()
		ocspHealth.Shutdown()
		caSrv.GracefulStop()
		ocspSrv.GracefulStop()
		wg.Wait()
		cai.Stop()
	})

	select {}
}
