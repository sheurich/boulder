---

# Detailed Trace: docker compose run --use-aliases boulder ./start.py

Based on my analysis, here's exactly what happens when you run this command:

## Phase 1: Docker Compose Infrastructure (0-30 seconds)

1. Docker Compose starts infrastructure containers in dependency order:

   - bmysql (MariaDB) starts on internal network
   - bproxysql waits for MySQL, then starts (connection pooling)
   - bredis_1 and bredis_2 start (rate limiting backends)
   - bconsul starts (service discovery)
   - bjaeger starts (distributed tracing)
   - bpkimetal starts (certificate validation)

2. Boulder container launches with:

   - Mounted volumes: source code, Go cache, SoftHSM tokens
   - Network aliases: boulder on 3 networks (bouldernet, publicnet, publicnet2)
   - DNS configured to use Consul (10.77.77.10)
   - Environment: FAKE_DNS=64.112.117.122, BOULDER_CONFIG_DIR=test/config

## Phase 2: Container Initialization (via test/entrypoint.sh)

3. System logging starts: rsyslogd launches for application logs
4. Dependency checks (blocking until available):
   ./test/wait-for-it.sh boulder-mysql 3306 # Wait for MySQL
   ./test/wait-for-it.sh bproxysql 6032 # Wait for ProxySQL
   ./test/wait-for-it.sh bpkimetal 8080 # Wait for PKIMetal
5. Database initialization (test/create_db.sh):

   - Creates 4 databases: boulder_sa_test, boulder_sa_integration, incidents_sa_test, incidents_sa_integration
   - Runs migrations using sql-migrate tool from /sa/db/ directory (or /sa/db-next/ for config-next)
   - Tracks applied migrations in gorp_migrations table
   - Creates database users with appropriate permissions

6. Python script launches: exec python3 ./start.py

## Phase 3: Build Phase (via start.py → startservers.install())

7. Component compilation:

   - Runs make GO_BUILD_FLAGS=""
   - Builds all binaries to /boulder/bin/:
     - Core services: boulder-ca, boulder-ra, boulder-va, boulder-sa, boulder-wfe2
     - Support services: boulder-publisher, crl-storer, nonce-service
     - Test services: chall-test-srv, ct-test-srv, pardot-test-srv

## Phase 4: Service Startup (via startservers.start())

8. DNS check: Verifies Consul is working by resolving publisher.service.consul
9. Challenge test server starts first (chall-test-srv):

   - HTTP-01 handler on 64.112.117.122:80
   - HTTPS on 64.112.117.122:443
   - TLS-ALPN-01 on 64.112.117.134:443
   - DNS on ports 8053, 8054
   - Management interface on :8055

10. Services start in topological order (dependency-resolved):

Tier 1 - No dependencies:

    - Remote VAs: remoteva-a (9397), remoteva-b (9498), remoteva-c (9499)
    - Storage: boulder-sa-1 (9395), boulder-sa-2 (9495)
    - Test servers: aia-test-srv (4502), ct-test-srv (4600), s3-test-srv (4501)
    - Nonce services: nonce-service-taro-1 (9301), nonce-service-taro-2 (9501)

Tier 2 - Depend on Tier 1:
    - Publishers: boulder-publisher-1 (9391), boulder-publisher-2 (9491)
    - SCT providers: boulder-ra-sct-provider-1 (9594), boulder-ra-sct-provider-2 (9694)

Tier 3 - Depend on previous tiers:
    - Validation: boulder-va-1 (9392), boulder-va-2 (9492)
    - Certificate Authority: boulder-ca-1 (9393), boulder-ca-2 (9493)

Tier 4 - Depend on core services:
    - Registration Authority: boulder-ra-1 (9394), boulder-ra-2 (9494)
    - CRL Storer: crl-storer (9309)

Tier 5 - Application layer:
    - Web Frontend: boulder-wfe2 (4001)
    - Self-service Frontend: sfe (4003)
    - Admin tools: bad-key-revoker (8020)

## Phase 5: Health Checks & Service Discovery

11. Each service undergoes health check:

    - gRPC services: ./bin/health-checker -addr service:port -host-override service.boulder
    - HTTP services: TCP port availability check via waitport()
    - 100-second timeout per service
    - Failed health checks abort startup with error message showing failed service

12. Service discovery via Consul:

    - Services are configured to use Consul DNS for service resolution
    - SRV records enable load balancing across multiple instances
    - Example: _sa._tcp.service.consul resolves to both SA instances
    - Service names resolve to appropriate internal IPs via Consul

## Phase 6: Ready State

13. Final state:

    - All 30+ services running and healthy
    - ACME endpoints available at http://localhost:4001/directory
    - Admin interface at http://localhost:4003
    - Full microservices mesh with:
      - Redundant instances (2x most services)
      - Load balancing via Consul
      - Mutual TLS between all services
      - Distributed tracing via Jaeger
      - Rate limiting via Redis
      - Database access via ProxySQL

Key Configuration Files Used

- docker-compose.yml: Container orchestration
- test/entrypoint.sh: Container initialization
- test/config/\*.json: Service configurations
- test/consul/config.hcl: Service discovery setup
- test/certs/ipki/: TLS certificates for inter-service communication

Process Monitoring

The start.py script continues running, monitoring all child processes. If any service crashes, it:

1. Detects the failure via process monitoring
2. Logs which service failed with PID and exit code
3. Sends SIGTERM to all other services
4. Exits with failure status

This ensures Boulder either runs with all services healthy or fails completely, preventing partial-failure states that could cause confusing behavior during development.
