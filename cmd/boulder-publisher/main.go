package main

import (
	"flag"
	"os"

	ct "github.com/google/certificate-transparency/go"

	"github.com/letsencrypt/boulder/cmd"
	"github.com/letsencrypt/boulder/core"
	bgrpc "github.com/letsencrypt/boulder/grpc"
	"github.com/letsencrypt/boulder/metrics"
	"github.com/letsencrypt/boulder/publisher"
	pubPB "github.com/letsencrypt/boulder/publisher/proto"
	"github.com/letsencrypt/boulder/rpc"
)

const clientName = "Publisher"

type config struct {
	Publisher struct {
		cmd.ServiceConfig
		SubmissionTimeout              cmd.ConfigDuration
		MaxConcurrentRPCServerRequests int64
	}

	Statsd cmd.StatsdConfig

	Syslog cmd.SyslogConfig

	Common struct {
		CT struct {
			Logs                       []cmd.LogDescription
			IntermediateBundleFilename string
		}
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
	err := cmd.ReadJSONFile(*configFile, &c)
	cmd.FailOnError(err, "Reading JSON config file into config structure")

	go cmd.DebugServer(c.Publisher.DebugAddr)

	stats, logger := cmd.StatsAndLogging(c.Statsd, c.Syslog)
	defer logger.AuditPanic()
	logger.Info(cmd.VersionString(clientName))

	logs := make([]*publisher.Log, len(c.Common.CT.Logs))
	for i, ld := range c.Common.CT.Logs {
		logs[i], err = publisher.NewLog(ld.URI, ld.Key)
		cmd.FailOnError(err, "Unable to parse CT log description")
	}

	if c.Common.CT.IntermediateBundleFilename == "" {
		logger.AuditErr("No CT submission bundle provided")
		os.Exit(1)
	}
	pemBundle, err := core.LoadCertBundle(c.Common.CT.IntermediateBundleFilename)
	cmd.FailOnError(err, "Failed to load CT submission bundle")
	bundle := []ct.ASN1Cert{}
	for _, cert := range pemBundle {
		bundle = append(bundle, ct.ASN1Cert(cert.Raw))
	}

	pubi := publisher.New(bundle, logs, c.Publisher.SubmissionTimeout.Duration, logger)

	go cmd.ProfileCmd("Publisher", stats)

	amqpConf := c.Publisher.AMQP
	pubi.SA, err = rpc.NewStorageAuthorityClient(clientName, amqpConf, stats)
	cmd.FailOnError(err, "Unable to create SA client")

	if c.Publisher.GRPC != nil {
		s, l, err := bgrpc.NewServer(c.Publisher.GRPC, metrics.NewStatsdScope(stats, "Publisher"))
		cmd.FailOnError(err, "Failed to setup gRPC server")
		gw := bgrpc.NewPublisherServerWrapper(pubi)
		pubPB.RegisterPublisherServer(s, gw)
		go func() {
			err = s.Serve(l)
			cmd.FailOnError(err, "gRPC service failed")
		}()
	}

	pubs, err := rpc.NewAmqpRPCServer(amqpConf, c.Publisher.MaxConcurrentRPCServerRequests, stats, logger)
	cmd.FailOnError(err, "Unable to create Publisher RPC server")
	err = rpc.NewPublisherServer(pubs, pubi)
	cmd.FailOnError(err, "Unable to setup Publisher RPC server")

	err = pubs.Start(amqpConf)
	cmd.FailOnError(err, "Unable to run Publisher RPC server")
}
