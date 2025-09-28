#!/usr/bin/env bash
#
# Boulder Kubernetes Cluster Teardown Script
# Destroys the kind cluster and cleans up all related resources
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
FORCE="false"
VERBOSE="false"

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

  if [ ${#missing_tools[@]} -ne 0 ]; then
    exit_error "Missing required tools: ${missing_tools[*]}. Please install them first."
  fi

  print_success "All dependencies are available"
}

#
# Confirmation Functions
#
function confirm_deletion() {
  if [ "$FORCE" == "true" ]; then
    return 0
  fi

  print_warning "This will destroy the kind cluster '$CLUSTER_NAME' and all its data."
  print_warning "This action cannot be undone."
  echo

  read -p "Are you sure you want to continue? (y/N): " -r
  echo

  if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    print_warning "Operation cancelled by user"
    exit 0
  fi
}

#
# Cleanup Functions
#
function cleanup_kubernetes_resources() {
  print_heading "Cleaning up Kubernetes resources..."

  # Check if cluster exists
  if ! kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
    print_warning "Cluster $CLUSTER_NAME does not exist, skipping K8s cleanup"
    return 0
  fi

  # Set kubectl context
  local context="kind-${CLUSTER_NAME}"
  if kubectl config get-contexts | grep -q "$context"; then
    kubectl config use-context "$context" >/dev/null 2>&1 || true

    # Clean up namespace resources if namespace exists
    if kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
      print_heading "Cleaning up namespace resources..."

      # Delete jobs (with grace period to allow proper cleanup)
      kubectl delete jobs --all -n "$NAMESPACE" --grace-period=30 --timeout=60s || true

      # Delete deployments
      kubectl delete deployments --all -n "$NAMESPACE" --grace-period=30 --timeout=60s || true

      # Delete services
      kubectl delete services --all -n "$NAMESPACE" --timeout=30s || true

      # Delete configmaps
      kubectl delete configmaps --all -n "$NAMESPACE" --timeout=30s || true

      # Delete secrets
      kubectl delete secrets --all -n "$NAMESPACE" --timeout=30s || true

      # Delete persistent volume claims
      kubectl delete pvc --all -n "$NAMESPACE" --timeout=60s || true

      # Finally delete the namespace
      kubectl delete namespace "$NAMESPACE" --timeout=120s || true

      print_success "Namespace resources cleaned up"
    else
      print_warning "Namespace $NAMESPACE does not exist, skipping resource cleanup"
    fi
  else
    print_warning "Context $context not found, skipping resource cleanup"
  fi
}

function delete_kind_cluster() {
  print_heading "Deleting kind cluster: $CLUSTER_NAME"

  # Check if cluster exists
  if ! kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
    print_warning "Cluster $CLUSTER_NAME does not exist"
    return 0
  fi

  # Delete the cluster
  if kind delete cluster --name "$CLUSTER_NAME"; then
    print_success "Kind cluster deleted successfully"
  else
    print_error "Failed to delete kind cluster"
    return 1
  fi
}

function cleanup_docker_resources() {
  print_heading "Cleaning up Docker resources..."

  # Remove any dangling Boulder-related containers
  local containers
  containers=$(docker ps -a --filter "label=io.x-k8s.kind.cluster=$CLUSTER_NAME" -q 2>/dev/null || true)

  if [ -n "$containers" ]; then
    print_heading "Removing kind-related containers..."
    docker rm -f $containers || true
  fi

  # Clean up any leftover networks
  local networks
  networks=$(docker network ls --filter "label=io.x-k8s.kind.cluster=$CLUSTER_NAME" -q 2>/dev/null || true)

  if [ -n "$networks" ]; then
    print_heading "Removing kind-related networks..."
    docker network rm $networks 2>/dev/null || true
  fi

  # Prune unused volumes (be careful with this)
  if [ "$FORCE" == "true" ]; then
    print_heading "Pruning unused Docker volumes..."
    docker volume prune -f || true
  fi

  print_success "Docker resources cleaned up"
}

function cleanup_kubectl_context() {
  print_heading "Cleaning up kubectl context..."

  local context="kind-${CLUSTER_NAME}"

  # Remove kubectl context
  if kubectl config get-contexts | grep -q "$context"; then
    kubectl config delete-context "$context" 2>/dev/null || true
    print_success "kubectl context removed: $context"
  else
    print_warning "kubectl context not found: $context"
  fi

  # Remove kubectl cluster entry
  if kubectl config get-clusters | grep -q "kind-${CLUSTER_NAME}"; then
    kubectl config delete-cluster "kind-${CLUSTER_NAME}" 2>/dev/null || true
    print_success "kubectl cluster entry removed: kind-${CLUSTER_NAME}"
  else
    print_warning "kubectl cluster entry not found: kind-${CLUSTER_NAME}"
  fi

  # Remove kubectl user entry
  if kubectl config get-users | grep -q "kind-${CLUSTER_NAME}"; then
    kubectl config delete-user "kind-${CLUSTER_NAME}" 2>/dev/null || true
    print_success "kubectl user entry removed: kind-${CLUSTER_NAME}"
  else
    print_warning "kubectl user entry not found: kind-${CLUSTER_NAME}"
  fi
}

function show_cleanup_summary() {
  print_heading "Cleanup Summary"

  echo "Cluster Name: $CLUSTER_NAME"
  echo "Namespace: $NAMESPACE"
  echo

  print_success "Boulder Kubernetes environment has been completely removed"

  if [ "$FORCE" != "true" ]; then
    echo
    print_heading "To recreate the environment, run:"
    echo "  ./k8s-up.sh"
  fi
}

#
# Usage and CLI
#
USAGE="$(cat <<-EOM

Boulder Kubernetes Cluster Teardown

Usage:
  $(basename "${0}") [OPTION]...

Destroys the Boulder kind cluster and cleans up all related resources.

Options:
    -c, --cluster-name=<NAME>         Use specific cluster name (default: $CLUSTER_NAME)
    -n, --namespace=<NAMESPACE>       Use specific namespace (default: $NAMESPACE)
    -f, --force                       Force deletion without confirmation
    -v, --verbose                     Enable verbose output
    -h, --help                        Show this help message

Examples:
    $(basename "${0}")                # Destroy cluster with confirmation
    $(basename "${0}") -f             # Force destroy without confirmation
    $(basename "${0}") -c my-cluster  # Destroy specific cluster

Safety:
    - This script will ask for confirmation unless --force is used
    - All data in the cluster will be permanently lost
    - Docker containers, networks, and (optionally) volumes will be cleaned up

EOM
)"

