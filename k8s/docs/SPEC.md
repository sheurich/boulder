---

# Boulder on Kubernetes: Specification

## Purpose

This document describes how to run the **Boulder** ACME server and its supporting services on a Kubernetes cluster, evolving from the existing Docker Compose–based integration test environment. The goal is to provide a repeatable, self-contained Kubernetes deployment that preserves the behaviour of the current integration tests while enabling gradual migration of Boulder’s components into native Kubernetes objects.

## Migration Strategy

1. **Phase 0: Analysis and Documentation**

   - Use automated discovery tools to map the existing Docker Compose setup:
     - Extract service dependency graph from `startservers.py`
     - Document all network communication patterns
     - Map configuration files and environment variables
     - Generate network topology diagrams
   - Deliverables:
     - Complete service dependency graph with circular dependency identification
     - Network communication matrix showing all service interactions
     - Configuration inventory with all 30+ config files mapped
     - Service startup sequence diagram

2. Phase 1: Drop-in CI Parity on Kubernetes (kind)

Goal

Run the existing Boulder CI integration environment unchanged in behavior on a local Kubernetes cluster (kind), acting as a drop-in replacement for Docker Compose. Boulder remains a single container running test.sh/startservers.py; Kubernetes only replaces Compose’s orchestration of non-Boulder services.

Non-Goals (Phase 1)
	•	No splitting of Boulder services.
	•	No new features (mesh, HPAs, multi-network segmentation, etc.).
	•	No changes to test harness semantics or expected outputs.
	•	No production-grade HA/DR; this is CI parity, not prod hardening.

Success Criteria / Acceptance Tests
	1.	Test Parity: The full CI suite passes with identical pass/fail outcomes as Compose on the same commit.
	2.	No Harness Changes: CI entry points (test.sh, t.sh, v2_integration.py) are invoked the same way. Any Kubernetes glue must be behind those entry points (e.g., a wrapper that runs the same commands inside the Boulder pod).
	3.	Network/Port Parity: Services are reachable under the same hostnames/ports the tests expect (see “Service Naming Parity”).
	4.	Image & Config Parity: All images/configs/fixtures are the same as Compose (pinned tags or digests), producing identical behavior.
	5.	Parallel CI Mode: CI can run Compose and K8s jobs side-by-side; a parity checker confirms identical results (exit codes, key log markers).

Deliverables
	•	k8s/ directory (in-repo) containing:
	•	cluster/kind-config.yaml (kind cluster def; pinned Kubernetes version).
	•	manifests/ for external deps (MariaDB, Redis×2, Consul, ProxySQL, any test servers used today).
	•	services/ shim Services that preserve Compose hostnames/ports.
	•	scripts/:
	•	k8s-up.sh / k8s-down.sh (create/destroy kind, apply manifests).
	•	tk8s.sh / tnk8s.sh thin wrapper that runs the existing test.sh (with `config` or `config-next`) inside the Boulder pod (e.g., kubectl exec), so call sites don’t change.
	•	README.md with one-command flows: make kind-up, make k8s-ci, make clean.
	•	ADR-001 (repo structure) and a brief Phase-1 scope note in k8s/README.md.
	•	CI workflow that can toggle orchestration via BOULDER_CI_ORCH={compose|k8s} and optionally run both then compare.

Cluster & Tooling Constraints
	•	Cluster: kind (pinned version), single node, default CNI.
	•	LoadBalancer: Not required in Phase 1. Use ClusterIP + NodePort/extraPortMappings only if the tests truly require host-reachable ports; otherwise keep traffic in-cluster.
	•	Ingress/mesh: Not used in Phase 1.
	•	Kubernetes version: Pin (e.g., 1.29/1.30) to avoid CI drift.

Service Naming Parity (Critical)

To keep the test harness untouched:
	•	Create Kubernetes Services with the exact DNS names the tests/Compose expect (e.g., if Compose used mariadb, expose a Service named mariadb on the same port).
	•	Where Compose used multiple ports per service, mirror them on the same Service if possible; otherwise create clearly named companion Services.
	•	If any component relies on localhost ports, either:
	•	run the tests inside the Boulder pod (preferred), or
	•	use kind extraPortMappings or hostPort for those specific cases (documented in kind-config.yaml).

