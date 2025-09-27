# Boulder Kubernetes Migration - Phase 0 Analysis

## Executive Summary

This document provides a comprehensive analysis of the Boulder Certificate Authority architecture as part of the Phase 0 analysis for Kubernetes migration. The analysis covers service dependencies, network communication patterns, configuration inventory, startup sequences, and provides the foundation for the subsequent migration phases.

## Boulder Service Architecture Overview

Boulder operates as a distributed microservices architecture with 22 distinct services that communicate via gRPC. The system follows ACME v2 protocol standards and implements a complete Certificate Authority with multi-perspective validation, Certificate Transparency logging, and comprehensive rate limiting.

### Service Inventory

#### Core ACME Services (8 services)
1. **boulder-wfe2** - Web Frontend (ACME v2 endpoints)
   - Port: 4001 (HTTP), 4431 (HTTPS), 8013 (debug)
   - Dependencies: RA, SA, nonce-services, email-exporter
   - Role: ACME protocol handler, JWS validation, nonce management

2. **boulder-ra-1/2** - Registration Authority (2 instances)
   - Ports: 9394/9494 (gRPC), 8002/8102 (debug)
   - Dependencies: SA, CA, VA, Publisher
   - Role: Certificate issuance orchestration, policy enforcement

3. **boulder-va-1/2** - Validation Authority (2 instances)
   - Ports: 9392/9492 (gRPC), 8004/8104 (debug)
   - Dependencies: Remote VAs
   - Role: Domain validation (HTTP-01, DNS-01, TLS-ALPN-01)

4. **boulder-ca-1/2** - Certificate Authority (2 instances)
   - Ports: 9393/9493 (gRPC), 8001/8101 (debug)
   - Dependencies: SA, SCT Provider
   - Role: Certificate signing and issuance

5. **boulder-sa-1/2** - Storage Authority (2 instances)
   - Ports: 9395/9495 (gRPC), 8003/8103 (debug)
   - Dependencies: Database infrastructure
   - Role: Database abstraction layer

#### Remote Validation Services (3 services)
6. **remoteva-a** - Remote Validation Authority A
   - Port: 9397 (gRPC), 8011 (debug)
   - Role: Multi-perspective validation from geographic location A

7. **remoteva-b** - Remote Validation Authority B
   - Port: 9498 (gRPC), 8012 (debug)
   - Role: Multi-perspective validation from geographic location B

8. **remoteva-c** - Remote Validation Authority C
   - Port: 9499 (gRPC), 8023 (debug)
   - Role: Multi-perspective validation from geographic location C

#### Supporting Services (6 services)
9. **boulder-publisher-1/2** - Certificate Transparency Publisher (2 instances)
   - Ports: 9391/9491 (gRPC), 8009/8109 (debug)
   - Role: CT log submission and SCT collection

10. **boulder-ra-sct-provider-1/2** - SCT Provider (2 instances)
    - Ports: 9594/9694 (gRPC), 8118/8119 (debug)
    - Dependencies: Publisher
    - Role: SCT delivery to CA (solves circular dependency)

11. **nonce-service-taro-1/2** - Nonce Service Taro DC (2 instances)
    - Ports: 9301/9501 (gRPC), 8111/8113 (debug)
    - Role: Anti-replay nonce generation and validation

12. **nonce-service-zinc-1** - Nonce Service Zinc DC (1 instance)
    - Port: 9401 (gRPC), 8112 (debug)
    - Role: Cross-datacenter nonce validation

#### Administrative Services (5 services)
13. **sfe** - Subscriber Frontend
    - Port: 4003 (HTTP), 8015 (debug)
    - Dependencies: RA, SA, zendesk-test-srv
    - Role: Subscriber management and support interface

14. **email-exporter** - Email Integration
    - Port: 9603 (gRPC), 8114 (debug)
    - Dependencies: pardot-test-srv
    - Role: Marketing automation integration

15. **bad-key-revoker** - Bad Key Revocation
    - Port: 8020 (debug)
    - Dependencies: RA
    - Role: Automated revocation of compromised keys

16. **crl-storer** - CRL Storage Service
    - Port: 9309 (gRPC), 9667 (debug)
    - Dependencies: s3-test-srv
    - Role: Certificate Revocation List management

