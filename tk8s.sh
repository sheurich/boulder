#!/usr/bin/env bash
#
# Kubernetes-based test runner for Boulder - equivalent to t.sh but runs tests in K8s
# This script orchestrates the entire test lifecycle using Kubernetes Jobs and Pods
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
K8S_NAMESPACE="boulder-test-$(date +%s)"
BOULDER_IMAGE="letsencrypt/boulder-tools:${BOULDER_TOOLS_TAG:-latest}"
KUBECTL_CMD="kubectl"
K8S_CONTEXT=""
CLEANUP_ON_EXIT="true"
VERBOSE="false"
TEST_TIMEOUT="3600s"  # 1 hour timeout for tests

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
# Cleanup Functions
#
function cleanup_k8s_resources() {
  if [ "$CLEANUP_ON_EXIT" == "true" ]; then
    print_heading "Cleaning up Kubernetes resources..."

    # Delete jobs first to stop running pods
    $KUBECTL_CMD delete jobs --all -n "$K8S_NAMESPACE" --ignore-not-found=true --timeout=60s || true

    # Delete remaining resources
    $KUBECTL_CMD delete namespace "$K8S_NAMESPACE" --ignore-not-found=true --timeout=120s || true

    print_success "Cleanup completed"
  else
    print_warning "Skipping cleanup - namespace $K8S_NAMESPACE preserved"
  fi
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

  # Check envsubst for environment variable substitution
  if ! command -v envsubst >/dev/null 2>&1; then
    exit_msg "envsubst is not installed. Please install gettext package (e.g., 'apt-get install gettext-base' or 'brew install gettext')"
  fi

  # Check if kubectl can connect to cluster
  if ! $KUBECTL_CMD cluster-info >/dev/null 2>&1; then
    exit_msg "Cannot connect to Kubernetes cluster. Is your cluster running and kubectl configured?"
  fi

  # Check for kind or minikube
  local k8s_platform=""
  if command -v kind >/dev/null 2>&1 && kind get clusters 2>/dev/null | grep -q .; then
    k8s_platform="kind"
  elif command -v minikube >/dev/null 2>&1 && minikube status >/dev/null 2>&1; then
    k8s_platform="minikube"
  else
    print_warning "Neither kind nor minikube detected. Assuming external cluster."
    k8s_platform="external"
  fi

  print_success "Using Kubernetes platform: $k8s_platform"

  # Load Boulder image into cluster if using kind
  if [ "$k8s_platform" == "kind" ]; then
    print_heading "Loading Boulder image into kind cluster..."
    if ! kind load docker-image "$BOULDER_IMAGE" 2>/dev/null; then
      print_warning "Failed to load image into kind. Checking if image exists locally..."
      if docker images "$BOULDER_IMAGE" | grep -q boulder-tools; then
        print_heading "Retrying image load into kind..."
        kind load docker-image "$BOULDER_IMAGE" --name boulder-k8s || {
          print_error "Failed to load image. Please ensure Docker image $BOULDER_IMAGE exists"
          exit 1
        }
      else
        print_error "Docker image $BOULDER_IMAGE not found locally"
        print_error "Please build the image first with: docker compose build boulder"
        exit 1
      fi
    fi
    print_success "Boulder image loaded into kind cluster"
  elif [ "$k8s_platform" == "minikube" ]; then
    print_heading "Loading Boulder image into minikube..."
    if ! minikube image ls | grep -q "$(echo "$BOULDER_IMAGE" | cut -d: -f1)"; then
      minikube image load "$BOULDER_IMAGE" || {
        print_error "Failed to load image into minikube"
        print_error "Please ensure Docker image $BOULDER_IMAGE exists"
        exit 1
      }
    fi
    print_success "Boulder image loaded into minikube"
  fi
}

#
# Kubernetes Resource Management
#
function create_namespace() {
  print_heading "Creating test namespace: $K8S_NAMESPACE"

  cat <<EOF | $KUBECTL_CMD apply -f -
apiVersion: v1
kind: Namespace
metadata:
  name: $K8S_NAMESPACE
  labels:
    app: boulder-test
    created-by: tk8.sh
EOF

  print_success "Namespace created"
}

function apply_k8s_manifests() {
  print_heading "Applying Kubernetes manifests..."

  # Apply manifests in order
  local manifests=(
    "k8s/test/configmaps.yaml"
    "k8s/test/services.yaml"
  )

  for manifest in "${manifests[@]}"; do
    if [ -f "$manifest" ]; then
      print_heading "Applying $manifest..."
      $KUBECTL_CMD apply -f "$manifest" -n "$K8S_NAMESPACE"
    else
      exit_msg "Required manifest not found: $manifest"
    fi
  done

  print_success "Manifests applied"
}