Images, Config, and Secrets
	•	Use the exact images as Compose (prefer digest pins).
	•	Mount the same config files and test fixtures (ConfigMaps/Secrets only as a transport; no format changes).
	•	Secrets in Phase 1 are test-only materials already present in the repo (e.g., /test/certs/ipki/), mounted as Kubernetes Secrets.
	•	Environment parity: propagate the same env vars the scripts expect; avoid renaming.

External Dependencies (K8s-managed; Boulder stays monolith)
	•	MariaDB: StatefulSet + PVC; same schema/init path as Compose.
	•	Redis (×2): Deployments (PVC only if Compose used persistence).
	•	Consul, ProxySQL: Deployments/StatefulSets mirroring Compose configs.
	•	Any test servers (CT log, AIA, S3 mock, challenge server, etc.) that the current CI actually uses are included. (If not used in Phase 1 CI, defer to later phases.)

Note: Keep initialization minimal—only what Compose already does. If Compose runs inline scripts, run the same scripts via a Job/InitContainer, not a re-imagined flow.

Orchestration & Startup
	•	Keep Boulder as one container launched exactly as today with startservers.py and test.sh.
	•	External deps get basic readiness probes approximating Compose’s “up” condition (e.g., TCP health or simple gRPC health where available). No aggressive timeouts yet.
	•	No change to Boulder’s startup ordering logic—Kubernetes just guarantees deps are reachable; startservers.py remains the source of truth.

Test Execution Path
	•	`tk8s.sh` creates the cluster (if needed), applies manifests, waits for readiness, then:
      •	`kubectl exec <boulder-pod> -- /path/to/test.sh` (or equivalent).
   •  This mirrors the existing `t.sh` → `test.sh` path.
      This preserves the test process environment and filesystem expectations.
	•	Log/Artifact collection: kubectl logs and kubectl cp helpers matching Compose’s log bundle output layout.

Parity Checker
	•	GitHub Actions can run both Compose and K8s jobs on the same PR/commit in parallel.

   A tiny script compares:
      •	Exit code,
      •	Count of passed/failed tests,
      •	Presence of known pass/fail markers in logs.
   Used by CI when running Compose vs K8s in parallel on the same commit.

Risks & Mitigations
	•	DNS/name mismatches → solved by Service naming parity and running tests inside the Boulder pod.
	•	Port exposure in kind → avoid unless truly required; otherwise use extraPortMappings for a few well-known ports.
	•	Config drift → pin images/config; add a parity check job to catch divergences early.

Milestones (Checklist)
	•	k8s/ scaffolding (cluster config, manifests, scripts, README).
	•	Services mirror Compose hostnames/ports.
	•	External deps healthy; Boulder pod boots and runs tests via kubectl exec.
	•	CI job for K8s path; optional parallel parity job.
	•	Green CI parity on a representative PR set.

## Profile Management Strategy

To support parallel development of test stability and staging evolution, the implementation uses Kustomize overlays to maintain separate Kubernetes profiles:

### Test Profile (Phase 1)
- **Purpose**: Exact replication of Docker Compose behavior for CI parity
- **Location**: `k8s/overlays/test/`
- **Characteristics**:
  - Boulder runs as monolithic container with startservers.py
  - External services configured identically to Docker Compose
  - Uses test/config directory configurations
  - Network aliases match Docker Compose service names
  - No service splitting or K8s-native features
- **Stability**: Frozen once Phase 1 complete; changes only for bug fixes

### Staging Profile (Phases 2-6)
- **Purpose**: Progressive implementation of cloud-native features
- **Location**: `k8s/overlays/staging/`
- **Evolution Path**:
  - Phase 2: Service splitting into groups
  - Phase 3: K8s-native initialization (Jobs/InitContainers)
  - Phase 4: ConfigMaps and Secrets management
  - Phase 5: Observability (Prometheus/Jaeger)
  - Phase 6: Production features (HPAs, mesh, multi-region)
