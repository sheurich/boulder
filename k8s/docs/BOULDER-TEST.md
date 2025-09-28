---

# Detailed Trace: ./t.sh

Based on comprehensive analysis, here's exactly what happens when you run Boulder's test suite:

## Phase 1: Wrapper Script Initialization (0-1 seconds)

1. Script setup (`t.sh:6-10` or `tn.sh:6-10`):

   - Set `errexit` mode to fail on any command error
   - Check for `realpath` availability and change to repository root
   - This ensures scripts run from the correct directory context

2. Certificate generation (`t.sh:13` or `tn.sh:13`):

   - Runs `docker compose run --rm bsetup` to generate test certificates
   - Uses `letsencrypt/boulder-tools` Docker image
   - Executes `test/certs/generate.sh` as entrypoint
   - Generates with `minica` tool:
     - Internal PKI certificates for service-to-service mTLS
     - Challenge test certificates for integration tests
     - PKCS#11 test tokens for HSM simulation
     - Service-specific certificates for all Boulder components

3. Docker Compose execution:

   - Standard config (`t.sh:15`): `docker compose run --rm --name boulder_tests boulder ./test.sh "$@"`
   - Config-next (`tn.sh:15`): Adds `-f docker-compose.next.yml` overlay
   - Key differences:
     - Standard: `BOULDER_CONFIG_DIR=test/config`, `GOCACHE=/boulder/.gocache/go-build`
     - Config-next: `BOULDER_CONFIG_DIR=test/config-next`, `GOCACHE=/boulder/.gocache/go-build-next`

## Phase 2: Docker Infrastructure Startup (1-10 seconds)

4. Infrastructure containers start in dependency order:

   - **bmysql** (MariaDB 10.11.13) starts on internal network
   - **bproxysql** waits for MySQL, then starts with connection pooling (port 6033)
   - **bredis_1** and **bredis_2** start on static IPs 10.77.77.4 and 10.77.77.5
   - **bconsul** starts on 10.77.77.10 for service discovery
   - **bjaeger** starts for distributed tracing collection
   - **bpkimetal** starts for certificate validation (port 8080)

5. Boulder test container launches with:

   - Mounted volumes: source code at `/boulder`, Go cache, SoftHSM tokens
   - Network configuration:
     - `bouldernet` (10.77.77.77): Internal services network
     - `publicnet` (64.112.117.122): HTTP-01 challenge network
     - `publicnet2` (64.112.117.134): TLS-ALPN-01 challenge network
   - DNS: Primary at 10.77.77.10 (Consul), backup at 8.8.8.8
   - Environment: `FAKE_DNS=64.112.117.122`, `BOULDER_CONFIG_DIR` set per config type

## Phase 3: Container Initialization (via test/entrypoint.sh)

6. System logging starts: rsyslogd launches for application logs
7. Service dependency checks (blocking with 40-second timeout each):
   - `./test/wait-for-it.sh boulder-mysql 3306` - Wait for MySQL
   - `./test/wait-for-it.sh bproxysql 6032` - Wait for ProxySQL admin interface
   - `./test/wait-for-it.sh bpkimetal 8080` - Wait for PKIMetal
8. Database initialization (`test/create_db.sh` with `MYSQL_CONTAINER=1`):

   - Sets MariaDB configuration: `binlog_format='MIXED'`, `max_connections=500`
   - Creates 4 databases:
     - `boulder_sa_test` and `boulder_sa_integration` for Storage Authority
     - `incidents_sa_test` and `incidents_sa_integration` for incident tracking
   - Runs migrations from:
     - Standard: `/sa/db/` directory
     - Config-next: `/sa/db-next/` directory
   - Uses `sql-migrate up` tool with temporal ordering (YYYYMMDDHHMMSS format)
   - Creates database users with role-based permissions:
     - `test_setup`: Full privileges for test management
     - `sa`, `sa_ro`: Storage Authority read/write and read-only access
     - Specialized users: `revoker`, `mailer`, `cert_checker`

9. Test script launches: `exec ./test.sh "$@"` with passed arguments

## Phase 4: Test Script Initialization (via test.sh)

10. Command-line argument parsing (`test.sh:128-152`):

    - Processes both short (`-u`) and long (`--unit`) option formats
    - Builds arrays for:
      - `RUN`: Test types to execute (lints, unit, integration)
      - `UNIT_FLAGS`: Go test flags for unit tests
      - `INTEGRATION_FLAGS`: Flags for integration test runner
      - `FILTER`: Test name filtering patterns

