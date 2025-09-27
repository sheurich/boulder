#!/bin/bash
#
# cleanup.sh - Clean up Boulder test resources in Kubernetes
#
# This script provides comprehensive cleanup of test resources including:
# - Jobs and their associated pods
# - Services and deployments
# - ConfigMaps and PersistentVolumeClaims
# - Namespaces
#

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

function log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

function log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

function log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

function log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Script configuration
NAMESPACE=""
KUBECTL_CMD="kubectl"
DRY_RUN=false
FORCE=false
WAIT_TIMEOUT="120s"
VERBOSE=false

function print_usage() {
    cat <<EOF
Boulder Kubernetes Test Cleanup Script

Usage: $(basename "$0") [OPTIONS]

Options:
    -n, --namespace NAMESPACE    Kubernetes namespace to cleanup (required)
    -k, --kubectl-context CTX    Use specific kubectl context
    -d, --dry-run               Show what would be deleted without actually deleting
    -f, --force                 Skip confirmation prompts
    -w, --wait-timeout TIMEOUT  Timeout for waiting on deletions (default: 120s)
    -v, --verbose               Enable verbose output
    -h, --help                  Show this help message

Examples:
    $(basename "$0") -n boulder-test-12345
    $(basename "$0") -n boulder-test-12345 --dry-run
    $(basename "$0") -n boulder-test-12345 --force

EOF
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -n|--namespace)
            NAMESPACE="$2"
            shift 2
            ;;
        -k|--kubectl-context)
            KUBECTL_CMD="kubectl --context=$2"
            shift 2
            ;;
        -d|--dry-run)
            DRY_RUN=true
            shift
            ;;
        -f|--force)
            FORCE=true
            shift
            ;;
        -w|--wait-timeout)
            WAIT_TIMEOUT="$2"
            shift 2
            ;;
        -v|--verbose)
            VERBOSE=true
            shift
            ;;
        -h|--help)
            print_usage
            exit 0
            ;;
        *)
            log_error "Unknown option: $1"
            print_usage
            exit 1
            ;;
    esac
done

# Validate required parameters
if [ -z "$NAMESPACE" ]; then
    log_error "Namespace is required. Use -n or --namespace option."
    print_usage
    exit 1
fi

# Check if kubectl is available and can connect
if ! command -v kubectl >/dev/null 2>&1; then
    log_error "kubectl is not installed or not in PATH"
    exit 1
fi

if ! $KUBECTL_CMD cluster-info >/dev/null 2>&1; then
    log_error "Cannot connect to Kubernetes cluster. Is your cluster running and kubectl configured?"
    exit 1
fi

# Check if namespace exists
if ! $KUBECTL_CMD get namespace "$NAMESPACE" >/dev/null 2>&1; then
    log_warning "Namespace '$NAMESPACE' does not exist or is not accessible"
    exit 0
fi

function execute_command() {
    local cmd="$1"
    local description="$2"

    if [ "$VERBOSE" = true ]; then
        log_info "Executing: $cmd"
    fi

    if [ "$DRY_RUN" = true ]; then
        log_info "[DRY RUN] Would execute: $description"
        return 0
    fi

    if eval "$cmd"; then
        log_success "$description"
        return 0
    else
        local exit_code=$?
        log_warning "$description failed (exit code: $exit_code)"
        return $exit_code
    fi
}

