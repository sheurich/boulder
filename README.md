Boulder - An ACME CA
====================

This is an implementation of an ACME-based CA. The [ACME protocol](https://github.com/ietf-wg-acme/acme/) allows the CA to automatically verify that an applicant for a certificate actually controls an identifier, and allows domain holders to issue and revoke certificates for their domains.

[![Build Status](https://travis-ci.org/letsencrypt/boulder.svg)](https://travis-ci.org/letsencrypt/boulder)
[![Coverage Status](https://coveralls.io/repos/letsencrypt/boulder/badge.svg)](https://coveralls.io/r/letsencrypt/boulder)

Quickstart
------

Boulder has a Dockerfile to make it easy to install and set up all its
dependencies. This is how the maintainers work on Boulder, and is our main
recommended way to run it.

To start Boulder in a Docker container, run:

    docker-compose up

To run tests:

    docker-compose run boulder ./test.sh

To run a specific unittest:

    docker-compose run boulder go test ./ra

The configuration in docker-compose.yml mounts your
[`$GOPATH`](https://golang.org/doc/code.html#GOPATH) on top of its own
`$GOPATH`. So you can edit code on your host and it will be immediately
reflected inside Docker images run with docker-compose.

By default, Boulder uses a fake DNS resolver that resolves all hostnames to
127.0.0.1. This is suitable for running integration tests inside the Docker
container. If you want Boulder to be able to communicate with a client running
on your host instead, you should find your host's Docker IP with:

    ifconfig | grep -A1 docker0

And edit docker-compose.yml to change the FAKE_DNS environment variable to
match.

If a base image changes (i.e. `letsencrypt/boulder-tools`) you will need to rebuild
images for both the boulder and bhsm containers and re-create them. The quickest way
to do this is with this command:

    ./docker-rebuild.sh

Slow start
----------

If you can't use the Docker setup, here are instructions for setting up a
Boulder development environment without it.

We recommend setting git's [fsckObjects
setting](https://groups.google.com/forum/#!topic/binary-transparency/f-BI4o8HZW0/discussion)
for better integrity guarantees when getting updates.

Boulder requires an installation of RabbitMQ, libtool-ltdl, goose, and
MariaDB 10.1 to work correctly. On Ubuntu and CentOS, you may have to
install RabbitMQ from https://rabbitmq.com/download.html to get a
recent version. If you want to save some trouble installing MariaDB and RabbitMQ
you can run them using Docker:

    docker-compose up -d bmysql brabbitmq bhsm

Also, Boulder requires Go 1.5. As of September 2015 this version is not yet
available in OS repositories, so you will have to install from https://golang.org/dl/.
Add ```${GOPATH}/bin``` to your path.

Ubuntu:

    sudo apt-get install libltdl3-dev mariadb-server rabbitmq-server

CentOS:

    sudo yum install libtool-ltdl-devel MariaDB-server MariaDB-client rabbitmq-server

Arch Linux:

    sudo pacman -S libtool mariadb rabbitmq --needed

OS X:

    brew install libtool mariadb rabbitmq

or

    sudo port install libtool mariadb-server rabbitmq-server

(On OS X, using port, you will have to add `CGO_CFLAGS="-I/opt/local/include" CGO_LDFLAGS="-L/opt/local/lib"` to your environment or `go` invocations.)

Edit /etc/hosts to add this line:

    127.0.0.1 boulder boulder-rabbitmq boulder-mysql

Resolve Go-dependencies, set up a database and RabbitMQ:

    export GO15VENDOREXPERIMENT=1
    ./test/setup.sh

**Note**: `setup.sh` calls `create_db.sh`, which uses the root MariaDB
user with the default password, so if you have disabled that account
or changed the password you may have to adjust the file or recreate the commands.

Install SoftHSM to store the CA private key in a way that can be accessed using
PKCS#11. Then run ./test/make-softhsm.sh and follow its instructions.

Start all boulder components with test configs (Ctrl-C kills all):

    ./start.py

Run tests:

    ./test.sh

Working with a client:

Check out the Certbot client from https://github.com/certbot/certbot and follow the setup instructions there. Once you've got the client set up, you'll probably want to run it against your local Boulder. There are a number of command line flags that are necessary to run the client against a local Boulder, and without root access. The simplest way to run the client locally is to source a file that provides an alias for letsencrypt that has all those flags:

    source ~/letsencrypt/tests/integration/_common.sh
    certbot_test certonly -a standalone -d example.com

Your local Boulder instance uses a fake DNS server that returns 127.0.0.1 for
any query, so you can use any value for the -d flag. You can also override that
value by setting the environment variable FAKE_DNS=1.2.3.4

Component Model
---------------

The CA is divided into the following main components:

1. Web Front End
2. Registration Authority
3. Validation Authority
4. Certificate Authority
5. Storage Authority
6. OCSP Updater
7. OCSP Responder

This component model lets us separate the function of the CA by security context.  The Web Front End and Validation Authority need access to the Internet, which puts them at greater risk of compromise.  The Registration Authority can live without Internet connectivity, but still needs to talk to the Web Front End and Validation Authority.  The Certificate Authority need only receive instructions from the Registration Authority. All components talk to the SA for storage, so lines indicating SA RPCs are not shown here.

```

                             +--------- OCSP Updater
                             |               |
                             v               |
                            CA               |
                             ^               |
                             |               v
       Subscriber -> WFE --> RA --> SA --> MariaDB
                             |               ^
Subscriber server <- VA <----+               |
                                             |
          Browser ------------------>  OCSP Responder

```

Internally, the logic of the system is based around four types of objects: registrations, authorizations, challenges, and certificates, mapping directly to the resources of the same name in ACME.

Requests from ACME clients result in new objects and changes to objects.  The Storage Authority maintains persistent copies of the current set of objects.

Objects are also passed from one component to another on change events.  For example, when a client provides a successful response to a validation challenge, it results in a change to the corresponding validation object.  The Validation Authority forwards the new validation object to the Storage Authority for storage, and to the Registration Authority for any updates to a related Authorization object.

Boulder uses AMQP as a message bus.  For components that you want to be remote, it is necessary to instantiate a "client" and "server" for that component.  The client implements the component's Go interface, while the server has the actual logic for the component.  More details in `amqp-rpc.go`.

The full details of how the various ACME operations happen in Boulder are laid out in [DESIGN.md](https://github.com/letsencrypt/boulder/blob/master/DESIGN.md)

Dependencies
------------

All Go dependencies are vendored under the vendor directory,
to [make dependency management easier](https://golang.org/cmd/go/#hdr-Vendor_Directories).

Local development also requires a RabbitMQ installation and MariaDB
10 installation (see above). MariaDB should be run on port 3306 for the
default integration tests.

To update the Go dependencies:

```
# Fetch godep
go get -u github.com/tools/godep
# Check out the currently vendorized version of each dependency.
godep restore
# Update to the latest version of a dependency. Alternately you can cd to the
# directory under GOPATH and check out a specific revision. Here's an example
# using cfssl:
go get -u github.com/cloudflare/cfssl/...
# Update the Godep config to the appropriate version.
godep update github.com/cloudflare/cfssl/...
# Save the dependencies
godep save ./...
git add Godeps vendor
git commit
```
