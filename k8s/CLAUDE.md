# CLAUDE.md - Kubernetes Migration

This file provides guidance to Claude Code when working with the Boulder Kubernetes migration in the k8s/ directory.

## Project Context

This is the Boulder Certificate Authority Kubernetes migration project, currently implementing **Phase 1: Drop-in CI Parity on Kubernetes**. The goal is to migrate Boulder from Docker Compose to Kubernetes while maintaining exact behavioral compatibility.

## Common Kubernetes Development Commands

### Primary Test Execution (Persistent Boulder Pattern)

**Run tests in Kubernetes using `tk8s.sh` (standard config):**

```bash
# Run all tests in Kubernetes cluster
./tk8s.sh

# Run specific test types
./tk8s.sh --unit                      # Unit tests only
./tk8s.sh --integration                # Integration tests only
./tk8s.sh --lints                      # Lints only

# Advanced options
./tk8s.sh --unit --verbose             # Verbose output
./tk8s.sh --filter TestName            # Run specific test
./tk8s.sh --unit --enable-race-detection  # Race detection
./tk8s.sh --coverage --coverage-dir=./test/coverage  # Coverage

# Custom cluster/namespace
KIND_CLUSTER=my-cluster NAMESPACE=my-ns ./tk8s.sh --unit
```

**Run tests with config-next using `tnk8s.sh`:**

```bash
# Same options as tk8s.sh but with next-generation config
./tnk8s.sh --unit
./tnk8s.sh --integration
./tnk8s.sh  # All tests with config-next
```

### Cluster Management

**Setup complete Kubernetes environment:**

```bash
# Standard configuration
./k8s/scripts/k8s-up.sh

# With config-next
BOULDER_CONFIG_DIR=test/config-next ./k8s/scripts/k8s-up.sh

# Custom cluster name
KIND_CLUSTER=boulder-test ./k8s/scripts/k8s-up.sh
```

**Teardown cluster and cleanup:**

```bash
# Complete cleanup
./k8s/scripts/k8s-down.sh

# Custom cluster
KIND_CLUSTER=boulder-test ./k8s/scripts/k8s-down.sh
```

### Direct Kubernetes Operations

```bash
# Check pod status
kubectl get pods -n boulder

# View Boulder logs
kubectl logs -n boulder deployment/boulder-monolith

# Execute commands in Boulder pod
kubectl exec -n boulder deployment/boulder-monolith -- ./test.sh --unit

# Check service endpoints
kubectl get services -n boulder

# View infrastructure services
kubectl get all -n boulder -l tier=infrastructure

# Database operations
kubectl exec -n boulder statefulset/bmysql -- mysql -u root -psecret boulder_sa -e "SHOW TABLES;"

# Redis operations
kubectl exec -n boulder statefulset/bredis-1 -- redis-cli ping
```

### Development Workflows

```bash
# Rebuild and reload Boulder image
docker build -t boulder:latest .
kind load docker-image boulder:latest --name boulder-k8s
kubectl rollout restart deployment/boulder-monolith -n boulder

# Apply configuration changes
kubectl apply -f k8s/test/configmaps.yaml
kubectl rollout restart deployment/boulder-monolith -n boulder

# Watch test execution
watch kubectl get pods -n boulder

# Port-forward for debugging
kubectl port-forward -n boulder service/bjaeger 16686:16686  # Jaeger UI
kubectl port-forward -n boulder service/bmysql 3306:3306      # MySQL
```

## Architecture Overview (Phase 1: Persistent Boulder Monolith)

The current implementation uses a **persistent Boulder monolith deployment** pattern where Boulder runs as a long-lived Kubernetes deployment with tests executed via `kubectl exec`. This maintains exact Docker Compose compatibility while providing a Kubernetes foundation.

### Execution Flow

```
[tk8s.sh/tnk8s.sh] → [kind cluster] → [Boulder pod] → [kubectl exec test.sh]
                                    ↓
                         [Infrastructure Services]
                         • MariaDB (bmysql)
                         • Redis (bredis-1, bredis-2)
                         • Consul (bconsul)
                         • ProxySQL (bproxysql)
                         • Jaeger (bjaeger)
                         • PKIMetal (bpkimetal)
```

