# Boulder Kubernetes Phase 1 Implementation Loop

## Your Mission
Implement Phase 1 of Boulder's Kubernetes migration as specified in `BOULDER-K8S-SPEC.md` - achieving drop-in CI parity with Docker Compose on a kind cluster.

## Workflow for Each Iteration

### 1. Assess Current State
```bash
# Check specification
cat k8s/docs/BOULDER-K8S-SPEC.md | head -128  # Review Phase 1 requirements

# Inventory k8s/ directory
repomix -o - k8s  # Full contents analysis
tree k8s/         # Quick directory structure

# Check git status
git status
git log --oneline -5
```

### 2. Identify and Fix Issues
Review completed work for:
- Missing service definitions matching Docker Compose names
- Incorrect ports or configurations
- Non-functional scripts or manifests
- Deviations from Phase 1 scope (NO service splitting!)

### 3. Continue Implementation

#### Phase 1 Deliverables Checklist: (all paths relative the BOULDER-REPO)
- [ ] `k8s/cluster/kind-config.yaml` - kind cluster definition with pinned K8s version
- [ ] `k8s/manifests/` - External dependencies as K8s objects:
  - [ ] MariaDB StatefulSet with PVC
  - [ ] Redis (×2) Deployments
  - [ ] Consul Deployment/StatefulSet
  - [ ] ProxySQL Deployment
  - [ ] PKIMetal, Challenge Test Server, CT Log Server, etc.
- [ ] `k8s/services/` - Service objects preserving Docker Compose names/ports
- [ ] `k8s/scripts/`:
  - [ ] `k8s-up.sh` - Create kind cluster and apply manifests
  - [ ] `k8s-down.sh` - Destroy cluster
- [ ] `k8s/README.md` - One-command usage instructions
- [ ] `tk8s.sh` - Wrapper for `test.sh` using kubectl exec (mirrors BOULDER-REPO/t.sh)
- [ ] `tnk8s.sh` - Wrapper for `test.sh` with config-next (mirrors BOULDER-REPO/tn.sh)
- [ ] Boulder Pod definition running `startservers.py` (monolithic, NO splitting)

#### Key Requirements:
- **Service Naming Parity**: Exact DNS names from Docker Compose (e.g., `mariadb`, `redis`, `consul`)
- **Network Compatibility**: Services accessible on same ports as Compose
- **Boulder Monolith**: Single Pod with `startservers.py` - NO service splitting in Phase 1
- **Test Execution**: `tk8s.sh` runs tests via `kubectl exec` into Boulder pod
- **Configuration**: Mount same configs via ConfigMaps/Secrets (no format changes)

### 4. Test Your Progress
```bash
# Create cluster and deploy
./k8s/scripts/k8s-up.sh

# Run tests
./tk8s.sh        # Should match t.sh behavior

# Clean up
./k8s/scripts/k8s-down.sh
```

## Success Criteria
Phase 1 is complete when:
1. Full CI test suite passes with identical results to Docker Compose
2. No changes required to test harness (test.sh, v2_integration.py)
3. Services reachable under same names/ports as Compose
4. tk8s.sh/tnk8s.sh provide drop-in replacement for t.sh/tn.sh

## Important Constraints
- **NO Boulder service splitting** - Keep as monolithic container
- **NO production features** - No mesh, HPAs, or advanced K8s features
- **Preserve ALL behavior** - This is CI parity, not optimization
- **Use existing images** - Same versions as docker-compose.yml

## Next Steps
After fixing any issues found, implement the next missing component from the checklist above. Focus on getting a minimal working deployment before adding completeness.