- **Namespace**: boulder-staging for isolation

### Development Profile (Existing)
- **Purpose**: Simplified local development
- **Location**: `k8s/overlays/dev/`
- **Features**: No TLS, local images, fast iteration

### Profile Selection
```bash
# Test environment (CI parity)
kubectl apply -k k8s/overlays/test/

# Staging environment (feature development)
kubectl apply -k k8s/overlays/staging/

# Development (local testing)
kubectl apply -k k8s/overlays/dev/
```

See ADR-002-MULTI-PROFILE-STRATEGY.md for detailed rationale.

3. **Phase 2: Split Boulder Services with Service Grouping**

   - Instead of splitting all services at once, use a phased approach:
     - **Group 1**: Core storage and validation (SA instances, Remote VAs)
     - **Group 2**: Certificate operations (CA instances with SCT providers)
     - **Group 3**: Registration and workflow (RA instances, Publishers)
     - **Group 4**: Frontend services (WFE2, SFE, Nonce services)
     - **Group 5**: Administrative services (CRL, bad-key-revoker, etc.)
   - Deploy service groups together initially, then gradually separate based on stability.
   - Use Kubernetes **readiness probes** and **liveness probes** to replicate the startup sequencing currently managed by `startservers.py`.
   - Replace the topological sort logic with Kubernetes service discovery and dependency handling (Pods become "ready" only after their health checks succeed).

4. **Phase 3: Kubernetes-native Initialization**

   - Replace Boulder’s `bsetup` stage with a Kubernetes **Job**, which runs once per cluster bootstrap and generates required test certificates and data.

     - Kubernetes Jobs are designed to “run to completion” exactly once.

   - Where initialization is required on every Pod startup (not cluster-wide), use **Init Containers**:

     - Init containers run to completion before the main app container starts.

5. **Phase 4: Config & Secrets Management**

   - Store Boulder configuration in **ConfigMaps**.
   - Handle keys, certs, and other sensitive data with **Kubernetes Secrets**.
   - Mount these into Pods at runtime.

6. **Phase 5: Observability and Scaling**

   - Integrate with Kubernetes logging (stdout/stderr collection).
   - Add Prometheus metrics scraping from Boulder services.
   - Integrate Jaeger distributed tracing with OpenTelemetry collectors.
   - Horizontal Pod Autoscalers (HPAs) can later be introduced for stateless services like RA or VA.
   - Consider service mesh (Istio/Linkerd) for advanced observability and traffic management.

## Deployment Details

### External Dependencies

- **Database (MariaDB)**: StatefulSet with PersistentVolumeClaims for durable storage.
- **Consul**: Deployment or StatefulSet, Service for discovery. Retained in early phases for compatibility with Boulder configs.
- **ProxySQL**: Deployment, Service for database connection pooling.
- **Redis (2 instances)**: Deployments for rate limiting backend. Boulder requires two separate Redis instances for redundancy.
- **Jaeger**: Deployment, Service for distributed tracing across Boulder microservices.
- **PKIMetal**: Deployment, Service for certificate validation (required for Boulder startup).
- **Challenge Test Server**: Deployment with multiple network interfaces for ACME challenge validation (HTTP-01, DNS-01, TLS-ALPN-01).
- **CT Log Test Server**: Deployment, Service for Certificate Transparency log submission testing.
- **AIA Test Server**: Deployment, Service for Authority Information Access certificate validation.
- **S3 Test Server**: Deployment, Service for Certificate Revocation List (CRL) storage testing.
- **Pardot Test Server**: Deployment, Service for Salesforce/Pardot API integration testing.
- **Zendesk Test Server**: Deployment, Service for Zendesk API integration testing.

### Boulder Services

Boulder consists of 30+ service instances across multiple categories:

#### Core ACME Services (Multiple Instances)
- **Registration Authority (RA)**: 2 main instances + 2 SCT provider instances
  - `boulder-ra-1`, `boulder-ra-2` (ports 9394, 9494)
  - `boulder-ra-sct-provider-1`, `boulder-ra-sct-provider-2` (ports 9594, 9694)
- **Storage Authority (SA)**: 2 instances for database operations (ports 9395, 9495)
- **Certificate Authority (CA)**: 2 instances for certificate issuance (ports 9393, 9493)
- **Validation Authority (VA)**: 2 instances for domain validation (ports 9392, 9492)
- **Publisher**: 2 instances for CT log submission (ports 9391, 9491)

#### Remote Validation Authorities
- **Remote VAs**: 3 geographically distributed instances for multi-perspective validation
  - `remoteva-a`, `remoteva-b`, `remoteva-c` (ports 9397, 9498, 9499)

#### Frontend Services
- **Web Frontend (WFE2)**: ACME v2 protocol handler (port 4001/4431)
- **Self-Service Frontend (SFE)**: Account management interface (port 4003)

#### Nonce Services (Multi-Datacenter)
- **Taro DC**: 2 instances (`nonce-service-taro-1`, `nonce-service-taro-2`)
- **Zinc DC**: 1 instance (`nonce-service-zinc-1`)

#### Administrative Services
- **CRL Services**: `crl-storer`, `crl-updater`
- **Bad Key Revoker**: Automated weak key detection
- **Email Exporter**: Salesforce/Pardot integration
- **Log Validator**: CT log validation

#### Migration Strategy
- Initially: single Pod running Boulder as today, inside a container with `startservers.py`.
- Phase 2: Separate Deployments for each service instance listed above.
- Service objects expose gRPC/HTTP ports to other Boulder components.

### Initialization

#### Database Initialization

Boulder requires complex database setup with 4 databases and specific migrations:

**Options for Migration Management:**

1. **Kubernetes Job with ConfigMap** (Simplest, closest to Docker Compose)
   - **Pros**: Simple, version-controlled migrations, easy rollback
   - **Cons**: ConfigMap size limits for large migration sets
   - **Implementation**: Store migrations in ConfigMap, Job mounts and executes them

2. **Kubernetes Job with PersistentVolume** (Most robust)
   - **Pros**: No size limits, persistent migration history, supports large schemas
   - **Cons**: Requires PV provisioning, more complex state management
   - **Implementation**: Init container copies migrations to PV, Job executes from PV

3. **Operator-based Migration** (Most Kubernetes-native)
   - **Pros**: Declarative, automatic rollback, integrated with GitOps
   - **Cons**: Requires custom operator development or third-party tools
   - **Implementation**: CRD defines desired schema version, operator handles migration

**Recommendation**: Use Kubernetes Job with ConfigMap (Option 1) for Phase 1 as it's simplest and most similar to the current Docker Compose setup. Consider migrating to PersistentVolume (Option 2) in later phases if migration sets grow large, or to Operator-based (Option 3) for production deployments requiring GitOps integration.

**Database Setup Requirements:**
- Create databases: `boulder_sa_test`, `boulder_sa_integration`, `incidents_sa_test`, `incidents_sa_integration`
- Run migrations from `/sa/db/` directory in specific order
- Create database users with appropriate permissions
- Configure ProxySQL for connection pooling

#### Service Initialization

- **Cluster-wide bootstrap**: Kubernetes Job for `bsetup` certificate generation
- **Per-Pod setup**: Init Containers for ephemeral setup tasks
- **TLS certificates**: Mount from `/test/certs/ipki/` via Secrets

### Networking

Boulder requires three distinct network segments to properly isolate traffic:

#### Network Topology
- **Internal Network (bouldernet equivalent - 10.77.77.0/24)**:
  - All Boulder microservices communication via gRPC
  - Infrastructure services (MySQL, Redis, Consul, Jaeger)
  - Service discovery and health checks
  - Kubernetes implementation: Default cluster network with NetworkPolicies