17. **log-validator** - CT Log Validator
    - Port: 8016 (debug)
    - Role: Certificate Transparency log validation

#### Test Infrastructure Services (6 services)
18. **aia-test-srv** - Authority Information Access Test Server
    - Port: 4502
    - Role: AIA URL testing and validation

19. **ct-test-srv** - Certificate Transparency Test Server
    - Port: 4600
    - Role: CT log testing and development

20. **s3-test-srv** - S3 Compatible Storage Test Server
    - Port: 4501
    - Role: CRL storage simulation

21. **pardot-test-srv** - Salesforce Pardot Test Server
    - Ports: 9601 (OAuth), 9602 (API)
    - Role: Marketing automation testing

22. **zendesk-test-srv** - Zendesk Test Server
    - Port: 9701
    - Role: Support ticket integration testing

## External Infrastructure Dependencies

### Database Layer
- **MariaDB** (bmysql) - Primary database backend
  - Version: 10.11.13
  - Network: bouldernet (internal)
  - Configuration: Slow query logging, bind to all interfaces
  - Used by: SA services for persistent storage

- **ProxySQL** (bproxysql) - Database connection pooling
  - Version: 2.5.4
  - Network: bouldernet (internal)
  - Dependencies: MariaDB
  - Used by: SA services for connection management

### Caching and Rate Limiting
- **Redis Instance 1** (bredis_1) - Rate limiting backend
  - Version: 6.2.7
  - Network: bouldernet (10.77.77.4)
  - Configuration: /test/redis-ratelimits.config
  - Used by: RA services for rate limiting

- **Redis Instance 2** (bredis_2) - Rate limiting backend
  - Version: 6.2.7
  - Network: bouldernet (10.77.77.5)
  - Configuration: /test/redis-ratelimits.config
  - Used by: RA services for rate limiting (redundancy)

### Service Discovery and Coordination
- **Consul** (bconsul) - Service discovery
  - Version: 1.15.4
  - Network: bouldernet (10.77.77.10)
  - Configuration: /test/consul/config.hcl
  - Used by: All services for service discovery

### Observability
- **Jaeger** (bjaeger) - Distributed tracing
  - Version: 1.50 (all-in-one)
  - Network: bouldernet (internal)
  - Used by: All services for request tracing

### PKI Operations
- **PKI Metal** (bpkimetal) - PKI validation service
  - Version: v1.20.0
  - Network: bouldernet (internal)
  - Used by: Validation services for additional PKI checks

## Network Architecture

### Network Segments

#### Internal Network (bouldernet)
- **Subnet**: 10.77.77.0/24
- **DHCP Range**: 10.77.77.128/25 (upper half)
- **Purpose**: Data center internal communication
- **Services**: All Boulder services and infrastructure
- **Key IPs**:
  - 10.77.77.10 - Consul (also DNS server)
  - 10.77.77.4 - Redis Instance 1
  - 10.77.77.5 - Redis Instance 2
  - 10.77.77.77 - Boulder monolith (Docker Compose mode)

#### Public Network 1 (publicnet)
- **Subnet**: 64.112.117.0/25
- **Purpose**: HTTP-01 challenge validation
- **Key IPs**:
  - 64.112.117.122 - Challenge test server (HTTP-01, HTTPS redirects)

#### Public Network 2 (publicnet2)
- **Subnet**: 64.112.117.128/25
- **Purpose**: TLS-ALPN-01 challenge validation and integration tests
- **Key IPs**:
  - 64.112.117.134 - Challenge test server (TLS-ALPN-01, custom HTTP servers)

### Communication Patterns

#### gRPC Service Communication
- **Protocol**: gRPC with Protocol Buffers
- **Security**: mTLS between all services
- **Service Discovery**: Consul-based with SRV records
- **Load Balancing**: Client-side load balancing via gRPC

#### Database Access Patterns
```
[SA Services] -> [ProxySQL] -> [MariaDB]
```
- Read-only and read-write separation
- Connection pooling via ProxySQL
- Transaction management for multi-table operations

