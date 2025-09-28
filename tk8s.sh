#!/usr/bin/env bash
#
# Kubernetes-based test runner for Boulder - equivalent to t.sh but runs tests in K8s
# This script runs tests inside a persistent Boulder monolith pod using kubectl exec
#

set -o errexit
set -o nounset
set -o pipefail

if type realpath >/dev/null 2>&1 ; then
  cd "$(realpath -- $(dirname -- "$0"))"
fi

#
# Defaults and Global Variables
#
K8S_NAMESPACE="boulder"
BOULDER_IMAGE="letsencrypt/boulder-tools:${BOULDER_TOOLS_TAG:-latest}"
KUBECTL_CMD="kubectl"
K8S_CONTEXT=""
VERBOSE="false"
KIND_CLUSTER_NAME="${KIND_CLUSTER:-boulder-k8s}"
PROFILE="test"  # Default to test profile for backward compatibility

# Test configuration
RACE="false"
STAGE="starting"
STATUS="FAILURE"
RUN=()
UNIT_PACKAGES=()
UNIT_FLAGS=()
INTEGRATION_FLAGS=()
FILTER=()
COVERAGE="false"
COVERAGE_DIR="test/coverage/$(date +%Y-%m-%d_%H-%M-%S)"
BOULDER_CONFIG_DIR="test/config"

#
# Color output functions
#
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

function print_colored() {
  local color=$1
  local message=$2
  echo -e "${color}${message}${NC}"
}

function print_heading() {
  echo
  print_colored "$BLUE" "▶ $1"
}

function print_success() {
  print_colored "$GREEN" "✓ $1"
}

function print_error() {
  print_colored "$RED" "✗ $1"
}

function print_warning() {
  print_colored "$YELLOW" "⚠ $1"
}

#
# Utility Functions
#
function cleanup_k8s_resources() {
  # No cleanup needed - we use persistent infrastructure
  print_success "Using persistent infrastructure - no cleanup needed"
}

function print_outcome() {
  if [ "$STATUS" == "SUCCESS" ]; then
    print_success "Test suite completed successfully!"
  else
    print_error "Test suite failed during stage: $STAGE"
  fi
}

function exit_msg() {
  print_error "$*"
  exit 2
}

function check_arg() {
  if [ -z "$OPTARG" ]; then
    exit_msg "No arg for --$OPT option, use: -h for help"
  fi
}

#
# Dependency Checks
#
function check_dependencies() {
  print_heading "Checking dependencies..."

  # Check kubectl
  if ! command -v kubectl >/dev/null 2>&1; then
    exit_msg "kubectl is not installed or not in PATH"
  fi

  # Check kind
  if ! command -v kind >/dev/null 2>&1; then
    exit_msg "kind is not installed or not in PATH"
  fi

  print_success "All dependencies found"
}

#
# Cluster Setup
#
function ensure_cluster_ready() {
  print_heading "Ensuring Kubernetes cluster and Boulder deployment are ready..."

  # Set kubectl context for kind cluster
  K8S_CONTEXT="kind-${KIND_CLUSTER_NAME}"
  KUBECTL_CMD="kubectl --context=$K8S_CONTEXT"

  # Check if k8s-up.sh script exists and run it
  if [ -f "k8s/scripts/k8s-up.sh" ]; then
    print_heading "Running k8s-up.sh to ensure cluster is ready..."
    ./k8s/scripts/k8s-up.sh --namespace "$K8S_NAMESPACE" --cluster-name "$KIND_CLUSTER_NAME"
  else
    # Fallback: basic cluster check
    if ! $KUBECTL_CMD cluster-info >/dev/null 2>&1; then
      exit_msg "Cannot connect to Kubernetes cluster. Please ensure kind cluster is running."
    fi

    # Check if Boulder monolith deployment exists and is ready
    if ! $KUBECTL_CMD get deployment boulder-monolith -n "$K8S_NAMESPACE" >/dev/null 2>&1; then
      exit_msg "Boulder monolith deployment not found. Please run k8s/scripts/k8s-up.sh first."
    fi
  fi

  print_success "Cluster and Boulder deployment are ready"
}

function wait_for_boulder_pod() {
  print_heading "Waiting for Boulder monolith pod to be ready..."

  # Wait for deployment to be available
  $KUBECTL_CMD wait --for=condition=Available deployment/boulder-monolith -n "$K8S_NAMESPACE" --timeout=300s || {
    exit_msg "Boulder monolith deployment not available"
  }

  # Wait for pod to be ready
  $KUBECTL_CMD wait --for=condition=Ready pods -l app=boulder-monolith -n "$K8S_NAMESPACE" --timeout=300s || {
    exit_msg "Boulder monolith pod not ready"
  }

  print_success "Boulder monolith pod is ready"
}