### Service Architecture

1. **Boulder Monolith** (`k8s/test/boulder-monolith.yaml`)
   - Runs all Boulder services in single container
   - Persistent deployment (not Job-based)
   - Tests executed inside via kubectl exec
   - Maintains Docker Compose behavior

2. **Infrastructure Services** (`k8s/manifests/`)
   - MariaDB: Primary database (StatefulSet + PVC)
   - Redis: Rate limiting backend (2x StatefulSet)
   - Consul: Service discovery (StatefulSet)
   - ProxySQL: Connection pooling (Deployment)
   - Jaeger: Distributed tracing (Deployment)
   - PKIMetal: PKI validation (Deployment)

3. **Test Infrastructure** (`k8s/test/`)
   - Challenge Test Server: HTTP-01, DNS-01, TLS-ALPN-01
   - CT Log Test Server: Certificate Transparency
   - AIA Test Server: Authority Information Access
   - S3 Test Server: CRL storage simulation

### Networking

Three network segments required (Phase 2+):
- **Frontend**: Internet-facing (80, 443, 4431)
- **Internal**: Service communication (gRPC)
- **Backend**: Database/cache access

Currently: All services in single namespace with network policies.

## Migration Phases Status

### Phase 0: Analysis and Documentation ✅ COMPLETE
- Comprehensive service inventory in `k8s/docs/PHASE0-ANALYSIS.md`
- Dependency mapping (6-level hierarchy)
- Network architecture documented
- Configuration analysis complete

### Phase 1: Drop-in CI Parity ⚠️ IN PROGRESS
**Goal**: Run existing Boulder CI unchanged on Kubernetes

**Completed**:
- ✅ Persistent Boulder monolith deployment
- ✅ kubectl exec test pattern
- ✅ All infrastructure services
- ✅ Database initialization Job
- ✅ Cluster management scripts
- ✅ ConfigMap-based configuration

**Remaining**:
- ⚠️ Test server verification
- ⚠️ Network policy validation
- ⚠️ Full integration test suite
- ⚠️ CI/CD pipeline integration

### Phase 2: Service Separation (FUTURE)
- Individual Deployments per Boulder service
- Kubernetes-native service discovery
- Gradual Consul replacement
- Inter-service gRPC via Services

### Phase 3: Kubernetes-native Features (FUTURE)
- Horizontal Pod Autoscaling
- Service mesh integration
- Production observability
- GitOps workflows

## Important Directories and Files

### Kubernetes Manifests
- `k8s/manifests/` - Infrastructure service definitions
- `k8s/test/` - Boulder deployment and test infrastructure
- `k8s/cluster/` - kind cluster configuration
- `k8s/scripts/` - Operational scripts
- `k8s/overlays/dev/` - Kustomize overlays for development

### Key Files
- `k8s/test/boulder-monolith.yaml` - Main Boulder deployment
- `k8s/test/database-init-job.yaml` - Database initialization
- `k8s/cluster/kind-config.yaml` - kind cluster definition
- `k8s/docs/SPEC.md` - Complete migration specification
- `k8s/docs/PHASE0-ANALYSIS.md` - Service dependency analysis

### Test Runners
- `tk8s.sh` - Standard config Kubernetes test runner
- `tnk8s.sh` - Config-next Kubernetes test runner
- `k8s/scripts/k8s-up.sh` - Complete cluster setup
- `k8s/scripts/k8s-down.sh` - Cluster teardown

## Quick Reference

| Task | Command |
|------|---------|
| Run all tests in K8s | `./tk8s.sh` |
| Run unit tests only | `./tk8s.sh --unit` |
| Run with config-next | `./tnk8s.sh` |
| Setup cluster | `./k8s/scripts/k8s-up.sh` |
| Teardown cluster | `./k8s/scripts/k8s-down.sh` |
| Check pod status | `kubectl get pods -n boulder` |
| View Boulder logs | `kubectl logs -n boulder deployment/boulder-monolith` |
| Execute in pod | `kubectl exec -n boulder deployment/boulder-monolith -- <cmd>` |
| Port-forward Jaeger | `kubectl port-forward -n boulder service/bjaeger 16686:16686` |

