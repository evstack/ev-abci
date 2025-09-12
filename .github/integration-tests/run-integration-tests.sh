#!/bin/bash
set -euo pipefail

# Integration Test Runner for IBC Attester System
# This script automates the complete integration test workflow

# Configuration
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INTEGRATION_DIR="$SCRIPT_DIR"
COMPOSE_FILE="$INTEGRATION_DIR/docker-compose.yml"
LOG_DIR="$INTEGRATION_DIR/logs"

# Default options
CLEANUP=${CLEANUP:-true}
VERBOSE=${VERBOSE:-false}
BUILD_FRESH=${BUILD_FRESH:-false}
TIMEOUT=${TIMEOUT:-600}  # 10 minutes default timeout

export DO_NOT_TRACK=${DO_NOT_TRACK:-true}
export EVNODE_VERSION=${EVNODE_VERSION:-"v1.0.0-beta.2.0.20250908090838-0584153217ed"}
export EVNODE_DA_VERSION=${EVNODE_DA_VERSION:-"v1.0.0-beta.1"}
export IGNITE_VERSION=${IGNITE_VERSION:-"v29.3.1"}
export IGNITE_EVOLVE_APP_VERSION=${IGNITE_EVOLVE_APP_VERSION:-"main"}
export GAIA_VERSION=${GAIA_VERSION:-"v25.1.0"}
export TASTORA_REF=${TASTORA_REF:-"main"}

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $*"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $*"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $*"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $*"
}

# Show usage
usage() {
    cat << EOF
Usage: $0 [OPTIONS]

Run IBC integration tests for the attester system.

OPTIONS:
    --no-cleanup         Don't cleanup containers after test
    --verbose           Enable verbose logging
    --build-fresh       Force rebuild of all Docker images
    --timeout SECONDS   Maximum time to wait for tests (default: 600)
    --help              Show this help message

EXAMPLES:
    $0                          # Run tests with default settings
    $0 --verbose --no-cleanup   # Run with verbose output, keep containers
    $0 --build-fresh            # Force rebuild images and run tests
    $0 --timeout 300            # Run with 5 minute timeout

ENVIRONMENT VARIABLES:
    CLEANUP=false       # Same as --no-cleanup
    VERBOSE=true        # Same as --verbose
    BUILD_FRESH=true    # Same as --build-fresh
    TIMEOUT=300         # Same as --timeout 300

EOF
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --no-cleanup)
            CLEANUP=false
            shift
            ;;
        --verbose)
            VERBOSE=true
            shift
            ;;
        --build-fresh)
            BUILD_FRESH=true
            shift
            ;;
        --timeout)
            TIMEOUT="$2"
            shift 2
            ;;
        --help)
            usage
            exit 0
            ;;
        *)
            log_error "Unknown option: $1"
            usage
            exit 1
            ;;
    esac
done

# Validate requirements
check_requirements() {
    log_info "Checking requirements..."
    
    # Check Docker
    if ! command -v docker &> /dev/null; then
        log_error "Docker is not installed or not in PATH"
        exit 1
    fi
    
    # Check Docker Compose
    if ! docker compose version &> /dev/null; then
        log_error "Docker Compose is not available"
        log_error "Make sure you have Docker Compose v2 installed"
        exit 1
    fi
    
    # Check if integration test directory exists
    if [[ ! -d "$INTEGRATION_DIR" ]]; then
        log_error "Integration test directory not found: $INTEGRATION_DIR"
        log_error "Run this script from the project root directory"
        exit 1
    fi
    
    # Check if compose file exists
    if [[ ! -f "$COMPOSE_FILE" ]]; then
        log_error "Docker Compose file not found: $COMPOSE_FILE"
        exit 1
    fi
    
    log_success "All requirements met"
}

