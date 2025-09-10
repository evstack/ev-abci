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