#!/bin/bash
set -e

# Default values mirroring the workflow
EVNODE_VERSION="v1.0.0-rc.4.0.20260216131057-1da76345e4b9"
IGNITE_VERSION="v29.6.1"
IGNITE_EVOLVE_APP_VERSION="main"
EVOLVE_IMAGE_REPO=${EVOLVE_IMAGE_REPO:-"ghcr.io/evstack/evolve-abci-gm"}
COSMOS_SDK_IMAGE_REPO=${COSMOS_SDK_IMAGE_REPO:-"ghcr.io/evstack/cosmos-sdk-gm"}

# Local defaults
IMAGE_TAG=${IMAGE_TAG:-"local"}
ENABLE_IBC="true"

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

log() { echo -e "${GREEN}[migration-test] $1${NC}"; }
error() { echo -e "${RED}[migration-test] $1${NC}"; }

# Help
if [[ "$1" == "--help" ]]; then
    echo "Usage: $0 [--build]"
    echo "Replicates the migration-tastora-single-node workflow job."
    echo ""
    echo "Options:"
    echo "  --build    Build Docker images locally before running tests."
    echo ""
    echo "Environment Variables:"
    echo "  EVOLVE_IMAGE_REPO       (default: $EVOLVE_IMAGE_REPO)"
    echo "  COSMOS_SDK_IMAGE_REPO   (default: $COSMOS_SDK_IMAGE_REPO)"
    echo "  IMAGE_TAG               (default: $IMAGE_TAG)"
    echo ""
    exit 0
fi

# Build if requested
if [[ "$1" == "--build" ]]; then
    log "Building Cosmos SDK image ($COSMOS_SDK_IMAGE_REPO:$IMAGE_TAG)..."
    docker build \
        --build-arg IGNITE_VERSION="$IGNITE_VERSION" \
        --build-arg ENABLE_IBC="$ENABLE_IBC" \
        -f Dockerfile.cosmos-sdk \
        -t "$COSMOS_SDK_IMAGE_REPO:$IMAGE_TAG" \
        .

    log "Building Evolve image ($EVOLVE_IMAGE_REPO:$IMAGE_TAG)..."
    docker build \
        --build-arg EVNODE_VERSION="$EVNODE_VERSION" \
        --build-arg IGNITE_VERSION="$IGNITE_VERSION" \
        --build-arg IGNITE_EVOLVE_APP_VERSION="$IGNITE_EVOLVE_APP_VERSION" \
        --build-arg ENABLE_IBC="$ENABLE_IBC" \
        -f tests/integration/docker/Dockerfile.gm \
        -t "$EVOLVE_IMAGE_REPO:$IMAGE_TAG" \
        .
    docker tag "$EVOLVE_IMAGE_REPO:$IMAGE_TAG" evabci/gm:local
    docker tag "$EVOLVE_IMAGE_REPO:$IMAGE_TAG" ghcr.io/evstack/evolve-abci-gm:local
fi

# Run Test
log "Running migration test..."
cd tests/integration

# Export expected env vars for the Go test
export EVOLVE_IMAGE_REPO="$EVOLVE_IMAGE_REPO"
export EVOLVE_IMAGE_TAG="$IMAGE_TAG"
export COSMOS_SDK_IMAGE_REPO="$COSMOS_SDK_IMAGE_REPO"
export COSMOS_SDK_IMAGE_TAG="$IMAGE_TAG"

go test -v -run TestMigrationSuite/TestCosmosToEvolveMigration -timeout 30m
