// Copyright 2014 ISRG.  All rights reserved
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package main

import (
	"github.com/letsencrypt/boulder/Godeps/_workspace/src/github.com/cactus/go-statsd-client/statsd"
	"github.com/letsencrypt/boulder/Godeps/_workspace/src/github.com/jmhodges/clock"
	"github.com/letsencrypt/boulder/ca"
	"github.com/letsencrypt/boulder/cmd"
	blog "github.com/letsencrypt/boulder/log"
	"github.com/letsencrypt/boulder/policy"
	"github.com/letsencrypt/boulder/rpc"
	"github.com/letsencrypt/boulder/sa"
)

func main() {
	app := cmd.NewAppShell("boulder-ca", "Handles issuance operations")
	app.Action = func(c cmd.Config) {
		stats, err := statsd.NewClient(c.Statsd.Server, c.Statsd.Prefix)
		cmd.FailOnError(err, "Couldn't connect to statsd")

		// Set up logging
		auditlogger, err := blog.Dial(c.Syslog.Network, c.Syslog.Server, c.Syslog.Tag, stats)
		cmd.FailOnError(err, "Could not connect to Syslog")
		auditlogger.Info(app.VersionString())

		// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
		defer auditlogger.AuditPanic()

		blog.SetAuditLogger(auditlogger)

		go cmd.DebugServer(c.CA.DebugAddr)

		paDbMap, err := sa.NewDbMap(c.PA.DBConnect)
		cmd.FailOnError(err, "Couldn't connect to policy database")
		pa, err := policy.NewPolicyAuthorityImpl(paDbMap, c.PA.EnforcePolicyWhitelist)
		cmd.FailOnError(err, "Couldn't create PA")

		cai, err := ca.NewCertificateAuthorityImpl(c.CA, clock.Default(), c.Common.IssuerCert)
		cmd.FailOnError(err, "Failed to create CA impl")
		cai.PA = pa

		go cmd.ProfileCmd("CA", stats)

		connectionHandler := func(srv *rpc.AmqpRPCServer) {
			saRPC, err := rpc.NewAmqpRPCClient("CA->SA", c.AMQP.SA.Server, srv.Channel, stats)
			cmd.FailOnError(err, "Unable to create RPC client")

			sac, err := rpc.NewStorageAuthorityClient(saRPC)
			cmd.FailOnError(err, "Failed to create SA client")

			pubRPC, err := rpc.NewAmqpRPCClient("CA->Publisher", c.AMQP.Publisher.Server, srv.Channel, stats)
			cmd.FailOnError(err, "Unable to create RPC client")

			pubc, err := rpc.NewPublisherClient(pubRPC)
			cmd.FailOnError(err, "Failed to create Publisher client")

			cai.Publisher = &pubc
			cai.SA = &sac
		}

		cas, err := rpc.NewAmqpRPCServer(c.AMQP.CA.Server, connectionHandler)
		cmd.FailOnError(err, "Unable to create CA RPC server")
		rpc.NewCertificateAuthorityServer(cas, cai)

		err = cas.Start(c)
		cmd.FailOnError(err, "Unable to run CA RPC server")
	}

	app.Run()
}
