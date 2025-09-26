---

# Boulder on Kubernetes: Specification

## Purpose

This document describes how to run the **Boulder** ACME server and its supporting services on a Kubernetes cluster, evolving from the existing Docker Compose–based integration test environment. The goal is to provide a repeatable, self-contained Kubernetes deployment that preserves the behaviour of the current integration tests while enabling gradual migration of Boulder’s components into native Kubernetes objects.

## Migration Strategy

1. **Phase 1: External Dependencies First**

   - Move Boulder’s external dependencies (MariaDB, Redis, Consul, ProxySQL) into Kubernetes Deployments/StatefulSets and Services.
   - Boulder itself will remain bundled in a single container initially, running `test.sh` and `startservers.py` as before.
   - Kubernetes’ role in this phase is simply replacing Docker Compose’s orchestration of non-Boulder services.

2. **Phase 2: Split Boulder Services**

   - Break Boulder’s microservices (RA, SA, CA, WFE, VA, etc.) into separate Deployments.
   - Use Kubernetes **readiness probes** and **liveness probes** to replicate the startup sequencing currently managed by `startservers.py`.
   - Replace the topological sort logic with Kubernetes service discovery and dependency handling (Pods become “ready” only after their health checks succeed).

3. **Phase 3: Kubernetes-native Initialization**

   - Replace Boulder’s `bsetup` stage with a Kubernetes **Job**, which runs once per cluster bootstrap and generates required test certificates and data.

     - Kubernetes Jobs are designed to “run to completion” exactly once.

   - Where initialization is required on every Pod startup (not cluster-wide), use **Init Containers**:

     - Init containers run to completion before the main app container starts.

4. **Phase 4: Config & Secrets Management**

   - Store Boulder configuration in **ConfigMaps**.
   - Handle keys, certs, and other sensitive data with **Kubernetes Secrets**.
   - Mount these into Pods at runtime.

5. **Phase 5: Observability and Scaling**

   - Integrate with Kubernetes logging (stdout/stderr collection).
   - Add Prometheus metrics scraping from Boulder services.
   - Horizontal Pod Autoscalers (HPAs) can later be introduced for stateless services like RA or VA.

## Deployment Details

### External Dependencies

- **Database (MariaDB)**: StatefulSet with PersistentVolumeClaims for durable storage.
- **Consul**: Deployment or StatefulSet, Service for discovery. Retained in early phases for compatibility with Boulder configs.
- **ProxySQL**: Deployment, Service.

### Boulder Services

- Initially: single Pod running Boulder as today, inside a container with `startservers.py`.
- Later: separate Deployments for each microservice (RA, CA, SA, VA, WFE, nonce, publisher, etc.).
- Service objects expose gRPC/HTTP ports to other Boulder components.

### Initialization

- **Cluster-wide bootstrap**: Kubernetes Job for `bsetup`.
- **Per-Pod setup**: Init Containers for ephemeral setup tasks (e.g., copying keys, waiting on service readiness).

### Networking

- Services provide DNS-based discovery (e.g., `boulder-ra.default.svc.cluster.local`).
- Ingress can be added later to expose ACME endpoints outside the cluster.

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
