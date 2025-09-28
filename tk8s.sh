#!/usr/bin/env bash
#
# Kubernetes-based test runner for Boulder - equivalent to t.sh but runs tests in K8s
# This script runs tests inside a persistent Boulder monolith pod using kubectl exec
#
# Output Modes:
#   --quiet: Suppress kubectl exec overhead for cleaner output (matches t.sh output)
#   --verbose: Show detailed command execution and environment variables
#   (default): Standard output with moderate verbosity
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
K8S_NAMESPACE="boulder-test"
BOULDER_IMAGE="letsencrypt/boulder-tools:${BOULDER_TOOLS_TAG:-latest}"
KUBECTL_CMD="kubectl"
K8S_CONTEXT=""
VERBOSE="false"
KIND_CLUSTER_NAME="${KIND_CLUSTER:-boulder-k8s}"
PROFILE="test"  # Default to test profile for backward compatibility
EPHEMERAL_MODE="false"  # Use Job-based ephemeral test execution
ENV_FILE=""  # Optional environment file to load
CONFIG_OVERRIDE_ENABLED="false"  # Whether to use dynamic configuration override
VALIDATE_CONFIG_ONLY="false"  # Only validate configuration and exit
RELOAD_CONFIG="false"  # Send configuration reload signal

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
export BOULDER_CONFIG_DIR
PRESERVE_DB="false"  # By default, reset database between test types (matches t.sh behavior)
NO_CLEANUP="false"  # Flag to disable cleanup for debugging
TEST_RUN_ID="$(date +%s)"  # Unique ID for this test run

# Output control variables
QUIET_MODE="false"  # Suppress kubectl overhead
SHOW_KUBECTL_CMDS="false"  # Show kubectl commands being executed

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
  print_heading "Cleaning up test resources..."

  local cleanup_failed=false

  # Clean up test Jobs with proper grace period
  if $KUBECTL_CMD get jobs -n "$K8S_NAMESPACE" -l app=boulder-test-runner 2>/dev/null | grep -q boulder-test; then
    print_heading "Cleaning up test Jobs..."
    if $KUBECTL_CMD delete jobs -n "$K8S_NAMESPACE" -l app=boulder-test-runner --grace-period=30; then
      print_success "Test Jobs cleaned up"
    else
      print_warning "Failed to clean up some Jobs"
      cleanup_failed=true
    fi
  fi

  # Clean up completed pods
  local completed_pods=$($KUBECTL_CMD get pods -n "$K8S_NAMESPACE" --field-selector=status.phase==Succeeded -o name 2>/dev/null)
  if [ -n "$completed_pods" ]; then
    print_heading "Cleaning up completed pods..."
    echo "$completed_pods" | xargs $KUBECTL_CMD delete -n "$K8S_NAMESPACE" --grace-period=0 || cleanup_failed=true
  fi

  # Clean up failed pods
  local failed_pods=$($KUBECTL_CMD get pods -n "$K8S_NAMESPACE" --field-selector=status.phase==Failed -o name 2>/dev/null)
  if [ -n "$failed_pods" ]; then
    print_heading "Cleaning up failed pods..."
    echo "$failed_pods" | xargs $KUBECTL_CMD delete -n "$K8S_NAMESPACE" --grace-period=0 || cleanup_failed=true
  fi

  # Clean up pods from this test run
  if [ -n "$TEST_RUN_ID" ]; then
    local test_run_pods=$($KUBECTL_CMD get pods -n "$K8S_NAMESPACE" -l "test-run=$TEST_RUN_ID" -o name 2>/dev/null)
    if [ -n "$test_run_pods" ]; then
      print_heading "Cleaning up pods from test run $TEST_RUN_ID..."
      echo "$test_run_pods" | xargs $KUBECTL_CMD delete -n "$K8S_NAMESPACE" --grace-period=30 || cleanup_failed=true
    fi
  fi

  # Clean up orphaned pods (older than 1 hour)
  cleanup_orphaned_resources

  # Clean up temporary ConfigMaps or Secrets
  $KUBECTL_CMD delete configmap -n "$K8S_NAMESPACE" -l temp=true 2>/dev/null || true
  $KUBECTL_CMD delete secret -n "$K8S_NAMESPACE" -l temp=true 2>/dev/null || true

  if [ "$cleanup_failed" = true ]; then
    print_warning "Some cleanup operations failed (non-critical)"
  else
    print_success "All test resources cleaned up"
  fi
}