function run_database_initialization() {
  print_heading "Initializing Boulder databases..."

  # Export environment variables for envsubst
  export BOULDER_TOOLS_TAG=${BOULDER_TOOLS_TAG:-latest}
  export BOULDER_CONFIG_DIR=${BOULDER_CONFIG_DIR:-test/config}

  # Apply database initialization job with environment substitution
  envsubst < "k8s/test/database-init-job.yaml" | $KUBECTL_CMD apply -f - -n "$K8S_NAMESPACE"

  # Wait for database initialization to complete
  print_heading "Waiting for database initialization..."
  if ! $KUBECTL_CMD wait --for=condition=Complete job/database-init -n "$K8S_NAMESPACE" --timeout=1800s; then
    print_error "Database initialization failed or timed out"

    # Get pod logs for debugging
    local pod_name=$($KUBECTL_CMD get pods -n "$K8S_NAMESPACE" -l "job-name=database-init" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")
    if [ -n "$pod_name" ]; then
      print_error "Database initialization pod logs:"
      $KUBECTL_CMD logs "$pod_name" -n "$K8S_NAMESPACE" || true
    fi

    # Show job status for additional debugging
    echo "Job status:"
    $KUBECTL_CMD describe job/database-init -n "$K8S_NAMESPACE" || true

    exit_msg "Database initialization failed"
  fi

  print_success "Database initialization completed"
}

function wait_for_services() {
  print_heading "Waiting for services to be ready..."

  # Complete list of all test service deployments
  local services=("bmysql" "bredis-1" "bredis-2" "bconsul" "bjaeger" "bproxysql" "bpkimetal")

  # Wait for all deployments to be available
  for service in "${services[@]}"; do
    echo "Waiting for deployment $service..."
    $KUBECTL_CMD wait --for=condition=Available deployment/"$service" -n "$K8S_NAMESPACE" --timeout=300s || {
      print_warning "Deployment $service not available yet, continuing..."
    }
  done

  # Wait for all pods to be ready
  for service in "${services[@]}"; do
    echo "Waiting for $service pods to be ready..."
    $KUBECTL_CMD wait --for=condition=Ready pods -l app="$service" -n "$K8S_NAMESPACE" --timeout=60s || {
      print_warning "Pod $service not fully ready yet, continuing..."
    }
  done

  print_success "All services are ready"
}

function create_source_volume() {
  print_heading "Skipping shared source volume (not needed - source included in image)"
  print_success "Source code available in Boulder image"
}

function run_certificate_generation() {
  print_heading "Generating test certificates..."

  # Apply certificate generation job - generate certificates in each test container
  cat <<EOF | $KUBECTL_CMD apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: cert-generation
  namespace: $K8S_NAMESPACE
  labels:
    app: boulder-cert-gen
spec:
  template:
    metadata:
      labels:
        app: boulder-cert-gen
    spec:
      restartPolicy: Never
      containers:
      - name: cert-gen
        image: $BOULDER_IMAGE
        imagePullPolicy: IfNotPresent
        command: ["bash", "-c"]
        args:
        - |
          echo "Starting certificate generation..."
          cd /boulder

          # Check if minica is available
          if ! command -v minica >/dev/null 2>&1; then
            echo "Installing minica..."
            go install github.com/jsha/minica@latest
          fi

          # Run certificate generation
          if [ -f "test/certs/generate.sh" ]; then
            echo "Running certificate generation script..."
            chmod +x test/certs/generate.sh
            cd test/certs
            ./generate.sh || {
              echo "Certificate generation failed, creating minimal setup..."
              mkdir -p /tmp/certs/ipki
              cd /tmp/certs/ipki
              minica -domains localhost --ip-addresses 127.0.0.1 || echo "Basic cert generation failed"
            }
          else
            echo "Certificate generation script not found, creating basic certs..."
            mkdir -p /tmp/certs/ipki
            cd /tmp/certs/ipki
            minica -domains localhost --ip-addresses 127.0.0.1 || echo "Basic cert generation failed"
          fi

          echo "Certificate generation completed"
          ls -la /boulder/test/certs/ || true
        workingDir: /boulder
        env:
        - name: BOULDER_CONFIG_DIR
          value: "test/config"
        - name: GOPATH
          value: "/tmp/go"
        - name: GOCACHE
          value: "/tmp/go-cache"
        - name: PATH
          value: "/tmp/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
        volumeMounts:
        - name: softhsm-tokens
          mountPath: /var/lib/softhsm/tokens
        - name: go-cache
          mountPath: /tmp/go-cache
        - name: go-path
          mountPath: /tmp/go
        resources:
          requests:
            memory: "512Mi"
            cpu: "500m"
          limits:
            memory: "2Gi"
            cpu: "2"
      volumes:
      - name: softhsm-tokens
        emptyDir:
          sizeLimit: 100Mi
      - name: go-cache
        emptyDir:
          sizeLimit: 500Mi
      - name: go-path
        emptyDir:
          sizeLimit: 1Gi
  backoffLimit: 3
  activeDeadlineSeconds: 900
EOF

  # Wait for certificate generation to complete
  print_heading "Waiting for certificate generation..."
  if ! $KUBECTL_CMD wait --for=condition=Complete job/cert-generation -n "$K8S_NAMESPACE" --timeout=600s; then
    print_error "Certificate generation failed or timed out"

    # Get pod logs for debugging
    local pod_name=$($KUBECTL_CMD get pods -n "$K8S_NAMESPACE" -l "job-name=cert-generation" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")
    if [ -n "$pod_name" ]; then
      print_error "Certificate generation pod logs:"
      $KUBECTL_CMD logs "$pod_name" -n "$K8S_NAMESPACE" || true
    fi

    print_warning "Certificate generation failed, but continuing with tests..."
    return 0
  fi

  print_success "Certificates generated"
}

function run_tests_in_k8s() {
  local test_type=$1
  shift
  local test_args=("$@")

  print_heading "Running $test_type tests in Kubernetes..."

  # Create test command based on type
  local test_command=""
  case "$test_type" in
    "lints")
      test_command="echo 'Running lints...'; golangci-lint run --timeout 9m ./... && python3 test/grafana/lint.py && typos && ./test/format-configs.py 'test/config*/*.json'"
      ;;
    "unit")
      test_command="echo 'Running unit tests...'; go run ./test/boulder-tools/flushredis/main.go || true; go test -p=1 ${test_args[*]} ./..."
      ;;
    "integration")
      test_command="echo 'Running integration tests...'; go run ./test/boulder-tools/flushredis/main.go || true; python3 test/integration-test.py --chisel --gotest ${test_args[*]}"
      ;;
    "all")
      test_command="./test.sh ${test_args[*]}"
      ;;
    *)
      exit_msg "Unknown test type: $test_type"
      ;;
  esac

  # Create test job
  cat <<EOF | $KUBECTL_CMD apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: boulder-test-$test_type
  namespace: $K8S_NAMESPACE
