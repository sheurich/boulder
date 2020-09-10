package main

import (
	"crypto"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/beeker1121/goque"

	"github.com/cloudflare/cfssl/helpers"
	pkcs11key "github.com/letsencrypt/pkcs11key/v4"

	"github.com/letsencrypt/boulder/ca"
	ca_config "github.com/letsencrypt/boulder/ca/config"
	capb "github.com/letsencrypt/boulder/ca/proto"
	"github.com/letsencrypt/boulder/cmd"
	"github.com/letsencrypt/boulder/core"
	"github.com/letsencrypt/boulder/features"
	"github.com/letsencrypt/boulder/goodkey"
	bgrpc "github.com/letsencrypt/boulder/grpc"
	"github.com/letsencrypt/boulder/policy"
	sapb "github.com/letsencrypt/boulder/sa/proto"
	bsigner "github.com/letsencrypt/boulder/signer"
)

type config struct {
	CA ca_config.CAConfig

	PA cmd.PAConfig

	Syslog cmd.SyslogConfig
}

func loadCFSSLIssuers(configs []ca_config.IssuerConfig) ([]ca.Issuer, error) {
	var issuers []ca.Issuer
	for _, issuerConfig := range configs {
		priv, cert, err := loadIssuer(issuerConfig)
		cmd.FailOnError(err, "Couldn't load private key")
		issuers = append(issuers, ca.Issuer{
			Signer: priv,
			Cert:   cert,
		})
	}
	return issuers, nil
}

func loadBoulderIssuers(configs []ca_config.IssuerConfig, profile bsigner.ProfileConfig, ignoredLints []string) ([]bsigner.Config, error) {
	boulderIssuerConfigs := make([]bsigner.Config, 0, len(configs))
	for _, issuerConfig := range configs {
		signer, issuer, err := loadIssuer(issuerConfig)
		if err != nil {
			return nil, err
		}
		boulderIssuerConfigs = append(boulderIssuerConfigs, bsigner.Config{
			Issuer:       issuer,
			Signer:       signer,
			IgnoredLints: ignoredLints,
			Clk:          cmd.Clock(),
			Profile:      profile,
		})
	}
	return boulderIssuerConfigs, nil
}

func loadIssuer(issuerConfig ca_config.IssuerConfig) (crypto.Signer, *x509.Certificate, error) {
	cert, err := core.LoadCert(issuerConfig.CertFile)
	if err != nil {
		return nil, nil, err
	}

	signer, err := loadSigner(issuerConfig, cert)
	if err != nil {
		return nil, nil, err
	}

	if !core.KeyDigestEquals(signer.Public(), cert.PublicKey) {
		return nil, nil, fmt.Errorf("Issuer key did not match issuer cert %s", issuerConfig.CertFile)
	}
	return signer, cert, err
}

func loadSigner(issuerConfig ca_config.IssuerConfig, cert *x509.Certificate) (crypto.Signer, error) {
	if issuerConfig.File != "" {
		keyBytes, err := ioutil.ReadFile(issuerConfig.File)
		if err != nil {
			return nil, fmt.Errorf("Could not read key file %s", issuerConfig.File)
		}

		signer, err := helpers.ParsePrivateKeyPEM(keyBytes)
		if err != nil {
			return nil, err
		}
		return signer, nil
	}

	var pkcs11Config *pkcs11key.Config
	if issuerConfig.ConfigFile != "" {
		contents, err := ioutil.ReadFile(issuerConfig.ConfigFile)
		if err != nil {
			return nil, err
		}
		pkcs11Config = new(pkcs11key.Config)
		err = json.Unmarshal(contents, pkcs11Config)
		if err != nil {
			return nil, err
		}
	} else {
		pkcs11Config = issuerConfig.PKCS11
	}
	if pkcs11Config.Module == "" ||
		pkcs11Config.TokenLabel == "" ||
		pkcs11Config.PIN == "" {
		return nil, fmt.Errorf("Missing a field in pkcs11Config %#v", pkcs11Config)
	}
	numSessions := issuerConfig.NumSessions
	if numSessions <= 0 {
		numSessions = 1
	}
	return pkcs11key.NewPool(numSessions, pkcs11Config.Module,
		pkcs11Config.TokenLabel, pkcs11Config.PIN, cert.PublicKey)
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

	var cfsslIssuers []ca.Issuer
	var boulderIssuerConfigs []bsigner.Config
	if features.Enabled(features.NonCFSSLSigner) {
		boulderIssuerConfigs, err = loadBoulderIssuers(c.CA.Issuers, c.CA.SignerProfile, c.CA.IgnoredLints)
		cmd.FailOnError(err, "Couldn't load issuers")
	} else {
		cfsslIssuers, err = loadCFSSLIssuers(c.CA.Issuers)
		cmd.FailOnError(err, "Couldn't load issuers")
	}

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
		c.CA,
		sa,
		pa,
		clk,
		scope,
		cfsslIssuers,
		boulderIssuerConfigs,
		kp,
		logger,
		orphanQueue)
	cmd.FailOnError(err, "Failed to create CA impl")

	if orphanQueue != nil {
		go cai.OrphanIntegrationLoop()
	}

	serverMetrics := bgrpc.NewServerMetrics(scope)
	caSrv, caListener, err := bgrpc.NewServer(c.CA.GRPCCA, tlsConfig, serverMetrics, clk)
	cmd.FailOnError(err, "Unable to setup CA gRPC server")
	caWrapper := bgrpc.NewCertificateAuthorityServer(cai)
	capb.RegisterCertificateAuthorityServer(caSrv, caWrapper)
	go func() {
		cmd.FailOnError(cmd.FilterShutdownErrors(caSrv.Serve(caListener)), "CA gRPC service failed")
	}()

	ocspSrv, ocspListener, err := bgrpc.NewServer(c.CA.GRPCOCSPGenerator, tlsConfig, serverMetrics, clk)
	cmd.FailOnError(err, "Unable to setup CA gRPC server")
	ocspWrapper := bgrpc.NewCertificateAuthorityServer(cai)
	capb.RegisterOCSPGeneratorServer(ocspSrv, ocspWrapper)
	go func() {
		cmd.FailOnError(cmd.FilterShutdownErrors(ocspSrv.Serve(ocspListener)),
			"OCSPGenerator gRPC service failed")
	}()

	go cmd.CatchSignals(logger, func() {
		caSrv.GracefulStop()
		ocspSrv.GracefulStop()
	})

	select {}
}