function cleanup_test_artifacts() {
  local test_type=$1

  print_heading "Cleaning up after $test_type tests..."

  # Get Boulder pod name
  local pod_name
  pod_name=$(get_boulder_pod_name 2>/dev/null || echo "")

  if [ -n "$pod_name" ]; then
    # Clean up test-specific artifacts
    case "$test_type" in
      "integration")
        # Clean up integration test data
        $KUBECTL_CMD exec "$pod_name" -n "$K8S_NAMESPACE" -- bash -c "rm -rf /tmp/boulder-test-* 2>/dev/null" || true
        ;;
      "unit")
        # Clean up unit test caches if needed
        $KUBECTL_CMD exec "$pod_name" -n "$K8S_NAMESPACE" -- bash -c "go clean -testcache 2>/dev/null" || true
        ;;
      "generate")
        # Clean up generated files
        $KUBECTL_CMD exec "$pod_name" -n "$K8S_NAMESPACE" -- bash -c "git clean -fd 2>/dev/null" || true
        ;;
    esac
  fi

  # Clean up any completed pods from this test type
  $KUBECTL_CMD delete pods -n "$K8S_NAMESPACE" \
    -l "test-type=$test_type" \
    --field-selector=status.phase!=Running 2>/dev/null || true

  print_success "Cleanup for $test_type completed"
}

function cleanup_orphaned_resources() {
  print_heading "Checking for orphaned resources..."

  # Find and clean up Jobs older than 24 hours
  local old_jobs=$($KUBECTL_CMD get jobs -n "$K8S_NAMESPACE" -o json 2>/dev/null | \
    jq -r '.items[] | select(.status.completionTime != null) | select(.status.completionTime | fromdateiso8601 < (now - 86400)) | .metadata.name' 2>/dev/null || echo "")

  if [ -n "$old_jobs" ]; then
    print_warning "Cleaning up old jobs: $old_jobs"
    echo "$old_jobs" | xargs $KUBECTL_CMD delete job -n "$K8S_NAMESPACE" 2>/dev/null || true
  fi

  # Find and clean up old pods
  local old_pods=$($KUBECTL_CMD get pods -n "$K8S_NAMESPACE" -o json 2>/dev/null | \
    jq -r '.items[] | select(.status.startTime != null) | select(.status.startTime | fromdateiso8601 < (now - 3600)) | select(.status.phase != "Running") | .metadata.name' 2>/dev/null || echo "")

  if [ -n "$old_pods" ]; then
    print_warning "Cleaning up old pods: $old_pods"
    echo "$old_pods" | xargs $KUBECTL_CMD delete pod -n "$K8S_NAMESPACE" --grace-period=0 2>/dev/null || true
  fi
}

function print_outcome() {
  if [ "$STATUS" == "SUCCESS" ]; then
    print_success "Test suite completed successfully!"
  else
    print_error "Test suite failed during stage: $STAGE"
  fi
}

#
# Output filtering functions
#
function filter_kubectl_output() {
  # Remove kubectl exec overhead from output
  # Preserves test output while removing Kubernetes noise
  grep -v "^Unable to use a TTY" | \
  grep -v "^defaulted container" | \
  grep -v "^error: unable to upgrade connection" | \
  grep -v "^command terminated with exit code"
}

