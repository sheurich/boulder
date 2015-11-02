Boulder - An ACME CA
====================

This is an initial implementation of an ACME-based CA. The [ACME protocol](https://github.com/letsencrypt/acme-spec/) allows the CA to automatically verify that an applicant for a certificate actually controls an identifier, and allows domain holders to issue and revoke certificates for their domains.


[![Build Status](https://travis-ci.org/letsencrypt/boulder.svg)](https://travis-ci.org/letsencrypt/boulder)
[![Coverage Status](https://coveralls.io/repos/letsencrypt/boulder/badge.svg)](https://coveralls.io/r/letsencrypt/boulder)

Quickstart
------

Boulder has a Dockerfile to make it easy to install and set up all its
dependencies. This approach is most suitable if you just need to set up Boulder
for the purpose of testing client software against it. To start Boulder
in a Docker container, run:

    ./test/run-docker.sh

Slow start
----------

This approach is better if you intend to develop on Boulder frequently, because
it's challenging to develop inside the Docker container.

Boulder requires an installation of RabbitMQ, libtool-ltdl, goose, and
MariaDB 10 to work correctly. On Ubuntu and CentOS, you may have to
install RabbitMQ from https://rabbitmq.com/download.html to get a
recent version.

Also, Boulder requires Go 1.5. As of September 2015 this version is not yet
available in OS repositories, so you will have to install from https://golang.org/dl/.

Ubuntu:

    sudo apt-get install libltdl3-dev mariadb-server rabbitmq-server

CentOS:

    sudo yum install libtool-ltdl-devel MariaDB-server MariaDB-client rabbitmq-server

OS X:

    brew install libtool mariadb rabbitmq

or

    sudo port install libtool mariadb-server rabbitmq-server

(On OS X, using port, you will have to add `CGO_CFLAGS="-I/opt/local/include" CGO_LDFLAGS="-L/opt/local/lib"` to your environment or `go` invocations.)

    > go get bitbucket.org/liamstask/goose/cmd/goose
    > go get github.com/letsencrypt/boulder/ # Ignore errors about no buildable files
    > cd $GOPATH/src/github.com/letsencrypt/boulder
    > ./test/create_db.sh
    # This starts each Boulder component with test configs. Ctrl-C kills all.
    > ./start.py
    # Run tests
    > ./test.sh

Note: `create_db.sh` it uses the root MariaDB user, so if you
have disabled that account you may have to adjust the file or
recreate the commands.

You can also check out the official client from
https://github.com/letsencrypt/letsencrypt/ and follow the setup
instructions there.

Component Model
---------------

The CA is divided into the following main components:

1. Web Front End
2. Registration Authority
3. Validation Authority
4. Certificate Authority
5. Storage Authority

This component model lets us separate the function of the CA by security context.  The Web Front End and Validation Authority need access to the Internet, which puts them at greater risk of compromise.  The Registration Authority can live without Internet connectivity, but still needs to talk to the Web Front End and Validation Authority.  The Certificate Authority need only receive instructions from the Registration Authority.

```

client <--ACME--> WFE ---+
  .                      |
  .                      +--- RA --- CA
  .                      |
client <-checks->  VA ---+

```

In Boulder, these components are represented by Go interfaces.  This allows us to have two operational modes: Consolidated and distributed.  In consolidated mode, the objects representing the different components interact directly, through function calls.  In distributed mode, each component runs in a separate process (possibly on a separate machine), and sees the other components' methods by way of a messaging layer.

Internally, the logic of the system is based around two types of objects, authorizations and certificates, mapping directly to the resources of the same name in ACME.

Requests from ACME clients result in new objects and changes to objects.  The Storage Authority maintains persistent copies of the current set of objects.

Objects are also passed from one component to another on change events.  For example, when a client provides a successful response to a validation challenge, it results in a change to the corresponding validation object.  The Validation Authority forward the new validation object to the Storage Authority for storage, and to the Registration Authority for any updates to a related Authorization object.

Boulder supports distributed operation using AMQP as a message bus (e.g., via RabbitMQ).  For components that you want to be remote, it is necessary to instantiate a "client" and "server" for that component.  The client implements the component's Go interface, while the server has the actual logic for the component.  More details in `amqp-rpc.go`.

The full details of how the various ACME operations happen in Boulder are laid out in [DESIGN.md](https://github.com/letsencrypt/boulder/blob/master/DESIGN.md)


Dependencies
------------

All Go dependencies are vendorized under the Godeps directory,
both to [make dependency management
easier](https://groups.google.com/forum/m/#!topic/golang-dev/nMWoEAG55v8)
and to [avoid insecure fallback in go
get](https://github.com/golang/go/issues/9637).

Local development also requires a RabbitMQ installation and MariaDB
10 installation (see above). MariaDB should be run on port 3306 for the
default integration tests.

To update the Go dependencies:

```
# Disable insecure fallback by blocking port 80.
sudo /sbin/iptables -A OUTPUT -p tcp --dport 80 -j DROP
# Fetch godep
go get -u https://github.com/tools/godep
# Update to the latest version of a dependency. Alternately you can cd to the
# directory under GOPATH and check out a specific revision. Here's an example
# using cfssl:
go get -u github.com/cloudflare/cfssl/...
# Update the Godep config to the appropriate version.
godep update github.com/cloudflare/cfssl/...
# Save the dependencies, rewriting any internal or external dependencies that
# may have been added.
godep save -r ./...
git add Godeps
git commit
# Assuming you had no other iptables rules, re-enable port 80.
sudo iptables -D OUTPUT 1
```


TODO
----

See [the issues list](https://github.com/letsencrypt/boulder/issues)
