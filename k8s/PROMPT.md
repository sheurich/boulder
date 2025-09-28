# Boulder Kubernetes Phase 1 Implementation Loop

## Your Mission
Implement Phase 1 of Boulder's Kubernetes migration as specified in `docs/SPEC.md` - achieving drop-in CI parity with Docker Compose on a kind cluster.

## Workflow for Each Iteration

### 1. Assess Current State
```bash
# Check specification
cat docs/SPEC.md | head -128  # Review Phase 1 requirements

# Check git status
git status
git log --oneline -5

# Verify cluster state
kubectl get pods -n boulder
```

### 2. Identify and Fix Issues
Review completed work for:
- Missing service definitions matching Docker Compose names
- Incorrect ports or configurations
- Non-functional scripts or manifests
- Deviations from Phase 1 scope (NO service splitting!)

### 3. Test Your Progress
```bash
# Create cluster and deploy
./scripts/k8s-up.sh

# Run tests (from parent directory)
cd .. && ./tk8s.sh        # Should match t.sh behavior

# Clean up when done
./scripts/k8s-down.sh
```

## Success Criteria
Phase 1 is complete when:
1. Full CI test suite passes with identical results to Docker Compose
2. No changes required to test harness (test.sh, v2_integration.py)
3. Services reachable under same names/ports as Compose
4. tk8s.sh/tnk8s.sh provide drop-in replacement for t.sh/tn.sh

## Next Steps
After fixing any issues found, implement the next missing component from the Phase 1 checklist (see k8s/CLAUDE.md). Focus on getting a minimal working deployment before adding completeness.

## Execution Strategy

Use parallel agents to accelerate implementation:
- **Parallel execution**: Run multiple independent agents simultaneously for analysis and validation
- **Agent selection**: Use specialized agents for targeted searches, general-purpose for complex tasks
- **Example**: When implementing a new service, analyze Docker Compose config while researching Kubernetes patterns in parallel

## Version Control

Commit changes at logical checkpoints:
- After completing each Phase 1 deliverable
- When a service or script becomes functional
- After fixing significant bugs
- Use clear commit messages referencing the specific component