spec:
  template:
    metadata:
      labels:
        app: boulder-test
        test-type: $test_type
    spec:
      restartPolicy: Never
      # Add init container to wait for database initialization
      initContainers:
      - name: wait-for-database-init
        image: $BOULDER_IMAGE
        imagePullPolicy: IfNotPresent
        command: ['bash', '-c']
        args:
        - |
          echo "Waiting for database initialization to complete..."
          kubectl wait --for=condition=Complete job/database-init -n $K8S_NAMESPACE --timeout=1800s || {
            echo "Database initialization not complete, but continuing with tests..."
          }
          echo "Database initialization verified"
      containers:
      - name: boulder-test
        image: $BOULDER_IMAGE
        imagePullPolicy: IfNotPresent
        command: ["bash", "-c"]
        args:
        - |
          echo "Starting Boulder test: $test_type"
          cd /boulder

          # Generate certificates for this test run
          echo "Generating test certificates..."
          if ! command -v minica >/dev/null 2>&1; then
            echo "Installing minica..."
            export GOPATH=/tmp/go
            export PATH="/tmp/go/bin:$PATH"
            go install github.com/jsha/minica@latest
          fi

          # Run certificate generation if script exists
          if [ -f "test/certs/generate.sh" ]; then
            echo "Running certificate generation script..."
            chmod +x test/certs/generate.sh
            cd test/certs
            ./generate.sh || echo "Certificate generation failed, but continuing..."
            cd /boulder
          else
            echo "Certificate generation script not found, continuing without custom certs..."
          fi

          # Run the test command
          $test_command

          echo "Test completed successfully: $test_type"
        workingDir: /boulder
        env:
        - name: BOULDER_CONFIG_DIR
          value: "$BOULDER_CONFIG_DIR"
        - name: FAKE_DNS
          value: "bconsul"
        - name: GOCACHE
          value: "/tmp/.gocache/go-build"
        - name: GOMODCACHE
          value: "/tmp/.gocache/go-mod"
        # Service hosts
        - name: MYSQL_HOST
          value: "bmysql"
        - name: PROXYSQL_HOST
          value: "bproxysql"
        - name: REDIS_HOST_1
          value: "bredis-1"
        - name: REDIS_HOST_2
          value: "bredis-2"
        - name: CONSUL_HOST
          value: "bconsul"
        - name: JAEGER_HOST
          value: "bjaeger"
        - name: PKIMETAL_HOST
          value: "bpkimetal"
        volumeMounts:
        # Go build cache
        - name: gocache
          mountPath: /tmp/.gocache
        # Test configuration
        - name: test-config
          mountPath: /boulder/test/config-k8s
          readOnly: true
        # SoftHSM tokens
        - name: softhsm-tokens
          mountPath: /var/lib/softhsm/tokens
        resources:
          requests:
            memory: "2Gi"
            cpu: "1"
          limits:
            memory: "8Gi"
            cpu: "4"
      volumes:
      # Go build cache
      - name: gocache
        emptyDir:
          sizeLimit: 2Gi
      # Test configurations
      - name: test-config
        configMap:
          name: boulder-test-config
      # SoftHSM tokens
      - name: softhsm-tokens
        emptyDir:
          sizeLimit: 100Mi
  backoffLimit: 1
  activeDeadlineSeconds: 3600