- **Public Network 1 (publicnet equivalent - 64.112.117.0/25)**:
  - HTTP-01 challenge validation (port 80)
  - HTTPS redirect testing (port 443)
  - Simulates public internet for challenge responses
  - Kubernetes implementation: LoadBalancer service with specific external IP

- **Public Network 2 (publicnet2 equivalent - 64.112.117.128/25)**:
  - TLS-ALPN-01 challenge validation (port 443)
  - Separate from HTTP to avoid port conflicts
  - Integration test HTTP servers
  - Kubernetes implementation: Second LoadBalancer service with distinct external IP

#### Kubernetes Network Implementation
- **NetworkPolicies**: Enforce network segmentation between internal and public traffic
- **Multi-homed Pods**: Challenge Test Server pods need interfaces on all three networks
- **Service Mesh**: Consider Istio/Linkerd for advanced traffic management
- **DNS**: Services provide DNS-based discovery (e.g., `boulder-ra.default.svc.cluster.local`)
- **Ingress/LoadBalancer**: Expose ACME endpoints and challenge validators to external traffic

### Service Discovery

Boulder uses Consul SRV records for sophisticated service discovery with load balancing:

#### Migration Strategy

**Recommendation**: Keep Consul initially, gradually migrate to Kubernetes native discovery.

**Phase 1: Consul on Kubernetes** (Minimal changes)
- Deploy Consul as StatefulSet with persistent storage
- Services continue using `_service._tcp.service.consul` SRV records
- Minimal code changes required in Boulder
- Maintains existing load balancing and health checking

**Phase 2: Hybrid Discovery** (Gradual migration)
- Implement Kubernetes Services alongside Consul
- Use CoreDNS to bridge between Consul and Kubernetes DNS
- Gradually update service configurations to use Kubernetes endpoints

**Phase 3: Native Kubernetes** (Full migration)
- Replace Consul SRV lookups with Kubernetes headless Services
- Implement custom gRPC resolvers for Kubernetes endpoints
- Use Service topology hints for zone-aware routing

### Dependency Management

Boulder has complex service dependencies including a circular dependency between CA and RA:

#### Circular Dependency Resolution Options

1. **SCT Provider Pattern** (Minimal change from Docker Compose)
   - **Complexity**: Low - mirrors existing architecture
   - **Divergence**: None - exact same pattern as Docker Compose
   - **Implementation**: Deploy separate `ra-sct-provider` pods that only serve SCT endpoints
   - **Pros**: No code changes, proven pattern, maintains separation of concerns
   - **Cons**: Additional pod overhead

2. **Init Container Sequencing** (Kubernetes-native)
   - **Complexity**: Medium - requires careful orchestration
   - **Divergence**: Medium - different startup mechanism
   - **Implementation**: Init containers check service availability before main container starts
   - **Pros**: Kubernetes-native, clear dependency declaration
   - **Cons**: Doesn't truly resolve circular dependency, just delays it

3. **Lazy Connection with Backoff** (Code change required)
   - **Complexity**: High - requires Boulder code modifications
   - **Divergence**: High - changes core connection logic
   - **Implementation**: Services connect to dependencies on first use with exponential backoff
   - **Pros**: Eliminates startup ordering requirements
   - **Cons**: Requires code changes, potential latency on first requests

4. **Kubernetes Operators** (Advanced)
   - **Complexity**: Very High - requires custom operator development
   - **Divergence**: Very High - completely different orchestration model
   - **Implementation**: Custom operator manages Boulder deployment with state machine
   - **Pros**: Full control over startup sequence, advanced orchestration
   - **Cons**: Significant development effort, maintenance overhead

**Recommendation**: Use SCT Provider Pattern (Option 1) initially as it requires zero code changes and exactly mirrors the current Docker Compose behavior.

#### Startup Sequencing

Implement dependency-aware startup using:
- **Readiness probes** with gRPC health checks
- **Init containers** to verify dependent services are available
- **Pod disruption budgets** to maintain service availability during updates

### Health Checks and Probes

Boulder uses gRPC health check protocol with specific requirements:

#### gRPC Health Check Configuration