function print_usage_exit() {
  echo "$USAGE"
  exit 0
}

#
# Main CLI Parser
#
while getopts c:n:fvh-: OPT; do
  if [ "$OPT" = - ]; then     # long option: reformulate OPT and OPTARG
    OPT="${OPTARG%%=*}"       # extract long option name
    OPTARG="${OPTARG#$OPT}"   # extract long option argument (may be empty)
    OPTARG="${OPTARG#=}"      # if long option argument, remove assigning `=`
  fi
  case "$OPT" in
    c | cluster-name )           CLUSTER_NAME="${OPTARG}" ;;
    n | namespace )              NAMESPACE="${OPTARG}" ;;
    f | force )                  FORCE="true" ;;
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
print_heading "Boulder Kubernetes Cluster Teardown"
print_heading "Configuration:"

echo "    CLUSTER_NAME: $CLUSTER_NAME"
echo "    NAMESPACE:    $NAMESPACE"
echo "    FORCE:        $FORCE"

# Check dependencies
check_dependencies

# Confirm deletion unless forced
confirm_deletion

# Execute cleanup steps
cleanup_kubernetes_resources
delete_kind_cluster
cleanup_docker_resources
cleanup_kubectl_context
show_cleanup_summary

print_success "Boulder Kubernetes cluster teardown completed successfully!"