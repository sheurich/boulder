#!/usr/bin/env bash
#
# Boulder Kubernetes Cluster Setup Script (Phase 1)
# Creates and initializes a kind cluster for Boulder development and testing
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
CLUSTER_NAME="${KIND_CLUSTER:-boulder-k8s}"
NAMESPACE="boulder-test"
BOULDER_IMAGE="letsencrypt/boulder-tools:${BOULDER_TOOLS_TAG:-latest}"
KIND_CONFIG_PATH="../cluster/kind-config.yaml"
KUBECTL_CMD="kubectl"
VERBOSE="false"
WAIT_TIMEOUT="600s"
BOULDER_CONFIG_DIR="test/config"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

#
# Output Functions
#
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

function exit_error() {
  print_error "$*"
  exit 1
}

#
# Dependency Checks
#
function check_dependencies() {
  print_heading "Checking dependencies..."

  # Check required tools
  local missing_tools=()

  if ! command -v kind >/dev/null 2>&1; then
    missing_tools+=("kind")
  fi

  if ! command -v kubectl >/dev/null 2>&1; then
    missing_tools+=("kubectl")
  fi

  if ! command -v docker >/dev/null 2>&1; then
    missing_tools+=("docker")
  fi

  if [ ${#missing_tools[@]} -ne 0 ]; then
    exit_error "Missing required tools: ${missing_tools[*]}. Please install them first."
  fi

  # Check if Docker is running
  if ! docker info >/dev/null 2>&1; then
    exit_error "Docker is not running. Please start Docker first."
  fi

  # Check for kind config file
  if [ ! -f "$KIND_CONFIG_PATH" ]; then
    exit_error "Kind configuration file not found: $KIND_CONFIG_PATH"
  fi

  print_success "All dependencies are available"
}

#
# Cluster Management
#
function create_cluster() {
  print_heading "Creating kind cluster: $CLUSTER_NAME"

  # Check if cluster already exists
  if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
    print_warning "Cluster $CLUSTER_NAME already exists, deleting first..."
    kind delete cluster --name "$CLUSTER_NAME" || true
  fi

  # Create the cluster with our configuration
  print_heading "Creating new cluster with configuration..."
  kind create cluster --name "$CLUSTER_NAME" --config "$KIND_CONFIG_PATH" --wait=5m

  # Set kubectl context
  kubectl cluster-info --context "kind-${CLUSTER_NAME}"

  print_success "Cluster created successfully"
}

function load_boulder_image() {
  print_heading "Loading Boulder image into cluster..."

  # Check if image exists locally
  if ! docker images "$BOULDER_IMAGE" | grep -q boulder-tools; then
    print_warning "Boulder image not found locally. Building first..."
    print_heading "Building Boulder image..."
    (cd ../../ && docker compose build boulder) || exit_error "Failed to build Boulder image"
  fi

  # Load image into kind cluster
  kind load docker-image "$BOULDER_IMAGE" --name "$CLUSTER_NAME" || {
    exit_error "Failed to load Boulder image into cluster"
  }

  print_success "Boulder image loaded into cluster"
}

function create_namespace() {
  print_heading "Creating Boulder namespace: $NAMESPACE"

  # Create namespace if it doesn't exist
  if ! kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
    kubectl create namespace "$NAMESPACE"
  else
    print_warning "Namespace $NAMESPACE already exists"
  fi

  print_success "Namespace ready"
}

function apply_manifests() {
  print_heading "Applying Kubernetes manifests..."

  # Return to boulder root directory for relative paths
  cd ../../

  # Apply manifests in correct order
  local manifests=(
    # Namespace first
    "k8s/test/namespace.yaml"

    # Configuration
    "k8s/test/configmaps.yaml"

    # Infrastructure services
    "k8s/manifests/mariadb/statefulset.yaml"
    "k8s/manifests/mariadb/service.yaml"
    "k8s/manifests/redis/configmap.yaml"
    "k8s/manifests/redis/redis-1-statefulset.yaml"
    "k8s/manifests/redis/redis-2-statefulset.yaml"
    "k8s/manifests/redis/services.yaml"
    "k8s/manifests/consul/configmap.yaml"
    "k8s/manifests/consul/statefulset.yaml"
    "k8s/manifests/consul/service.yaml"
    "k8s/manifests/proxysql/configmap.yaml"
    "k8s/manifests/proxysql/deployment.yaml"
    "k8s/manifests/proxysql/service.yaml"
    "k8s/manifests/jaeger/deployment.yaml"
    "k8s/manifests/jaeger/service.yaml"
    "k8s/manifests/pkimetal/deployment.yaml"
    "k8s/manifests/pkimetal/service.yaml"

    # Test servers
    "k8s/test/test-servers.yaml"

    # Services overlay (if exists)
    "k8s/services/services.yaml"
    "k8s/test/services.yaml"

    # Network policies
    "k8s/test/network-policies.yaml"
  )

  for manifest in "${manifests[@]}"; do
    if [ -f "$manifest" ]; then
      print_heading "Applying $manifest..."
      kubectl apply -f "$manifest" || print_warning "Failed to apply $manifest, continuing..."
    else
      print_warning "Manifest not found: $manifest, skipping..."
    fi
  done

  print_success "Manifests applied"
}

function wait_for_services() {
  print_heading "Waiting for services to be ready..."

  # Complete list of all infrastructure services with their types
  local statefulsets=("bmysql" "bredis-1" "bredis-2" "bconsul")
  local deployments=("bjaeger" "bproxysql" "bpkimetal")

  # Wait for StatefulSets to be ready
  for service in "${statefulsets[@]}"; do
    echo "Waiting for StatefulSet $service..."
    kubectl wait --for=jsonpath='{.status.readyReplicas}'=1 statefulset/"$service" -n "$NAMESPACE" --timeout="$WAIT_TIMEOUT" || {
      print_warning "StatefulSet $service not ready within timeout, checking pod status..."
      kubectl get pods -n "$NAMESPACE" -l app="$service"
    }
  done

  # Wait for Deployments to be available
  for service in "${deployments[@]}"; do
    echo "Waiting for Deployment $service..."
    kubectl wait --for=condition=available deployment/"$service" -n "$NAMESPACE" --timeout="$WAIT_TIMEOUT" || {
      print_warning "Deployment $service not available within timeout, checking pod status..."
      kubectl get pods -n "$NAMESPACE" -l app="$service"
    }
  done

  # Wait for all pods to be ready
  echo "Waiting for all pods to be ready..."
  kubectl wait --for=condition=Ready pods --all -n "$NAMESPACE" --timeout=120s || {
    print_warning "Some pods not fully ready yet, continuing..."
    kubectl get pods -n "$NAMESPACE"
  }

  # Check test servers if they exist
  local test_servers=("challtestsrv" "loadbalancer" "ct-test" "aia-test-srv" "s3-test-srv")
  for server in "${test_servers[@]}"; do
    if kubectl get deployment "$server" -n "$NAMESPACE" >/dev/null 2>&1; then
      echo "Waiting for test server $server..."
      kubectl wait --for=condition=available deployment/"$server" -n "$NAMESPACE" --timeout=60s || {
        print_warning "Test server $server not ready, continuing..."
      }
    fi
  done

  print_success "All infrastructure services are ready"
}

function run_database_initialization() {
  print_heading "Initializing Boulder databases..."

  # Apply database initialization job
  export BOULDER_TOOLS_TAG=${BOULDER_TOOLS_TAG:-latest}
  export BOULDER_CONFIG_DIR=${BOULDER_CONFIG_DIR:-test/config}

  if command -v envsubst >/dev/null 2>&1; then
    envsubst < "k8s/test/database-init-job.yaml" | kubectl apply -f - -n "$NAMESPACE"
  else
    # Fallback: substitute manually
    sed "s/\${BOULDER_TOOLS_TAG}/${BOULDER_TOOLS_TAG}/g; s/\${BOULDER_CONFIG_DIR}/${BOULDER_CONFIG_DIR//\//\\/}/g" "k8s/test/database-init-job.yaml" | kubectl apply -f - -n "$NAMESPACE"
  fi

  # Wait for database initialization to complete
  print_heading "Waiting for database initialization..."
  if ! kubectl wait --for=condition=Complete job/database-init -n "$NAMESPACE" --timeout="$WAIT_TIMEOUT"; then
    print_error "Database initialization failed or timed out"

    # Get pod logs for debugging
    local pod_name=$(kubectl get pods -n "$NAMESPACE" -l "job-name=database-init" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")
    if [ -n "$pod_name" ]; then
      print_error "Database initialization pod logs:"
      kubectl logs "$pod_name" -n "$NAMESPACE" || true
    fi

    # Show job status for additional debugging
    echo "Job status:"
    kubectl describe job/database-init -n "$NAMESPACE" || true

    exit_error "Database initialization failed"
  fi

  print_success "Database initialization completed"
}

function start_boulder_monolith() {
  print_heading "Starting Boulder monolith deployment..."

  # Determine Boulder repository path (absolute path)
  # We're in k8s/scripts, so Boulder root is ../..
  BOULDER_REPO_PATH="$(cd ../.. && pwd)"
  print_heading "Using Boulder repository path: $BOULDER_REPO_PATH"

  # Apply Boulder monolith deployment
  export BOULDER_TOOLS_TAG=${BOULDER_TOOLS_TAG:-latest}
  export BOULDER_CONFIG_DIR=${BOULDER_CONFIG_DIR:-test/config}
  export BOULDER_REPO_PATH

  if command -v envsubst >/dev/null 2>&1; then
    envsubst < "k8s/test/boulder-monolith.yaml" | kubectl apply -f - -n "$NAMESPACE"
  else
    # Fallback: substitute manually - need to escape path slashes for sed
    BOULDER_REPO_PATH_ESCAPED="${BOULDER_REPO_PATH//\//\\/}"
    sed "s/\${BOULDER_TOOLS_TAG}/${BOULDER_TOOLS_TAG}/g; s/\${BOULDER_CONFIG_DIR}/${BOULDER_CONFIG_DIR//\//\\/}/g; s/\${BOULDER_REPO_PATH}/${BOULDER_REPO_PATH_ESCAPED}/g" "k8s/test/boulder-monolith.yaml" | kubectl apply -f - -n "$NAMESPACE"
  fi

  # Wait for Boulder deployment to be ready
  print_heading "Waiting for Boulder deployment..."
  kubectl wait --for=condition=Available deployment/boulder-monolith -n "$NAMESPACE" --timeout="$WAIT_TIMEOUT" || {
    print_warning "Boulder deployment not ready within timeout"
  }

  kubectl wait --for=condition=Ready pods -l app=boulder-monolith -n "$NAMESPACE" --timeout=120s || {
    print_warning "Boulder pods not fully ready yet"
  }

  print_success "Boulder monolith deployment started"
}

function show_cluster_info() {
  print_heading "Cluster Information"

  echo "Cluster Name: $CLUSTER_NAME"
  echo "Namespace: $NAMESPACE"
  echo "Boulder Image: $BOULDER_IMAGE"
  echo

  print_heading "Access Information"
  echo "Boulder WFE2 (ACME): http://localhost:4001/acme/directory"
  echo "Boulder SFE: http://localhost:4003"
  echo "Jaeger UI: http://localhost:16686"
  echo "Consul UI: http://localhost:8500"
  echo "ProxySQL Web UI: http://localhost:6080"
  echo

  print_heading "Useful Commands"
  echo "View cluster status: kubectl get all -n $NAMESPACE"
  echo "View logs: kubectl logs -f deployment/boulder-monolith -n $NAMESPACE"
  echo "Delete cluster: kind delete cluster --name $CLUSTER_NAME"
  echo "Scale down cluster: ./k8s-down.sh"
}

#
# Usage and CLI
#
USAGE="$(cat <<-EOM

Boulder Kubernetes Cluster Setup (Phase 1)

Usage:
  $(basename "${0}") [OPTION]...

Sets up a complete Boulder development environment in Kubernetes using kind.

Options:
    -c, --cluster-name=<NAME>         Use specific cluster name (default: $CLUSTER_NAME)
    -n, --namespace=<NAMESPACE>       Use specific namespace (default: $NAMESPACE)
    -i, --image=<IMAGE>               Use specific Boulder image (default: $BOULDER_IMAGE)
    -t, --timeout=<DURATION>          Wait timeout for services (default: $WAIT_TIMEOUT)
    --config-next                     Use test/config-next instead of test/config
    -v, --verbose                     Enable verbose output
    -h, --help                        Show this help message

Examples:
    $(basename "${0}")                # Create cluster with defaults
    $(basename "${0}") --config-next  # Use next-generation config
    $(basename "${0}") -v             # Verbose output

Requirements:
    - kind (Kubernetes in Docker)
    - kubectl
    - docker
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
while getopts c:n:i:t:vh-: OPT; do
  if [ "$OPT" = - ]; then     # long option: reformulate OPT and OPTARG
    OPT="${OPTARG%%=*}"       # extract long option name
    OPTARG="${OPTARG#$OPT}"   # extract long option argument (may be empty)
    OPTARG="${OPTARG#=}"      # if long option argument, remove assigning `=`
  fi
  case "$OPT" in
    c | cluster-name )           CLUSTER_NAME="${OPTARG}" ;;
    n | namespace )              NAMESPACE="${OPTARG}" ;;
    i | image )                  BOULDER_IMAGE="${OPTARG}" ;;
    t | timeout )                WAIT_TIMEOUT="${OPTARG}" ;;
    config-next )                BOULDER_CONFIG_DIR="test/config-next" ;;
    v | verbose )                VERBOSE="true" ;;
    h | help )                   print_usage_exit ;;
    ??* )                        exit_error "Illegal option --$OPT" ;;
    ? )                          exit 2 ;;
  esac
done
shift $((OPTIND-1))

#
# Main Execution
#
print_heading "Boulder Kubernetes Cluster Setup (Phase 1)"
print_heading "Configuration:"

echo "    CLUSTER_NAME:       $CLUSTER_NAME"
echo "    NAMESPACE:          $NAMESPACE"
echo "    BOULDER_IMAGE:      $BOULDER_IMAGE"
echo "    KIND_CONFIG_PATH:   $KIND_CONFIG_PATH"
echo "    BOULDER_CONFIG_DIR: $BOULDER_CONFIG_DIR"
echo "    WAIT_TIMEOUT:       $WAIT_TIMEOUT"

# Execute setup steps
check_dependencies
create_cluster
load_boulder_image
create_namespace
apply_manifests
wait_for_services
run_database_initialization
start_boulder_monolith
show_cluster_info

print_success "Boulder Kubernetes cluster setup completed successfully!"
print_success "Your Boulder development environment is ready to use."