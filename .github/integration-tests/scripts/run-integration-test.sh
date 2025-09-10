#!/bin/bash
set -euo pipefail

echo "🧪 Starting IBC Integration Tests"
echo "================================"
echo ""

# Configuration
GM_RPC="${GM_RPC:-http://gm-chain:26757}"
GM_GRPC="${GM_GRPC:-http://gm-chain:9190}"
GM_API="${GM_API:-http://gm-chain:1417}"
GAIA_RPC="${GAIA_RPC:-http://gaia-chain:26657}"
GAIA_GRPC="${GAIA_GRPC:-http://gaia-chain:9090}"
GAIA_API="${GAIA_API:-http://gaia-chain:1317}"
HERMES_HOME="${HERMES_HOME:-/home/tester/.hermes}"

# Test parameters
MAX_WAIT_TIME="${MAX_WAIT_TIME:-300}"  # 5 minutes max wait

echo "📋 Test Configuration:"
echo "   GM Chain RPC: $GM_RPC"
echo "   GM Chain API: $GM_API"
echo "   Gaia Chain RPC: $GAIA_RPC"
echo "   Gaia Chain API: $GAIA_API"
echo "   Max Wait Time: ${MAX_WAIT_TIME}s"
echo ""

# Step 1: Wait for chains to be ready
echo "🔄 Step 1: Waiting for chains to be ready..."
echo ""

echo "   Waiting for Gaia chain..."
./wait-for-chain.sh "$GAIA_RPC" "$GAIA_API"

echo ""
echo "   Waiting for GM chain..."
./wait-for-chain.sh "$GM_RPC" "$GM_API"

echo ""
echo "✅ Both chains are ready!"
echo ""

# Step 2: Wait for attester to be running
echo "🔄 Step 2: Waiting for attester to start..."
echo ""
./wait-for-attester.sh

echo ""
echo "✅ Attester is running!"
echo ""

# Step 3: Final validation
echo "🔄 Step 3: Final validation..."
echo ""

# Check that both chains are still healthy
echo "   Checking chain health..."
if ! curl -f -s "$GM_RPC/status" >/dev/null; then
    echo "❌ GM chain is not healthy"
    exit 1
fi

if ! curl -f -s "$GAIA_RPC/status" >/dev/null; then
    echo "❌ Gaia chain is not healthy"
    exit 1
fi

echo "✅ All chains are healthy"
echo ""

echo "🎉 Basic Setup Validation Passed!"
echo "================================="
echo ""
echo "✅ All validation steps completed successfully:"
echo "   ✅ Chains initialized and running"
echo "   ✅ Attester service operational"
echo "   ✅ System ready for IBC setup"
echo ""

# Step 4: Run IBC transfer tests
echo "🔄 Step 4: Running IBC transfer tests..."
echo ""
./test-transfers.sh

echo ""
echo "🎉 Integration Tests Completed Successfully!"
echo "=========================================="
echo ""
echo "✅ All test steps completed successfully:"
echo "   ✅ Chains initialized and running"
echo "   ✅ Attester service operational"  
echo "   ✅ IBC transfers tested"
echo ""
echo "🚀 Your IBC integration system is fully operational!"