```yaml
readinessProbe:
  exec:
    command: ["/boulder/health-checker", "-config", "/config/health-checker.json"]
  initialDelaySeconds: 10
  periodSeconds: 2
  timeoutSeconds: 10
  successThreshold: 1
  failureThreshold: 50  # 100-second total timeout (50 * 2s)

livenessProbe:
  grpc:
    port: 9393
    service: "ca.CertificateAuthority"  # Service-specific health check
  initialDelaySeconds: 30
  periodSeconds: 10
```

**Requirements:**
- Custom health-checker binary for complex health validation
- TLS with host override for certificate validation
- Service-specific health endpoints (e.g., `sa.StorageAuthority`)
- 100-second timeout for service availability during startup

### Configuration Management

Boulder's configuration involves 30+ service-specific JSON files:

#### Configuration Strategy

**ConfigMaps for Service Configs:**
- One ConfigMap per service type (e.g., `boulder-ra-config`, `boulder-ca-config`)
- Mount at `/config/` in containers
- Version configs with labels for rollback capability

**Secrets for Sensitive Data:**
- TLS certificates from `/test/certs/ipki/`
- Database credentials
- API keys for external services

**Environment Variables:**
- `BOULDER_CONFIG_DIR` for config directory override
- `FAKE_DNS` for DNS resolution override
- Feature flags per service

### Stateful Services Considerations

Some Boulder services maintain state that requires special handling:

#### Stateful Components

1. **Nonce Services**
   - Maintain nonce state with prefix-based routing
   - Use StatefulSet with stable network identities
   - Configure anti-affinity for datacenter distribution

2. **Rate Limiting (Redis)**
   - Two Redis instances with consistent hashing
   - StatefulSet with persistent volumes
   - Redis Cluster or Sentinel for HA

3. **Database Connections (ProxySQL)**
   - Connection pool state
   - StatefulSet with connection persistence
   - Session affinity for connection reuse

#### Implementation Recommendations

- Use **StatefulSets** for services requiring stable identities
- Configure **PersistentVolumes** for state that must survive pod restarts
- Implement **session affinity** where connection state matters
- Use **pod disruption budgets** to prevent data loss during updates

## Testing Strategy

Ensure Boulder's comprehensive test suite works with Kubernetes deployment:

### Running Integration Tests

1. **Test Environment Setup**
   - Deploy test instance of Boulder on Kubernetes
   - Use separate namespace (e.g., `boulder-test`)
   - Configure test-specific network policies

2. **Test Execution**
   - Modify `t.sh` wrapper to target Kubernetes deployment
   - Use `kubectl exec` for running tests inside cluster
   - Port-forward services for external test execution

3. **Test Data Management**
   - Use Kubernetes Jobs for `bsetup` test data generation
   - ConfigMaps for test configuration
   - Temporary PVs for test artifacts

### CI/CD Pipeline Modifications

1. **Build Pipeline**
   - Build Docker images with version tags
   - Push to container registry
   - Update Kubernetes manifests with new image versions

2. **Deployment Pipeline**
   - Use Helm or Kustomize for environment-specific configs
   - Implement blue-green or canary deployments
   - Automated rollback on health check failures

3. **Test Pipeline**
   - Spin up ephemeral Kubernetes cluster (e.g., kind, k3s)
   - Deploy Boulder and run full test suite
   - Collect logs and metrics for analysis
   - Tear down cluster after tests

4. **Integration with Existing Tests**
   - Adapt `test.sh` to work with Kubernetes endpoints
   - Update `v2_integration.py` to use Kubernetes service discovery
   - Modify challenge test client to work with LoadBalancer IPs

## Roadmap

- **Short term**:

  - Port external dependencies into Kubernetes.
  - Run Boulder monolithically as in Docker Compose.

- **Medium term**:

  - Split Boulder into native Deployments with probes.
  - Replace Consul gradually with Kubernetes DNS.

- **Long term**:

  - Add scaling and observability.
  - Transition Boulder integration tests to run directly inside Kubernetes CI pipelines.
