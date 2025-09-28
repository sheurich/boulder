# Boulder Kubernetes Scripts

This directory contains operational scripts for managing Boulder's Kubernetes deployment.

## Available Scripts

### k8s-up.sh
**Purpose**: Set up and configure a complete Boulder Kubernetes environment

**Usage**:
```bash
# Default setup (test profile)
./k8s/scripts/k8s-up.sh

# With specific profile (once implemented)
./k8s/scripts/k8s-up.sh --profile staging

# Custom namespace
./k8s/scripts/k8s-up.sh --namespace boulder-custom

# Custom cluster name
./k8s/scripts/k8s-up.sh --cluster-name my-boulder
```

**Features**:
- Creates kind cluster if not exists
- Loads Boulder Docker image into cluster
- Deploys all infrastructure services
- Initializes database
- Waits for all services to be ready
- Validates deployment

### k8s-down.sh
**Purpose**: Tear down Boulder Kubernetes environment and clean up resources

**Usage**:
```bash
# Default teardown
./k8s/scripts/k8s-down.sh

# Custom cluster
./k8s/scripts/k8s-down.sh --cluster-name my-boulder
```

**Features**:
- Deletes all Boulder resources
- Optionally deletes kind cluster
- Cleans up persistent volumes
- Removes namespace

## Test Runners

### tk8s.sh
**Purpose**: Run Boulder tests in Kubernetes (standard config)

**Usage**:
```bash
# Run all tests
./tk8s.sh

# Run with specific profile (to be implemented)
./tk8s.sh --profile test
./tk8s.sh --profile staging

# Run specific test types
./tk8s.sh --unit
./tk8s.sh --integration
./tk8s.sh --lints

# Advanced options
./tk8s.sh --unit --verbose
./tk8s.sh --filter TestName
./tk8s.sh --coverage
```

### tnk8s.sh
**Purpose**: Run Boulder tests in Kubernetes (config-next)

**Usage**:
```bash
# Same options as tk8s.sh but with config-next
./tnk8s.sh
./tnk8s.sh --unit
./tnk8s.sh --profile staging  # To be implemented
```

## Profile Support (To Be Implemented)

The scripts will support multiple configuration profiles:

### Test Profile
```bash
# Deploy test environment
./k8s/scripts/k8s-up.sh --profile test

# Run tests in test profile
./tk8s.sh --profile test
```

**Characteristics**:
- Namespace: `boulder`
- Configuration: `test/config`
- Purpose: CI/CD compatibility

### Staging Profile
```bash
# Deploy staging environment
./k8s/scripts/k8s-up.sh --profile staging

# Run tests in staging profile
./tk8s.sh --profile staging
```

**Characteristics**:
- Namespace: `boulder-staging`
- Configuration: Progressive through phases
- Purpose: Feature development

### Development Profile
```bash
# Deploy dev environment
./k8s/scripts/k8s-up.sh --profile dev

# Run tests in dev profile
./tk8s.sh --profile dev
```

**Characteristics**:
- Namespace: `boulder`
- Configuration: Simplified, no TLS
- Purpose: Local development

## Environment Variables

### Configuration
- `BOULDER_CONFIG_DIR`: Config directory (default: `test/config`)
- `BOULDER_K8S_PROFILE`: Active profile (test|staging|dev)
- `KIND_CLUSTER`: Kind cluster name (default: `boulder-k8s`)
- `K8S_NAMESPACE`: Kubernetes namespace (default: `boulder`)

### Test Options
- `BOULDER_TOOLS_TAG`: Docker image tag (default: `latest`)
- `VERBOSE`: Enable verbose output
- `RACE`: Enable race detection
- `COVERAGE`: Enable coverage collection
- `COVERAGE_DIR`: Coverage output directory

## Common Workflows

### Initial Setup
```bash
# 1. Create cluster and deploy Boulder
./k8s/scripts/k8s-up.sh

# 2. Run tests
./tk8s.sh

# 3. Clean up
./k8s/scripts/k8s-down.sh
```

### Development Iteration
```bash
# 1. Make code changes
vim some/file.go

# 2. Rebuild and reload
docker build -t boulder:latest .
kind load docker-image boulder:latest --name boulder-k8s
kubectl rollout restart deployment/boulder-monolith -n boulder

# 3. Run specific tests
./tk8s.sh --unit --filter TestMyChange
```

### Profile Testing
```bash
# Test in CI environment
./tk8s.sh --profile test

# Test in staging environment
./tk8s.sh --profile staging

# Compare results
diff test-results.txt staging-results.txt
```

## Troubleshooting

### Script Issues

| Problem | Solution |
|---------|----------|
| Cluster not found | Run `k8s-up.sh` first |
| Image not found | Check `docker images` and `kind load` |
| Tests fail | Check logs: `kubectl logs -n boulder` |
| Port conflicts | Check existing port forwards |
| Permission denied | Ensure scripts are executable: `chmod +x` |

### Profile Issues

| Problem | Solution |
|---------|----------|
| Wrong namespace | Check `kubectl config get-contexts` |
| Profile not found | Verify overlay exists in `k8s/overlays/` |
| Config mismatch | Check ConfigMaps in namespace |
| Services not ready | Wait or check pod status |

### Debugging Commands

```bash
# Check cluster status
kubectl cluster-info --context kind-boulder-k8s

# View all Boulder resources
kubectl get all -n boulder

# Check pod logs
kubectl logs -n boulder deployment/boulder-monolith

# Describe failing pod
kubectl describe pod -n boulder <pod-name>

# Check service endpoints
kubectl get endpoints -n boulder

# View events
kubectl get events -n boulder --sort-by='.lastTimestamp'
```

## Best Practices

1. **Always verify cluster state** before running tests
2. **Use appropriate profile** for your use case
3. **Clean up resources** when done
4. **Monitor logs** during test execution
5. **Document profile changes** in PHASE-STATUS.md
6. **Test profile switching** to ensure isolation
7. **Validate both tk8s.sh and tnk8s.sh** when making changes

## Future Enhancements

- [ ] Profile auto-detection based on context
- [ ] Parallel test execution across profiles
- [ ] Automated performance comparison
- [ ] Profile-specific health checks
- [ ] Integration with CI/CD pipelines
- [ ] Remote cluster support
- [ ] Multi-cluster deployments

## See Also

- [Boulder K8s README](../README.md)
- [Test Profile](../overlays/test/README.md)
- [Staging Profile](../overlays/staging/README.md)
- [Development Profile](../overlays/dev/README.md)
- [Phase Status](../overlays/staging/PHASE-STATUS.md)