function get_boulder_pod_name() {
  $KUBECTL_CMD get pods -l app=boulder-monolith -n "$K8S_NAMESPACE" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || {
    exit_msg "Could not find Boulder monolith pod"
  }
}

function run_tests_in_boulder_pod() {
  local test_type=$1
  shift
  local test_args=("$@")

  print_heading "Running $test_type tests in Boulder pod..."

  # Get the Boulder pod name
  local pod_name
  pod_name=$(get_boulder_pod_name)

  print_heading "Using Boulder pod: $pod_name"

  # Create test command based on type and arguments
  local test_command=("./test.sh")

  # Add test type flags
  case "$test_type" in
    "lints")
      test_command+=("--lints")
      ;;
    "unit")
      test_command+=("--unit")
      ;;
    "integration")
      test_command+=("--integration")
      ;;
    "all")
      # No specific flag needed - test.sh runs all by default
      ;;
    *)
      exit_msg "Unknown test type: $test_type"
      ;;
  esac

  # Add test arguments
  test_command+=("${test_args[@]}")

  print_heading "Running command: ${test_command[*]}"

  # Execute test.sh inside the Boulder pod
  if $KUBECTL_CMD exec "$pod_name" -n "$K8S_NAMESPACE" -- "${test_command[@]}"; then
    print_success "$test_type tests completed successfully"
    return 0
  else
    print_error "$test_type tests failed"
    return 1
  fi
}

#
# CLI Usage and Argument Parsing
#
USAGE="$(cat <<-EOM

Boulder Kubernetes Test Suite CLI

Usage:
  $(basename "${0}") [OPTION]...

Runs Boulder test suite inside a persistent Boulder monolith pod using kubectl exec.
With no options passed, runs standard battery of tests (lint, unit, and integration).

Options:
    -l, --lints                           Run lints only
    -u, --unit                            Run unit tests only
    -v, --verbose                         Enable verbose output for tests
    -w, --unit-without-cache              Disable go test caching for unit tests
    -p <DIR>, --unit-test-package=<DIR>   Run unit tests for specific go package(s)
    -e, --enable-race-detection           Enable race detection for unit and integration tests
    -n, --config-next                     Use test/config-next instead of test/config
    -i, --integration                     Run integration tests only
    -c, --coverage                        Enable coverage for tests
    -d <DIR>, --coverage-directory=<DIR>  Directory to store coverage files in
                                          Default: test/coverage/<timestamp>
    -f <REGEX>, --filter=<REGEX>          Run only those tests matching the regular expression
    -k <CONTEXT>, --kube-context=<CONTEXT> Use specific kubectl context
    -N <NAMESPACE>, --namespace=<NAMESPACE> Use specific Kubernetes namespace (default: boulder)
    --cluster-name=<NAME>                 Kind cluster name (default: boulder-k8s)
    --profile=<PROFILE>                   Configuration profile: test|staging|dev (default: test)
    -h, --help                            Show this help message

Examples:
    $(basename "${0}")                    # Run all tests (test profile)
    $(basename "${0}") --unit             # Run unit tests only
    $(basename "${0}") --integration      # Run integration tests only
    $(basename "${0}") --lints            # Run lints only
    $(basename "${0}") -p ./va --unit     # Run unit tests for VA package only
    $(basename "${0}") --profile staging  # Run tests in staging profile

Requirements:
    - kubectl configured and connected to a kind cluster
    - kind cluster with Boulder monolith deployment running
    - Run k8s/scripts/k8s-up.sh first to set up the cluster

EOM
)"

function print_usage_exit() {
  echo "$USAGE"
  exit 0
}

