#!/bin/bash
#
# wait-for-services.sh - Wait for all Boulder test dependencies to be ready
#
# This script is run in a Kubernetes pod to verify that all required services
# are available before starting the test execution.
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

# Maximum time to wait for services (in seconds)
MAX_WAIT_TIME=300
START_TIME=$(date +%s)

function check_timeout() {
    local current_time=$(date +%s)
    local elapsed=$((current_time - START_TIME))

    if [ $elapsed -gt $MAX_WAIT_TIME ]; then
        log_error "Timeout reached (${MAX_WAIT_TIME}s). Services are not ready."
        exit 1
    fi
}

function wait_for_tcp_service() {
    local service_name=$1
    local port=$2
    local max_attempts=60
    local attempt=1

    log_info "Waiting for $service_name:$port..."

    while [ $attempt -le $max_attempts ]; do
        check_timeout

        if timeout 5 bash -c "</dev/tcp/$service_name/$port" >/dev/null 2>&1; then
            log_success "$service_name:$port is ready"
            return 0
        fi

        if [ $((attempt % 10)) -eq 0 ]; then
            log_info "Still waiting for $service_name:$port (attempt $attempt/$max_attempts)..."
        fi

        sleep 2
        attempt=$((attempt + 1))
    done

    log_error "Failed to connect to $service_name:$port after $max_attempts attempts"
    return 1
}

function wait_for_http_service() {
    local service_name=$1
    local port=$2
    local path=${3:-"/"}
    local max_attempts=60
    local attempt=1

    log_info "Waiting for HTTP service $service_name:$port$path..."

    while [ $attempt -le $max_attempts ]; do
        check_timeout

        if curl -f -s --max-time 5 "http://$service_name:$port$path" >/dev/null 2>&1; then
            log_success "$service_name:$port$path is ready"
            return 0
        fi

        if [ $((attempt % 10)) -eq 0 ]; then
            log_info "Still waiting for HTTP $service_name:$port$path (attempt $attempt/$max_attempts)..."
        fi

        sleep 2
        attempt=$((attempt + 1))
    done

    log_error "Failed to reach HTTP service $service_name:$port$path after $max_attempts attempts"
    return 1
}

function wait_for_mysql() {
    local host=$1
    local max_attempts=60
    local attempt=1

    log_info "Waiting for MySQL on $host..."

    while [ $attempt -le $max_attempts ]; do
        check_timeout

        if mysqladmin ping -h "$host" --silent 2>/dev/null; then
            log_success "MySQL on $host is ready"
            return 0
        fi

        if [ $((attempt % 10)) -eq 0 ]; then
            log_info "Still waiting for MySQL on $host (attempt $attempt/$max_attempts)..."
        fi

        sleep 2
        attempt=$((attempt + 1))
    done

    log_error "Failed to connect to MySQL on $host after $max_attempts attempts"
    return 1
}

function wait_for_redis() {
    local host=$1
    local port=${2:-6379}
    local max_attempts=60
    local attempt=1

    log_info "Waiting for Redis on $host:$port..."

    while [ $attempt -le $max_attempts ]; do
        check_timeout

        if redis-cli -h "$host" -p "$port" ping 2>/dev/null | grep -q "PONG"; then
            log_success "Redis on $host:$port is ready"
            return 0
        fi

        if [ $((attempt % 10)) -eq 0 ]; then
            log_info "Still waiting for Redis on $host:$port (attempt $attempt/$max_attempts)..."
        fi

        sleep 2
        attempt=$((attempt + 1))
    done

    log_error "Failed to connect to Redis on $host:$port after $max_attempts attempts"
    return 1
}

function check_dns_resolution() {
    local service_name=$1

    log_info "Checking DNS resolution for $service_name..."

    if nslookup "$service_name" >/dev/null 2>&1; then
        log_success "DNS resolution for $service_name is working"
        return 0
    else
        log_error "DNS resolution failed for $service_name"
        return 1
    fi
}

# Main service checking sequence
log_info "Starting service readiness checks..."
log_info "Maximum wait time: ${MAX_WAIT_TIME} seconds"

# Array of services to check with their types and parameters
declare -a SERVICES=(
    "dns:bmysql"
    "dns:bproxysql"
    "dns:bredis-1"
    "dns:bredis-2"
    "dns:bconsul"
    "dns:bjaeger"
    "dns:bpkimetal"
    "mysql:bmysql"
    "redis:bredis-1:6379"
    "redis:bredis-2:6379"
    "tcp:bproxysql:6033"
    "http:bconsul:8500:/v1/status/leader"
    "http:bjaeger:16686:/"
    "http:bpkimetal:8080:/"
)

# Check all services
failed_services=()

for service_config in "${SERVICES[@]}"; do
    IFS=':' read -ra PARTS <<< "$service_config"
    service_type="${PARTS[0]}"
    service_name="${PARTS[1]}"

    case "$service_type" in
        "dns")
            if ! check_dns_resolution "$service_name"; then
                failed_services+=("$service_name (DNS)")
            fi
            ;;
        "mysql")
            if ! wait_for_mysql "$service_name"; then
                failed_services+=("$service_name (MySQL)")
            fi
            ;;
        "redis")
            port="${PARTS[2]:-6379}"
            if ! wait_for_redis "$service_name" "$port"; then
                failed_services+=("$service_name:$port (Redis)")
            fi
            ;;
        "tcp")
            port="${PARTS[2]}"
            if ! wait_for_tcp_service "$service_name" "$port"; then
                failed_services+=("$service_name:$port (TCP)")
            fi
            ;;
        "http")
            port="${PARTS[2]}"
            path="${PARTS[3]:-/}"
            if ! wait_for_http_service "$service_name" "$port" "$path"; then
                failed_services+=("$service_name:$port$path (HTTP)")
            fi
            ;;
        *)
            log_error "Unknown service type: $service_type"
            failed_services+=("$service_config (unknown type)")
            ;;
    esac
done

# Report results
if [ ${#failed_services[@]} -eq 0 ]; then
    log_success "All services are ready! Total time: $(($(date +%s) - START_TIME))s"

    # Final connectivity test
    log_info "Performing final connectivity tests..."

    # Test database connectivity
    if mysql -h bmysql -u root -e "SELECT 1;" >/dev/null 2>&1; then
        log_success "Database connectivity test passed"
    else
        log_warning "Database connectivity test failed"
    fi

    # Test Redis connectivity
    if redis-cli -h bredis-1 SET test-key "test-value" >/dev/null 2>&1 && \
       redis-cli -h bredis-1 GET test-key >/dev/null 2>&1; then
        log_success "Redis connectivity test passed"
        redis-cli -h bredis-1 DEL test-key >/dev/null 2>&1
    else
        log_warning "Redis connectivity test failed"
    fi

    # Test Consul connectivity
    if curl -f -s http://bconsul:8500/v1/catalog/services >/dev/null 2>&1; then
        log_success "Consul connectivity test passed"
    else
        log_warning "Consul connectivity test failed"
    fi

    log_success "Service readiness check completed successfully!"
    exit 0
else
    log_error "The following services failed readiness checks:"
    for service in "${failed_services[@]}"; do
        log_error "  - $service"
    done

    log_error "Service readiness check failed!"
    exit 1
fi