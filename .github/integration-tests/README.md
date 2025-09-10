# IBC Integration Tests

This directory contains automated integration tests for the IBC attester system. The tests validate the complete workflow from chain initialization to IBC token transfers using Docker containerization.

## Quick Start

Run the complete integration test suite:

```bash
# From project root directory
./run-integration-tests.sh
```

## Test Architecture

The integration tests use Docker Compose to orchestrate multiple services:

```
┌─────────────┐    ┌──────────────┐    ┌─────────────┐
│  Gaia Chain │    │   GM Chain   │    │  Attester   │
│  (Cosmos)   │◄──►│  (Rollkit)   │◄──►│  Service    │
└─────────────┘    └──────────────┘    └─────────────┘
       ▲                   ▲                   ▲
       │                   │                   │
       └───────────────────┼───────────────────┘
                           │
                   ┌───────────────┐
                   │ Test Runner   │
                   │   (Hermes)    │
                   └───────────────┘
```

### Services

1. **local-da**: Mock Data Availability layer for Rollkit
2. **gaia-chain**: Cosmos Hub (Gaia) chain container
3. **gm-chain**: Your GM chain with Rollkit and attester support
4. **attester**: Attester service that creates attestations
5. **test-runner**: Test orchestrator with Hermes IBC relayer

## Test Workflow

The integration test performs these steps:

1. **Initialize Chains**: Start Gaia and GM chains with proper configuration
2. **Start Attester**: Launch attester service and wait for attestation activity
3. **Setup IBC**: Create IBC clients, connections, and transfer channels
4. **Test Transfers**: Execute bidirectional IBC token transfers
5. **Validate Results**: Verify system health and functionality

## Usage Options

### Basic Usage

```bash
# Run tests with default settings
./run-integration-tests.sh

# Run with verbose output
./run-integration-tests.sh --verbose

# Skip cleanup (for debugging)
./run-integration-tests.sh --no-cleanup

# Force rebuild images
./run-integration-tests.sh --build-fresh

# Custom timeout (in seconds)
./run-integration-tests.sh --timeout 300
```

### Environment Variables

```bash
# Same as command line options
CLEANUP=false ./run-integration-tests.sh
VERBOSE=true ./run-integration-tests.sh
BUILD_FRESH=true ./run-integration-tests.sh
TIMEOUT=300 ./run-integration-tests.sh
```

### Docker Compose Direct Usage

For advanced debugging, you can use Docker Compose directly:

```bash
cd integration-tests

# Start services step by step
docker compose up -d local-da
docker compose up -d gaia-chain gm-chain
docker compose up -d attester

# Check service status
docker compose ps
docker compose logs gm-chain

# Run tests
docker compose run --rm test-runner

# Cleanup
docker compose down -v
```

## Configuration Files

### Docker Services

- `docker/Dockerfile.gaia`: Gaia chain container
- `docker/Dockerfile.gm`: GM chain with Rollkit
- `docker/Dockerfile.attester`: Attester service
- `docker/Dockerfile.test`: Test runner with Hermes

### Scripts

- `scripts/init-gaia.sh`: Initialize Gaia chain
- `scripts/init-gm.sh`: Initialize GM chain with attester mode
- `scripts/run-attester.sh`: Start attester service
- `scripts/setup-ibc.sh`: Configure IBC connections
- `scripts/test-transfers.sh`: Execute transfer tests
- `scripts/wait-for-*.sh`: Health check utilities

### Configuration

- `config/hermes.toml`: Hermes relayer configuration
- `docker-compose.yml`: Service orchestration

## Troubleshooting

### Common Issues

1. **Services not starting**:
   ```bash
   # Check service logs
   cd integration-tests
   docker compose logs gm-chain
   docker compose logs gaia-chain
   ```

2. **Attester not working**:
   ```bash
   # Check attester logs
   docker compose logs attester
   
   # Verify GM chain has network module
   curl http://localhost:1417/evabci/network/v1/params
   ```

3. **IBC setup failing**:
   ```bash
   # Check test runner logs
   docker compose logs test-runner
   
   # Manually run IBC setup
   docker compose run --rm test-runner bash
   ./setup-ibc.sh
   ```

4. **Port conflicts**:
   ```bash
   # Check if ports are in use
   netstat -tulpn | grep -E "(26657|26757|1417|1317)"
   
   # Stop conflicting services
   docker compose -f ../mis_scripts/run_gm.sh down
   ```

### Debug Mode

Run tests with maximum debugging:

```bash
# Enable all debugging
VERBOSE=true DEBUG=1 ./run-integration-tests.sh --no-cleanup

# Then inspect containers
cd integration-tests
docker compose exec gm-chain bash
docker compose exec test-runner bash
```

### Log Collection

Logs are automatically collected in `integration-tests/logs/` with timestamps. Each test run creates a separate log directory with:

- Individual container logs
- Service status snapshots
- Test execution summaries

## GitHub Actions

The tests run automatically in GitHub Actions on:

- Push to `main` or `develop` branches  
- Pull requests to `main`
- Manual workflow dispatch

See `.github/workflows/integration-test.yml` for CI configuration.

### Manual CI Trigger

```bash
# Trigger via GitHub CLI
gh workflow run integration-test.yml

# With debug mode
gh workflow run integration-test.yml -f debug_mode=true
```

## Development

### Adding New Tests

1. Create new test script in `scripts/`
2. Add to `scripts/run-integration-test.sh`
3. Update Docker Compose if needed
4. Test locally before committing

### Modifying Services

1. Update respective Dockerfile
2. Rebuild with `--build-fresh`
3. Test changes locally
4. Update documentation

### Performance Tuning

- Adjust timeouts in scripts and compose file
- Optimize Docker image builds with better caching
- Use specific versions for dependencies

## Comparison with Manual Tests

| Manual Process | Automated Equivalent |
|----------------|---------------------|
| `mis_scripts/run_gaia.sh --reset` | `gaia-chain` service |
| `mis_scripts/run_gm.sh --attester` | `gm-chain` + `attester` services |
| `go run ./attester/main.go ...` | `attester` service |
| `mis_scripts/setup_ibc.sh` | `setup-ibc.sh` script |
| `mis_scripts/test_ibc_transfer.sh` | `test-transfers.sh` script |

## Next Steps

- **Enhanced Testing**: Add more comprehensive transfer validation
- **Error Scenarios**: Test failure cases and recovery
- **Performance Tests**: Add load testing capabilities
- **Multi-Chain**: Support additional chain types
- **Parallel Testing**: Run multiple test scenarios simultaneously