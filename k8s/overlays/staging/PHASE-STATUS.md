# Boulder Kubernetes Migration - Staging Phase Status

## Overview

This document tracks the progress of the Boulder Kubernetes staging environment through Phases 2-6 of the migration. The staging overlay serves as the development and validation environment for new Kubernetes-native features before they are promoted to production.

## Phase Status Summary

| Phase | Description | Status | Start Date | Target Date | Completion |
|-------|-------------|--------|------------|-------------|------------|
| **Phase 2** | Service Splitting | 🔴 Not Started | - | TBD | 0% |
| **Phase 3** | K8s-native Initialization | 🔴 Not Started | - | TBD | 0% |
| **Phase 4** | ConfigMaps/Secrets Management | 🔴 Not Started | - | TBD | 0% |
| **Phase 5** | Observability & Monitoring | 🔴 Not Started | - | TBD | 0% |
| **Phase 6** | Production Features | 🔴 Not Started | - | TBD | 0% |

**Legend**: 🔴 Not Started | 🟡 In Progress | 🟢 Complete | ⚠️ Blocked

---

## Phase 2: Service Splitting

### Goal
Split the Boulder monolith into separate Kubernetes Deployments for each service component.

### Status: 🔴 Not Started

### Service Groups

#### Group 1: Core Storage and Validation
- [ ] SA-1 Deployment (port 9395)
- [ ] SA-2 Deployment (port 9495)
- [ ] VA-1 Deployment (port 9392)
- [ ] VA-2 Deployment (port 9492)
- [ ] Remote VA-A Deployment (port 9397)
- [ ] Remote VA-B Deployment (port 9498)
- [ ] Remote VA-C Deployment (port 9499)

#### Group 2: Certificate Operations
- [ ] CA-1 Deployment (port 9393)
- [ ] CA-2 Deployment (port 9493)
- [ ] RA-SCT-Provider-1 Deployment (port 9594)
- [ ] RA-SCT-Provider-2 Deployment (port 9694)

#### Group 3: Registration and Workflow
- [ ] RA-1 Deployment (port 9394)
- [ ] RA-2 Deployment (port 9494)
- [ ] Publisher-1 Deployment (port 9391)
- [ ] Publisher-2 Deployment (port 9491)

#### Group 4: Frontend Services
- [ ] WFE2 Deployment (ports 4001, 4431)
- [ ] SFE Deployment (port 4003)
- [ ] Nonce-Service-Taro-1 Deployment
- [ ] Nonce-Service-Taro-2 Deployment
- [ ] Nonce-Service-Zinc-1 Deployment

#### Group 5: Administrative Services
- [ ] CRL-Storer Deployment
- [ ] CRL-Updater Deployment
- [ ] Bad-Key-Revoker Deployment
- [ ] Email-Exporter Deployment
- [ ] Log-Validator Deployment

### Implementation Tasks
- [ ] Create deployment manifests for each service
- [ ] Define Service objects for inter-service communication
- [ ] Configure gRPC health checks
- [ ] Implement readiness and liveness probes
- [ ] Set up network policies for service isolation
- [ ] Update Consul configuration for service discovery
- [ ] Test service-to-service communication
- [ ] Validate startup sequencing

### Success Criteria
- All services start successfully
- Inter-service gRPC communication works
- All integration tests pass
- No performance degradation
- Rollback procedure tested

---

## Phase 3: Kubernetes-native Initialization

### Goal
Replace Boulder's startup scripts with Kubernetes Jobs and InitContainers.

### Status: 🔴 Not Started

### Tasks
- [ ] Create database initialization Job
- [ ] Create certificate generation Job (bsetup replacement)
- [ ] Implement InitContainers for per-pod setup
- [ ] Move migrations to ConfigMaps
- [ ] Create bootstrap Job for first-time setup
- [ ] Implement health check InitContainers
- [ ] Document initialization sequence

### Success Criteria
- Jobs complete successfully
- Idempotent initialization
- Proper error handling
- Clean restart capability

---

## Phase 4: ConfigMaps and Secrets Management

### Goal
Externalize all configuration from container images.

### Status: 🔴 Not Started

### Tasks
- [ ] Create ConfigMap for each service configuration
- [ ] Migrate database credentials to Secrets
- [ ] Migrate TLS certificates to Secrets
- [ ] Implement configuration hot-reload (if supported)
- [ ] Create environment-specific overlays
- [ ] Document configuration schema
- [ ] Implement configuration validation