#### Rate Limiting Flow
```
[RA Services] -> [Redis Cluster] (GCRA algorithm)
```
- Dual Redis instances for redundancy
- Generic Cell Rate Algorithm (GCRA)
- Hierarchical limits (per-domain, per-account, per-IP)

## Service Startup Dependencies

### Dependency Graph (Topologically Sorted)

```
Level 1 (No Dependencies):
├── remoteva-a
├── remoteva-b
├── remoteva-c
├── boulder-sa-1
├── boulder-sa-2
├── aia-test-srv
├── ct-test-srv
├── s3-test-srv
├── pardot-test-srv
├── nonce-service-taro-1
├── nonce-service-taro-2
├── nonce-service-zinc-1
├── zendesk-test-srv
└── log-validator

Level 2 (Infrastructure Dependencies):
├── boulder-publisher-1
├── boulder-publisher-2
├── crl-storer (depends on: s3-test-srv)
├── email-exporter (depends on: pardot-test-srv)
└── boulder-va-1/2 (depends on: remoteva-a, remoteva-b)

Level 3 (SCT Providers):
├── boulder-ra-sct-provider-1 (depends on: publishers)
└── boulder-ra-sct-provider-2 (depends on: publishers)

Level 4 (Certificate Authority):
├── boulder-ca-1 (depends on: SA, SCT providers)
└── boulder-ca-2 (depends on: SA, SCT providers)

Level 5 (Registration Authority):
├── boulder-ra-1 (depends on: SA, CA, VA, publishers)
└── boulder-ra-2 (depends on: SA, CA, VA, publishers)

Level 6 (Frontend Services):
├── boulder-wfe2 (depends on: RA, SA, nonce-services, email-exporter)
├── sfe (depends on: RA, SA, zendesk-test-srv)
└── bad-key-revoker (depends on: RA)
```

### Critical Path Analysis

The longest dependency chain for full system startup:
1. **Infrastructure Services** (0s) - Independent services start immediately
2. **Publishers** (5-10s) - Wait for basic infrastructure
3. **SCT Providers** (10-15s) - Wait for publishers to be ready
4. **Certificate Authority** (15-25s) - Wait for SA and SCT providers
5. **Registration Authority** (25-35s) - Wait for SA, CA, VA, publishers
6. **Web Frontend** (35-45s) - Wait for RA, SA, nonce services

**Total estimated startup time**: 45-60 seconds for full system

## Configuration Management

### Configuration Files Structure
```
test/config/
├── Core Services
│   ├── wfe2.json          # Web Frontend configuration
│   ├── ra.json            # Registration Authority
│   ├── va.json            # Validation Authority
│   ├── ca.json            # Certificate Authority
│   └── sa.json            # Storage Authority
├── Supporting Services
│   ├── publisher.json     # CT Publisher
│   ├── nonce-a.json       # Nonce service (taro DC)
│   ├── nonce-b.json       # Nonce service (zinc DC)
│   └── sfe.json           # Subscriber Frontend
├── Administrative
│   ├── admin.json         # Administrative interface
│   ├── bad-key-revoker.json
│   ├── cert-checker.json
│   ├── crl-storer.json
│   ├── crl-updater.json
│   ├── email-exporter.json
│   ├── health-checker.json
│   └── log-validator.json
├── Test Infrastructure
│   ├── pardot-test-srv.json
│   └── zendesk-test-srv.json
└── Remote Validation
    ├── remoteva-a.json
    ├── remoteva-b.json
    └── remoteva-c.json
```

### Configuration Categories

#### Database Configuration
- Connection strings for MariaDB and ProxySQL
- Read/write separation settings
- Transaction timeout configurations
- Migration tracking

#### gRPC Service Configuration
- Service addresses and ports
- TLS certificate configurations
- Client connection settings
- Health check parameters

#### Rate Limiting Configuration
- Redis connection parameters
- GCRA algorithm settings
- Rate limit hierarchies
- Override configurations

#### ACME Protocol Configuration
- Validation timeouts
- Challenge type support
- Certificate profiles
- Policy enforcement rules

## Security Architecture

### Certificate Management
- **Internal PKI**: mTLS certificates for inter-service communication
- **Certificate Profiles**: Different certificate types and lifetimes
- **HSM Integration**: PKCS#11 support for hardware security modules
- **Weak Key Detection**: Automated validation using zlint