function run_kubectl_quietly() {
  # Run kubectl command with cleaner output
  local cmd=("$@")

  if [ "$SHOW_KUBECTL_CMDS" = "true" ]; then
    print_heading "kubectl ${cmd[*]}"
  fi

  if [ "$QUIET_MODE" = "true" ]; then
    "${cmd[@]}" 2>&1 | filter_kubectl_output
  else
    "${cmd[@]}"
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
# Configuration Management Functions
#
function load_env_file() {
  local env_file=$1

  if [ -f "$env_file" ]; then
    print_heading "Loading environment from $env_file"

    while IFS='=' read -r key value; do
      # Skip comments and empty lines
      [[ "$key" =~ ^#.*$ ]] && continue
      [[ -z "$key" ]] && continue

      # Remove leading/trailing whitespace
      key=$(echo "$key" | xargs)
      value=$(echo "$value" | xargs)

      # Remove quotes if present
      value="${value%\"}"
      value="${value#\"}"
      value="${value%\'}"
      value="${value#\'}"

      # Export the variable
      export "$key=$value"
      print_success "Set $key"
    done < "$env_file"
  else
    exit_msg "Environment file not found: $env_file"
  fi
}

function load_profile_config() {
  local profile=$1

  print_heading "Loading configuration profile: $profile"

  case "$profile" in
    "test")
      export BOULDER_CONFIG_DIR="test/config"
      export FAKE_DNS="64.112.117.122"
      export GORACE="halt_on_error=1"
      export GOCACHE="/boulder/.gocache/go-build"
      ;;
    "test-next")
      export BOULDER_CONFIG_DIR="test/config-next"
      export FAKE_DNS="64.112.117.122"
      export GORACE="halt_on_error=1"
      export GOCACHE="/boulder/.gocache/go-build-next"
      ;;
    "staging")
      export BOULDER_CONFIG_DIR="test/config-staging"
      export FAKE_DNS="10.0.0.1"
      export GORACE="halt_on_error=1"
      export GOCACHE="/boulder/.gocache/go-build-staging"
      ;;
    "dev")
      export BOULDER_CONFIG_DIR="test/config-dev"
      export FAKE_DNS="127.0.0.1"
      export GORACE="halt_on_error=1"
      export GOCACHE="/boulder/.gocache/go-build-dev"
      ;;
    *)
      print_warning "Unknown profile: $profile, using defaults"
      ;;
  esac

  print_success "Loaded profile: $profile"
}