11. Default test selection (`test.sh:156-159`):

    - If no test types specified, defaults to: `RUN+=("lints" "unit" "integration")`
    - Runs all three test phases in sequence

12. Filter validation (`test.sh:162-177`):

    - Prevents using `--filter` with both `--unit` and `--integration`
    - Transforms filter format based on test type:
      - Unit tests: `--test.run "${FILTER[@]}"`
      - Integration tests: `--filter "${FILTER[@]}"`

## Phase 5: Linting Phase (if enabled)

13. Linting execution (`test.sh:222-232`):

    - **Go code linting**: `golangci-lint run --timeout 9m ./...`
      - Runs multiple linters in parallel
      - 9-minute timeout for complete codebase analysis
    - **Grafana linting**: `python3 test/grafana/lint.py`
      - Validates dashboard JSON configurations
    - **Spell checking**: `typos` with `.typos.toml` configuration
      - Uses `run_and_expect_silence` - fails if any output produced
    - **Config formatting**: `./test/format-configs.py 'test/config*/*.json'`
      - Validates JSON formatting consistency with tab indentation

## Phase 6: Unit Testing Phase (if enabled)

14. Unit test preparation (`test.sh:237-240`):

    - Runs `flush_redis()` to clear all Redis cache data
    - Uses custom Go utility at `test/boulder-tools/flushredis/main.go`
    - Connects to Redis ring using Boulder's bredis configuration
    - Calls `FlushAll()` on each shard in the ring

15. Unit test configuration (`test.sh:242-246`):

    - **Serial execution**: Adds `-p=1` flag to prevent database conflicts
    - **Package selection**: Default `./...` or specific via `--unit-test-package`
    - **Coverage flags** (if enabled):
      - `-cover`: Enable coverage collection
      - `-covermode=atomic`: Thread-safe for race detection
      - `-coverprofile=${COVERAGE_DIR}/unit.coverprofile`
      - `-coverpkg=${UNIT_CSV}`: Comma-separated package list
    - **Race detection** (if enabled): Adds `-race` flag

16. Unit test execution (`test.sh:247`):

    - Runs: `go test "${UNIT_FLAGS[@]}" "${UNIT_PACKAGES[@]}" "${FILTER[@]}"`
    - Tests run with full infrastructure (MySQL, Redis, Consul) available
    - Build tags exclude files with `//go:build integration`
    - Exit on first failure due to `set -e`

## Phase 7: Integration Testing Phase (if enabled)

17. Integration test preparation (`test.sh:253-256`):

    - Runs `flush_redis()` again for clean state
    - Builds argument array for Python test orchestrator
    - Adds flags: `--chisel` (always), verbose/standard based on settings

18. Service build phase (`test/startservers.py:176-187`):

    - Runs `make GO_BUILD_FLAGS=""` to build all Boulder binaries
    - Adds `-race` flag if race detection enabled
    - Adds `-cover` flag if coverage collection enabled
    - Builds to `/boulder/bin/` directory:
      - Core services: boulder-ca, boulder-ra, boulder-va, boulder-sa, boulder-wfe2
      - Support services: boulder-publisher, crl-storer, nonce-service
      - Test services: chall-test-srv, ct-test-srv, pardot-test-srv

19. Service startup orchestration (`test/startservers.py:200-241`):

    - **DNS check**: Verifies `publisher.service.consul` resolves via Consul
    - **Challenge test server** starts first (`chall-test-srv`):
      - HTTP-01 handler on 64.112.117.122:80
      - HTTPS on 64.112.117.122:443
      - TLS-ALPN-01 on 64.112.117.134:443
      - DNS on ports 8053, 8054
      - Management API on :8055

20. Services start in topological dependency order:

**Tier 1 - No dependencies:**

    - Remote VAs: remoteva-a (9397), remoteva-b (9498), remoteva-c (9499)
    - Storage: boulder-sa-1 (9395), boulder-sa-2 (9495)
    - Test servers: aia-test-srv (4502), ct-test-srv (4600), s3-test-srv (4501)
    - Nonce services: nonce-service-taro-1 (9301), nonce-service-taro-2 (9501)

**Tier 2 - Depend on Tier 1:**

    - Publishers: boulder-publisher-1 (9391), boulder-publisher-2 (9491)
    - SCT providers: boulder-ra-sct-provider-1 (9594), boulder-ra-sct-provider-2 (9694)