### Multi-Perspective Validation
- **Geographic Distribution**: Remote VAs across different RIRs
- **BGP Hijacking Prevention**: Multiple validation points
- **DNS Manipulation Protection**: Cross-region validation
- **Validation Quorum**: Majority consensus required

### Access Control
- **Service Authentication**: mTLS for all gRPC communication
- **Database Access**: User-based database permissions
- **API Security**: JWS signature validation
- **Network Segmentation**: Isolated network segments

## Storage and Data Flow

### Database Schema
- **4 Primary Databases**: Core Boulder data storage
- **Migration System**: sql-migrate for schema evolution
- **Read Replicas**: Separate read-only connections
- **Backup Strategy**: Database-level backup and recovery

### Certificate Transparency Flow
```
[CA] -> [Precertificate] -> [Publisher] -> [CT Logs] -> [SCT] -> [CA] -> [Final Certificate]
```

### ACME Protocol Data Flow
```
[Client] -> [WFE2] -> [RA] -> [VA] (validation) -> [CA] (issuance) -> [Publisher] (CT) -> [Client]
```

## Performance and Scalability Characteristics

### Service Scaling Patterns
- **Stateless Services**: WFE2, VA, RA (horizontal scaling)
- **Stateful Services**: SA, CA (vertical scaling with read replicas)
- **Coordination Services**: Consul (cluster scaling)
- **Storage Services**: Redis (master-replica), MariaDB (read replicas)

### Resource Requirements (per service)
- **Memory**: 256MB - 2GB depending on service type
- **CPU**: 0.5 - 2 cores per service instance
- **Network**: Low latency internal communication critical
- **Storage**: Persistent storage for SA and database services

### Bottlenecks and Constraints
- **Database Performance**: SA services are I/O bound
- **Validation Latency**: Remote VA responses affect issuance time
- **CT Log Submission**: External CT log availability impacts issuance
- **Certificate Signing**: CA services CPU-bound for signing operations

## Migration Readiness Assessment

### Phase 1 Prerequisites ✅
- **Service Inventory Complete**: All 22 services identified and documented
- **Dependency Mapping**: Complete dependency graph established
- **Network Requirements**: Multi-segment networking requirements defined
- **Configuration Analysis**: All configuration files catalogued

### Phase 1 Challenges Identified
- **Complex Dependencies**: 6-level dependency hierarchy requires careful orchestration
- **Network Isolation**: Multi-network setup needed for different validation types
- **Service Discovery**: Consul integration with Kubernetes DNS
- **Database Initialization**: Multi-database setup with proper permissions

### Kubernetes Migration Strategy
- **Phase 1**: Monolithic deployment (current approach) ✅
- **Phase 2**: Service separation with dependency management
- **Phase 3**: Kubernetes-native features (service discovery, autoscaling)
- **Phase 4**: Production optimization (service mesh, multi-region)

## Conclusion

The Phase 0 analysis reveals Boulder as a sophisticated distributed system with 22 services, complex interdependencies, and strict ordering requirements. The system demonstrates mature microservices patterns with proper separation of concerns, robust security, and comprehensive observability.

**Key findings for Kubernetes migration:**

1. **Complexity**: 22 services with 6-level dependency hierarchy requires careful orchestration
2. **Network Requirements**: Multi-segment networking essential for security and validation
3. **State Management**: Mix of stateless and stateful services with different scaling needs
4. **Configuration Complexity**: 22 configuration files with interdependencies
5. **External Dependencies**: 6 infrastructure services (database, cache, service discovery)

**Migration Approach Validation:**
The persistent monolith approach chosen for Phase 1 is appropriate given:
- Eliminates complex inter-service orchestration during initial migration
- Maintains existing startup sequence and dependency management
- Provides stable foundation for subsequent service separation
- Reduces risk of service communication issues during transition

The analysis confirms that the Phase 1 implementation approach is sound and provides the necessary foundation for successful Kubernetes migration in subsequent phases.

---
*Phase 0 Analysis Complete - September 27, 2025*
*Services: 22 identified, Dependencies: 6-level hierarchy, Networks: 3 segments*