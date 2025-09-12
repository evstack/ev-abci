#!/bin/bash
set -euo pipefail

# Configuration
CHAIN_ID="${CHAIN_ID:-gm}"
GM_HOME="${GM_HOME:-/tmp/.gm}"
GM_NODE="${GM_NODE:-http://gm-chain:26757}"
GM_API="${GM_API:-http://gm-chain:1417}"
MNEMONIC="${MNEMONIC:-abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about}"
VERBOSE="${VERBOSE:-true}"

echo "🤖 Starting attester service..."
echo "   Chain ID: $CHAIN_ID"
echo "   Node: $GM_NODE"
echo "   API: $GM_API"
echo "   Home: $GM_HOME"

# Wait for GM chain to be ready
echo "⏳ Waiting for GM chain to be ready..."
./wait-for-chain.sh "$GM_NODE" "$GM_API"

echo "🔍 Checking for required validator files..."
echo "   Looking for files in $GM_HOME..."
echo "   Directory contents:"
find "$GM_HOME" -type f 2>/dev/null || echo "   Directory doesn't exist or is empty"

# The gm-chain copies files to /shared which is mounted as $GM_HOME
# Files should be at $GM_HOME/config/ and $GM_HOME/data/ 
PRIV_KEY_FILE="$GM_HOME/config/priv_validator_key.json"
PRIV_STATE_FILE="$GM_HOME/data/priv_validator_state.json"

if [[ ! -f "$PRIV_KEY_FILE" ]]; then
    echo "❌ ERROR: priv_validator_key.json not found at $PRIV_KEY_FILE"
    echo "   Available files in config:"
    ls -la "$GM_HOME/config/" 2>/dev/null || echo "   config/ directory doesn't exist"
    exit 1
fi

if [[ ! -f "$PRIV_STATE_FILE" ]]; then
    echo "❌ ERROR: priv_validator_state.json not found at $PRIV_STATE_FILE"
    echo "   Available files in data:"
    ls -la "$GM_HOME/data/" 2>/dev/null || echo "   data/ directory doesn't exist"
    exit 1
fi

echo "✅ Validator files found at:"
echo "   Key: $PRIV_KEY_FILE"
echo "   State: $PRIV_STATE_FILE"
echo "🚀 Attester is ready, starting attestation..."

# Build attester command
ATTESTER_CMD=(attester
    --chain-id="$CHAIN_ID"
    --home="$GM_HOME"
    --mnemonic="$MNEMONIC"
    --api-addr="$GM_API"
    --node="$GM_NODE"
)

if [[ "$VERBOSE" == "true" ]]; then
    ATTESTER_CMD+=(--verbose)
fi

echo "   Command: ${ATTESTER_CMD[*]}"

# Execute attester
exec "${ATTESTER_CMD[@]}"