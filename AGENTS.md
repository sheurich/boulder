# Boulder ACME CA - AI Assistant Guide

## Overview

Boulder is Let's Encrypt's production-grade ACME Certificate Authority implementation. This guide helps AI assistants understand the codebase structure, architecture, and how to effectively navigate and modify this complex Go-based microservices system.

## Architecture Summary

Boulder implements the ACME protocol (RFC 8555) using a microservices architecture with 7 main components:

```
                       ┌─────────┐
                       │Publisher│ (CT logs)
                       └────┬────┘
                            │
      ┌──────┐        ┌─────┴─────┐        ┌──────┐
      │ WFE  │───────▶│    RA     │◀──────▶│  CA  │
      └──┬───┘        └─────┬─────┘        └──────┘
         │                  │
         ▼                  ▼
    [Internet]         ┌─────────┐
         │             │   VA    │
         ▼             └────┬────┘
   [Client Server]          │
                           ▼
                      [Internet]
                           │
                           ▼
                    [Client Server]

    All components interact with:
    ┌─────────────────────────┐
    │   Storage Authority     │
    │         (SA)            │
    └───────────┬─────────────┘
                │
                ▼
           [MariaDB]
```

### Component Responsibilities

| Component | Purpose | Key Files |
|-----------|---------|-----------|
| **WFE** (Web Front End) | ACME API endpoints, rate limiting | `wfe2/wfe.go`, `cmd/boulder-wfe2/main.go` |
| **RA** (Registration Authority) | Business logic, orchestration | `ra/ra.go`, `cmd/boulder-ra/main.go` |
| **VA** (Validation Authority) | Domain control validation | `va/va.go`, `cmd/boulder-va/main.go` |
| **CA** (Certificate Authority) | Certificate signing | `ca/ca.go`, `cmd/boulder-ca/main.go` |
| **SA** (Storage Authority) | Database persistence | `sa/sa.go`, `cmd/boulder-sa/main.go` |
| **Publisher** | CT log submission | `publisher/publisher.go` |
| **CRL Updater** | Revocation lists | `crl/updater/updater.go` |

## Key Technologies

- **Language**: Go 1.25.0
- **RPC**: gRPC with Protocol Buffers
- **Database**: MariaDB with ProxySQL
- **Caching**: Redis (rate limiting, nonces)
- **Service Discovery**: Consul
- **Observability**: Prometheus, OpenTelemetry, Jaeger
- **Development**: Docker Compose

## Important Directories

```
boulder/
├── cmd/              # Service entry points
│   ├── boulder-wfe2/ # Web Front End binary
│   ├── boulder-ra/   # Registration Authority binary
│   ├── boulder-ca/   # Certificate Authority binary
│   ├── boulder-va/   # Validation Authority binary
│   └── boulder-sa/   # Storage Authority binary
├── wfe2/            # WFE implementation
├── ra/              # RA implementation
├── ca/              # CA implementation
├── va/              # VA implementation
├── sa/              # SA implementation
├── core/            # Core types and interfaces
├── grpc/            # gRPC utilities
├── ratelimits/      # Rate limiting implementation
├── issuance/        # Certificate issuance logic
├── test/            # Test infrastructure
│   ├── config/      # Service configurations
│   └── integration/ # Integration tests
└── docs/            # Architecture documentation
```

## Protocol Buffer Definitions

Key proto files defining service contracts:

- `ra/proto/ra.proto` - Registration Authority service
- `ca/proto/ca.proto` - Certificate Authority service
- `va/proto/va.proto` - Validation Authority service
- `sa/proto/sa.proto` - Storage Authority service
- `core/proto/core.proto` - Shared types

### Regenerating Proto Code

After modifying any `.proto` files, regenerate the Go code:

```bash
./t.sh --generate  # or ./t.sh -g
```

This command:
1. Runs inside Docker with the correct environment
2. Installs necessary dependencies
3. Executes `go generate ./...`
4. Verifies no unintended files changed

**Note**: Never manually edit `*_pb.go` files - they are auto-generated.

## Common Tasks

### 1. Understanding ACME Request Flow

Start with `docs/DESIGN.md` which documents complete flows:
- Account creation: WFE → RA → SA
- Order creation: WFE → RA → SA
- Challenge validation: WFE → RA → VA → Internet
- Certificate issuance: WFE → RA → CA → Publisher → SA

### 2. Adding a New ACME Endpoint

