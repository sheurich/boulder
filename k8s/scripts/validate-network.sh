#!/usr/bin/env bash
set -euo pipefail

echo "=== Boulder Kubernetes Network Validation ==="

NAMESPACE="${1:-boulder-test}"
KUBECTL="${KUBECTL_CMD:-kubectl}"

# Color codes
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

function pass() { echo -e "${GREEN}✓${NC} $1"; }
function fail() { echo -e "${RED}✗${NC} $1"; exit 1; }
function warn() { echo -e "${YELLOW}⚠${NC} $1"; }

echo "Checking network policies in namespace: $NAMESPACE"

# Check if network policies exist
if ! $KUBECTL get networkpolicies -n "$NAMESPACE" &>/dev/null; then
  fail "No network policies found in $NAMESPACE"
fi

# Verify service labels
echo ""
echo "Verifying service network segment labels..."

# Infrastructure services should be on internal network
for service in bmysql boulder-redis-1 boulder-redis-2 bconsul bproxysql bjaeger bpkimetal; do
  # Use the display name for user-friendly output
  local display_name=$service
  if [[ "$service" == boulder-redis-1 ]]; then
    display_name="bredis-1"
  elif [[ "$service" == boulder-redis-2 ]]; then
    display_name="bredis-2"
  fi

  if $KUBECTL get pods -n "$NAMESPACE" -l "app=$service,network-segment=internal" --no-headers 2>/dev/null | grep -q Running; then
    pass "$display_name has correct internal network label"
  else
    warn "$display_name missing network-segment=internal label"
  fi
done

# Challenge test server should be on public network
if $KUBECTL get pods -n "$NAMESPACE" -l "app=chall-test-srv,network-segment=public1" --no-headers 2>/dev/null | grep -q Running; then
  pass "chall-test-srv has correct public network label"
else
  warn "chall-test-srv missing network-segment=public1 label"
fi

# CT log test server should be on public network
if $KUBECTL get pods -n "$NAMESPACE" -l "app=ct-test-srv,network-segment=public1" --no-headers 2>/dev/null | grep -q Running; then
  pass "ct-test-srv has correct public network label"
else
  warn "ct-test-srv missing network-segment=public1 label"
fi

# AIA test server should be on internal network
if $KUBECTL get pods -n "$NAMESPACE" -l "app=aia-test-srv,network-segment=internal" --no-headers 2>/dev/null | grep -q Running; then
  pass "aia-test-srv has correct internal network label"
else
  warn "aia-test-srv missing network-segment=internal label"
fi

# Boulder monolith should have special cross-network access
if $KUBECTL get pods -n "$NAMESPACE" -l "app=boulder-monolith,network-segment=boulder" --no-headers 2>/dev/null | grep -q Running; then
  pass "boulder-monolith has correct boulder network label"
else
  warn "boulder-monolith missing network-segment=boulder label"
fi

echo ""
echo "Testing network isolation..."

# Create test pod on public network
echo "Creating test pod on public network..."
$KUBECTL run test-public --image=busybox --labels="network-segment=public1" \
  -n "$NAMESPACE" --restart=Never --command -- sleep 30 2>/dev/null || true

# Wait for pod to be ready
sleep 3

# Test that public segment cannot reach internal services
echo "Testing public -> internal isolation..."
if $KUBECTL exec test-public -n "$NAMESPACE" -- timeout 2 nc -zv bmysql 3306 2>&1 | grep -q "succeeded\|open"; then
  fail "Public segment can reach internal services (should be blocked)"
else
  pass "Public segment correctly blocked from internal services"
fi

# Test that public segment cannot reach Redis
if $KUBECTL exec test-public -n "$NAMESPACE" -- timeout 2 nc -zv bredis-1 6379 2>&1 | grep -q "succeeded\|open"; then
  fail "Public segment can reach Redis (should be blocked)"
else
  pass "Public segment correctly blocked from Redis"
fi

# Test that public segment cannot reach Consul
if $KUBECTL exec test-public -n "$NAMESPACE" -- timeout 2 nc -zv bconsul 8500 2>&1 | grep -q "succeeded\|open"; then
  fail "Public segment can reach Consul (should be blocked)"
else
  pass "Public segment correctly blocked from Consul"
fi

# Create test pod on internal network
echo ""
echo "Creating test pod on internal network..."
$KUBECTL run test-internal --image=busybox --labels="network-segment=internal" \
  -n "$NAMESPACE" --restart=Never --command -- sleep 30 2>/dev/null || true

# Wait for pod to be ready
sleep 3

# Test that internal segment cannot reach public services
echo "Testing internal -> public isolation..."
if $KUBECTL exec test-internal -n "$NAMESPACE" -- timeout 2 nc -zv chall-test-srv 80 2>&1 | grep -q "succeeded\|open"; then
  warn "Internal segment can reach public services (consider if this should be blocked)"
else
  pass "Internal segment blocked from public services"
fi

# Test that internal segment can reach other internal services
echo "Testing internal -> internal connectivity..."
if $KUBECTL exec test-internal -n "$NAMESPACE" -- timeout 2 nc -zv bmysql 3306 2>&1 | grep -q "succeeded\|open"; then
  pass "Internal segment can reach other internal services"
else
  fail "Internal segment cannot reach other internal services"
fi

# Test that Boulder can reach all services
echo ""
echo "Testing Boulder cross-network access..."
BOULDER_POD=$($KUBECTL get pods -n "$NAMESPACE" -l app=boulder-monolith -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")

if [ -n "$BOULDER_POD" ]; then
  # Test Boulder -> internal services
  if $KUBECTL exec "$BOULDER_POD" -n "$NAMESPACE" -- timeout 2 nc -zv bmysql 3306 2>&1 | grep -q "succeeded\|open"; then
    pass "Boulder can reach internal services (MySQL)"
  else
    fail "Boulder cannot reach internal services (MySQL)"
  fi

  if $KUBECTL exec "$BOULDER_POD" -n "$NAMESPACE" -- timeout 2 nc -zv bredis-1 6379 2>&1 | grep -q "succeeded\|open"; then
    pass "Boulder can reach internal services (Redis)"
  else
    fail "Boulder cannot reach internal services (Redis)"
  fi

  # Test Boulder -> public services
  if $KUBECTL exec "$BOULDER_POD" -n "$NAMESPACE" -- timeout 2 nc -zv chall-test-srv 5002 2>&1 | grep -q "succeeded\|open"; then
    pass "Boulder can reach public services (challenge test server)"
  else
    warn "Boulder cannot reach challenge test server on port 5002"
  fi

  # Test Boulder -> external network (for CT logs)
  if $KUBECTL exec "$BOULDER_POD" -n "$NAMESPACE" -- timeout 2 nc -zv google.com 443 2>&1 | grep -q "succeeded\|open"; then
    pass "Boulder can reach external network (for CT logs)"
  else
    warn "Boulder cannot reach external network"
  fi
else
  warn "Boulder monolith pod not found - skipping Boulder connectivity tests"
fi

# Cleanup test pods
echo ""
echo "Cleaning up test pods..."
$KUBECTL delete pod test-public -n "$NAMESPACE" --grace-period=0 --force 2>/dev/null || true
$KUBECTL delete pod test-internal -n "$NAMESPACE" --grace-period=0 --force 2>/dev/null || true

echo ""
echo "Network validation complete"