#
# Main CLI Parser
#
while getopts luvwecinhd:p:f:k:N:-: OPT; do
  if [ "$OPT" = - ]; then     # long option: reformulate OPT and OPTARG
    OPT="${OPTARG%%=*}"       # extract long option name
    OPTARG="${OPTARG#$OPT}"   # extract long option argument (may be empty)
    OPTARG="${OPTARG#=}"      # if long option argument, remove assigning `=`
  fi
  case "$OPT" in
    l | lints )                      RUN+=("lints") ;;
    u | unit )                       RUN+=("unit") ;;
    v | verbose )                    VERBOSE="true"; UNIT_FLAGS+=("-v"); INTEGRATION_FLAGS+=("-v") ;;
    w | unit-without-cache )         UNIT_FLAGS+=("-count=1") ;;
    p | unit-test-package )          check_arg; UNIT_PACKAGES+=("${OPTARG}") ;;
    e | enable-race-detection )      RACE="true"; UNIT_FLAGS+=("-race") ;;
    i | integration )                RUN+=("integration") ;;
    f | filter )                     check_arg; FILTER+=("${OPTARG}") ;;
    n | config-next )                BOULDER_CONFIG_DIR="test/config-next" ;;
    c | coverage )                   COVERAGE="true" ;;
    d | coverage-dir )               check_arg; COVERAGE_DIR="${OPTARG}" ;;
    k | kube-context )               check_arg; K8S_CONTEXT="${OPTARG}" ;;
    N | namespace )                  check_arg; K8S_NAMESPACE="${OPTARG}" ;;
    cluster-name )                   check_arg; KIND_CLUSTER_NAME="${OPTARG}" ;;
    profile )                        check_arg; PROFILE="${OPTARG}" ;;
    h | help )                       print_usage_exit ;;
    ??* )                            exit_msg "Illegal option --$OPT" ;;
    ? )                              exit 2 ;;
  esac
done
shift $((OPTIND-1))

# Set kubectl context if specified
if [ -n "$K8S_CONTEXT" ]; then
  KUBECTL_CMD="kubectl --context=$K8S_CONTEXT"
fi

# Adjust namespace based on profile if not explicitly set
if [ "$PROFILE" = "staging" ] && [ "$K8S_NAMESPACE" = "boulder" ]; then
  K8S_NAMESPACE="boulder-staging"
fi

# The list of segments to run. Order doesn't matter.
if [ -z "${RUN[@]+x}" ]; then
  RUN+=("lints" "unit" "integration")
fi

# Validation
if [[ "${RUN[@]}" =~ unit ]] && [[ "${RUN[@]}" =~ integration ]] && [[ -n "${FILTER[@]+x}" ]]; then
  exit_msg "Illegal option: (-f, --filter) when specifying both (-u, --unit) and (-i, --integration)"
fi

# Setup signal handlers
trap "print_outcome" EXIT

#
# Main Execution
#
print_heading "Boulder Kubernetes Test Suite"
print_heading "Configuration:"

echo "    RUN:                ${RUN[*]}"
echo "    PROFILE:            $PROFILE"
echo "    NAMESPACE:          $K8S_NAMESPACE"
echo "    BOULDER_CONFIG_DIR: $BOULDER_CONFIG_DIR"
echo "    BOULDER_IMAGE:      $BOULDER_IMAGE"
echo "    KUBECTL_CONTEXT:    ${K8S_CONTEXT:-default}"
echo "    KIND_CLUSTER_NAME:  $KIND_CLUSTER_NAME"
if [ -n "${UNIT_PACKAGES[@]+x}" ]; then
  echo "    UNIT_PACKAGES:      ${UNIT_PACKAGES[*]}"
fi
if [ -n "${FILTER[@]+x}" ]; then
  echo "    FILTER:             ${FILTER[*]}"
fi

# Check dependencies and ensure cluster is ready
check_dependencies
ensure_cluster_ready
wait_for_boulder_pod

# Run tests based on configuration
test_failed=false

for test_type in "${RUN[@]}"; do
  STAGE="$test_type"

  # Prepare test arguments
  test_args=()

  if [ "$test_type" == "unit" ]; then
    test_args+=("${UNIT_FLAGS[@]}")
    if [ -n "${UNIT_PACKAGES[@]+x}" ]; then
      for pkg in "${UNIT_PACKAGES[@]}"; do
        test_args+=("-p" "$pkg")
      done
    fi
  elif [ "$test_type" == "integration" ]; then
    test_args+=("${INTEGRATION_FLAGS[@]}")
  fi

  if [ -n "${FILTER[@]+x}" ]; then
    test_args+=("-f" "${FILTER[@]}")
  fi

  if [ "$COVERAGE" == "true" ]; then
    test_args+=("-c" "-d" "$COVERAGE_DIR")
  fi

  # Run the test
  if ! run_tests_in_boulder_pod "$test_type" "${test_args[@]}"; then
    test_failed=true
    break
  fi
done

# Set final status
if [ "$test_failed" == "false" ]; then
  STATUS="SUCCESS"
fi