# Boulder Kubernetes Phase 1 Implementation

This directory contains the Kubernetes manifests for Phase 1 of Boulder's migration from Docker Compose to Kubernetes.

## Overview

Phase 1 focuses on migrating external dependencies (MariaDB, ProxySQL, Redis, Consul, Jaeger, PKIMetal) to Kubernetes while keeping Boulder services bundled in a single container, maintaining compatibility with the existing architecture.

The decision to embed the Kubernetes configuration within the Boulder repository (rather than maintaining a separate repository) is documented in [ADR-001-repo-structure](docs/ADR-001-repo-structure.md). This approach enables integrated development and simplifies the path to upstream contribution.

## Working with Multiple Worktrees

When working with multiple git worktrees of the Boulder repository, each worktree can run its own kind cluster to avoid conflicts. Use the `KIND_CLUSTER` environment variable to specify a unique cluster name:

```bash
# For branch b1
export KIND_CLUSTER=boulder-k8s-b1

# For main branch
export KIND_CLUSTER=boulder-k8s-main

# For feature branches
export KIND_CLUSTER=boulder-k8s-feature-xyz
```

All scripts (`k8s-up.sh`, `k8s-down.sh`, `tk8s.sh`, `tnk8s.sh`) respect this environment variable. If not set, they default to `boulder-k8s`.

### Resource Requirements

Running multiple kind clusters requires sufficient Docker resources. Recommended minimums per cluster:
- Memory: 8GB
- CPUs: 4

Adjust Docker Desktop settings accordingly when running multiple clusters simultaneously.

## Directory Structure

```
k8s/
├── namespaces/           # Namespace and RBAC configuration
├── external-services/    # External dependency manifests
│   ├── mariadb/         # Database backend
│   ├── proxysql/        # Database proxy
│   ├── redis/           # Rate limiting (2 instances)
│   ├── consul/          # Service discovery
│   ├── jaeger/          # Distributed tracing
│   └── pkimetal/        # Certificate transparency
├── boulder/             # Boulder application deployment
├── secrets/             # TLS certificates and secrets
├── networking/          # Network policies
└── kustomization.yaml   # Kustomize configuration
```

## Configuration Profiles

Boulder Kubernetes supports multiple configuration profiles via Kustomize overlays:

### Available Profiles

| Profile | Purpose | Namespace | Use Case |
|---------|---------|-----------|----------|
| **test** | CI parity with Docker Compose | boulder | Integration testing, CI/CD |
| **staging** | Progressive migration (Phases 2-6) | boulder-staging | Feature development |
| **dev** | Simplified local development | boulder | Local testing |
| **base** | Production configuration | boulder | Production deployment |

### Profile Usage

```bash
# Deploy test profile (Phase 1 CI parity)
kubectl apply -k k8s/overlays/test/

# Deploy staging profile (Phases 2-6 development)
kubectl apply -k k8s/overlays/staging/

# Deploy development profile (simplified)
kubectl apply -k k8s/overlays/dev/

# Deploy base/production
kubectl apply -k k8s/
```

### Test Execution with Profiles

Once profile support is added to test scripts:
```bash
./tk8s.sh --profile test      # Run tests in test profile
./tk8s.sh --profile staging   # Run tests in staging profile
```

For detailed profile information, see:
- Test: `k8s/overlays/test/README.md`
- Staging: `k8s/overlays/staging/README.md`
- Dev: `k8s/overlays/dev/README.md`

## Prerequisites

1. Kubernetes cluster (1.24+)
2. kubectl configured
3. Kustomize or kubectl with kustomize support
4. Boulder Docker image: `letsencrypt/boulder-tools:latest`

## Deployment Instructions

### 1. Prepare Secrets

Before deploying, update the placeholder secrets with actual values:

