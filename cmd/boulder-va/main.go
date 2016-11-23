package main

import (
	"flag"
	"os"
	"time"

	"github.com/jmhodges/clock"
	"google.golang.org/grpc"

	"github.com/letsencrypt/boulder/bdns"
	"github.com/letsencrypt/boulder/cdr"
	"github.com/letsencrypt/boulder/cmd"
	bgrpc "github.com/letsencrypt/boulder/grpc"
	"github.com/letsencrypt/boulder/metrics"
	"github.com/letsencrypt/boulder/rpc"
	"github.com/letsencrypt/boulder/va"
)

const clientName = "VA"

type config struct {
	VA struct {
		cmd.ServiceConfig

		UserAgent string

		IssuerDomain string

		PortConfig cmd.PortConfig

		MaxConcurrentRPCServerRequests int64

		GoogleSafeBrowsing *cmd.GoogleSafeBrowsingConfig

		CAADistributedResolver *cmd.CAADistributedResolverConfig

		// The number of times to try a DNS query (that has a temporary error)
		// before giving up. May be short-circuited by deadlines. A zero value
		// will be turned into 1.
		DNSTries int

		// Feature flag to enable enforcement of CAA SERVFAILs.
		CAASERVFAILExceptions string
	}

	Statsd cmd.StatsdConfig

	Syslog cmd.SyslogConfig

	Common struct {
		DNSResolver               string
		DNSTimeout                string
		DNSAllowLoopbackAddresses bool
	}
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

	stats, logger := cmd.StatsAndLogging(c.Statsd, c.Syslog)
	scope := metrics.NewStatsdScope(stats, "VA")
	defer logger.AuditPanic()
	logger.Info(cmd.VersionString(clientName))

	pc := &cmd.PortConfig{
		HTTPPort:  80,
		HTTPSPort: 443,
		TLSPort:   443,
	}
	if c.VA.PortConfig.HTTPPort != 0 {
		pc.HTTPPort = c.VA.PortConfig.HTTPPort
	}
	if c.VA.PortConfig.HTTPSPort != 0 {
		pc.HTTPSPort = c.VA.PortConfig.HTTPSPort
	}
	if c.VA.PortConfig.TLSPort != 0 {
		pc.TLSPort = c.VA.PortConfig.TLSPort
	}

	sbc := newGoogleSafeBrowsing(c.VA.GoogleSafeBrowsing)

	var cdrClient *cdr.CAADistributedResolver
	if c.VA.CAADistributedResolver != nil {
		var err error
		cdrClient, err = cdr.New(
			scope,
			c.VA.CAADistributedResolver.Timeout.Duration,
			c.VA.CAADistributedResolver.MaxFailures,
			c.VA.CAADistributedResolver.Proxies,
			logger)
		cmd.FailOnError(err, "Failed to create CAADistributedResolver")
	}

	dnsTimeout, err := time.ParseDuration(c.Common.DNSTimeout)
	cmd.FailOnError(err, "Couldn't parse DNS timeout")
	dnsTries := c.VA.DNSTries
	if dnsTries < 1 {
		dnsTries = 1
	}
	clk := clock.Default()
	caaSERVFAILExceptions, err := bdns.ReadHostList(c.VA.CAASERVFAILExceptions)
	cmd.FailOnError(err, "Couldn't read CAASERVFAILExceptions file")
	var resolver bdns.DNSResolver
	if !c.Common.DNSAllowLoopbackAddresses {
		r := bdns.NewDNSResolverImpl(
			dnsTimeout,
			[]string{c.Common.DNSResolver},
			caaSERVFAILExceptions,
			scope,
			clk,
			dnsTries)
		resolver = r
	} else {
		r := bdns.NewTestDNSResolverImpl(dnsTimeout, []string{c.Common.DNSResolver}, scope, clk, dnsTries)
		resolver = r
	}

	vai := va.NewValidationAuthorityImpl(
		pc,
		sbc,
		cdrClient,
		resolver,
		c.VA.UserAgent,
		c.VA.IssuerDomain,
		scope,
		clk,
		logger)

	amqpConf := c.VA.AMQP
	var grpcSrv *grpc.Server
	if c.VA.GRPC != nil {
		s, l, err := bgrpc.NewServer(c.VA.GRPC, scope)
		cmd.FailOnError(err, "Unable to setup VA gRPC server")
		err = bgrpc.RegisterValidationAuthorityGRPCServer(s, vai)
		cmd.FailOnError(err, "Unable to register VA gRPC server")
		go func() {
			err = s.Serve(l)
			cmd.FailOnError(err, "VA gRPC service failed")
		}()
		grpcSrv = s
	}

	vas, err := rpc.NewAmqpRPCServer(amqpConf, c.VA.MaxConcurrentRPCServerRequests, scope, logger)
	cmd.FailOnError(err, "Unable to create VA RPC server")

	go cmd.CatchSignals(logger, func() {
		vas.Stop()
		if grpcSrv != nil {
			grpcSrv.GracefulStop()
		}
	})

	err = rpc.NewValidationAuthorityServer(vas, vai)
	cmd.FailOnError(err, "Unable to setup VA RPC server")

	go cmd.DebugServer(c.VA.DebugAddr)
	go cmd.ProfileCmd(scope)

	err = vas.Start(amqpConf)
	cmd.FailOnError(err, "Unable to run VA RPC server")
}