## Development Guidelines

### When Working on Kubernetes Migration

1. **Always verify cluster state** before running tests:
   ```bash
   kubectl get pods -n boulder
   ```

2. **Use appropriate test runner** based on config:
   - `tk8s.sh` for standard config
   - `tnk8s.sh` for config-next

3. **Check service dependencies** when debugging:
   ```bash
   kubectl get all -n boulder -l tier=infrastructure
   ```

4. **Monitor logs** during test execution:
   ```bash
   kubectl logs -f -n boulder deployment/boulder-monolith
   ```

5. **Validate manifests** before applying:
   ```bash
   kubectl apply --dry-run=client -f k8s/manifests/
   ```

### Common Issues and Solutions

| Issue | Solution |
|-------|----------|
| Tests fail with connection errors | Check infrastructure services: `kubectl get pods -n boulder` |
| Database not initialized | Run: `kubectl apply -f k8s/test/database-init-job.yaml` |
| Services not discovering each other | Check Consul: `kubectl logs -n boulder statefulset/bconsul` |
| Rate limiting not working | Verify Redis: `kubectl exec -n boulder statefulset/bredis-1 -- redis-cli ping` |
| Can't connect to services | Check network policies: `kubectl get networkpolicies -n boulder` |

### Testing Strategy

1. **Unit Tests**: Run inside Boulder container, no external dependencies
2. **Integration Tests**: Full stack with all infrastructure services
3. **Validation**: Use `k8s/test/validate-phase1.sh` for comprehensive checks
4. **Debugging**: Use Jaeger UI for distributed tracing

## External Documentation

### Boulder Documentation
- [Boulder GitHub](https://github.com/letsencrypt/boulder)
- [ACME Protocol RFC 8555](https://datatracker.ietf.org/doc/html/rfc8555)
- [Boulder Architecture Docs](https://github.com/letsencrypt/boulder/tree/main/docs)

### Kubernetes Resources
- [kind Documentation](https://kind.sigs.k8s.io/)
- [Kubernetes Docs](https://kubernetes.io/docs/)
- [kubectl Cheat Sheet](https://kubernetes.io/docs/reference/kubectl/cheatsheet/)
- [Kustomize Documentation](https://kustomize.io/)

### Container Tools
- [Docker Documentation](https://docs.docker.com/)
- [Docker Compose to Kubernetes](https://kompose.io/)

## Important Notes

- **Phase 1 Focus**: Maintain exact Docker Compose behavior - no optimizations yet
- **kubectl exec Pattern**: Tests run inside persistent Boulder pod, not as Jobs
- **Service Names**: Preserve Docker Compose names (bmysql, bredis-1, etc.)
- **Configuration**: ConfigMaps for Boulder config, environment variables for service config
- **Database**: Must be initialized before Boulder starts (database-init Job)
- **Networking**: Currently single namespace; Phase 2 will introduce network segmentation
- **Testing**: All existing Boulder tests must pass unchanged

## Code Style for Kubernetes Work

- Use consistent labeling: `app`, `component`, `tier`
- Follow Kubernetes naming conventions: lowercase, hyphens
- Use ConfigMaps for configuration, Secrets for sensitive data
- Implement proper health checks (liveness, readiness)
- Always specify resource requests/limits (Phase 2+)
- Use namespaces for isolation
- Document manifest purpose with annotations

# important-instruction-reminders
- Focus on Kubernetes migration tasks, not general Boulder development
- Prioritize Phase 1 completion before suggesting Phase 2+ features
- Always verify cluster state before making changes
- Use tk8s.sh/tnk8s.sh for testing, not direct docker compose
- Maintain exact Docker Compose compatibility in Phase 1
- Test changes with both standard and config-next configurations