1. Define handler method in `wfe2/wfe.go` (e.g., `NewOrder` at line 2275)
2. Add path constant in `wfe2/wfe.go:51-74` (e.g., `newOrderPath = "/acme/new-order"`)
3. Register route in `wfe2/wfe.go:405-432` in the `Handler()` method
4. Implement RA logic if needed in `ra/ra.go`
5. Add proto RPC if new RA functionality in `ra/proto/ra.proto`
6. Regenerate proto code (see [Regenerating Proto Code](#regenerating-proto-code))
7. Update tests in `test/integration/`

### 3. Decision Heuristics for Modifications

When modifying Boulder, use these guidelines to ensure completeness:

#### Adding a New Challenge Type
1. Add constant to `core/objects.go:56-60` (e.g., `ChallengeTypeDNSAccount01`)
2. Update validation switch in `va/va.go:421-437`
3. Update storage mappings in `sa/model.go:383-395` (`challTypeToUint` and `uintToChallType`)
4. Implement validation logic in appropriate `va/*.go` file
5. Add feature flag if experimental (see Feature Flags section)
6. Update RA orchestration via `PerformValidation` in `ra/ra.go:1493`
7. Update integration tests

#### Database Schema Changes
1. Modify models in `sa/model.go` (e.g., `orderModel` at line 304)
2. Create migration script in `sa/db/boulder_sa/YYYYMMDDHHMMSS_Description.sql`
3. Use pointer fields for nullable columns (e.g., `*string` for optional fields)
4. Include both `-- +migrate Up` and `-- +migrate Down` sections
5. Future migrations go in `sa/db-next/boulder_sa/`
6. Ensure backward compatibility for existing rows
7. Update integration tests to verify migration

#### External Behavior Changes
- Update integration tests in `test/integration/`
- Update `docs/DESIGN.md` with new flows
- Re-check multi-perspective quorum logic if touching validation
- Verify metrics are updated appropriately

### 4. Modifying Validation Logic

- HTTP-01 validation: `va/http.go`
- DNS-01 validation: `va/dns.go`
- TLS-ALPN-01 validation: `va/tlsalpn.go`
- DNS-Account-01 validation: `va/dns.go` (feature-gated via `features.Get().DNSAccount01Enabled`)
- CAA checking: `va/caa.go`

### 5. Rate Limiting Changes

- Default limits: `test/config/wfe2-ratelimit-defaults.yml`
- Override limits: `test/config/wfe2-ratelimit-overrides.yml`
- Implementation: `ratelimits/` directory

### 6. Database Schema Changes

1. Modify models in `sa/model.go`
2. Create migration script in `sa/db/boulder_sa/YYYYMMDDHHMMSS_Description.sql`
3. Update SA service in `sa/sa.go`
4. Regenerate proto if needed (see [Regenerating Proto Code](#regenerating-proto-code))

## Testing

### Running Tests

```bash
# All tests
docker compose run --use-aliases boulder ./test.sh

# Unit tests only
docker compose run --use-aliases boulder ./test.sh --unit

# Integration tests
docker compose run --use-aliases boulder ./test.sh --integration

# Specific component
docker compose run --use-aliases boulder ./test.sh --unit --filter=./va
```

### Test Infrastructure

- `test/startservers.py` - Service orchestration
- `test/entrypoint.sh` - Container initialization
- `test/integration/` - End-to-end ACME tests
- `test/config/` - Test configurations

## Development Workflow

### Local Setup

1. Clone repository
2. Run `docker compose up` to start all services
3. Services available at:
   - WFE: http://localhost:4001
   - Prometheus: http://localhost:9090
   - Jaeger: http://localhost:16686

### Service Discovery

Services find each other via Consul DNS:
- Pattern: `{service}.service.consul`
- Example: `ra.service.consul`
- Configured in each service's JSON config

### Debugging

- Service logs: `docker compose logs {service}`
- Metrics: http://localhost:{8000-8100}/metrics
- Traces: Jaeger UI at http://localhost:16686
- Database: `mysql -h localhost -P 3306`

## Key Architectural Patterns

1. **Microservices with gRPC** - Strong service boundaries
2. **Repository Pattern** - All DB access through SA
3. **Multi-Perspective Validation** - Multiple VA vantage points with quorum requirements
4. **Two-Phase Issuance** - Precertificate → SCTs → Final cert
5. **Policy-Driven** - Centralized policy logic via the `policy/` package (not a separate service)
6. **Async Validation** - Non-blocking challenge processing

## Error Handling & ACME Problems

Boulder uses a unified error system that maps internal errors to ACME problem types:

### Error Flow
1. **Internal Error**: Services use `berrors.BoulderError` types (`errors/errors.go`)
2. **gRPC Transport**: Errors encoded in metadata, type information preserved
3. **WFE Conversion**: `web.ProblemDetailsForError()` maps to ACME problems
4. **Client Response**: JSON-encoded `probs.ProblemDetails` with appropriate HTTP status

### Key Error Types (`errors/errors.go:40-74`)
- `berrors.NotFoundError()` → `404 Not Found`
- `berrors.MalformedError()` → `400 Bad Request`
- `berrors.UnauthorizedError()` → `403 Forbidden`
- `berrors.RateLimitError()` → `429 Too Many Requests`
- `berrors.CAAError()` → `403 CAA Problem`
- `berrors.ConnectionFailure` → `400 Connection Problem`
- `berrors.DNS` → `400 DNS Problem`

### Usage Pattern
```go
// In SA/VA/RA services:
return nil, berrors.NotFoundError("registration with ID '%d' not found", regID)

// In WFE:
prob := web.ProblemDetailsForError(err, "Problem getting authorization")
```

### Guidelines
- Create new error types only for distinct ACME problem types
- Reuse existing types with specific detail messages
- Use `errors.Is()` for type checking: `if errors.Is(err, berrors.NotFound)`

## Feature Flags

Boulder uses a global singleton feature flag system (`features/features.go`) for gradual rollout:

### Configuration
```json
{
  "serviceName": {
    "features": {
      "DNSAccount01Enabled": true,
      "AsyncFinalize": true
    }
  }
}
```

### Access Pattern
```go
// Initialization (once per service):
features.Set(c.ServiceName.Features)

// Usage (anywhere in codebase):
if features.Get().DNSAccount01Enabled {
    // feature-enabled behavior
}
```

### Lifecycle
1. **Rollout**: Gate new functionality with flag (default: false)
2. **Enable**: Deploy code, then enable via config
3. **Deprecate**: Once stable, remove flag checks from code
4. **Cleanup**: Remove from config after production deployment

## Multi-Perspective Validation (MPIC)

Boulder implements Multi-Perspective Issuance Corroboration per CA/Browser Forum BR Section 3.2.2.9:

### Configuration (`va/va.go`)
- **Minimum Perspectives**: 3 (as of March 15, 2026)
- **Required RIRs**: 2 distinct Regional Internet Registries
- **Quorum Logic**: Implemented in `doRemoteOperation()` function

### Quorum Requirements (`maxAllowedFailures()` at `va/va.go:292-300`)
| Remote VAs | Max Failures | Required Successes |
|------------|--------------|-------------------|
| 2-5        | 1            | n-1               |
| 6+         | 2            | n-2               |

### RIR Diversity
- Must validate from at least 2 of: ARIN, RIPE, APNIC, LACNIC, AFRINIC
- Configured per remote VA in `test/config/va.json`
- Enforced in `va/va.go:628`: `len(passedRIRs) >= requiredRIRs`

### Security Implications
- **Never reduce** `requiredRIRs` below 2 (violates BR requirements)
- **Never increase** `maxAllowedFailures` beyond BR-specified limits
- Changes affect BGP hijacking and regional attack resistance

## Metrics & Observability

Boulder uses Prometheus metrics with OpenTelemetry tracing:

### Naming Convention
All metrics use **snake_case** (e.g., `validation_latency`, `http_errors`)

### Registration Pattern (`initMetrics`)
```go
func initMetrics(stats prometheus.Registerer) *vaMetrics {
    validationLatency := prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "validation_latency",
            Help:    "Histogram of validation latency",
            Buckets: metrics.InternetFacingBuckets,
        },
        []string{"operation", "perspective", "challenge_type", "problem_type", "result"},
    )
    stats.MustRegister(validationLatency)
    // ...
}
```

### Key Metrics
- `validation_latency`: Challenge validation performance
- `http_errors`: Client request errors
- `grpc_lag`: gRPC call latency
- `db_open_connections`: Database connection pool stats
- `ct_submission_time_seconds`: Certificate Transparency submission time

### Observability Endpoints
- Metrics: `http://localhost:{8000-8100}/metrics`
- Traces: Jaeger UI at `http://localhost:16686`

### Important: Metric names are contracts
Renaming metrics breaks dashboards and alerts. Add new metrics rather than renaming.

## Security Considerations

- **Network Isolation**: Components separated by network context
- **TLS Everywhere**: All gRPC uses mutual TLS
- **HSM Integration**: Keys stored in PKCS#11 devices
- **Rate Limiting**: Redis-backed per-account limits
- **Multi-Perspective**: Validation from multiple network locations with quorum requirements

## Important Configuration Files

- `docker-compose.yml` - Infrastructure setup
- `test/config/*.json` - Service configurations
- `test/config/wfe2-ratelimit-*.yml` - Rate limit policies
- `sa/db/dbconfig.yml` - Database configuration

## Useful Commands for AI Assistants

When working with Boulder:

```bash
# Find all handlers for an ACME endpoint
grep -r "newOrder" wfe2/

# Locate proto definitions
find . -name "*.proto"

# Find service entry points
ls -la cmd/boulder-*/main.go

# Search for specific validation logic
grep -r "http-01" va/

# Find rate limit implementations
grep -r "NewLimiter" ratelimits/
```

## Tips for AI Assistants

1. **Always trace the full flow**: Start from WFE and follow through RA, VA/CA, to SA
2. **Check proto files first**: They define the contracts between services
3. **Read DESIGN.md**: It explains the complete ACME flows
4. **Use repomix**: Pack the codebase for comprehensive analysis
5. **Test locally**: Use docker compose for full integration testing
6. **Check configurations**: JSON configs in test/config/ show how services connect
7. **Follow naming conventions**: Services use `{service}pb` for proto packages

## Common Pitfalls (Critical for Deployment)

### Verified Architecture Constraints

1. **Parallel DB Mutations** (`test.sh:183-192`)
   - Tests MUST run with `-p=1` flag for serial execution
   - Unit tests share a database instance and clean up after themselves
   - Parallel execution causes race conditions and flaky failures

2. **Proto Field Number Reuse** (all `.proto` files)
   - NEVER reuse field numbers - mark as `reserved` instead
   - Example: `reserved 2; // Previously dnsNames`
   - Reusing numbers breaks wire compatibility with older systems

3. **Metrics Contract Breakage** (`ra/ra.go:129-222`, `va/va.go:119-127`)
   - Metric names feed into Prometheus/Grafana dashboards
   - Renaming metrics (e.g., `validation_latency`) breaks monitoring
   - Always add new metrics rather than renaming existing ones

4. **Database Access Boundary** (`docs/DESIGN.md:15-20`)
   - Only SA touches the database - verified by code search
   - All DB operations go through SA's gRPC interface
   - Direct DB access from other components violates architecture

5. **Feature Flag Timing** (`docs/CONTRIBUTING.md:180-194`)
   - Must remove flag from production config before removing from code
   - Premature removal causes deployment failures
   - Follow the documented deprecation process

6. **Validation Thresholds** (`va/va.go:275,292-300`)
   - `singleDialTimeout`: 10 seconds (hardcoded)
   - Quorum thresholds mandated by BR Section 3.2.2.9
   - Changing these affects security guarantees

### Additional Known Issues
- **Circular dependencies**: Some services have special patterns (e.g., RA-SCT-Provider)
- **Consul SRV lookups**: Services discover each other via DNS, not direct connections
- **Certificate chains**: Multiple chains configured for different scenarios
- **Rate limit testing**: Requires Redis to be running

## Getting Help

- Architecture: `docs/DESIGN.md`
- ACME divergences: `docs/acme-divergences.md`
- Contributing: `docs/CONTRIBUTING.md`
- CRL design: `docs/CRLS.md`
- Error handling: `docs/error-handling.md`

## Quick Reference

### Service Ports (Development)

- WFE HTTP: 4001
- WFE HTTPS: 4431
- VA HTTP: 5002
- VA HTTPS: 5001
- CA: 9093
- RA: 9094
- SA: 9095
- Prometheus: 9090
- Jaeger: 16686
- MariaDB: 3306
- Redis: 6379

### Key Environment Variables

- `FAKE_DNS`: Override DNS resolution for testing
- `BOULDER_CONFIG_DIR`: Configuration directory
- `GOCACHE`: Go build cache location
- `GOFLAGS`: Go compiler flags

This guide should help AI assistants effectively navigate and modify the Boulder codebase. For specific implementation details, refer to the source files and documentation mentioned above.