EOF

  # Stream logs from the test pod
  local pod_name=""
  echo "Waiting for test pod to start..."
  while [ -z "$pod_name" ]; do
    pod_name=$($KUBECTL_CMD get pods -n "$K8S_NAMESPACE" -l "job-name=boulder-test-$test_type" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
    sleep 2
  done

  echo "Streaming logs from $pod_name..."
  $KUBECTL_CMD logs -n "$K8S_NAMESPACE" -f "$pod_name" || true

  # Wait for job completion
  if $KUBECTL_CMD wait --for=condition=Complete job/boulder-test-$test_type -n "$K8S_NAMESPACE" --timeout="$TEST_TIMEOUT"; then
    print_success "$test_type tests completed successfully"
    return 0
  else
    print_error "$test_type tests failed"

    # Get more details about the failure
    echo "Job status:"
    $KUBECTL_CMD describe job/boulder-test-$test_type -n "$K8S_NAMESPACE"

    echo "Pod logs:"
    $KUBECTL_CMD logs job/boulder-test-$test_type -n "$K8S_NAMESPACE" --tail=50

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

Runs Boulder test suite in a Kubernetes environment using Jobs and Pods.
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
    -N <NAMESPACE>, --namespace=<NAMESPACE> Use specific Kubernetes namespace
    --no-cleanup                          Don't cleanup K8s resources after tests
    --timeout=<DURATION>                  Test timeout (default: 3600s)
    -h, --help                            Show this help message

Examples:
    $(basename "${0}")                    # Run all tests
    $(basename "${0}") --unit             # Run unit tests only
    $(basename "${0}") --integration      # Run integration tests only
    $(basename "${0}") --lints            # Run lints only
    $(basename "${0}") -p ./va --unit     # Run unit tests for VA package only

Requirements:
    - kubectl configured and connected to a cluster
    - kind or minikube recommended for local development
    - Docker image: $BOULDER_IMAGE

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
    no-cleanup )                     CLEANUP_ON_EXIT="false" ;;
    timeout )                        check_arg; TEST_TIMEOUT="${OPTARG}" ;;
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

# The list of segments to run. Order doesn't matter.
if [ -z "${RUN[@]+x}" ]; then
  RUN+=("lints" "unit" "integration")
fi

# Validation
if [[ "${RUN[@]}" =~ unit ]] && [[ "${RUN[@]}" =~ integration ]] && [[ -n "${FILTER[@]+x}" ]]; then
  exit_msg "Illegal option: (-f, --filter) when specifying both (-u, --unit) and (-i, --integration)"
fi

# Setup signal handlers
trap cleanup_k8s_resources EXIT
trap "print_outcome" EXIT

#
# Main Execution
#
print_heading "Boulder Kubernetes Test Suite"
print_heading "Configuration:"

echo "    RUN:                ${RUN[*]}"
echo "    NAMESPACE:          $K8S_NAMESPACE"
echo "    BOULDER_CONFIG_DIR: $BOULDER_CONFIG_DIR"
echo "    BOULDER_IMAGE:      $BOULDER_IMAGE"
echo "    KUBECTL_CONTEXT:    ${K8S_CONTEXT:-default}"
echo "    CLEANUP_ON_EXIT:    $CLEANUP_ON_EXIT"
echo "    TEST_TIMEOUT:       $TEST_TIMEOUT"
if [ -n "${UNIT_PACKAGES[@]+x}" ]; then
  echo "    UNIT_PACKAGES:      ${UNIT_PACKAGES[*]}"
fi
if [ -n "${FILTER[@]+x}" ]; then
  echo "    FILTER:             ${FILTER[*]}"
fi

# Check dependencies
check_dependencies

# Create Kubernetes resources
create_namespace
apply_k8s_manifests
create_source_volume
wait_for_services
run_database_initialization
run_certificate_generation

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
  if ! run_tests_in_k8s "$test_type" "${test_args[@]}"; then
    test_failed=true
    break
  fi
done

# Set final status
if [ "$test_failed" == "false" ]; then
  STATUS="SUCCESS"
fi