```bash
# Generate base64-encoded secrets from Boulder test certificates
cat test/certs/ipki/minica.pem | base64
cat test/certs/ipki/redis/cert.pem | base64
cat test/certs/ipki/redis/key.pem | base64
cat test/certs/ipki/consul.boulder/cert.pem | base64
cat test/certs/ipki/consul.boulder/key.pem | base64
```

Update `k8s/secrets/tls-secrets.yaml` with the encoded values.

### 2. Deploy with Kustomize

```bash
# Deploy all resources
kubectl apply -k k8s/

# Or preview first
kubectl kustomize k8s/

# Watch deployment progress
kubectl -n boulder get pods -w
```

### 3. Verify Deployment

```bash
# Check all pods are running
kubectl -n boulder get pods

# Check services
kubectl -n boulder get svc

# View logs
kubectl -n boulder logs -f deployment/boulder

# Access Boulder WFE
kubectl -n boulder port-forward deployment/boulder 4001:4001
curl http://localhost:4001/directory
```

## Service Endpoints

After deployment, services are available at:

- **Boulder WFE**: `boulder-wfe:4001`
- **Boulder SFE**: `boulder-wfe:4003`
- **MariaDB**: `boulder-mysql:3306`
- **ProxySQL**: `boulder-proxysql:6033`
- **Redis 1**: `boulder-redis-1-0.boulder-redis-1:4218`
- **Redis 2**: `boulder-redis-2-0.boulder-redis-2:4218`
- **Consul**: `boulder-consul:8500`
- **Jaeger UI**: `boulder-jaeger:16686`
- **PKIMetal**: `boulder-pkimetal:80`

## Testing

```bash
# Port-forward to access Boulder
kubectl -n boulder port-forward deployment/boulder 4001:4001

# Run integration tests (from Boulder repository)
./t.sh --integration
```

## Monitoring

```bash
# View Jaeger UI
kubectl -n boulder port-forward svc/boulder-jaeger 16686:16686
# Open http://localhost:16686

# View Consul UI
kubectl -n boulder port-forward svc/boulder-consul 8500:8500
# Open http://localhost:8500

# View ProxySQL admin
kubectl -n boulder port-forward svc/boulder-proxysql 6080:6080
# Open http://localhost:6080 (stats:stats)
```

## Troubleshooting

### Common Issues

1. **Pods stuck in Init state**
   - Check dependency services are running
   - Review init container logs: `kubectl -n boulder logs <pod> -c wait-for-dependencies`

2. **Database connection failures**
   - Verify ProxySQL is running and configured
   - Check MariaDB is accessible through ProxySQL

3. **Redis TLS errors**
   - Ensure TLS secrets are properly configured
   - Verify Redis configuration matches certificate paths

4. **Consul service discovery issues**
   - Check Consul is running and accessible
   - Verify DNS resolution: `kubectl -n boulder exec deployment/boulder -- nslookup consul.service.consul`

### Debug Commands

```bash
# Get pod details
kubectl -n boulder describe pod <pod-name>

# View container logs
kubectl -n boulder logs <pod-name> -c <container-name>

# Execute commands in Boulder container
kubectl -n boulder exec -it deployment/boulder -- bash

# Check service endpoints
kubectl -n boulder get endpoints

# View events
kubectl -n boulder get events --sort-by='.lastTimestamp'
```

## Cleanup

```bash
# Remove all resources
kubectl delete -k k8s/

# Or delete namespace (removes everything)
kubectl delete namespace boulder
```

## Next Steps (Phase 2)

Phase 2 will involve:
- Splitting Boulder services into individual deployments
- Migrating from Consul to Kubernetes-native service discovery
- Implementing proper configuration management with ConfigMaps
- Adding horizontal pod autoscaling
- Implementing proper persistent storage for production use

## Notes

- This Phase 1 implementation maintains maximum compatibility with the existing Docker Compose setup
- All external services use their original configurations adapted for Kubernetes
- Boulder container runs all services via `start.py` as in Docker Compose
- Network policies provide basic isolation similar to Docker networks
- Secrets are placeholders and must be replaced with actual values for testing