function validate_config() {
  local config_dir=$1

  print_heading "Validating configuration in $config_dir..."

  local pod_name
  pod_name=$(get_boulder_pod_name)

  # Run config validation inside the pod - check if JSON files are valid
  if $KUBECTL_CMD exec "$pod_name" -n "$K8S_NAMESPACE" --env="BOULDER_CONFIG_DIR=$config_dir" -- bash -c "
    cd /boulder && \
    for f in $config_dir/*.json; do
      if [ -f \"\$f\" ]; then
        python3 -m json.tool \"\$f\" > /dev/null || exit 1
      fi
    done
  " 2>/dev/null; then
    print_success "Configuration is valid"
    return 0
  else
    print_error "Configuration validation failed"
    return 1
  fi
}

function reload_config() {
  local pod_name
  pod_name=$(get_boulder_pod_name)

  print_heading "Reloading configuration in Boulder pod..."

  # Send SIGHUP to reload config (if supported by Boulder services)
  # Note: Boulder services may need to be modified to support hot reload
  if $KUBECTL_CMD exec "$pod_name" -n "$K8S_NAMESPACE" -- bash -c "pkill -HUP -f 'start.py' 2>/dev/null || true"; then
    print_success "Configuration reload signal sent"
  else
    print_warning "Configuration reload may not be supported by all Boulder services"
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
    # Pass --config-next flag to k8s-up.sh if we're using config-next
    local k8s_up_args=("--namespace" "$K8S_NAMESPACE" "--cluster-name" "$KIND_CLUSTER_NAME")
    if [ "$BOULDER_CONFIG_DIR" = "test/config-next" ]; then
      k8s_up_args+=("--config-next")
    fi
    ./k8s/scripts/k8s-up.sh "${k8s_up_args[@]}"
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

function verify_certificates() {
  if [ "$QUIET_MODE" = "false" ]; then
    print_heading "Generating fresh test certificates..."
  fi

  local pod_name
  pod_name=$(get_boulder_pod_name)

  # Always regenerate certificates to ensure clean test environment
  # This matches the behavior of docker compose run --rm bsetup
  print_heading "Generating certificates (equivalent to bsetup)..."

  # Run certificate generation inside the Boulder pod with environment variables
  local cert_env_args=()
  [ -n "${BOULDER_CONFIG_DIR:-}" ] && cert_env_args+=("--env=BOULDER_CONFIG_DIR=$BOULDER_CONFIG_DIR")
  [ -n "${FAKE_DNS:-}" ] && cert_env_args+=("--env=FAKE_DNS=$FAKE_DNS")

  if $KUBECTL_CMD exec "$pod_name" -n "$K8S_NAMESPACE" "${cert_env_args[@]}" -- bash -c "cd /boulder && ./test/certs/generate.sh" 2>/dev/null; then
    print_success "Test certificates generated successfully"
    return 0
  else
    # Fallback: Generate using docker and copy
    if [ -f "test/certs/generate.sh" ]; then
      print_warning "Pod generation failed, trying Docker fallback..."
      docker run --rm \
        -v "$(pwd):/boulder" \
        -w /boulder \
        "$BOULDER_IMAGE" \
        ./test/certs/generate.sh

      # Copy certificates to the pod
      print_heading "Copying certificates to Boulder pod..."
      $KUBECTL_CMD cp test/certs "$pod_name":/boulder/test/ -n "$K8S_NAMESPACE"

      print_success "Certificates generated and copied to Boulder pod"
    else
      print_error "Certificate generation script not found at test/certs/generate.sh"
      return 1
    fi
  fi
}

function reset_database() {
  print_heading "Resetting database to clean state..."

  local pod_name
  pod_name=$(get_boulder_pod_name)

  # Set MYSQL_CONTAINER=1 to use the correct database connection settings
  # This matches what happens in the entrypoint.sh for Docker containers
  local db_env_args=()
  [ -n "${BOULDER_CONFIG_DIR:-}" ] && db_env_args+=("--env=BOULDER_CONFIG_DIR=$BOULDER_CONFIG_DIR")

  if $KUBECTL_CMD exec "$pod_name" -n "$K8S_NAMESPACE" "${db_env_args[@]}" -- bash -c "cd /boulder && MYSQL_CONTAINER=1 ./test/create_db.sh"; then
    print_success "Database reset completed"
    return 0
  else
    print_error "Failed to reset database"
    return 1
  fi
}

function clean_database_state() {
  if [ "$QUIET_MODE" = "false" ]; then
    print_heading "Cleaning database state..."
  fi

  local pod_name
  pod_name=$(get_boulder_pod_name)

  # Drop and recreate all test databases
  # This ensures a completely clean state, matching the behavior of t.sh
  # which gets a fresh container with fresh database each time
  local reset_script='
  mysql -u root -h bmysql -e "
    DROP DATABASE IF EXISTS boulder_sa_test;
    DROP DATABASE IF EXISTS boulder_sa_integration;
    DROP DATABASE IF EXISTS incidents_sa_test;
    DROP DATABASE IF EXISTS incidents_sa_integration;
    CREATE DATABASE IF NOT EXISTS boulder_sa_test;
    CREATE DATABASE IF NOT EXISTS boulder_sa_integration;
    CREATE DATABASE IF NOT EXISTS incidents_sa_test;
    CREATE DATABASE IF NOT EXISTS incidents_sa_integration;
  "
  '

  if $KUBECTL_CMD exec "$pod_name" -n "$K8S_NAMESPACE" -- bash -c "$reset_script"; then
    print_success "Databases dropped and recreated"

    # Now run migrations and setup via create_db.sh
    # MYSQL_CONTAINER=1 tells create_db.sh to use bmysql host
    local db_env_args=()
    [ -n "${BOULDER_CONFIG_DIR:-}" ] && db_env_args+=("--env=BOULDER_CONFIG_DIR=$BOULDER_CONFIG_DIR")

    if $KUBECTL_CMD exec "$pod_name" -n "$K8S_NAMESPACE" "${db_env_args[@]}" -- bash -c "cd /boulder && MYSQL_CONTAINER=1 ./test/create_db.sh"; then
      print_success "Database migrations completed"
      return 0
    else
      print_error "Failed to run database migrations"
      return 1
    fi
  else
    print_error "Failed to drop and recreate databases"
    return 1
  fi
}

function verify_services_ready() {
  print_heading "Verifying infrastructure services are ready..."

  local services=("bmysql" "bredis-1" "bredis-2" "bconsul" "bproxysql" "bpkimetal" "bjaeger")

  for service in "${services[@]}"; do
    if ! $KUBECTL_CMD get pods -n "$K8S_NAMESPACE" -l "app=$service" --no-headers 2>/dev/null | grep -q "Running"; then
      print_warning "Service $service is not running"
      return 1
    fi
  done

  print_success "All infrastructure services are ready"
  return 0
}

function run_k8s_lints() {
  local lint_failed=false

  print_heading "Running k8s-specific lints..."

  # Check for yamllint
  if command -v yamllint >/dev/null 2>&1; then
    print_heading "Linting YAML files in k8s/ directory..."

    # Find all YAML files in k8s/ directory
    local yaml_files
    yaml_files=$(find k8s -type f \( -name "*.yaml" -o -name "*.yml" \) 2>/dev/null)

    if [ -n "$yaml_files" ]; then
      # Run yamllint on all YAML files
      if echo "$yaml_files" | xargs yamllint -d relaxed; then
        print_success "YAML linting passed"
      else
        print_error "YAML linting failed"
        lint_failed=true
      fi
    else
      print_warning "No YAML files found in k8s/ directory"
    fi
  else
    print_warning "yamllint not found - skipping YAML linting (install with: brew install yamllint)"
  fi

  # Check for shellcheck
  if command -v shellcheck >/dev/null 2>&1; then
    print_heading "Linting shell scripts in k8s/ directory..."

    # Find all shell scripts in k8s/ directory (files with .sh extension)
    local shell_files
    shell_files=$(find k8s -type f -name "*.sh" 2>/dev/null)

    if [ -n "$shell_files" ]; then
      # Run shellcheck on all shell scripts
      if echo "$shell_files" | xargs shellcheck; then
        print_success "Shell script linting passed"
      else
        print_error "Shell script linting failed"
        lint_failed=true
      fi
    else
      print_warning "No shell scripts found in k8s/ directory"
    fi
  else
    print_warning "shellcheck not found - skipping shell script linting (install with: brew install shellcheck)"
  fi

  # Also lint tk8s.sh and tnk8s.sh themselves
  if command -v shellcheck >/dev/null 2>&1; then
    print_heading "Linting tk8s.sh and tnk8s.sh..."
    local test_scripts=()
    [ -f "tk8s.sh" ] && test_scripts+=("tk8s.sh")
    [ -f "tnk8s.sh" ] && test_scripts+=("tnk8s.sh")

    if [ ${#test_scripts[@]} -gt 0 ]; then
      if shellcheck "${test_scripts[@]}"; then
        print_success "Test script linting passed"
      else
        print_error "Test script linting failed"
        lint_failed=true
      fi
    fi
  fi

  if [ "$lint_failed" = true ]; then
    return 1
  fi
  return 0
}

function run_tests_in_boulder_pod() {
  local test_type=$1
  shift
  local test_args=("$@")

  if [ "$QUIET_MODE" = "false" ]; then
    print_heading "Running $test_type tests in Boulder pod..."
  fi

  # Get the Boulder pod name
  local pod_name
  pod_name=$(get_boulder_pod_name)

  if [ "$VERBOSE" = "true" ]; then
    print_heading "Using Boulder pod: $pod_name"
  fi

  # Build environment variable arguments for kubectl exec
  local env_args=()

  # Add Boulder configuration directory
  if [ -n "${BOULDER_CONFIG_DIR:-}" ]; then
    env_args+=("--env=BOULDER_CONFIG_DIR=$BOULDER_CONFIG_DIR")
  fi

  # Add FAKE_DNS if set
  if [ -n "${FAKE_DNS:-}" ]; then
    env_args+=("--env=FAKE_DNS=$FAKE_DNS")
  fi

  # Add GORACE if set
  if [ -n "${GORACE:-}" ]; then
    env_args+=("--env=GORACE=$GORACE")
  fi

  # Add GOCACHE if set
  if [ -n "${GOCACHE:-}" ]; then
    env_args+=("--env=GOCACHE=$GOCACHE")
  fi

  # Add any additional Boulder flags
  if [ -n "${BOULDER_EXTRA_FLAGS:-}" ]; then
    env_args+=("--env=BOULDER_EXTRA_FLAGS=$BOULDER_EXTRA_FLAGS")
  fi

  # Add any Go-specific environment variables
  if [ -n "${GODEBUG:-}" ]; then
    env_args+=("--env=GODEBUG=$GODEBUG")
  fi

  if [ -n "${GOMAXPROCS:-}" ]; then
    env_args+=("--env=GOMAXPROCS=$GOMAXPROCS")
  fi

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
    "generate")
      test_command+=("--generate")
      ;;
    "start")
      test_command+=("--start-py")
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

  # Only show command details in verbose mode
  if [ "$VERBOSE" = "true" ]; then
    print_heading "Running command: ${test_command[*]}"
    if [ ${#env_args[@]} -gt 0 ]; then
      print_heading "With environment overrides:"
      for env_arg in "${env_args[@]}"; do
        echo "    $env_arg"
      done
    fi
  fi

  # Add labels to track this test run
  $KUBECTL_CMD label pod "$pod_name" -n "$K8S_NAMESPACE" \
    "test-type=$test_type" \
    "test-run=$TEST_RUN_ID" \
    --overwrite 2>/dev/null || true

  # Execute test.sh inside the Boulder pod with cleaner output
  if [ "$QUIET_MODE" = "true" ]; then
    # Suppress kubectl overhead
    if $KUBECTL_CMD exec "$pod_name" -n "$K8S_NAMESPACE" "${env_args[@]}" -- "${test_command[@]}" 2>&1 | filter_kubectl_output; then
      [ "$QUIET_MODE" = "false" ] && print_success "$test_type tests completed successfully"
      return 0
    else
      print_error "$test_type tests failed"
      return 1
    fi
  else
    # Normal output mode
    if $KUBECTL_CMD exec "$pod_name" -n "$K8S_NAMESPACE" "${env_args[@]}" -- "${test_command[@]}"; then
      print_success "$test_type tests completed successfully"
      return 0
    else
      print_error "$test_type tests failed"
      return 1
    fi
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
    -l, --lints                           Run lints only (includes k8s YAML and shell script linting)
    -u, --unit                            Run unit tests only
    -v, --verbose                         Enable verbose output for tests
    -q, --quiet                           Suppress kubectl overhead in output
    --show-kubectl                        Show kubectl commands being executed
    -w, --unit-without-cache              Disable go test caching for unit tests
    -p <DIR>, --unit-test-package=<DIR>   Run unit tests for specific go package(s)
    -e, --enable-race-detection           Enable race detection for unit and integration tests
    -n, --config-next                     Use test/config-next instead of test/config
    -i, --integration                     Run integration tests only
    -s, --start-py                        Run start.py test (verify Boulder services start)
    -g, --generate                        Run code generation test
    -c, --coverage                        Enable coverage for tests
    -d <DIR>, --coverage-directory=<DIR>  Directory to store coverage files in
                                          Default: test/coverage/<timestamp>
    -f <REGEX>, --filter=<REGEX>          Run only those tests matching the regular expression
    -k <CONTEXT>, --kube-context=<CONTEXT> Use specific kubectl context
    -N <NAMESPACE>, --namespace=<NAMESPACE> Use specific Kubernetes namespace (default: boulder-test)
    --cluster-name=<NAME>                 Kind cluster name (default: boulder-k8s)
    --profile=<PROFILE>                   Configuration profile: test|test-next|staging|dev (default: test)
    --preserve-db                          Skip database reset between test types (for debugging)
    --no-cleanup                          Disable automatic cleanup (for debugging)
    --env-file=<FILE>                     Load environment variables from file
    --validate-config                     Validate configuration files only
    --reload-config                        Send configuration reload signal to Boulder services
    -h, --help                            Show this help message

Examples:
    $(basename "${0}")                    # Run all tests (test profile)
    $(basename "${0}") --unit             # Run unit tests only
    $(basename "${0}") --integration      # Run integration tests only
    $(basename "${0}") --lints            # Run lints only
    $(basename "${0}") -p ./va --unit     # Run unit tests for VA package only
    $(basename "${0}") --profile staging  # Run tests in staging profile
    $(basename "${0}") --quiet --unit     # Run unit tests with clean output (no kubectl overhead)
    $(basename "${0}") --env-file .env.testing --unit  # Use environment file
    FAKE_DNS=192.168.1.1 $(basename "${0}") --unit    # Override specific variable
    $(basename "${0}") --validate-config  # Validate configuration only

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
while getopts luvwecinhsgqd:p:f:k:N:-: OPT; do
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
    s | start-py )                   RUN+=("start") ;;
    g | generate )                   RUN+=("generate") ;;
    f | filter )                     check_arg; FILTER+=("${OPTARG}") ;;
    n | config-next )                BOULDER_CONFIG_DIR="test/config-next" ;;
    c | coverage )                   COVERAGE="true" ;;
    d | coverage-dir | coverage-directory ) check_arg; COVERAGE_DIR="${OPTARG}" ;;
    k | kube-context )               check_arg; K8S_CONTEXT="${OPTARG}" ;;
    N | namespace )                  check_arg; K8S_NAMESPACE="${OPTARG}" ;;
    cluster-name )                   check_arg; KIND_CLUSTER_NAME="${OPTARG}" ;;
    profile )                        check_arg; PROFILE="${OPTARG}" ;;
    preserve-db )                    PRESERVE_DB="true" ;;
    no-cleanup )                     NO_CLEANUP="true" ;;
    env-file )                       check_arg; ENV_FILE="${OPTARG}"; CONFIG_OVERRIDE_ENABLED="true" ;;
    validate-config )                VALIDATE_CONFIG_ONLY="true" ;;
    reload-config )                  RELOAD_CONFIG="true" ;;
    q | quiet )                      QUIET_MODE="true" ;;
    show-kubectl )                   SHOW_KUBECTL_CMDS="true" ;;
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

# Load profile configuration
load_profile_config "$PROFILE"

# If --config-next was specified, override with test-next profile
if [ "$BOULDER_CONFIG_DIR" == "test/config-next" ] && [ "$PROFILE" == "test" ]; then
  PROFILE="test-next"
  load_profile_config "$PROFILE"
fi

# Load environment file if specified (overrides profile settings)
if [ -n "$ENV_FILE" ]; then
  load_env_file "$ENV_FILE"
fi

# Adjust namespace based on profile if not explicitly set
if [ "$PROFILE" = "staging" ] && [ "$K8S_NAMESPACE" = "boulder-test" ]; then
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

# Setup signal handlers with cleanup control
if [ "$NO_CLEANUP" = "false" ]; then
  trap "cleanup_k8s_resources; print_outcome" EXIT
else
  trap "print_outcome" EXIT
  print_warning "Cleanup disabled (--no-cleanup flag set) - resources will persist"
fi

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
echo "    PRESERVE_DB:        $PRESERVE_DB"
echo "    NO_CLEANUP:         $NO_CLEANUP"
echo "    TEST_RUN_ID:        $TEST_RUN_ID"
if [ "$QUIET_MODE" = "true" ]; then
  echo "    OUTPUT_MODE:        Quiet (kubectl overhead suppressed)"
elif [ "$VERBOSE" = "true" ]; then
  echo "    OUTPUT_MODE:        Verbose"
else
  echo "    OUTPUT_MODE:        Normal"
fi
if [ -n "${UNIT_PACKAGES[@]+x}" ]; then
  echo "    UNIT_PACKAGES:      ${UNIT_PACKAGES[*]}"
fi
if [ -n "${FILTER[@]+x}" ]; then
  echo "    FILTER:             ${FILTER[*]}"
fi

# Check dependencies
check_dependencies

# Run k8s-specific lints separately (not part of Boulder lints)
# This is done first and doesn't require the cluster to be running
print_heading "Running K8s-specific lints (YAML and shell scripts)..."
if [ -x "k8s/scripts/k8s-lint.sh" ]; then
  if ./k8s/scripts/k8s-lint.sh; then
    print_success "K8s lints passed"
  else
    print_warning "K8s lints failed (non-blocking)"
  fi
else
  # Fallback to inline k8s linting if script doesn't exist
  if ! run_k8s_lints; then
    print_warning "K8s lints failed (non-blocking)"
  fi
fi

# Always need cluster for any tests that run in the pod
# (Boulder lints, unit tests, integration tests)
ensure_cluster_ready
wait_for_boulder_pod

# Verify test environment is ready
verify_services_ready

# Validate network isolation if the validation script exists
if [ -x "k8s/scripts/validate-network.sh" ]; then
  print_heading "Validating network isolation..."
  if ./k8s/scripts/validate-network.sh "$K8S_NAMESPACE"; then
    print_success "Network isolation validated"
  else
    print_warning "Network isolation validation failed (non-blocking)"
  fi
fi

# Handle validate-config-only mode
if [ "${VALIDATE_CONFIG_ONLY:-false}" == "true" ]; then
  validate_config "$BOULDER_CONFIG_DIR"
  exit $?
fi

# Handle reload-config mode
if [ "${RELOAD_CONFIG:-false}" == "true" ]; then
  reload_config
  exit $?
fi

# Run tests based on configuration
test_failed=false

for test_type in "${RUN[@]}"; do
  STAGE="$test_type"

  # Regenerate certificates before EACH test type (matches t.sh behavior)
  # Skip certificate generation for lints as they don't need certificates
  if [ "$test_type" != "lints" ]; then
    if [ "$QUIET_MODE" = "false" ]; then
      print_heading "Regenerating certificates for $test_type tests..."
    fi
    if ! verify_certificates; then
      print_error "Failed to generate certificates for $test_type"
      test_failed=true
      break
    fi
  else
    if [ "$QUIET_MODE" = "false" ]; then
      print_heading "Skipping certificate generation for lints (not needed)..."
    fi
  fi

  # Reset database before each test type (matches t.sh fresh container behavior)
  # Skip database reset for lints as they don't need database
  if [ "$test_type" != "lints" ]; then
    if [ "$PRESERVE_DB" = "false" ]; then
      if [ "$QUIET_MODE" = "false" ]; then
        print_heading "Resetting database for $test_type tests..."
      fi
      if ! clean_database_state; then
        print_error "Failed to reset database for $test_type"
        test_failed=true
        break
      fi
    else
      print_warning "Skipping database reset (--preserve-db flag set)"
    fi
  else
    if [ "$QUIET_MODE" = "false" ]; then
      print_heading "Skipping database reset for lints (not needed)..."
    fi
  fi

  # Prepare test arguments
  test_args=()

  if [ "$test_type" == "unit" ]; then
    test_args+=("${UNIT_FLAGS[@]}")
    # Add unit package arguments if specified
    if [ ${#UNIT_PACKAGES[@]} -gt 0 ]; then
      for pkg in "${UNIT_PACKAGES[@]}"; do
        test_args+=("--unit-test-package=$pkg")
      done
    fi
  elif [ "$test_type" == "integration" ]; then
    test_args+=("${INTEGRATION_FLAGS[@]}")
  fi

  if [ ${#FILTER[@]} -gt 0 ]; then
    test_args+=("-f" "${FILTER[@]}")
  fi

  if [ "$COVERAGE" == "true" ]; then
    test_args+=("-c" "-d" "$COVERAGE_DIR")
  fi

  # Run the test in the pod
  if ! run_tests_in_boulder_pod "$test_type" "${test_args[@]}"; then
    test_failed=true
    break
  fi

  # Clean up after each test type
  if [ "$NO_CLEANUP" = "false" ]; then
    cleanup_test_artifacts "$test_type"
  fi
done

# Set final status
if [ "$test_failed" == "false" ]; then
  STATUS="SUCCESS"
fi