# Boulder Kubernetes Test Profile

## Overview

The test profile provides **Phase 1 CI parity** with the existing Docker Compose environment. This overlay maintains exact behavioral compatibility with Docker Compose while running on Kubernetes, ensuring all existing tests pass without modification.

## Purpose

- **CI/CD Compatibility**: Drop-in replacement for Docker Compose in CI pipelines
- **Test Stability**: Frozen configuration to ensure consistent test results
- **Service Parity**: Exact service naming and behavior from Docker Compose
- **Configuration**: Uses existing `test/config` directory without modification

## Characteristics

### Boulder Deployment
- **Architecture**: Monolithic container running all Boulder services
- **Startup**: Uses `startservers.py` to maintain existing startup sequence
- **Configuration**: Mounts `test/config` directory for Boulder configuration
- **Testing**: Tests executed via `kubectl exec` into the persistent pod

### Service Names (Docker Compose Compatibility)
All services maintain their Docker Compose names for compatibility:
- `bmysql` - MariaDB database
- `bproxysql` - ProxySQL connection pooler
- `bredis-1`, `bredis-2` - Redis instances for rate limiting
- `bconsul` - Consul for service discovery
- `bjaeger` - Jaeger for distributed tracing
- `bpkimetal` - PKIMetal for PKI operations

### Test Infrastructure
- Challenge Test Server (HTTP-01, DNS-01, TLS-ALPN-01)
- CT Log Test Server
- AIA Test Server
- S3 Test Server
- Load Balancer for external access

## Deployment

### Quick Start

```bash
# Deploy test profile
kubectl apply -k k8s/overlays/test/

# Verify deployment
kubectl get pods -n boulder

# Run tests (after profile support is added)
./tk8s.sh --profile test
```

### Manual Deployment

```bash
# Create namespace and deploy
kubectl create namespace boulder
kubectl apply -k k8s/overlays/test/

# Wait for all pods to be ready
kubectl wait --for=condition=Ready pods --all -n boulder --timeout=300s

# Check service status
kubectl get all -n boulder -l environment=test
```

## Testing

### Using Test Scripts

Once profile support is implemented:
```bash
# Run all tests
./tk8s.sh --profile test

# Run unit tests only
./tk8s.sh --profile test --unit

# Run integration tests only
./tk8s.sh --profile test --integration

# Run with config-next
./tnk8s.sh --profile test
```

### Direct Test Execution

```bash
# Get Boulder pod name
BOULDER_POD=$(kubectl get pods -n boulder -l app=boulder-monolith -o jsonpath='{.items[0].metadata.name}')

# Run tests directly
kubectl exec -n boulder $BOULDER_POD -- ./test.sh --unit
kubectl exec -n boulder $BOULDER_POD -- ./test.sh --integration
```

## Verification

### Service Connectivity

```bash
# Check all services are running
kubectl get pods -n boulder

# Verify service endpoints
kubectl get services -n boulder

# Test database connectivity
kubectl exec -n boulder deployment/boulder-monolith -- mysql -h bmysql -u root -psecret -e "SHOW DATABASES;"

# Test Redis connectivity
kubectl exec -n boulder statefulset/bredis-1 -- redis-cli ping

# Test Consul connectivity
kubectl exec -n boulder deployment/boulder-monolith -- curl -s http://bconsul:8500/v1/status/leader
```

### Logs and Debugging

```bash
# View Boulder logs
kubectl logs -n boulder deployment/boulder-monolith

# View infrastructure service logs
kubectl logs -n boulder statefulset/bmysql
kubectl logs -n boulder statefulset/bredis-1
kubectl logs -n boulder statefulset/bconsul

# Access Jaeger UI (port-forward)
kubectl port-forward -n boulder service/bjaeger 16686:16686
# Open http://localhost:16686
```

## Stability Policy

This test profile is **frozen for CI stability**. Changes are only permitted for:

1. **Bug Fixes**: Addressing test failures or deployment issues
2. **Security Updates**: Critical security patches for dependencies
3. **CI Requirements**: Updates required by CI infrastructure changes

All changes must:
- Maintain exact Docker Compose behavioral compatibility
- Pass all existing tests without modification
- Be reviewed for impact on CI stability

## Differences from Docker Compose

While maintaining behavioral compatibility, the implementation differs in:

1. **Orchestration**: Kubernetes instead of Docker Compose
2. **Service Discovery**: Kubernetes Services instead of Docker networks
3. **Configuration**: ConfigMaps for service configs (maintaining same values)
4. **Persistence**: StatefulSets for stateful services
5. **Health Checks**: Kubernetes probes instead of Docker health checks

## Troubleshooting

### Common Issues

| Issue | Solution |
|-------|----------|
| Services not starting | Check logs: `kubectl logs -n boulder <pod-name>` |
| Database connection errors | Verify bmysql is running: `kubectl get pod -n boulder bmysql-0` |
| Redis connection issues | Check Redis pods: `kubectl get statefulset -n boulder` |
| Tests failing | Compare with Docker Compose: ensure same config values |
| Network issues | Verify network policies: `kubectl get networkpolicies -n boulder` |

### Cleanup

```bash
# Delete test deployment
kubectl delete -k k8s/overlays/test/

# Or delete entire namespace
kubectl delete namespace boulder
```

## See Also

- [Boulder K8s Specification](../../docs/SPEC.md)
- [Phase 1 Completion Report](../../docs/PHASE1-COMPLETION-REPORT.md)
- [Main README](../../README.md)
- [Development Profile](../dev/README.md)
- [Staging Profile](../staging/README.md)