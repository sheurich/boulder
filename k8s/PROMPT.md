# Boulder Kubernetes Phase 1 Implementation Loop

## Your Mission
Implement Phase 1 of Boulder's Kubernetes migration as specified in `docs/SPEC.md` - achieving drop-in CI parity with Docker Compose on a kind cluster.

## Workflow for Each Iteration

### 0. Pre-Flight Checks
```bash
# Verify dependencies
command -v kind >/dev/null 2>&1 || echo "ERROR: kind not installed"
command -v kubectl >/dev/null 2>&1 || echo "ERROR: kubectl not installed"
docker info >/dev/null 2>&1 || echo "ERROR: Docker not running"

# Check for running cluster
kubectl get nodes 2>/dev/null && echo "WARNING: Cluster already exists"
```

### 1. Assess Current State
```bash
# Check specification
cat docs/SPEC.md | head -128  # Review Phase 1 requirements

# Check git status
git status
git log --oneline -5

# Verify cluster state
kubectl get pods -n boulder
```

### 2. Identify and Fix Issues
Review completed work for:
- Missing service definitions matching Docker Compose names
- Incorrect ports or configurations
- Non-functional scripts or manifests
- Deviations from Phase 1 scope (NO service splitting!)

### 3. Test Your Progress
```bash
# Create cluster and deploy
./scripts/k8s-up.sh

# Validate deployment readiness
./scripts/validate-network.sh
./test/validate-phase1.sh

# Run tests with comparison (from parent directory)
cd .. && {
  # Run both and compare
  ./t.sh --unit > /tmp/compose.out 2>&1
  ./tk8s.sh --unit > /tmp/k8s.out 2>&1
  diff -u /tmp/compose.out /tmp/k8s.out || echo "DIFFERENCES FOUND"
}

# Clean up when done
./scripts/k8s-down.sh
```

### 4. Debugging Common Issues
```bash
# Check pod logs for failures
kubectl logs -n boulder -l app=boulder --tail=50

# Verify service connectivity
kubectl exec -n boulder deploy/boulder-monolith -- nc -zv bmysql 3306

# Check database initialization
kubectl logs -n boulder job/database-init --tail=100

# Validate DNS resolution
kubectl exec -n boulder deploy/boulder-monolith -- nslookup bmysql
```

### 5. Validation Gates (Run Before Proceeding)
```bash
# Gate 1: Infrastructure ready
kubectl wait --for=condition=ready pod -n boulder -l app=bmysql --timeout=60s
kubectl wait --for=condition=ready pod -n boulder -l app=bredis-1 --timeout=60s

# Gate 2: Boulder operational
curl -f http://localhost:4001/directory || exit 1

# Gate 3: Test execution parity
./tk8s.sh --lints && echo "✓ Lints pass"
./tk8s.sh --unit --filter TestNewAccount && echo "✓ Sample unit test passes"
```

## Success Criteria
Phase 1 is complete when:
1. Full CI test suite passes with identical results to Docker Compose
2. No changes required to test harness (test.sh, v2_integration.py)
3. Services reachable under same names/ports as Compose
4. tk8s.sh/tnk8s.sh provide drop-in replacement for t.sh/tn.sh

## Next Steps
After fixing any issues found, implement the next missing component from the Phase 1 checklist (see k8s/CLAUDE.md). Focus on getting a minimal working deployment before adding completeness.

## Execution Strategy

Use parallel agents to accelerate implementation:
- **Parallel execution**: Run multiple independent agents simultaneously for analysis and validation
- **Agent selection**: Use specialized agents for targeted searches, general-purpose for complex tasks
- **Example**: When implementing a new service, analyze Docker Compose config while researching Kubernetes patterns in parallel

## Agent Task Templates

### For Analysis Tasks
- **codebase-analyzer**: "Analyze why tests fail in K8s but pass in Docker Compose"
- **codebase-locator**: "Find all database initialization code paths"
- **codebase-pattern-finder**: "Find patterns for service health checks"

### For Implementation Tasks
- **general-purpose**: "Fix the database connection timeout in boulder-monolith"
- **code-reviewer**: "Review the network policies for security gaps"

## Success Metrics Dashboard
```bash
# Quick status check
echo "=== Phase 1 Completion Status ==="
[ -f scripts/k8s-up.sh ] && echo "✓ Cluster setup script" || echo "✗ Missing k8s-up.sh"
[ -f ../tk8s.sh ] && echo "✓ Test runner script" || echo "✗ Missing tk8s.sh"
kubectl get pod -n boulder -l app=boulder 2>/dev/null && echo "✓ Boulder deployed" || echo "✗ Boulder not running"
./test/validate-phase1.sh --quiet && echo "✓ Phase 1 validation" || echo "✗ Validation fails"
```

## Test Progression (Start Small, Build Up)
1. **Smoke Test**: `./tk8s.sh --start-py` - Boulder starts and ACME directory responds
2. **Lint Test**: `./tk8s.sh --lints` - Code quality checks pass
3. **Single Unit Test**: `./tk8s.sh --unit --filter TestNewAccount` - Isolated test
4. **Unit Suite**: `./tk8s.sh --unit` - All unit tests
5. **Integration**: `./tk8s.sh --integration` - Full integration suite
6. **Full Suite**: `./tk8s.sh` - Complete test run matching t.sh

## Common Failure Recovery
- **Pod CrashLoopBackOff**: Check logs, verify dependencies, restart deployment
- **Database Connection Failed**: Verify MySQL pod, check ProxySQL, validate credentials
- **Test Timeout**: Increase timeouts in tk8s.sh, check resource limits
- **Network Unreachable**: Validate NetworkPolicies, check DNS configuration

## Version Control

Commit changes at logical checkpoints:
- After completing each Phase 1 deliverable
- When a service or script becomes functional
- After fixing significant bugs
- Use clear commit messages referencing the specific component
