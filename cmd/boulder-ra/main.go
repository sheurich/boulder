// Copyright 2014 ISRG.  All rights reserved
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package main

import (
	"github.com/letsencrypt/boulder/Godeps/_workspace/src/github.com/cactus/go-statsd-client/statsd"
	"github.com/letsencrypt/boulder/Godeps/_workspace/src/github.com/streadway/amqp"

	"github.com/letsencrypt/boulder/cmd"
	blog "github.com/letsencrypt/boulder/log"
	"github.com/letsencrypt/boulder/ra"
	"github.com/letsencrypt/boulder/rpc"
)

func main() {
	app := cmd.NewAppShell("boulder-ra")
	app.Action = func(c cmd.Config) {
		stats, err := statsd.NewClient(c.Statsd.Server, c.Statsd.Prefix)
		cmd.FailOnError(err, "Couldn't connect to statsd")

		// Set up logging
		auditlogger, err := blog.Dial(c.Syslog.Network, c.Syslog.Server, c.Syslog.Tag, stats)
		cmd.FailOnError(err, "Could not connect to Syslog")

		rai := ra.NewRegistrationAuthorityImpl(auditlogger)

		go cmd.ProfileCmd("RA", stats)


		for {
			ch := cmd.AmqpChannel(c.AMQP.Server)
			closeChan := ch.NotifyClose(make(chan *amqp.Error, 1))

			vac, err := rpc.NewValidationAuthorityClient(c.AMQP.VA.Client, c.AMQP.VA.Server, ch)
			cmd.FailOnError(err, "Unable to create VA client")

			cac, err := rpc.NewCertificateAuthorityClient(c.AMQP.CA.Client, c.AMQP.CA.Server, ch)
			cmd.FailOnError(err, "Unable to create CA client")

			sac, err := rpc.NewStorageAuthorityClient(c.AMQP.SA.Client, c.AMQP.SA.Server, ch)
			cmd.FailOnError(err, "Unable to create SA client")

			rai.VA = &vac
			rai.CA = &cac
			rai.SA = &sac

			ras, err := rpc.NewRegistrationAuthorityServer(c.AMQP.RA.Server, ch, &rai)
			cmd.FailOnError(err, "Unable to create RA server")

			cmd.RunUntilSignaled(auditlogger, ras, closeChan)
		}

	}

	app.Run()
}
