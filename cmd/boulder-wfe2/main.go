package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"

	"github.com/letsencrypt/boulder/cmd"
	"github.com/letsencrypt/boulder/core"
	"github.com/letsencrypt/boulder/features"
	"github.com/letsencrypt/boulder/goodkey"
	bgrpc "github.com/letsencrypt/boulder/grpc"
	blog "github.com/letsencrypt/boulder/log"
	"github.com/letsencrypt/boulder/metrics"
	rapb "github.com/letsencrypt/boulder/ra/proto"
	sapb "github.com/letsencrypt/boulder/sa/proto"
	"github.com/letsencrypt/boulder/wfe2"
)

type config struct {
	WFE struct {
		cmd.ServiceConfig
		ListenAddress    string
		TLSListenAddress string

		ServerCertificatePath string
		ServerKeyPath         string

		AllowOrigins []string

		ShutdownStopTimeout cmd.ConfigDuration

		SubscriberAgreementURL string

		AcceptRevocationReason bool
		AllowAuthzDeactivation bool

		TLS cmd.TLSConfig

		RAService *cmd.GRPCClientConfig
		SAService *cmd.GRPCClientConfig

		// CertificateChains maps AIA issuer URLs to certificate filenames.
		// Certificates are read into the chain in the order they are defined in the
		// slice of filenames.
		CertificateChains map[string][]string

		Features map[string]bool
	}

	SubscriberAgreementURL string

	Syslog cmd.SyslogConfig

	Common struct {
		IssuerCert string
	}
}

// loadCertificateFile loads a PEM certificate from the certFile provided. It
// validates that the PEM is well-formed with no leftover bytes, and contains
// only a well-formed X509 certificate. If the cert file meets these
// requirements the PEM bytes from the file are returned, otherwise an error is
// returned. If the PEM contents of a certFile do not have a trailing newline
// one is added.
func loadCertificateFile(aiaIssuerURL, certFile string) ([]byte, error) {
	pemBytes, err := ioutil.ReadFile(certFile)
	if err != nil {
		return nil, fmt.Errorf(
			"CertificateChain entry for AIA issuer url %q has an "+
				"invalid chain file: %q - error reading contents: %s",
			aiaIssuerURL, certFile, err)
	}
	if bytes.Contains(pemBytes, []byte("\r\n")) {
		return nil, fmt.Errorf(
			"CertificateChain entry for AIA issuer url %q has an "+
				"invalid chain file: %q - contents had CRLF line endings",
			aiaIssuerURL, certFile)
	}
	// Try to decode the contents as PEM
	certBlock, rest := pem.Decode(pemBytes)
	if certBlock == nil {
		return nil, fmt.Errorf(
			"CertificateChain entry for AIA issuer url %q has an "+
				"invalid chain file: %q - contents did not decode as PEM",
			aiaIssuerURL, certFile)
	}
	// The PEM contents must be a CERTIFICATE
	if certBlock.Type != "CERTIFICATE" {
		return nil, fmt.Errorf(
			"CertificateChain entry for AIA issuer url %q has an "+
				"invalid chain file: %q - PEM block type incorrect, found "+
				"%q, expected \"CERTIFICATE\"",
			aiaIssuerURL, certFile, certBlock.Type)
	}
	// The PEM Certificate must successfully parse
	if _, err := x509.ParseCertificate(certBlock.Bytes); err != nil {
		return nil, fmt.Errorf(
			"CertificateChain entry for AIA issuer url %q has an "+
				"invalid chain file: %q - certificate bytes failed to parse: %s",
			aiaIssuerURL, certFile, err)
	}
	// If there are bytes leftover we must reject the file otherwise these
	// leftover bytes will end up in a served certificate chain.
	if len(rest) != 0 {
		return nil, fmt.Errorf(
			"CertificateChain entry for AIA issuer url %q has an "+
				"invalid chain file: %q - PEM contents had unused remainder "+
				"input (%d bytes)",
			aiaIssuerURL, certFile, len(rest))
	}
	// If the PEM contents don't end in a \n, add it.
	if pemBytes[len(pemBytes)-1] != '\n' {
		pemBytes = append(pemBytes, '\n')
	}
	return pemBytes, nil
}

// loadCertificateChains processes the provided chainConfig of AIA Issuer URLs
// and cert filenames. For each AIA issuer URL all of its cert filenames are
// read, validated as PEM certificates, and concatenated together separated by
// newlines. The combined PEM certificate chain contents for each are returned
// in the results map, keyed by the AIA Issuer URL.
func loadCertificateChains(chainConfig map[string][]string) (map[string][]byte, error) {
	results := make(map[string][]byte, len(chainConfig))

	// For each AIA Issuer URL we need to read the chain cert files
	for aiaIssuerURL, certFiles := range chainConfig {
		var buffer bytes.Buffer

		// There must be at least one chain file specified
		if len(certFiles) == 0 {
			return nil, fmt.Errorf(
				"CertificateChain entry for AIA issuer url %q has no chain "+
					"file names configured",
				aiaIssuerURL)
		}

		// certFiles are read and appended in the order they appear in the
		// configuration
		for _, c := range certFiles {
			// Prepend a newline before each chain entry
			buffer.Write([]byte("\n"))

			// Read and validate the chain file contents
			pemBytes, err := loadCertificateFile(aiaIssuerURL, c)
			if err != nil {
				return nil, err
			}

			// Write the PEM bytes to the result buffer for this AIAIssuer
			buffer.Write(pemBytes)
		}

		// Save the full PEM chain contents
		results[aiaIssuerURL] = buffer.Bytes()
	}
	return results, nil
}