### Configuration Items
- [ ] RA configuration (ra-config.json)
- [ ] CA configuration (ca-config.json)
- [ ] SA configuration (sa-config.json)
- [ ] VA configuration (va-config.json)
- [ ] WFE configuration (wfe-config.json)
- [ ] Publisher configuration
- [ ] Nonce service configuration
- [ ] CRL service configuration

### Success Criteria
- All configs externalized
- Secrets properly protected
- Configuration changes without rebuild
- Validation prevents bad configs

---

## Phase 5: Observability and Monitoring

### Goal
Implement production-grade observability stack.

### Status: 🔴 Not Started

### Tasks
- [ ] Configure Prometheus metrics collection
- [ ] Create ServiceMonitor resources
- [ ] Enhance Jaeger tracing configuration
- [ ] Create Grafana dashboards
- [ ] Implement log aggregation
- [ ] Set up alerting rules
- [ ] Configure SLI/SLO monitoring
- [ ] Optional: Service mesh integration

### Metrics to Track
- [ ] Request latency (p50, p95, p99)
- [ ] Error rates by service
- [ ] Certificate issuance rate
- [ ] Database query performance
- [ ] Cache hit rates
- [ ] Resource utilization

### Success Criteria
- All services expose metrics
- Dashboards show key metrics
- Alerts fire appropriately
- Tracing shows request flow
- Logs are aggregated and searchable

---

## Phase 6: Production Features

### Goal
Implement scaling, resilience, and multi-region capabilities.

### Status: 🔴 Not Started

### Tasks
- [ ] Configure Horizontal Pod Autoscalers (HPAs)
- [ ] Implement Pod Disruption Budgets (PDBs)
- [ ] Set up network segmentation
- [ ] Configure resource requests/limits
- [ ] Implement multi-region deployment
- [ ] Set up cross-region replication
- [ ] Configure topology-aware routing
- [ ] Implement circuit breakers
- [ ] Set up canary deployments
- [ ] Configure blue-green deployments

### Scaling Targets
- [ ] WFE: HPA based on CPU/request rate
- [ ] RA: HPA based on queue depth
- [ ] VA: HPA based on validation latency
- [ ] CA: Fixed replicas with PDB
- [ ] SA: Fixed replicas with PDB

### Success Criteria
- Auto-scaling responds to load
- Zero-downtime deployments
- Graceful degradation under failure
- Multi-region failover works
- Resource utilization optimized

---

## Dependencies and Blockers

### Current Blockers
None - Phase 1 (test profile) provides stable foundation

### Dependencies Between Phases
- Phase 3 depends on Phase 2 (services must be split first)
- Phase 4 can proceed partially in parallel with Phase 2
- Phase 5 requires Phase 2 completion (need separate services to monitor)
- Phase 6 requires Phases 2-4 (need stable microservices architecture)

---

## Risk Register

| Risk | Impact | Probability | Mitigation |
|------|--------|-------------|------------|
| Service discovery failures | High | Medium | Keep Consul initially, gradual migration |
| Performance degradation | High | Low | Benchmark each phase, rollback capability |
| Configuration complexity | Medium | High | Automation, validation, documentation |
| Circular dependencies | High | Medium | Use SCT provider pattern from Docker Compose |
| Resource overhead | Medium | Medium | Right-size pods, use VPA recommendations |

---

## Validation Checklist

Before promoting each phase to production:

### Phase 2 Validation
- [ ] All services healthy
- [ ] Integration tests pass
- [ ] Performance benchmarked
- [ ] Rollback tested
- [ ] Documentation complete

### Phase 3 Validation
- [ ] Jobs complete successfully
- [ ] Restart resilience verified
- [ ] Error scenarios tested
- [ ] Recovery procedures documented

### Phase 4 Validation
- [ ] Configuration changes tested
- [ ] Secret rotation tested
- [ ] Bad config rejection verified
- [ ] Rollback procedures tested

### Phase 5 Validation
- [ ] Metrics collecting properly
- [ ] Alerts firing correctly
- [ ] Dashboards useful
- [ ] Logs aggregated successfully

### Phase 6 Validation
- [ ] Auto-scaling tested under load
- [ ] Failover scenarios tested
- [ ] Resource limits appropriate
- [ ] Cost optimization reviewed

---

## Notes and Lessons Learned

### Phase 2 Notes
- (To be added during implementation)

### Phase 3 Notes
- (To be added during implementation)

### Phase 4 Notes
- (To be added during implementation)

### Phase 5 Notes
- (To be added during implementation)

### Phase 6 Notes
- (To be added during implementation)

---

## Review History

| Date | Reviewer | Phase | Status Update |
|------|----------|-------|---------------|
| (TBD) | - | - | Initial document creation |

---

*Last Updated: Document created as part of multi-profile strategy implementation*