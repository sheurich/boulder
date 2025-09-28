# Boulder Kubernetes Staging Profile

## Overview

The staging profile provides a progressive implementation environment for **Phases 2-6** of the Boulder Kubernetes migration. This overlay evolves from the Phase 1 monolith through service splitting, Kubernetes-native features, and production-ready capabilities.

## Purpose

- **Feature Development**: Test Phases 2-6 features without disrupting CI
- **Progressive Migration**: Gradual evolution from monolith to microservices
- **Production Preparation**: Validate production features before deployment
- **Risk Mitigation**: Isolated environment for experimental changes

## Current State: Phase 2 Preparation

The staging overlay currently mirrors the test profile (Phase 1) and is ready for Phase 2 service splitting development.

## Phase Roadmap

### Phase 2: Service Splitting (Next)
**Goal**: Split Boulder monolith into separate Kubernetes Deployments

**Service Groups**:
1. **Core Storage**: SA instances, database operations
2. **Validation**: VA instances, Remote VAs for multi-perspective validation
3. **Certificate Operations**: CA instances with SCT providers
4. **Registration**: RA instances, workflow orchestration
5. **Frontend**: WFE2, SFE, Nonce services
6. **Administrative**: CRL services, bad-key-revoker, log-validator

**Implementation**: Add patches to kustomization.yaml for each service group

### Phase 3: Kubernetes-native Initialization
**Goal**: Replace startup scripts with Kubernetes patterns

**Features**:
- Database initialization Job
- Certificate generation Job
- InitContainers for per-pod setup
- ConfigMap-based migrations

### Phase 4: Configuration Management
**Goal**: Externalize configuration from containers

**Features**:
- ConfigMaps for each service's JSON config
- Secrets for TLS certificates and credentials
- Environment-specific overlays
- Dynamic configuration reloading

### Phase 5: Observability
**Goal**: Production-grade monitoring and tracing

**Features**:
- Prometheus metrics collection
- Enhanced Jaeger tracing
- Grafana dashboards
- Log aggregation
- Service mesh integration (optional)

### Phase 6: Production Features
**Goal**: Scale, resilience, and multi-region support

**Features**:
- Horizontal Pod Autoscalers (HPAs)
- Pod Disruption Budgets (PDBs)
- Network segmentation
- Multi-region deployment
- Advanced traffic management

## Deployment

### Prerequisites

```bash
# Create staging namespace
kubectl create namespace boulder-staging
```

### Deploy Staging Profile

```bash
# Deploy staging environment
kubectl apply -k k8s/overlays/staging/

# Verify deployment
kubectl get pods -n boulder-staging

# Check service status
kubectl get all -n boulder-staging -l environment=staging
```

### Testing in Staging

```bash
# Run tests (after profile support is added)
./tk8s.sh --profile staging

# Or manually
BOULDER_POD=$(kubectl get pods -n boulder-staging -l app=boulder-monolith -o jsonpath='{.items[0].metadata.name}')
kubectl exec -n boulder-staging $BOULDER_POD -- ./test.sh
```

## Development Workflow

### Adding Phase 2 Features

1. Create service deployment patches:
   ```bash
   # Example: phase2-ra-deployment.yaml
   vim k8s/overlays/staging/phase2-ra-deployment.yaml
   ```

2. Add to kustomization.yaml:
   ```yaml
   patchesStrategicMerge:
     - phase2-ra-deployment.yaml
   ```

3. Apply and test:
   ```bash
   kubectl apply -k k8s/overlays/staging/
   kubectl get pods -n boulder-staging
   ```

### Validating Changes

1. **Functional Testing**: Run full test suite
2. **Performance Testing**: Compare with test profile
3. **Stability Testing**: Long-running tests
4. **Rollback Testing**: Verify rollback procedures

## Namespace Isolation

The staging profile uses `boulder-staging` namespace to:
- Isolate from test environment
- Enable parallel deployments
- Prevent resource conflicts
- Allow independent scaling

## Configuration

### Current Configuration (Phase 1 Compatible)
- Uses `test/config` directory
- Monolithic Boulder deployment
- All infrastructure services

### Future Configuration (Phases 2-6)
- Service-specific ConfigMaps
- Externalized secrets
- Environment-specific values
- Dynamic updates without restarts

## Monitoring

### Logs

```bash
# View all logs
kubectl logs -n boulder-staging -l environment=staging

# Specific service (after splitting)
kubectl logs -n boulder-staging -l app=boulder-ra

# Follow logs
kubectl logs -n boulder-staging deployment/boulder-monolith -f
```

### Metrics

```bash
# Port-forward Jaeger
kubectl port-forward -n boulder-staging service/bjaeger 16687:16686
# Open http://localhost:16687

# Future: Prometheus metrics
# kubectl port-forward -n boulder-staging service/prometheus 9090:9090
```

## Troubleshooting

### Common Issues

| Issue | Solution |
|-------|----------|
| Namespace not found | Create: `kubectl create namespace boulder-staging` |
| Services failing | Check dependencies: `kubectl get pods -n boulder-staging` |
| Config issues | Verify ConfigMaps: `kubectl get cm -n boulder-staging` |
| Network problems | Check policies: `kubectl get networkpolicies -n boulder-staging` |
| Resource limits | Check resources: `kubectl top pods -n boulder-staging` |

### Phase-Specific Issues

**Phase 2 (Service Splitting)**:
- Service discovery failures: Check Service definitions
- gRPC connection issues: Verify network policies
- Startup order problems: Check readiness probes

**Phase 3 (K8s Initialization)**:
- Job failures: Check Job logs
- InitContainer issues: Describe pod for details

**Phase 4 (ConfigMaps)**:
- Mount failures: Verify ConfigMap names
- Config syntax errors: Validate JSON configs

## Success Criteria

Before promoting features from staging to production:

1. **All tests pass** with same results as test profile
2. **No performance degradation** compared to baseline
3. **Deployment complexity** is manageable
4. **Rollback procedures** are tested and documented
5. **Monitoring** shows stable operation
6. **Documentation** is complete

## Cleanup

```bash
# Delete staging deployment
kubectl delete -k k8s/overlays/staging/

# Or delete entire namespace
kubectl delete namespace boulder-staging
```

## See Also

- [Phase Status Tracking](PHASE-STATUS.md)
- [Boulder K8s Specification](../../docs/BOULDER-K8S-SPEC.md)
- [Test Profile](../test/README.md)
- [Development Profile](../dev/README.md)
- [ADR-002 Multi-Profile Strategy](../../docs/ADR-002-MULTI-PROFILE-STRATEGY.md)