**Tier 3 - Depend on previous tiers:**

    - Validation: boulder-va-1 (9392), boulder-va-2 (9492)
    - Certificate Authority: boulder-ca-1 (9393), boulder-ca-2 (9493)

**Tier 4 - Depend on core services:**

    - Registration Authority: boulder-ra-1 (9394), boulder-ra-2 (9494)
    - CRL Storer: crl-storer (9309)

**Tier 5 - Application layer:**

    - Web Frontend: boulder-wfe2 (4001)
    - Self-service Frontend: sfe (4003)
    - Admin tools: bad-key-revoker (8020)

21. Service health verification:

    - **gRPC services**: `./bin/health-checker -addr service:port -host-override service.boulder`
    - **HTTP services**: TCP port availability check
    - **Timeout**: 100 seconds per service
    - **Failure handling**: Aborts test run if any service fails health check

## Phase 8: Test Execution

22. Python integration tests (`test/integration-test.py:110-116`):

    - Discovers test functions starting with `test_` in `v2_integration` module
    - Applies regex filter if `--filter` specified
    - Uses chisel ACME client connecting to `http://boulder.service.consul:4001/directory`
    - Manages challenge responses via `http://10.77.77.77:8055` API

23. Go integration tests (`test/integration-test.py:29-42`):

    - Runs: `go test -tags integration -count=1 -race ./test/integration`
    - Uses `//go:build integration` tag for test selection
    - Applies `--test.run` filter if specified
    - Creates ACME clients for test scenarios

24. Load balance verification (`test/integration-test.py:118-140`):

    - Checks `grpc_server_handled_total` metrics on all service endpoints
    - Validates requests distributed across multiple instances:
      - SA: ports 8003, 8103
      - Publisher: ports 8009, 8109
      - VA: ports 8004, 8104
      - CA: ports 8001, 8101
      - RA: ports 8002, 8102

## Phase 9: Additional Test Types

25. Start test (`test.sh:282-294`) - if specifically requested:

    - Starts `python3 start.py` in background
    - Polls `http://localhost:4001/directory` for up to 115 seconds
    - Validates Boulder startup success

26. Generate test (`test.sh:300-315`) - if specifically requested:

    - Installs code generation dependencies
    - Runs `go generate ./...` to regenerate all generated code
    - Uses `git diff --exit-code .` to ensure no changes
    - Fails if generated code not up-to-date

## Phase 10: Coverage Processing (if enabled)

27. Coverage data collection:

    - **Unit tests**: Single `unit.coverprofile` file in coverage directory
    - **Integration tests**: Binary coverage data from service execution
    - **Processing** (`test/integration-test.py:145-155`):
      - Uses `go tool covdata textfmt` to convert binary to text format
      - Outputs to `integration.coverprofile`
      - Combines coverage from all service instances

## Phase 11: Test Completion

28. Exit handling (`test.sh:198`, `319`):

    - Trap function `print_outcome()` displays colored status
    - Green SUCCESS message if all tests pass
    - Red FAILURE message with failed stage name
    - Exit code propagates to wrapper script

29. Container cleanup:

    - `--rm` flag ensures test container is removed
    - Infrastructure containers remain running for next test run
    - Database state preserved between runs (except table contents)

30. Final state:

    - All test phases complete with pass/fail status
    - Coverage data available in specified directory
    - Test results displayed with appropriate exit code
    - Ready for next test execution

## Key Configuration Files Used

- `t.sh` / `tn.sh`: Wrapper scripts for test execution
- `docker-compose.yml`: Container orchestration
- `docker-compose.next.yml`: Config-next overlay
- `test/entrypoint.sh`: Container initialization
- `test.sh`: Main test orchestration script
- `test/create_db.sh`: Database setup and migration
- `test/config/*.json`: Service configurations (standard)
- `test/config-next/*.json`: Service configurations (next-gen)
- `test/certs/generate.sh`: Certificate generation
- `sa/db/dbconfig.yml`: Database migration configuration
- `test/integration-test.py`: Integration test runner
- `test/startservers.py`: Service startup orchestration

## Process Monitoring

The test system monitors all processes during execution:

1. Each service process tracked by PID
2. Health checks verify service availability before proceeding
3. Failed services cause immediate test abort with error details
4. Exit traps ensure proper cleanup and status reporting
5. Coverage data collected throughout execution if enabled

This ensures Boulder tests run in a complete, isolated environment that mirrors production architecture while providing fast feedback and comprehensive validation of all components.