# Setup test environment
setup_environment() {
    log_info "Setting up test environment..."
    
    # Create log directory
    mkdir -p "$LOG_DIR"
    
    # Set script permissions
    chmod +x "$INTEGRATION_DIR"/scripts/*.sh
    
    # Clean up any previous runs
    cd "$INTEGRATION_DIR"
    docker compose down -v --remove-orphans &>/dev/null || true
    
    log_success "Test environment ready"
}

# Build Docker images
build_images() {
    log_info "Building Docker images..."
    cd "$INTEGRATION_DIR"
    
    local build_args=()
    
    if [[ "$BUILD_FRESH" == "true" ]]; then
        log_info "Force rebuilding all images..."
        build_args+=(--no-cache)
    fi
    
    if [[ "$VERBOSE" == "true" ]]; then
        build_args+=(--progress=plain)
    fi
    
    log_info "Using versions: IGNITE_VERSION=$IGNITE_VERSION, IGNITE_EVOLVE_APP_VERSION=$IGNITE_EVOLVE_APP_VERSION, EVNODE_VERSION=$EVNODE_VERSION, EVNODE_DA_VERSION=$EVNODE_DA_VERSION, GAIA_VERSION=$GAIA_VERSION"
    
    if ! docker compose build "${build_args[@]+"${build_args[@]}"}"; then
        log_error "Failed to build Docker images"
        return 1
    fi
    
    log_success "Docker images built successfully"
}

# Start services
start_services() {
    log_info "Starting services..."
    cd "$INTEGRATION_DIR"
    
    # Start infrastructure services first (including attester)
    log_info "Starting infrastructure services (local-da, gaia-chain, gm-chain, attester)..."
    docker compose up -d local-da gaia-chain gm-chain attester
    
    # Wait for chains to be healthy
    log_info "Waiting for chains to be healthy..."
    local max_wait=120
    local wait_count=0
    
    while [[ $wait_count -lt $max_wait ]]; do
        if docker compose ps | grep -E "(gaia-chain|gm-chain)" | grep -q "healthy"; then
            log_success "Chains are healthy"
            break
        fi
        
        sleep 5
        wait_count=$((wait_count + 5))
        log_info "Waiting for chains... ($wait_count/${max_wait}s)"
    done
    
    if [[ $wait_count -ge $max_wait ]]; then
        log_error "Chains did not become healthy within ${max_wait}s"
        # Dump recent gm-chain logs to console for quick diagnosis
        log_info "Recent gm-chain logs (last 200 lines):"
        docker compose logs --tail=200 gm-chain || true
        show_service_status
        return 1
    fi
    
    log_success "Infrastructure services started and ready"
}

# Setup IBC connections
setup_ibc() {
    log_info "Starting IBC setup..."
    cd "$INTEGRATION_DIR"
    
    # Start IBC setup container (runs to completion)
    docker compose up -d ibc-setup

    # Wait for ibc-setup to produce the connection info in the shared volume
    log_info "Waiting for IBC channels to be created..."
    local max_wait=600  # allow up to 10 minutes for setup
    local wait_count=0

    while [[ $wait_count -lt $max_wait ]]; do
        # Probe the shared volume using the test-runner image (has /tmp mounted)
        if docker compose run --rm -T test-runner sh -lc 'test -f /tmp/ibc-connection-info' >/dev/null 2>&1; then
            log_success "IBC channels created successfully"
            return 0
        fi

        sleep 10
        wait_count=$((wait_count + 10))
        log_info "Waiting for IBC setup to complete... ($wait_count/${max_wait}s)"

        # Show some progress by showing recent logs
        if [[ $((wait_count % 60)) -eq 0 ]]; then
            log_info "Recent IBC setup logs:"
            docker compose logs --tail=10 ibc-setup || true
            
            # Also check if attester is still having issues
            if docker compose ps attester | grep -q "Restarting\|Exited"; then
                log_warning "Note: Attester service is still restarting, which may affect IBC setup"
                docker compose logs --tail=5 attester || true
            fi
        fi
    done

    log_error "IBC setup did not produce connection info within ${max_wait}s"
    docker compose logs ibc-setup || true
    return 1
}

# Start Hermes relayer
start_hermes() {
    log_info "Starting Hermes IBC relayer..."
    cd "$INTEGRATION_DIR"
    
    docker compose up -d hermes-relayer
    
    # Give it a moment to start
    sleep 5
    
    # Check if hermes is running
    if docker compose ps hermes-relayer | grep -q "Up"; then
        log_success "Hermes relayer started successfully"
    else
        log_error "Failed to start Hermes relayer"
        docker compose logs hermes-relayer
        return 1
    fi
}

# Run integration tests
run_tests() {
    log_info "Running integration tests..."
    cd "$INTEGRATION_DIR"
    
    # Ensure connection info is available before starting tests
    if ! docker compose run --rm -T test-runner sh -lc 'test -f /tmp/ibc-connection-info' >/dev/null 2>&1; then
        log_error "Connection info not available; cannot run tests"
        docker compose logs ibc-setup || true
        return 1
    fi

    # Start test runner
    docker compose up -d test-runner

    # Robustly wait for the test container lifecycle to finish
    local container_name="ibc-test-runner"
    local appear_timeout=60  # wait up to 60s for container to appear
    local appear_wait=0

    log_info "Waiting for test-runner container to appear..."
    while [[ $appear_wait -lt $appear_timeout ]]; do
        if docker ps -a --format '{{.Names}}' | grep -qx "$container_name"; then
            break
        fi
        sleep 2
        appear_wait=$((appear_wait + 2))
    done

    if ! docker ps -a --format '{{.Names}}' | grep -qx "$container_name"; then
        log_error "Test runner container did not appear within ${appear_timeout}s"
        docker compose logs test-runner || true
        return 1
    fi

    # Wait for tests to complete
    log_info "Waiting for integration tests to complete..."
    local max_wait=300  # 5 minutes for tests
    local wait_count=0

    while [[ $wait_count -lt $max_wait ]]; do
        local status
        status=$(docker inspect -f '{{.State.Status}}' "$container_name" 2>/dev/null || echo "unknown")

        if [[ "$status" == "exited" || "$status" == "dead" ]]; then
            local exit_code
            exit_code=$(docker inspect -f '{{.State.ExitCode}}' "$container_name" 2>/dev/null || echo "1")
            if [[ "$exit_code" == "0" ]]; then
                log_success "Integration tests passed!"
                docker compose logs test-runner || true
                return 0
            else
                log_error "Integration tests failed with exit code: $exit_code"
                docker compose logs test-runner || true
                return 1
            fi
        fi

        sleep 5
        wait_count=$((wait_count + 5))
        log_info "Running tests... ($wait_count/${max_wait}s) [status: $status]"

        # Show progress periodically
        if [[ $((wait_count % 30)) -eq 0 ]]; then
            log_info "Recent test logs:"
            docker compose logs --tail=5 test-runner || true
        fi
    done

    log_error "Tests did not complete within ${max_wait}s"
    docker compose logs test-runner || true
    return 1
}

# Show service status
show_service_status() {
    log_info "Service status:"
    cd "$INTEGRATION_DIR"
    docker compose ps
    
    if [[ "$VERBOSE" == "true" ]]; then
        echo ""
        log_info "Service logs:"
        for service in local-da gaia-chain gm-chain attester; do
            echo ""
            echo "=== $service ==="
            docker compose logs --tail=20 "$service" 2>/dev/null || echo "No logs available"
        done
    else
        # Always show attester logs if it's having issues
        if docker compose ps attester | grep -q "Restarting\|Exited"; then
            echo ""
            log_warning "Attester service appears to be having issues:"
            echo "=== attester logs ==="
            docker compose logs --tail=50 attester 2>/dev/null || echo "No logs available"
        fi
    fi
}


# Cleanup function
cleanup() {
    if [[ "$CLEANUP" == "true" ]]; then
        log_info "Cleaning up test environment..."
        cd "$INTEGRATION_DIR"
        
        # Collect logs before cleanup
        collect_logs
        
        # Stop and remove containers
        docker compose down -v --remove-orphans
        
        # Optional: clean up images (uncomment if needed)
        # docker compose down --rmi all
        
        log_success "Cleanup completed"
    else
        log_warning "Skipping cleanup (--no-cleanup specified)"
        log_info "To manually cleanup run: cd $INTEGRATION_DIR && docker compose down -v"
    fi
}

# Collect logs for debugging
collect_logs() {
    log_info "Collecting logs..."
    cd "$INTEGRATION_DIR"
    
    local timestamp=$(date +"%Y%m%d_%H%M%S")
    local log_archive="$LOG_DIR/integration_test_logs_$timestamp"
    
    mkdir -p "$log_archive"
    
    # Collect container logs
    for service in local-da gaia-chain gm-chain attester ibc-setup hermes-relayer test-runner; do
        docker compose logs "$service" > "$log_archive/${service}.log" 2>/dev/null || echo "No logs for $service" > "$log_archive/${service}.log"
    done

    # Attempt to collect gm-chain node home (config/data/logs)
    mkdir -p "$log_archive/gm-home"
    docker compose cp gm-chain:/home/gm/.gm "$log_archive/gm-home" 2>/dev/null || true
    
    # Collect service status
    docker compose ps > "$log_archive/service_status.txt" 2>/dev/null || true
    
    # Create summary
    cat > "$log_archive/test_summary.txt" << EOF
Integration Test Summary
========================
Date: $(date)
Script: $0
Arguments: $*
Cleanup: $CLEANUP
Verbose: $VERBOSE
Build Fresh: $BUILD_FRESH
Timeout: $TIMEOUT
EOF
    
    log_success "Logs collected in: $log_archive"
}

# Main execution function
main() {
    log_info "🧪 Starting IBC Integration Tests"
    log_info "=================================="
    
    local start_time=$(date +%s)
    local exit_code=0
    
    # Setup trap for cleanup
    trap cleanup EXIT
    
    # Execute test steps
    check_requirements
    setup_environment
    
    if ! build_images; then
        log_error "Image build failed"
        exit_code=1
        exit $exit_code
    fi
    
    # Step 1: Start infrastructure services (local-da, gaia-chain, gm-chain, attester)
    if ! start_services; then
        log_error "Service startup failed"
        exit_code=1
        exit $exit_code
    fi
    
    show_service_status
    
    # Step 2: Setup IBC connections and wait for completion
    if ! setup_ibc; then
        log_error "IBC setup failed"
        exit_code=1
        exit $exit_code
    fi
    
    # Step 3: Start Hermes relayer
    if ! start_hermes; then
        log_error "Hermes relayer startup failed"
        exit_code=1
        exit $exit_code
    fi
    
    # Step 4: Run integration tests
    if ! run_tests; then
        log_error "Integration tests failed"
        exit_code=1
        exit $exit_code
    fi
    
    # Calculate duration
    local end_time=$(date +%s)
    local duration=$((end_time - start_time))
    
    log_info "Test duration: ${duration}s"
    log_info "=================================="
    
    exit $exit_code
}

# Handle script interruption
trap 'log_error "Script interrupted"; cleanup; exit 130' INT TERM

# Run main function
main "$@"
