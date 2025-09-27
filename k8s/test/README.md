# Boulder Kubernetes Test Runner (tk8.sh)

This directory contains a comprehensive Kubernetes-based test runner for Boulder that provides an alternative to the Docker Compose-based `t.sh` script.

## Overview

The `tk8.sh` script orchestrates Boulder testing in a Kubernetes environment, providing:

- **Isolated test execution** in dedicated Kubernetes namespaces
- **Service dependency management** with MySQL, Redis, Consul, and other required services
- **Automated certificate generation** using Kubernetes Jobs
- **Streaming test output** with real-time feedback
- **Comprehensive cleanup** of test resources
- **Support for all test types** (unit, integration, lints)

## Quick Start

### Prerequisites

1. **kubectl** configured and connected to a Kubernetes cluster
2. **kind** or **minikube** (recommended for local development)
3. Docker image: `letsencrypt/boulder-tools:latest`

### Basic Usage

```bash
# Run all tests (lints, unit, integration)
./tk8.sh

# Run unit tests only
./tk8.sh --unit

# Run integration tests only
./tk8.sh --integration

# Run lints only
./tk8.sh --lints

# Run with verbose output
./tk8.sh --unit --verbose

# Use specific kubectl context
./tk8.sh --kube-context my-context

# Skip cleanup (for debugging)
./tk8.sh --unit --no-cleanup
```

## Architecture

### Components

1. **tk8.sh** - Main orchestration script
2. **k8s/test/services.yaml** - Service dependencies (MySQL, Redis, Consul, etc.)
3. **k8s/test/configmaps.yaml** - Configuration files for services
4. **k8s/test/wait-for-services.sh** - Service readiness verification
5. **k8s/test/cleanup.sh** - Resource cleanup utility

### Test Flow

1. **Dependency Check** - Verify kubectl and cluster access
2. **Image Loading** - Load Boulder image into kind/minikube (if applicable)
3. **Namespace Creation** - Create isolated test namespace
4. **Resource Deployment** - Deploy services, ConfigMaps, and dependencies
5. **Service Readiness** - Wait for all services to be ready
6. **Certificate Generation** - Generate test certificates using Kubernetes Job
7. **Test Execution** - Run requested tests in isolated Kubernetes Jobs
8. **Result Collection** - Stream logs and capture test results
9. **Cleanup** - Remove all test resources

## File Structure

```
k8s/test/
├── README.md                    # This file
├── services.yaml               # Service deployments (MySQL, Redis, Consul, etc.)
├── configmaps.yaml            # Configuration files for services
├── certificate-job.yaml       # Certificate generation job template
├── test-runner-job.yaml       # Test execution job template
├── namespace.yaml             # Namespace template
├── wait-for-services.sh       # Service readiness check script
└── cleanup.sh                 # Resource cleanup script
```

## Service Dependencies

The test environment includes the following services:

- **bmysql** - MariaDB 10.11.13 database
- **bproxysql** - ProxySQL connection manager
- **bredis-1** & **bredis-2** - Redis instances for rate limiting
- **bconsul** - Consul service discovery
- **bjaeger** - Jaeger tracing (for observability)
- **bpkimetal** - PKI Metal service

## Configuration

### Environment Variables

The test environment sets up the following environment variables:

- `BOULDER_CONFIG_DIR` - Configuration directory (test/config or test/config-next)
- `FAKE_DNS` - DNS override for testing
- `MYSQL_HOST` - MySQL service hostname
- `REDIS_HOST_1` / `REDIS_HOST_2` - Redis service hostnames
- `CONSUL_HOST` - Consul service hostname
- `GOCACHE` - Go build cache directory

### Kubernetes Resources

Tests run with the following resource limits:

- **Memory**: 2Gi request, 8Gi limit
- **CPU**: 1 core request, 4 cores limit
- **Timeout**: 1 hour per test

## Command Line Options

