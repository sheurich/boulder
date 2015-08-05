# This Makefile also tricks Travis into not running 'go get' for our
# build. See http://docs.travis-ci.com/user/languages/go/

OBJDIR ?= ./bin
DESTDIR ?= /usr/local/bin
ARCHIVEDIR ?= /tmp

VERSION ?= 1.0.0
EPOCH ?= 1
MAINTAINER ?= "Community"

OBJECTS = activity-monitor \
	admin-revoker \
	boulder-ca \
	boulder-ra \
	boulder-sa \
	boulder-va \
	boulder-wfe \
	expiration-mailer \
	ocsp-updater \
	ocsp-responder

# Build environment variables (referencing core/util.go)
COMMIT_ID = $(shell git rev-parse --short HEAD)

BUILD_ID = $(shell git symbolic-ref --short HEAD 2>/dev/null) +$(COMMIT_ID)
BUILD_ID_VAR = github.com/letsencrypt/boulder/core.BuildID

BUILD_HOST = $(shell whoami)@$(shell hostname)
BUILD_HOST_VAR = github.com/letsencrypt/boulder/core.BuildHost

BUILD_TIME = $(shell date -u)
BUILD_TIME_VAR = github.com/letsencrypt/boulder/core.BuildTime

.PHONY: all build
all: build

build: $(OBJECTS)

pre:
	@mkdir -p $(OBJDIR)
	@echo [go] lib/github.com/mattn/go-sqlite3
	@go install ./Godeps/_workspace/src/github.com/mattn/go-sqlite3

# Compile each of the binaries
$(OBJECTS): pre
	@echo [go] bin/$@
	@go build -o ./bin/$@ -ldflags \
		"-X $(BUILD_ID_VAR) '$(BUILD_ID)' -X $(BUILD_TIME_VAR) '$(BUILD_TIME)' \
		 -X $(BUILD_HOST_VAR) '$(BUILD_HOST)'" \
		cmd/$@/main.go

clean:
	rm -f $(OBJDIR)/*
	rmdir $(OBJDIR)

# Install to a destination directory. Defaults to /usr/local/, but you can
# override it with the DESTDIR variable. Example:
#
# DESTDIR=~/bin make install
install:
	@mkdir -p $(DESTDIR)
	$(foreach var,$(OBJECTS), install -m 0755 $(OBJDIR)/$(var) $(DESTDIR)/;)

# Produce a tarball of the current commit; you can set the destination in the
# ARCHIVEDIR variable.
archive:
	git archive --output=$(ARCHIVEDIR)/boulder-$(COMMIT_ID).tar.gz \
		--prefix=boulder-$(COMMIT_ID)/ $(COMMIT_ID)

# Building an RPM requires `fpm` from https://github.com/jordansissel/fpm
# which you can install with `gem install fpm`.
# It is recommended that maintainers use environment overrides to specify
# Version and Epoch, such as:
#
# VERSION=0.1.9 EPOCH=52 MAINTAINER="$(whoami)" ARCHIVEDIR=/tmp make build rpm
rpm:
	fpm -s dir -t rpm --rpm-digest sha256 --name "boulder" \
		--license "Mozilla Public License v2.0" --vendor "ISRG" \
		--url "https://github.com/letsencrypt/boulder" --prefix=/opt/boulder \
		--version $(VERSION) --iteration $(COMMIT_ID) --epoch $(EPOCH) \
		--package $(ARCHIVEDIR)/boulder-$(VERSION)-$(COMMIT_ID).x86_64.rpm \
		--description "Boulder is an ACME-compatible X.509 Certificate Authority" \
		--depends "libtool-ltdl" --maintainer "$(MAINTAINER)" \
		test/boulder-config.json $(foreach var,$(OBJECTS), $(OBJDIR)/$(var))
