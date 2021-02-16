package main

import (
	"flag"
	"os"

	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/letsencrypt/boulder/cmd"
	"github.com/letsencrypt/boulder/features"
	bgrpc "github.com/letsencrypt/boulder/grpc"
	"github.com/letsencrypt/boulder/sa"
	sapb "github.com/letsencrypt/boulder/sa/proto"
)

type config struct {
	SA struct {
		cmd.ServiceConfig
		DB cmd.DBConfig
		// TODO(#5275): Remove once all configs in dev, staging and prod
		// have been updated to contain the `dbconfig` field
		cmd.DeprecatedDBConfig

		Features map[string]bool

		// Max simultaneous SQL queries caused by a single RPC.
		ParallelismPerRPC int
	}

	Syslog cmd.SyslogConfig
}

func main() {
	grpcAddr := flag.String("addr", "", "gRPC listen address override")
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

	err = features.Set(c.SA.Features)
	cmd.FailOnError(err, "Failed to set feature flags")

	if *grpcAddr != "" {
		c.SA.GRPC.Address = *grpcAddr
	}
	if *debugAddr != "" {
		c.SA.DebugAddr = *debugAddr
	}

	scope, logger := cmd.StatsAndLogging(c.Syslog, c.SA.DebugAddr)
	defer logger.AuditPanic()
	logger.Info(cmd.VersionString())

	saConf := c.SA

	// TODO(#5275): Remove once all configs in dev, staging and prod
	// have been updated to contain the `dbconfig` field
	cmd.DefaultDBConfig(&saConf.DB, &saConf.DeprecatedDBConfig)

	saDbSettings := sa.DbSettings{
		MaxOpenConns:    saConf.DB.MaxOpenConns,
		MaxIdleConns:    saConf.DB.MaxIdleConns,
		ConnMaxLifetime: saConf.DB.ConnMaxLifetime.Duration,
		ConnMaxIdleTime: saConf.DB.ConnMaxIdleTime.Duration,
	}

	dbURL, err := saConf.DB.URL()
	cmd.FailOnError(err, "Couldn't load DB URL")

	dbMap, err := sa.NewDbMap(dbURL, saDbSettings)
	cmd.FailOnError(err, "Couldn't connect to SA database")

	// Collect and periodically report DB metrics using the DBMap and prometheus scope.
	sa.InitDBMetrics(dbMap, scope, saDbSettings)

	clk := cmd.Clock()

	parallel := saConf.ParallelismPerRPC
	if parallel < 1 {
		parallel = 1
	}
	sai, err := sa.NewSQLStorageAuthority(dbMap, clk, logger, scope, parallel)
	cmd.FailOnError(err, "Failed to create SA impl")

	tls, err := c.SA.TLS.Load()
	cmd.FailOnError(err, "TLS config")
	serverMetrics := bgrpc.NewServerMetrics(scope)
	grpcSrv, listener, err := bgrpc.NewServer(c.SA.GRPC, tls, serverMetrics, clk)
	cmd.FailOnError(err, "Unable to setup SA gRPC server")
	gw := bgrpc.NewStorageAuthorityServer(sai)
	sapb.RegisterStorageAuthorityServer(grpcSrv, gw)
	hs := health.NewServer()
	healthpb.RegisterHealthServer(grpcSrv, hs)

	go cmd.CatchSignals(logger, func() {
		hs.Shutdown()
		grpcSrv.GracefulStop()
	})

	err = cmd.FilterShutdownErrors(grpcSrv.Serve(listener))
	cmd.FailOnError(err, "SA gRPC service failed")
}