func setupWFE(c config, logger blog.Logger, stats metrics.Scope) (core.RegistrationAuthority, core.StorageAuthority) {
	tlsConfig, err := c.WFE.TLS.Load()
	cmd.FailOnError(err, "TLS config")
	clientMetrics := bgrpc.NewClientMetrics(stats)
	raConn, err := bgrpc.ClientSetup(c.WFE.RAService, tlsConfig, clientMetrics)
	cmd.FailOnError(err, "Failed to load credentials and create gRPC connection to RA")
	rac := bgrpc.NewRegistrationAuthorityClient(rapb.NewRegistrationAuthorityClient(raConn))

	saConn, err := bgrpc.ClientSetup(c.WFE.SAService, tlsConfig, clientMetrics)
	cmd.FailOnError(err, "Failed to load credentials and create gRPC connection to SA")
	sac := bgrpc.NewStorageAuthorityClient(sapb.NewStorageAuthorityClient(saConn))

	return rac, sac
}

func main() {
	configFile := flag.String("config", "", "File path to the configuration file for this service")
	flag.Parse()
	if *configFile == "" {
		flag.Usage()
		os.Exit(1)
	}

	var c config
	err := cmd.ReadConfigFile(*configFile, &c)
	cmd.FailOnError(err, "Reading JSON config file into config structure")

	certChains, err := loadCertificateChains(c.WFE.CertificateChains)
	cmd.FailOnError(err, "Couldn't read configured CertificateChains")

	err = features.Set(c.WFE.Features)
	cmd.FailOnError(err, "Failed to set feature flags")

	scope, logger := cmd.StatsAndLogging(c.Syslog, c.WFE.DebugAddr)
	defer logger.AuditPanic()
	logger.Info(cmd.VersionString())

	kp, err := goodkey.NewKeyPolicy("") // don't load any weak keys
	cmd.FailOnError(err, "Unable to create key policy")
	wfe, err := wfe2.NewWebFrontEndImpl(scope, cmd.Clock(), kp, certChains, logger)
	cmd.FailOnError(err, "Unable to create WFE")
	rac, sac := setupWFE(c, logger, scope)
	wfe.RA = rac
	wfe.SA = sac

	// TODO: remove this check once the production config uses the SubscriberAgreementURL in the wfe section
	if c.WFE.SubscriberAgreementURL != "" {
		wfe.SubscriberAgreementURL = c.WFE.SubscriberAgreementURL
	} else {
		wfe.SubscriberAgreementURL = c.SubscriberAgreementURL
	}

	wfe.AllowOrigins = c.WFE.AllowOrigins
	wfe.AcceptRevocationReason = c.WFE.AcceptRevocationReason
	wfe.AllowAuthzDeactivation = c.WFE.AllowAuthzDeactivation

	wfe.IssuerCert, err = cmd.LoadCert(c.Common.IssuerCert)
	cmd.FailOnError(err, fmt.Sprintf("Couldn't read issuer cert [%s]", c.Common.IssuerCert))

	logger.Info(fmt.Sprintf("WFE using key policy: %#v", kp))

	logger.Info(fmt.Sprintf("Server running, listening on %s...\n", c.WFE.ListenAddress))
	handler := wfe.Handler()
	srv := &http.Server{
		Addr:    c.WFE.ListenAddress,
		Handler: handler,
	}

	go func() {
		err := srv.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			cmd.FailOnError(err, "Running HTTP server")
		}
	}()

	var tlsSrv *http.Server
	if c.WFE.TLSListenAddress != "" {
		tlsSrv = &http.Server{
			Addr:    c.WFE.TLSListenAddress,
			Handler: handler,
		}
		go func() {
			err := tlsSrv.ListenAndServeTLS(c.WFE.ServerCertificatePath, c.WFE.ServerKeyPath)
			if err != nil && err != http.ErrServerClosed {
				cmd.FailOnError(err, "Running TLS server")
			}
		}()
	}

	done := make(chan bool)
	go cmd.CatchSignals(logger, func() {
		ctx, cancel := context.WithTimeout(context.Background(), c.WFE.ShutdownStopTimeout.Duration)
		defer cancel()
		_ = srv.Shutdown(ctx)
		if tlsSrv != nil {
			_ = tlsSrv.Shutdown(ctx)
		}
		done <- true
	})

	// https://godoc.org/net/http#Server.Shutdown:
	// When Shutdown is called, Serve, ListenAndServe, and ListenAndServeTLS
	// immediately return ErrServerClosed. Make sure the program doesn't exit and
	// waits instead for Shutdown to return.
	<-done
}
