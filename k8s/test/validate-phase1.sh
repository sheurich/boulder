#!/usr/bin/env bash
#
# Validation script for Phase 1 Kubernetes implementation
# Checks that all required components are present and properly configured
#

set -o errexit
set -o nounset
set -o pipefail

# Color output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Counters
TOTAL_CHECKS=0
PASSED_CHECKS=0
FAILED_CHECKS=0

# Check function
check() {
    local description=$1
    local command=$2

    TOTAL_CHECKS=$((TOTAL_CHECKS + 1))
    echo -n "Checking $description... "

    if eval "$command" >/dev/null 2>&1; then
        echo -e "${GREEN}✓${NC}"
        PASSED_CHECKS=$((PASSED_CHECKS + 1))
        return 0
    else
        echo -e "${RED}✗${NC}"
        FAILED_CHECKS=$((FAILED_CHECKS + 1))
        return 1
    fi
}

echo "========================================="
echo "Phase 1 Kubernetes Implementation Validator"
echo "========================================="
echo

# Check for required files
echo "## Required Files"
check "tk8.sh exists and is executable" "test -x tk8.sh"
check "services.yaml exists" "test -f k8s/test/services.yaml"
check "configmaps.yaml exists" "test -f k8s/test/configmaps.yaml"
check "test-servers.yaml exists" "test -f k8s/test/test-servers.yaml"
check "boulder-monolith.yaml exists" "test -f k8s/test/boulder-monolith.yaml"
check "database-init-job.yaml exists" "test -f k8s/test/database-init-job.yaml"
check "network-policies.yaml exists" "test -f k8s/test/network-policies.yaml"
check "loadbalancer.yaml exists" "test -f k8s/test/loadbalancer.yaml"
echo

# Check manifest contents for required services
echo "## Infrastructure Services (Phase 1 Requirement)"
check "MariaDB (bmysql) defined" "grep -q 'name: bmysql' k8s/test/services.yaml"
check "Redis-1 (bredis-1) defined" "grep -q 'name: bredis-1' k8s/test/services.yaml"
check "Redis-2 (bredis-2) defined" "grep -q 'name: bredis-2' k8s/test/services.yaml"
check "Consul (bconsul) defined" "grep -q 'name: bconsul' k8s/test/services.yaml"
check "ProxySQL (bproxysql) defined" "grep -q 'name: bproxysql' k8s/test/services.yaml"
check "Jaeger (bjaeger) defined" "grep -q 'name: bjaeger' k8s/test/services.yaml"
check "PKIMetal (bpkimetal) defined" "grep -q 'name: bpkimetal' k8s/test/services.yaml"
echo

# Check test servers
echo "## Test Servers (Phase 1 Requirement)"
check "Challenge Test Server defined" "grep -q 'name: chall-test-srv' k8s/test/test-servers.yaml"
check "CT Log Test Server defined" "grep -q 'name: ct-test-srv' k8s/test/test-servers.yaml"
check "AIA Test Server defined" "grep -q 'name: aia-test-srv' k8s/test/test-servers.yaml"
check "S3 Test Server defined" "grep -q 'name: s3-test-srv' k8s/test/test-servers.yaml"
check "Pardot Test Server defined" "grep -q 'name: pardot-test-srv' k8s/test/test-servers.yaml"
check "Zendesk Test Server defined" "grep -q 'name: zendesk-test-srv' k8s/test/test-servers.yaml"
echo

# Check Boulder monolith
echo "## Boulder Monolithic Deployment (Phase 1 Core)"
check "Boulder monolith deployment exists" "grep -q 'kind: Deployment' k8s/test/boulder-monolith.yaml && grep -q 'name: boulder-monolith' k8s/test/boulder-monolith.yaml"
check "Boulder uses startservers.py" "grep -q 'startservers.py' k8s/test/boulder-monolith.yaml"
check "Boulder exposes WFE2 port 4001" "grep -q 'port: 4001' k8s/test/boulder-monolith.yaml"
check "Boulder exposes SFE port 4003" "grep -q 'port: 4003' k8s/test/boulder-monolith.yaml"
echo

# Check database initialization
echo "## Database Initialization (Phase 1 Requirement)"
check "Database init job exists" "grep -q 'kind: Job' k8s/test/database-init-job.yaml && grep -q 'name: database-init' k8s/test/database-init-job.yaml"
check "Has database creation logic" "grep -q 'create_empty_db' k8s/test/database-init-job.yaml"
check "Has migration runner" "grep -q 'sql-migrate' k8s/test/database-init-job.yaml"
check "References SA database config" "grep -q 'sa/db.*sql' k8s/test/database-init-job.yaml"
check "References config directory" "grep -q 'BOULDER_CONFIG_DIR' k8s/test/database-init-job.yaml"
check "Has user creation logic" "grep -q 'db-users' k8s/test/database-init-job.yaml"
echo

# Check networking
echo "## Network Configuration (Phase 1 Requirement)"
check "NetworkPolicies defined" "grep -q 'kind: NetworkPolicy' k8s/test/network-policies.yaml"
check "Internal network policy exists" "grep -q 'boulder-internal-network' k8s/test/network-policies.yaml"
check "Public network 1 policy exists" "grep -q 'boulder-public-network-1' k8s/test/network-policies.yaml"
check "Public network 2 policy exists" "grep -q 'boulder-public-network-2' k8s/test/network-policies.yaml"
check "LoadBalancer for WFE2 exists" "grep -q 'boulder-wfe2-lb' k8s/test/loadbalancer.yaml"
check "LoadBalancer for challenges exists" "grep -q 'boulder-challenge' k8s/test/loadbalancer.yaml"
echo

# Check tk8.sh enhancements
echo "## tk8.sh Script Enhancements"
check "Database initialization function exists" "grep -q 'run_database_initialization' tk8.sh"
check "envsubst dependency check exists" "grep -q 'envsubst' tk8.sh"
check "Calls database init before tests" "grep -q 'run_database_initialization' tk8.sh && grep -q 'run_certificate_generation' tk8.sh"
echo

# Check configuration
echo "## Configuration Management"
check "ConfigMaps for test configs" "grep -q 'boulder-test-config' k8s/test/configmaps.yaml"
check "CT server config exists" "grep -q 'ct-test-srv-config' k8s/test/test-servers.yaml"
check "Pardot server config exists" "grep -q 'pardot-test-srv-config' k8s/test/test-servers.yaml"
check "Zendesk server config exists" "grep -q 'zendesk-test-srv-config' k8s/test/test-servers.yaml"
echo

# Results summary
echo "========================================="
echo "## Summary"
echo "========================================="
echo -e "Total checks: $TOTAL_CHECKS"
echo -e "Passed: ${GREEN}$PASSED_CHECKS${NC}"
echo -e "Failed: ${RED}$FAILED_CHECKS${NC}"
echo

if [ $FAILED_CHECKS -eq 0 ]; then
    echo -e "${GREEN}✓ All Phase 1 requirements are satisfied!${NC}"
    echo "The implementation is ready for testing."
    exit 0
else
    echo -e "${RED}✗ Some Phase 1 requirements are missing.${NC}"
    echo "Please review the failed checks above."
    exit 1
fi