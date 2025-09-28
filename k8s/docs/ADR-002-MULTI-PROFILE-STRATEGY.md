# ADR-002: Multi-Profile Kubernetes Configuration Strategy

## Status
Accepted

## Context

Boulder's migration from Docker Compose to Kubernetes follows a phased approach outlined in the Boulder K8s Specification. Phase 1 focuses on achieving CI parity by replicating the existing Docker Compose test environment exactly, while Phases 2-6 progressively decompose Boulder into cloud-native services suitable for production deployment.

To support this migration strategy, we need:
1. A test environment that maintains exact Docker Compose compatibility for CI
2. A staging environment that can evolve through Phases 2-6 without disrupting tests
3. The ability to run both environments in parallel for validation
4. Clear separation of concerns between test stability and feature development

## Decision

We will implement a multi-profile configuration strategy using Kustomize overlays to maintain separate Kubernetes environments:

### Profile Structure

```
k8s/
├── base/                    # Common resources shared across profiles
│   ├── kustomization.yaml
│   ├── boulder/
│   ├── external-services/
│   └── networking/
├── overlays/
│   ├── test/               # Phase 1: Docker-compose parity
│   │   ├── kustomization.yaml
│   │   ├── boulder-monolith.yaml
│   │   └── patches/
│   ├── staging/            # Phases 2-6: Progressive decomposition
│   │   ├── kustomization.yaml
│   │   └── patches/
│   └── dev/                # Existing simplified development
│       ├── kustomization.yaml
│       └── patches/
```

### Test Profile
- **Purpose**: Exact replication of Docker Compose behavior for CI parity
- **Characteristics**:
  - Boulder runs as a monolithic container with `startservers.py`
  - External services configured identically to Docker Compose
  - Uses `test/config` directory configurations
  - Network aliases match Docker Compose service names
  - No service splitting or Kubernetes-native features
- **Stability**: Frozen once Phase 1 is complete; changes only for bug fixes

### Staging Profile
- **Purpose**: Progressive implementation of Phases 2-6 toward production readiness
- **Characteristics**:
  - Phase 2: Gradual service splitting into groups
  - Phase 3: Kubernetes-native initialization (Jobs/InitContainers)
  - Phase 4: ConfigMaps and Secrets management
  - Phase 5: Observability integration (Prometheus/Jaeger)
  - Phase 6: Production features (HPAs, service mesh, multi-region)
- **Evolution**: Continuously evolving through the migration phases

### Profile Selection

Scripts will support profile selection via command-line flags:
```bash
# Test environment (Phase 1 parity)
kubectl apply -k k8s/overlays/test/
./tk8s.sh --profile test

# Staging environment (Phases 2-6)
kubectl apply -k k8s/overlays/staging/
./tk8s.sh --profile staging

# Default remains test for backward compatibility
./tk8s.sh  # Uses test profile
```

## Consequences

### Positive
- **Parallel Development**: Test and staging can evolve independently
- **CI Stability**: Test profile ensures CI continues working during staging development
- **Clear Boundaries**: Explicit separation between stable test and experimental staging
- **Progressive Migration**: Features can be validated in staging before promotion
- **Risk Mitigation**: Staging experiments don't break existing tests

### Negative
- **Duplication**: Some configuration duplication between profiles
- **Maintenance**: Two profiles to maintain instead of one
- **Complexity**: Developers need to understand which profile to use when

### Neutral
- **Learning Curve**: Team needs to understand Kustomize overlay patterns
- **Documentation**: Requires clear documentation of profile purposes and usage

## Implementation Details

### Configuration Differences

| Aspect | Test Profile | Staging Profile |
|--------|-------------|-----------------|
| Boulder Services | Single monolith pod | Split into service groups |
| Config Source | Mounted from test/config | ConfigMaps/Secrets |
| Initialization | bsetup in container | K8s Jobs/InitContainers |
| Service Discovery | Hardcoded aliases | K8s Services + gradual Consul migration |
| Database Init | startservers.py | K8s Job with migrations |
| Networking | Single flat network | Multi-network segmentation |
| Observability | Basic logs | Prometheus/Jaeger/mesh |
| Scaling | Fixed single pod | HPAs for stateless services |

### Namespace Strategy

Profiles can run in separate namespaces for complete isolation:
- Test: `boulder-test` or `boulder` (default)
- Staging: `boulder-staging`
- Dev: `boulder-dev`

### CI Integration

CI workflows will:
1. Run test profile for all PR validation
2. Optionally run staging profile for feature branches
3. Compare results between profiles when validating staging changes
4. Use environment variables to control profile selection:
   ```bash
   BOULDER_K8S_PROFILE=test    # or staging
   ```

## References

- Boulder K8s Specification (k8s/docs/SPEC.md)
- Phase 1 Completion Report (k8s/docs/PHASE1-COMPLETION-REPORT.md)
- Kustomize Documentation: https://kustomize.io/
- Let's Encrypt Staging Environment: https://letsencrypt.org/docs/staging-environment/