```
Usage: tk8.sh [OPTION]...

Options:
    -l, --lints                           Run lints only
    -u, --unit                            Run unit tests only
    -v, --verbose                         Enable verbose output for tests
    -w, --unit-without-cache              Disable go test caching for unit tests
    -p <DIR>, --unit-test-package=<DIR>   Run unit tests for specific go package(s)
    -e, --enable-race-detection           Enable race detection for unit and integration tests
    -n, --config-next                     Use test/config-next instead of test/config
    -i, --integration                     Run integration tests only
    -c, --coverage                        Enable coverage for tests
    -d <DIR>, --coverage-directory=<DIR>  Directory to store coverage files in
    -f <REGEX>, --filter=<REGEX>          Run only those tests matching the regular expression
    -k <CONTEXT>, --kube-context=<CONTEXT> Use specific kubectl context
    -N <NAMESPACE>, --namespace=<NAMESPACE> Use specific Kubernetes namespace
    --no-cleanup                          Don't cleanup K8s resources after tests
    --timeout=<DURATION>                  Test timeout (default: 3600s)
    -h, --help                            Show help message
```

## Troubleshooting

### Common Issues

1. **kubectl not found or not configured**
   ```bash
   kubectl cluster-info
   ```

2. **Image loading failures with kind**
   ```bash
   kind load docker-image letsencrypt/boulder-tools:latest
   ```

3. **Service startup issues**
   ```bash
   kubectl get pods -n <namespace>
   kubectl logs <pod-name> -n <namespace>
   ```

4. **Test failures**
   ```bash
   # Use no-cleanup to inspect resources
   ./tk8.sh --unit --no-cleanup
   kubectl exec -it <test-pod> -n <namespace> -- bash
   ```

### Manual Cleanup

If automatic cleanup fails:

```bash
# List test namespaces
kubectl get namespaces | grep boulder-test

# Clean up specific namespace
./k8s/test/cleanup.sh -n boulder-test-12345

# Force cleanup
./k8s/test/cleanup.sh -n boulder-test-12345 --force
```

### Debugging Services

```bash
# Check service status
kubectl get all -n <namespace>

# Check service logs
kubectl logs deployment/bmysql -n <namespace>
kubectl logs deployment/bconsul -n <namespace>

# Test service connectivity
kubectl run debug --image=letsencrypt/boulder-tools:latest -n <namespace> --rm -it -- bash
```

## Performance Considerations

- **Startup Time**: Initial startup can take 2-5 minutes for service readiness
- **Resource Usage**: Tests require significant CPU and memory resources
- **Storage**: Uses ephemeral storage for certificates and build cache
- **Network**: All communication happens within the Kubernetes cluster network

## Comparison with t.sh

| Feature | t.sh (Docker Compose) | tk8.sh (Kubernetes) |
|---------|----------------------|---------------------|
| Isolation | Container-level | Namespace-level |
| Scalability | Single machine | Multi-node capable |
| Resource Management | Docker limits | Kubernetes requests/limits |
| Service Discovery | Docker DNS | Kubernetes DNS + Consul |
| Persistence | Docker volumes | Kubernetes PVC/EmptyDir |
| Debugging | Docker exec | kubectl exec |
| Cleanup | Docker compose down | Automated K8s cleanup |

## Contributing

When modifying the Kubernetes test runner:

1. **Test thoroughly** with different Kubernetes environments
2. **Update documentation** for any new features or options
3. **Maintain backward compatibility** with existing command-line options
4. **Follow Kubernetes best practices** for resource definitions
5. **Add appropriate error handling** and logging

## Limitations

- **Local development**: Requires Kubernetes cluster (kind/minikube recommended)
- **Image requirements**: Depends on `letsencrypt/boulder-tools` image
- **Resource intensive**: Requires more resources than Docker Compose
- **Network complexity**: Additional network setup compared to Docker Compose
- **Storage**: Currently uses ephemeral storage (no persistence across runs)