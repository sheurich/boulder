#!/bin/bash -ex

apt-get update

# Install system deps
apt-get install -y --no-install-recommends \
  mariadb-client-core-10.1 \
  rpm \
  ruby \
  ruby-dev \
  rsyslog \
  softhsm \
  build-essential \
  cmake \
  libssl-dev \
  libseccomp-dev \
  opensc \
  unzip \
  python3-pip \
  gcc \
  ca-certificates \
  openssl

curl -L https://github.com/google/protobuf/releases/download/v3.11.4/protoc-3.11.4-linux-x86_64.zip -o /tmp/protoc.zip
unzip /tmp/protoc.zip -d /usr/local/protoc

# Override default GOBIN and GOCACHE
export GOBIN=/usr/local/bin GOCACHE=/tmp/gocache

# Install golangci-lint
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $GOBIN v1.24.0

# Install protobuf and testing/dev tools.
# Note: The version of golang/protobuf is partially tied to the version of grpc
# used by Boulder overall. Updating it may require updating the grpc version
# and vice versa.
GO111MODULE=on go get \
  bitbucket.org/liamstask/goose/cmd/goose \
  github.com/golang/mock/mockgen@v1.3.0 \
  github.com/golang/protobuf/proto@v1.4.0 \
  github.com/golang/protobuf/protoc-gen-go@v1.4.0 \
  github.com/mattn/goveralls@v0.0.3 \
  github.com/modocache/gover \
  golang.org/x/tools/cover \
  golang.org/x/tools/cmd/stringer

# Pebble's latest version is v2+, but it's not properly go mod compatible, so we
# fetch it in GOPATH mode.
go get github.com/letsencrypt/pebble/cmd/pebble-challtestsrv

go clean -cache
go clean -modcache

pip3 install -r /tmp/requirements.txt

# Install pkcs11-proxy. Checked out commit was master HEAD at time
# of writing
git clone https://github.com/SUNET/pkcs11-proxy /tmp/pkcs11-proxy && \
  cd /tmp/pkcs11-proxy && \
  git checkout 944684f78bca0c8da6cabe3fa273fed3db44a890 && \
  cmake . && make && make install && \
  cd - && rm -r /tmp/pkcs11-proxy

# Setup SoftHSM
echo "directories.tokendir = /var/lib/softhsm/tokens/" > /etc/softhsm/softhsm2.conf
mkdir -p /var/lib/softhsm/tokens
softhsm2-util --slot 0 --init-token --label intermediate --pin 5678 --so-pin 1234
softhsm2-util --slot 1 --init-token --label root --pin 5678 --so-pin 1234

gem install --no-document fpm

# We can't remove libseccomp-dev as it contains a shared object that is required
# for pkcs11-proxy to run properly
apt-get autoremove -y libssl-dev ruby-dev cmake
apt-get clean -y

rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*
