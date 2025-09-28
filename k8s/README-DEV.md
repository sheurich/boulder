# Boulder Kubernetes Development Environment

## Quick Start for Development

The development overlay removes TLS requirements and simplifies the configuration for local testing with kind or minikube.

### Deploy to kind

```bash
# Create kind cluster
kind create cluster --config k8s/kind-config-simple.yaml

# Load Boulder image into kind
kind load docker-image letsencrypt/boulder-tools:latest --name boulder-k8s

# Deploy with development overlay
kubectl apply -k k8s/overlays/dev/

# Check pod status
kubectl -n boulder get pods -w

# Port-forward services
kubectl -n boulder port-forward deployment/boulder 8080:8080
kubectl -n boulder port-forward service/boulder-jaeger 16686:16686
```

### What the Dev Overlay Changes

1. **Removes TLS Requirements**:
   - Redis runs on standard port 6379 without TLS
   - Consul runs without mTLS certificates
   - Simplified authentication

2. **Simplified Boulder Container**:
   - Uses local image (`imagePullPolicy: Never`)
   - Runs development HTTP server for testing
   - Reduced init container checks

3. **Faster Startup**:
   - No certificate generation
   - Minimal dependency checks
   - Simplified configurations

### Production vs Development

| Component | Production | Development |
|-----------|------------|-------------|
| Redis | TLS on port 4218 | Plain on port 6379 |
| Consul | mTLS with certificates | Plain HTTP |
| Boulder | Full start.py | Simple HTTP server |
| Secrets | Real certificates | Empty/placeholder |
| Image Pull | Always | Never (local) |

### Testing Connectivity

```bash
# Test service connectivity from Boulder pod
kubectl -n boulder exec deployment/boulder -- python3 -c "
import socket
for service in [('boulder-mysql', 3306), ('boulder-redis-1', 6379), ('boulder-consul', 8500)]:
    s = socket.socket()
    result = s.connect_ex(service)
    print(f'{service[0]}:{service[1]} - {'OK' if result == 0 else f'FAILED ({result})'})
    s.close()
"
```

## Why This Structure?

The development overlay pattern allows us to:
1. Keep production configurations intact in the base manifests
2. Override only what's needed for development
3. Test the same architecture with simplified requirements
4. Easily switch between dev and production configurations

## Next Steps for Production

To use the production configuration:
1. Generate real TLS certificates
2. Update secrets in `k8s/secrets/tls-secrets.yaml`
3. Deploy without the dev overlay: `kubectl apply -k k8s/`
4. Configure Boulder with actual ACME settings

## Working with Multiple Profiles

The Boulder Kubernetes migration uses Kustomize overlays to support multiple configuration profiles:

### Profile Overview

```
k8s/
├── base/                    # Production configuration
├── overlays/
│   ├── test/               # Phase 1: Docker Compose parity
│   ├── staging/            # Phases 2-6: Progressive migration
│   └── dev/                # Simplified development
```

### Switching Between Profiles

```bash
# Deploy test profile (CI testing)
kubectl apply -k k8s/overlays/test/
export BOULDER_K8S_PROFILE=test

# Deploy staging profile (feature development)
kubectl apply -k k8s/overlays/staging/
export BOULDER_K8S_PROFILE=staging

# Deploy dev profile (local development)
kubectl apply -k k8s/overlays/dev/
export BOULDER_K8S_PROFILE=dev
```

### Profile Characteristics

| Aspect | Test | Staging | Dev |
|--------|------|---------|-----|
| **Boulder** | Monolith | Progressive split | Monolith |
| **Config** | test/config | ConfigMaps | Simplified |
| **Security** | Full TLS | Full TLS | No TLS |
| **Namespace** | boulder | boulder-staging | boulder |
| **Purpose** | CI parity | Feature development | Local dev |

### Development Workflow

1. **Local Development**: Use dev profile for quick iteration
2. **Feature Development**: Use staging profile for Phases 2-6 work
3. **CI Validation**: Use test profile to ensure no regressions
4. **Production Prep**: Use base configuration with proper secrets

### Troubleshooting Profile Issues

| Issue | Solution |
|-------|----------|
| Wrong profile deployed | Check namespace: `kubectl get ns` |
| Services not found | Verify profile: `kubectl get deploy -n <namespace> -l app.kubernetes.io/profile` |
| Config mismatch | Check ConfigMaps: `kubectl get cm -n <namespace>` |
| Can't switch profiles | Delete old namespace first: `kubectl delete ns <namespace>` |