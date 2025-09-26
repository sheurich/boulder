---

# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Common Development Commands

### Testing

Boulder provides convenient wrapper scripts that automatically handle test setup:

**Standard testing with `t.sh` (recommended):**

```bash
# Run all tests (lints, unit, and integration)
./t.sh

# Run unit tests only
./t.sh --unit

# Run specific unit tests
./t.sh --unit --unit-test-package=./va

# Run unit tests with race detection
./t.sh --unit --enable-race-detection

# Run integration tests only
./t.sh --integration

# Run specific integration tests
./t.sh --filter TestGenerateValidity/TestWFECORS

# Run tests with coverage
./t.sh --unit --integration --coverage --coverage-dir=./test/coverage/mytestrun

# Run lints only
./t.sh --lints

# Run tests with verbose output
./t.sh --unit --verbose
```

**Testing with next-generation configuration using `tn.sh`:**

```bash
# Same options as t.sh, but uses config-next
./tn.sh --unit
./tn.sh --integration
./tn.sh  # Run all tests with config-next
```

**Direct Docker approach (for debugging or special cases):**

```bash
# Manual setup required
docker compose run --rm bsetup  # Generate certificates first
docker compose run --use-aliases boulder ./test.sh --unit
```

Note: Both `t.sh` and `tn.sh` automatically run `bsetup` to generate test certificates, eliminating manual setup steps.

### Building

Build all Boulder components:

```bash
docker compose up
```

Build specific components with Go (inside container):

```bash
GOBIN=$(pwd)/bin go install -mod=vendor ./...
```

### Starting Services

Start Boulder in development mode:

```bash
docker compose run --use-aliases boulder ./start.py
```

Start Boulder with custom FAKE_DNS (to point to your host):

```bash
docker compose run --use-aliases -e FAKE_DNS=172.17.0.1 --service-ports boulder ./start.py
```

### Direct Commands (for debugging)

Run Go linter directly:

```bash
docker compose run --use-aliases boulder golangci-lint run --timeout 9m ./...
```

Check spelling:

```bash
docker compose run --use-aliases boulder typos
```

Format configuration files:

```bash
docker compose run --use-aliases boulder ./test/format-configs.py 'test/config*/*.json'
```

Generate test certificates manually:

```bash
docker compose run --rm bsetup
```

## Quick Reference

| Task                        | Command                                               |
| --------------------------- | ----------------------------------------------------- |
| Run all tests               | `./t.sh`                                              |
| Run all tests (config-next) | `./tn.sh`                                             |
| Run unit tests only         | `./t.sh --unit`                                       |
| Run lints only              | `./t.sh --lints`                                      |
| Run specific test           | `./t.sh --filter TestName`                            |
| Run with coverage           | `./t.sh --unit --coverage`                            |
| Start Boulder               | `docker compose run --use-aliases boulder ./start.py` |
| Clean up containers         | `docker compose down`                                 |

## Architecture Overview

Boulder is an ACME-based Certificate Authority implementation built with a microservices architecture. The system consists of several key components that communicate via gRPC:

### Core Components

1. **Web Front End (WFE2)**: Handles ACME protocol endpoints (`/wfe2/wfe.go:90`)

   - Implements ACME v2 protocol with endpoints like `/acme/new-acct`, `/acme/new-order`
   - Validates JWS signatures and manages nonce services with dual clients
   - Routes requests to RA, SA, and email services via gRPC

2. **Registration Authority (RA)**: Orchestrates certificate issuance workflow (`/ra/ra.go:72`)

   - Coordinates between CA, VA, SA, and Publisher services
   - Enforces policy checks including CAA validation (7-hour recheck window)
   - Manages rate limiting with Redis backend using GCRA algorithm
   - Handles certificate profiles for different validation types

3. **Validation Authority (VA)**: Performs domain validation (`/va/va.go:84`)

   - Supports HTTP-01, DNS-01, and TLS-ALPN-01 challenge types
   - Uses multi-perspective validation with remote VA instances for security
   - Implements geographic distribution across different RIRs
   - Prevents BGP hijacking and DNS manipulation attacks

4. **Certificate Authority (CA)**: Issues certificates (`/ca/ca.go:78`)

   - Manages issuer certificates by algorithm and NameID
   - Supports PKCS#11 integration for hardware security modules
   - Generates precertificates for Certificate Transparency submission
   - Uses certificate profiles for different certificate types and lifetimes

5. **Storage Authority (SA)**: Database abstraction layer (`/sa/sa.go:40`)

   - Provides read-only and read-write database operations
   - Uses MariaDB with ProxySQL for connection pooling
   - Handles database migrations in `/sa/db/` and `/sa/db-next/`
   - Separates read and write operations for scaling

6. **Publisher**: Publishes to Certificate Transparency logs (`/publisher/publisher.go:36`)

   - Maintains cached connections to multiple CT logs
   - Supports parallel submission with retry logic
   - Uses Google's CT client library for log submission