function cleanup_jobs() {
    log_info "Cleaning up Jobs..."

    local jobs
    jobs=$($KUBECTL_CMD get jobs -n "$NAMESPACE" -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")

    if [ -z "$jobs" ]; then
        log_info "No Jobs found in namespace $NAMESPACE"
        return 0
    fi

    for job in $jobs; do
        execute_command \
            "$KUBECTL_CMD delete job '$job' -n '$NAMESPACE' --ignore-not-found=true --timeout=$WAIT_TIMEOUT" \
            "Deleted Job: $job"
    done
}

function cleanup_deployments() {
    log_info "Cleaning up Deployments..."

    local deployments
    deployments=$($KUBECTL_CMD get deployments -n "$NAMESPACE" -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")

    if [ -z "$deployments" ]; then
        log_info "No Deployments found in namespace $NAMESPACE"
        return 0
    fi

    for deployment in $deployments; do
        execute_command \
            "$KUBECTL_CMD delete deployment '$deployment' -n '$NAMESPACE' --ignore-not-found=true --timeout=$WAIT_TIMEOUT" \
            "Deleted Deployment: $deployment"
    done
}

function cleanup_services() {
    log_info "Cleaning up Services..."

    local services
    services=$($KUBECTL_CMD get services -n "$NAMESPACE" -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")

    if [ -z "$services" ]; then
        log_info "No Services found in namespace $NAMESPACE"
        return 0
    fi

    for service in $services; do
        execute_command \
            "$KUBECTL_CMD delete service '$service' -n '$NAMESPACE' --ignore-not-found=true --timeout=$WAIT_TIMEOUT" \
            "Deleted Service: $service"
    done
}

function cleanup_configmaps() {
    log_info "Cleaning up ConfigMaps..."

    local configmaps
    configmaps=$($KUBECTL_CMD get configmaps -n "$NAMESPACE" -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")

    if [ -z "$configmaps" ]; then
        log_info "No ConfigMaps found in namespace $NAMESPACE"
        return 0
    fi

    for configmap in $configmaps; do
        # Skip system ConfigMaps
        if [[ "$configmap" =~ ^(kube-root-ca.crt|default-token-.*)$ ]]; then
            if [ "$VERBOSE" = true ]; then
                log_info "Skipping system ConfigMap: $configmap"
            fi
            continue
        fi

        execute_command \
            "$KUBECTL_CMD delete configmap '$configmap' -n '$NAMESPACE' --ignore-not-found=true --timeout=$WAIT_TIMEOUT" \
            "Deleted ConfigMap: $configmap"
    done
}

function cleanup_secrets() {
    log_info "Cleaning up Secrets..."

    local secrets
    secrets=$($KUBECTL_CMD get secrets -n "$NAMESPACE" -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")

    if [ -z "$secrets" ]; then
        log_info "No Secrets found in namespace $NAMESPACE"
        return 0
    fi

    for secret in $secrets; do
        # Skip system secrets
        if [[ "$secret" =~ ^(default-token-.*)$ ]]; then
            if [ "$VERBOSE" = true ]; then
                log_info "Skipping system Secret: $secret"
            fi
            continue
        fi

        execute_command \
            "$KUBECTL_CMD delete secret '$secret' -n '$NAMESPACE' --ignore-not-found=true --timeout=$WAIT_TIMEOUT" \
            "Deleted Secret: $secret"
    done
}

function cleanup_pvcs() {
    log_info "Cleaning up PersistentVolumeClaims..."

    local pvcs
    pvcs=$($KUBECTL_CMD get pvc -n "$NAMESPACE" -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")

    if [ -z "$pvcs" ]; then
        log_info "No PersistentVolumeClaims found in namespace $NAMESPACE"
        return 0
    fi

    for pvc in $pvcs; do
        execute_command \
            "$KUBECTL_CMD delete pvc '$pvc' -n '$NAMESPACE' --ignore-not-found=true --timeout=$WAIT_TIMEOUT" \
            "Deleted PersistentVolumeClaim: $pvc"
    done
}

function cleanup_pods() {
    log_info "Cleaning up orphaned Pods..."

    # Wait a moment for Job deletion to clean up most pods
    if [ "$DRY_RUN" = false ]; then
        sleep 5
    fi

    local pods
    pods=$($KUBECTL_CMD get pods -n "$NAMESPACE" -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")

    if [ -z "$pods" ]; then
        log_info "No Pods found in namespace $NAMESPACE"
        return 0
    fi

    for pod in $pods; do
        execute_command \
            "$KUBECTL_CMD delete pod '$pod' -n '$NAMESPACE' --ignore-not-found=true --timeout=$WAIT_TIMEOUT --force" \
            "Deleted Pod: $pod"
    done
}

function cleanup_namespace() {
    log_info "Cleaning up namespace '$NAMESPACE'..."

    execute_command \
        "$KUBECTL_CMD delete namespace '$NAMESPACE' --ignore-not-found=true --timeout=$WAIT_TIMEOUT" \
        "Deleted namespace: $NAMESPACE"
}

function wait_for_namespace_deletion() {
    if [ "$DRY_RUN" = true ]; then
        return 0
    fi

    log_info "Waiting for namespace deletion to complete..."

    local max_wait=60
    local count=0

    while $KUBECTL_CMD get namespace "$NAMESPACE" >/dev/null 2>&1; do
        if [ $count -ge $max_wait ]; then
            log_warning "Namespace deletion is taking longer than expected..."
            break
        fi

        sleep 2
        count=$((count + 1))

        if [ $((count % 15)) -eq 0 ]; then
            log_info "Still waiting for namespace deletion... (${count}s)"
        fi
    done

    if ! $KUBECTL_CMD get namespace "$NAMESPACE" >/dev/null 2>&1; then
        log_success "Namespace '$NAMESPACE' has been completely removed"
    else
        log_warning "Namespace '$NAMESPACE' may still be terminating"
    fi
}

function print_summary() {
    if [ "$DRY_RUN" = true ]; then
        log_info "Dry run completed. No resources were actually deleted."
        return
    fi

    log_info "Cleanup summary for namespace '$NAMESPACE':"
    log_info "  - Jobs and associated Pods"
    log_info "  - Deployments and associated ReplicaSets/Pods"
    log_info "  - Services"
    log_info "  - ConfigMaps (excluding system ConfigMaps)"
    log_info "  - Secrets (excluding system Secrets)"
    log_info "  - PersistentVolumeClaims"
    log_info "  - Namespace itself"
}

# Main execution
log_info "Boulder Kubernetes Test Cleanup"
log_info "Namespace: $NAMESPACE"
log_info "Dry run: $DRY_RUN"
log_info "Force: $FORCE"

# Show what resources exist before cleanup
if [ "$VERBOSE" = true ]; then
    log_info "Current resources in namespace '$NAMESPACE':"
    $KUBECTL_CMD get all,configmaps,secrets,pvc -n "$NAMESPACE" 2>/dev/null || log_info "No resources found or namespace not accessible"
fi

# Confirmation prompt (unless force or dry-run)
if [ "$FORCE" = false ] && [ "$DRY_RUN" = false ]; then
    echo
    read -p "Are you sure you want to delete ALL resources in namespace '$NAMESPACE'? (y/N): " -r
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        log_info "Cleanup cancelled by user"
        exit 0
    fi
fi

echo
log_info "Starting cleanup process..."

# Execute cleanup in order
cleanup_jobs
cleanup_deployments
cleanup_services
cleanup_configmaps
cleanup_secrets
cleanup_pvcs
cleanup_pods
cleanup_namespace

# Wait for complete deletion if not dry run
if [ "$DRY_RUN" = false ]; then
    wait_for_namespace_deletion
fi

print_summary
log_success "Cleanup completed!"