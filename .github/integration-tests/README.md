# IBC Integration Tests for Evolve Network

This directory contains a comprehensive suite of automated integration tests to validate IBC (Inter-Blockchain Communication) functionality between Evolve Network chains and Cosmos Hub (Gaia).

## 📋 Overview

The test system creates a complete local environment with multiple interconnected blockchains to validate:
- Setup and operation of rollup chains with Evolve
- Establishment of IBC connections and channels
- Token transfers between chains
- Attestation service functionality
- IBC message relaying with Hermes

## 🏗️ System Architecture

### Main Components

1. **Local DA (Data Availability)**
   - Local data availability service
   - Port: 7980
   - Version: configured by `EVNODE_DA_VERSION`

2. **Gaia Chain (Cosmos Hub)**
   - Standard Cosmos chain for IBC testing
   - Ports: 26657 (RPC), 26656 (P2P), 9090 (gRPC), 1317 (API)
   - Version: Gaia v25.1.0 by default

3. **GM Chain (Evolve Chain)**
   - Rollup chain built with Evolve/Rollkit
   - Configured with attester mode enabled
   - Ports: 26757 (RPC), 26756 (P2P), 9190 (gRPC), 1417 (API)
   - Depends on Local DA service

4. **Attester Service**
   - Attestation service for GM chain
   - Connects to GM chain to provide attestations
   - Automatically restarts until connection is established

5. **IBC Setup**
   - Configures IBC connections and channels between chains
   - Uses Hermes as IBC relayer
   - Generates shared connection information

6. **Hermes Relayer**
   - Continuous IBC relayer for inter-chain messages
   - Version: Hermes 1.13.1
   - Monitors and relays IBC packets

7. **Test Runner**
   - Executes integration tests
   - Validates IBC transfers and system functionality

## 📁 File Structure

```
.github/integration-tests/
├── docker-compose.yml           # Docker services definition
├── run-integration-tests.sh     # Main execution script
├── docker/                      # Dockerfiles for each service
│   ├── Dockerfile.attester      # Attester service image
│   ├── Dockerfile.gm            # GM chain (Evolve) image
│   ├── Dockerfile.local-da      # Local DA service image
│   └── Dockerfile.test          # Test runner image
├── scripts/                     # Configuration and test scripts
│   ├── init-gaia.sh            # Gaia initialization
│   ├── init-gm.sh              # GM chain initialization
│   ├── setup-ibc.sh            # IBC connection setup
│   ├── test-transfers.sh       # IBC transfer tests
│   ├── run-attester.sh         # Attester service runner
│   ├── wait-for-attester.sh    # Wait for attester availability
│   ├── wait-for-chain.sh       # Wait for chain availability
│   ├── wait-for-da.sh          # Wait for DA service
│   ├── prepare-deps.sh         # Dependencies preparation
│   └── run-integration-test.sh # Container test script
├── config/
│   └── hermes.toml             # Hermes relayer configuration
├── patches/                     # Code patches
│   └── app-wiring/
│       └── patch-app-wiring.sh # App configuration patches
└── logs/                       # Logs directory (generated)
```

## 🚀 Usage

### Basic Execution

```bash
# Run tests with default configuration
./github/integration-tests/run-integration-tests.sh

# Run with verbose logging
./github/integration-tests/run-integration-tests.sh --verbose

# Keep containers after tests
./github/integration-tests/run-integration-tests.sh --no-cleanup

# Rebuild all images
./github/integration-tests/run-integration-tests.sh --build-fresh

# Set custom timeout (in seconds)
./github/integration-tests/run-integration-tests.sh --timeout 300
```

### Environment Variables

