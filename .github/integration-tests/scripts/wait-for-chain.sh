#!/bin/bash
set -euo pipefail

NODE_URL="${1:-http://localhost:26757}"
API_URL="${2:-http://localhost:1417}"
MAX_ATTEMPTS="${MAX_ATTEMPTS:-30}"
SLEEP_INTERVAL="${SLEEP_INTERVAL:-2}"

echo "⏳ Waiting for chain to be ready..."
echo "   Node RPC: $NODE_URL"
echo "   API: $API_URL"

for i in $(seq 1 $MAX_ATTEMPTS); do
    echo "   Attempt $i/$MAX_ATTEMPTS..."
    
    # Check if RPC is accessible and chain is producing blocks
    if curl -f -s "$NODE_URL/status" >/dev/null 2>&1; then
        # Get current block height - use /block endpoint for Rollkit/ev-abci compatibility
        HEIGHT=$(curl -s "$NODE_URL/block" | jq -r '.result.block.header.height // 0' 2>/dev/null || echo "0")
        
        if [[ "$HEIGHT" -gt 0 ]]; then
            echo "   Chain is producing blocks (height: $HEIGHT)"
            echo "✅ Chain is ready!"
            echo "   Block height: $HEIGHT"
            exit 0
        else
            echo "   Chain accessible but not producing blocks yet..."
        fi
    else
        echo "   Chain not yet accessible..."
    fi
    
    if [[ $i -lt $MAX_ATTEMPTS ]]; then
        sleep $SLEEP_INTERVAL
    fi
done

echo "❌ Chain is not ready after $MAX_ATTEMPTS attempts"
echo "   RPC URL: $NODE_URL"
echo "   API URL: $API_URL"
echo "   Consider increasing MAX_ATTEMPTS or checking chain service"
exit 1