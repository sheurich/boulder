# ADR 001: Repository Structure for Boulder-on-Kubernetes

**Date:** 2025-09-27

## Status
Accepted

## Context
We are beginning the migration of Boulder’s Docker Compose–based integration test environment into Kubernetes. One of the first architectural decisions is how to organize the Kubernetes-related configuration, manifests, and documentation relative to the existing Boulder codebase.

This decision affects developer experience, maintainability, and the ability to propose changes upstream to Boulder maintainers.

## Options Considered

### Option 1: Separate Repository with Submodule
- Create a new repository dedicated to Kubernetes deployment.
- Reference the upstream Boulder repository as a Git submodule.
- Kubernetes manifests, initialization scripts, and documentation live only in the new repository.

**Pros:**
- Clear separation of concerns.
- No changes to upstream Boulder repository required.
- Freedom to iterate independently.

**Cons:**
- Synchronization overhead with upstream Boulder updates.
- More friction for developers needing context across repos.
- Less discoverable for Boulder contributors who expect deployment configs in the main repo.

---

### Option 2: Embed as Subdirectory in Boulder Repository (k8s/)
- Add a `k8s/` directory in the Boulder repository (or fork).
- Place all Kubernetes manifests, configs, and documentation in this directory.

**Pros:**
- Integrated developer experience: one repo contains both code and Kubernetes setup.
- Easier for AI-assisted and human developers to reason about Boulder and its deployment together.
- Reduces submodule synchronization issues.
- Increases visibility to maintainers and contributors.

**Cons:**
- Requires changes to upstream Boulder repo (or maintenance of a fork until accepted).
- Potentially larger repository footprint.

---

## Decision
We will embed the Kubernetes deployment as a `k8s/` subdirectory within the Boulder repository. At this stage, we will maintain a fork that contains the `k8s/` directory. We will propose this structure to the Boulder maintainers for upstream inclusion.

The initial scope of what we propose upstream will be at least the CI-related Kubernetes configuration (corresponding to **Phase 1** of the migration plan), with the possibility of later phases being merged as well.

## Consequences
- Developers cloning the Boulder repo will immediately see and be able to use the Kubernetes integration.
- While we maintain a fork, we must keep it synchronized with upstream Boulder changes until adoption.
- Early visibility may encourage Boulder maintainers to provide feedback sooner.
- Long-term, this avoids the synchronization burden of submodules and aligns with developer-friendly practices.
