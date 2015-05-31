// Copyright 2014 ISRG.  All rights reserved
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package main

import (
	"github.com/letsencrypt/boulder/Godeps/_workspace/src/github.com/cactus/go-statsd-client/statsd"
	"github.com/letsencrypt/boulder/Godeps/_workspace/src/github.com/streadway/amqp"

	"github.com/letsencrypt/boulder/ca"
	"github.com/letsencrypt/boulder/cmd"
	blog "github.com/letsencrypt/boulder/log"
	"github.com/letsencrypt/boulder/rpc"
)

func main() {
	app := cmd.NewAppShell("boulder-ca")
	app.Action = func(c cmd.Config) {
		stats, err := statsd.NewClient(c.Statsd.Server, c.Statsd.Prefix)
		cmd.FailOnError(err, "Couldn't connect to statsd")

		// Set up logging
		auditlogger, err := blog.Dial(c.Syslog.Network, c.Syslog.Server, c.Syslog.Tag, stats)
		cmd.FailOnError(err, "Could not connect to Syslog")

		// AUDIT[ Error Conditions ] 9cc4d537-8534-4970-8665-4b382abe82f3
		defer auditlogger.AuditPanic()

		blog.SetAuditLogger(auditlogger)

		cadb, err := ca.NewCertificateAuthorityDatabaseImpl(c.CA.DBDriver, c.CA.DBName)
		cmd.FailOnError(err, "Failed to create CA database")

		if c.SQL.CreateTables {
			err = cadb.CreateTablesIfNotExists()
			cmd.FailOnError(err, "Failed to create CA tables")
		}

		cai, err := ca.NewCertificateAuthorityImpl(cadb, c.CA)
		cmd.FailOnError(err, "Failed to create CA impl")

		go cmd.ProfileCmd("CA", stats)

		for {
			ch := cmd.AmqpChannel(c.AMQP.Server)
			closeChan := ch.NotifyClose(make(chan *amqp.Error, 1))

			sac, err := rpc.NewStorageAuthorityClient("CA->SA", c.AMQP.SA.Server, ch)
			cmd.FailOnError(err, "Failed to create SA client")

			cai.SA = &sac

			cas, err := rpc.NewCertificateAuthorityServer(c.AMQP.CA.Server, ch, cai)
			cmd.FailOnError(err, "Unable to create CA server")

			auditlogger.Info(app.VersionString())

			cmd.RunUntilSignaled(auditlogger, cas, closeChan)
		}
	}

	app.Run()
}
