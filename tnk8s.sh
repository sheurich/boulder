#!/usr/bin/env bash
#
# Kubernetes-based test runner for Boulder with config-next - equivalent to tn.sh but runs tests in K8s
#

set -o errexit

if type realpath >/dev/null 2>&1 ; then
  cd "$(realpath -- $(dirname -- "$0"))"
fi

# Run Kubernetes tests with config-next
export BOULDER_CONFIG_DIR=test/config-next
exec ./tk8s.sh "$@"