7. **CRL Management**: Handles Certificate Revocation Lists (`/crl/crl.go`)
   - Generates and signs CRLs using CA private keys
   - Supports temporal CRL sharding for scalability
   - Manages CRL distribution to distribution points

### Communication Patterns

- **gRPC Services**: All inter-service communication uses gRPC with Protocol Buffers
- **Service Discovery**: Uses Consul for service discovery with SRV record lookups
- **Load Balancing**: Custom nonce balancer for nonce service distribution
- **Security**: mTLS between all services using internal PKI certificates
- **Configuration**: Service-specific timeouts in JSON config files

### ACME Protocol Flow

1. **Account Creation**: Client → WFE2 (`/wfe2/wfe.go:55`) → RA → SA stores account
2. **Order Creation**: Client → WFE2 → RA creates authorization challenges → SA stores
3. **Challenge Response**: Client → WFE2 → RA coordinates with VA for validation
4. **Domain Validation**: VA performs multi-perspective validation using remote VAs
5. **CAA Validation**: RA validates CAA records with 7-hour caching
6. **Certificate Issuance**: RA sends CSR to CA → CA generates precertificate
7. **CT Log Submission**: Publisher submits precertificate to CT logs
8. **Final Certificate**: CA issues final certificate with SCTs → SA stores

### Database and Storage

- **Backend**: MariaDB 10.11.13 with ProxySQL connection management
- **Migrations**: Schema evolution through `/sa/db/boulder_sa/` migration files
- **Read Separation**: Separate read-only connections for query scaling
- **Transactions**: Multi-table operations use database transactions
- **Monitoring**: Prometheus metrics for database operation latency

### Rate Limiting

- **Algorithm**: GCRA (Generic Cell Rate Algorithm) with Redis backend
- **Hierarchy**: Multiple limit types (per-domain, per-account, per-IP)
- **Configuration**: YAML-based defaults with per-account overrides
- **Dynamic Updates**: Live configuration changes without restart
- **Jitter**: 0-3% jitter on retry-after to prevent thundering herd

### Security Features

- **Multi-Perspective Validation**: Geographic distribution prevents BGP attacks
- **CAA Validation**: Certificate Authority Authorization with recheck logic
- **Key Security**: PKCS#11 HSM support and weak key detection
- **Certificate Linting**: Automated validation using zlint
- **OCSP/CRL**: Comprehensive revocation status management

### Testing Infrastructure

- **Wrapper Scripts**: `t.sh` (standard config) and `tn.sh` (config-next) handle setup automatically
- **Test Types**: Unit tests (`*_test.go`), integration tests (Python + Go)
- **Docker Environment**: Complete microservices setup with fake DNS
- **Service Dependencies**: Even unit tests require MySQL, Redis, and Consul running
- **Serial Execution**: Unit tests run with `-p=1` to prevent database conflicts
- **Build Tags**: Integration tests use `//go:build integration` tag
- **Load Testing**: Performance testing in `/test/load-generator/`
- **Mock Services**: Test implementations in `/mocks/` directory
- **Coverage**: Combined unit and integration coverage collection

## Code Style Guidelines

- Error handling: All errors must be addressed (returned, handled, or explicitly ignored with `_`)
- Avoid named return values to prevent subtle bugs
- Use separate lines for operations and error checking:
  ```go
  err := someOperation(args)
  if err != nil {
    return nil, fmt.Errorf("some operation failed: %w", err)
  }
  ```
- TODOs must include context: `// TODO(email@example.com): Description` or `// TODO(#1234): Description`
- Follow gRPC patterns for component communication
- Tests required for all new functionality and bug fixes
- Use `go install -mod=vendor` when building to use vendored dependencies

## Configuration Management

- **Development Config**: `/test/config/` - standard configuration (use `t.sh`)
- **Next-Gen Config**: `/test/config-next/` - next-generation configuration (use `tn.sh`)
- **Docker Compose**: `docker-compose.yml` for standard, `docker-compose.next.yml` for config-next
- **Service Discovery**: Consul-based with DNS authority configuration
- **TLS Configuration**: mTLS certificates and CA trust setup
- **Feature Flags**: Runtime feature toggles through configuration files
- **Rate Limit Config**: YAML-based with hierarchical limits and overrides

## Important Directories

- `/cmd/`: Executable entry points for each component
- `/wfe2/`: Web Frontend (ACME v2 protocol handlers)
- `/ra/`: Registration Authority (workflow orchestration)
- `/va/`: Validation Authority (domain validation)
- `/ca/`: Certificate Authority (certificate issuance)
- `/sa/`: Storage Authority (database abstraction)
- `/publisher/`: Certificate Transparency log publisher
- `/crl/`: Certificate Revocation List management
- `/ratelimits/`: Rate limiting implementation with Redis
- `/core/`: Core data structures and utilities
- `/grpc/`: gRPC utilities and middleware
- `/test/`: Integration tests, test utilities, and configurations
- `/mocks/`: Generated mock implementations for testing
- `/vendor/`: Vendored dependencies (use `-mod=vendor` flag)
- `/docs/`: Architecture documentation and design decisions