```bash
# Component versions
export EVNODE_VERSION="v1.0.0-beta.2.0.20250908090838-0584153217ed"
export EVNODE_DA_VERSION="v1.0.0-beta.1"
export IGNITE_VERSION="v29.3.1"
export IGNITE_EVOLVE_APP_VERSION="main"
export GAIA_VERSION="v25.1.0"

# Execution options
export CLEANUP=false       # Don't cleanup after tests
export VERBOSE=true        # Detailed logs
export BUILD_FRESH=true    # Rebuild images
export TIMEOUT=300         # Timeout in seconds
```

## 🔄 Execution Flow

1. **Requirements Verification**
   - Docker and Docker Compose installed
   - Configuration files present

2. **Environment Preparation**
   - Create log directories
   - Clean previous runs
   - Set permissions

3. **Image Building**
   - Local DA with Evolve binaries
   - GM chain with Ignite and custom modules
   - Attester with required tools
   - Test runner with testing tools

4. **Base Services Startup**
   - Local DA for data availability
   - Gaia chain (local Cosmos Hub)
   - GM chain (Evolve chain)
   - Attester service

5. **IBC Configuration**
   - Create IBC clients
   - Establish connections
   - Open transfer channels
   - Configure relayer accounts

6. **Relayer Startup**
   - Hermes begins relaying packets
   - Continuous monitoring of both chains

7. **Test Execution**
   - Validate initial balances
   - IBC transfers between chains
   - Verify token reception
   - Test timeouts and errors

8. **Results Collection**
   - Logs from all services
   - Final chain states
   - Test summary

## 🧪 Tests Executed

### IBC Transfer Test (`test-transfers.sh`)

1. **Initial Setup**
   - Import test wallets
   - Verify established IBC channels

2. **Transfer Tests**
   - Token transfer from GM to Gaia
   - Token transfer from Gaia to GM
   - Verify updated balances
   - Validate IBC denominations

3. **Validations**
   - Confirm token reception
   - Verify correct fees
   - IBC packet status

## 🐛 Debugging

### Service Logs

```bash
# View logs for specific service
docker compose -f .github/integration-tests/docker-compose.yml logs gm-chain

# View real-time logs
docker compose -f .github/integration-tests/docker-compose.yml logs -f attester

# View status of all services
docker compose -f .github/integration-tests/docker-compose.yml ps
```

### Container Access

```bash
# Access GM chain
docker exec -it gm-chain /bin/bash

# Check chain status
docker exec gm-chain gmd status

# View recent blocks
docker exec gm-chain gmd query block
```

### Log Files

Logs are automatically saved in:
```
.github/integration-tests/logs/integration_test_logs_[timestamp]/
├── local-da.log
├── gaia-chain.log
├── gm-chain.log
├── attester.log
├── ibc-setup.log
├── hermes-relayer.log
├── test-runner.log
├── service_status.txt
└── test_summary.txt
```

## 📊 Results Interpretation

### Success
- All chains start correctly
- IBC connections established
- Transfers completed without errors
- Exit code: 0

### Common Failures

1. **Chain healthcheck failure**
   - Verify available ports
   - Review initialization logs

2. **IBC setup timeout**
   - Verify attester is working
   - Review Hermes configuration

3. **Transfer failures**
   - Verify active IBC channels
   - Review account balances

## 🔧 Advanced Configuration

### Modify Hermes Configuration

Edit `.github/integration-tests/config/hermes.toml` to adjust:
- Timeouts
- Gas prices
- Relay strategies

### Customize Chains

Modify corresponding Dockerfiles to:
- Change binary versions
- Adjust genesis configuration
- Add custom modules

### Add New Tests

1. Create script in `scripts/`
2. Add step in `run-integration-test.sh`
3. Update test-runner in docker-compose.yml

## 📝 Maintenance

### Version Updates

```bash
# In docker-compose.yml or environment variables
EVNODE_VERSION=new-version
GAIA_VERSION=new-version
```

### Complete Cleanup

```bash
# Remove all containers and volumes
cd .github/integration-tests
docker compose down -v --remove-orphans

# Remove built images
docker compose down --rmi all
```
