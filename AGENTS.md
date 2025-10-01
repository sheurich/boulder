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

## Common Tasks

### 1. Understanding ACME Request Flow

Start with `docs/DESIGN.md` which documents complete flows:
- Account creation: WFE → RA → SA
- Order creation: WFE → RA → SA
- Challenge validation: WFE → RA → VA → Internet
- Certificate issuance: WFE → RA → CA → Publisher → SA

### 2. Adding a New ACME Endpoint

1. Define handler in `wfe2/wfe.go`
2. Add route in `wfe2/wfe.go:51-74`
3. Implement RA logic if needed in `ra/ra.go`
4. Add proto RPC if new RA functionality in `ra/proto/ra.proto`
5. Update tests in `test/integration/`

### 3. Modifying Validation Logic

- HTTP-01 validation: `va/http.go`
- DNS-01 validation: `va/dns.go`
- TLS-ALPN-01 validation: `va/tlsalpn.go`
- CAA checking: `va/caa.go`

### 4. Rate Limiting Changes

- Default limits: `test/config/wfe2-ratelimit-defaults.yml`
- Override limits: `test/config/wfe2-ratelimit-overrides.yml`
- Implementation: `ratelimits/` directory

### 5. Database Schema Changes

1. Modify models in `sa/model.go`
2. Create migration script
3. Update SA service in `sa/sa.go`
4. Regenerate proto if needed

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
3. **Multi-Perspective Validation** - Multiple VA vantage points
4. **Two-Phase Issuance** - Precertificate → SCTs → Final cert
5. **Policy-Driven** - Centralized policy in PA
6. **Async Validation** - Non-blocking challenge processing

## Security Considerations

- **Network Isolation**: Components separated by network context
- **TLS Everywhere**: All gRPC uses mutual TLS
- **HSM Integration**: Keys stored in PKCS#11 devices
- **Rate Limiting**: Redis-backed per-account limits
- **Multi-Perspective**: Validation from multiple network locations

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

## Common Pitfalls

- **Circular dependencies**: Some services have special patterns (e.g., RA-SCT-Provider)
- **Consul SRV lookups**: Services discover each other via DNS, not direct connections
- **Database transactions**: Only SA should touch the database
- **Rate limit testing**: Requires Redis to be running
- **Certificate chains**: Multiple chains configured for different scenarios

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
