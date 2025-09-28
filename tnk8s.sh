#!/usr/bin/env bash
#
# Kubernetes-based test runner for Boulder with config-next - equivalent to tn.sh but runs tests in K8s
#

set -o errexit

if type realpath >/dev/null 2>&1 ; then
  cd "$(realpath -- $(dirname -- "$0"))"
fi

# Run Kubernetes tests with config-next by adding the --config-next flag
exec ./tk8s.sh --config-next "$@"