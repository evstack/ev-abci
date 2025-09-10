#!/bin/bash
set -euo pipefail

GM_RPC="${GM_RPC:-http://gm-chain:26757}"
MAX_ATTEMPTS="${MAX_ATTEMPTS:-60}"
SLEEP_INTERVAL="${SLEEP_INTERVAL:-5}"

echo "⏳ Waiting for attester to be operational..."
echo "   GM RPC: $GM_RPC"
echo "   Checking for attestation activity..."

# Function to check if attester is working by looking for attestations
check_attester_activity() {
    # Check if there are any attester signatures being submitted
    # We'll look for network module queries and attestation activity
    
    # First, check if the chain has network module endpoints available
    if ! curl -f -s "$GM_RPC/status" >/dev/null 2>&1; then
        return 1
    fi
    
    # Get current block height
    HEIGHT=$(curl -s "$GM_RPC/status" 2>/dev/null | jq -r '.result.sync_info.latest_block_height // 0' || echo "0")
    
    if [[ "$HEIGHT" -lt 10 ]]; then
        echo "   Chain height too low ($HEIGHT), waiting for more blocks..."
        return 1
    fi
    
    # Check if we can query network module (indicates attester setup is ready)
    # Try to get epoch information - if this works, the network module is active
    local api_url="http://gm-chain:1417"
    if curl -f -s "$api_url/evabci/network/v1/params" >/dev/null 2>&1; then
        echo "   Network module is responsive"
        
        # Check for any attestation activity in recent blocks
        # Look for attestation at a checkpoint height (multiples of 10)
        local checkpoint_height=$((HEIGHT - HEIGHT % 10))
        if [[ $checkpoint_height -gt 0 ]]; then
            local attestation_result=$(curl -s "$api_url/evabci/network/v1/attestation/$checkpoint_height" 2>/dev/null || echo "")
            if [[ -n "$attestation_result" ]] && echo "$attestation_result" | jq -e '.bitmap' >/dev/null 2>&1; then
                echo "   Found attestation data at height $checkpoint_height"
                return 0
            fi
        fi
        
        # If we can query the network module but don't see attestations yet,
        # the attester might still be starting up
        echo "   Network module ready, waiting for attestation activity..."
        return 1
    else
        echo "   Network module not yet available..."
        return 1
    fi
}

for i in $(seq 1 $MAX_ATTEMPTS); do
    echo "   Attempt $i/$MAX_ATTEMPTS..."
    
    if check_attester_activity; then
        echo "✅ Attester is operational!"
        exit 0
    fi
    
    if [[ $i -lt $MAX_ATTEMPTS ]]; then
        echo "   Waiting ${SLEEP_INTERVAL}s before next check..."
        sleep $SLEEP_INTERVAL
    fi
done

echo "❌ Attester did not become operational after $MAX_ATTEMPTS attempts"
echo "   This could mean:"
echo "   1. Attester container failed to start"
echo "   2. Attester is not successfully connecting to GM chain"
echo "   3. Attester is not producing valid attestations"
echo "   4. Network module is not properly configured"
echo ""
echo "🔍 Debug information:"
echo "   GM RPC: $GM_RPC"

# Try to get some debug info
HEIGHT=$(curl -s "$GM_RPC/status" 2>/dev/null | jq -r '.result.sync_info.latest_block_height // "unknown"' || echo "unknown")
echo "   Current chain height: $HEIGHT"

# Check if network module params are available
api_url="http://gm-chain:1417"
if curl -f -s "$api_url/evabci/network/v1/params" >/dev/null 2>&1; then
    echo "   ✅ Network module is accessible"
    
    # Try to get epoch info
    params=$(curl -s "$api_url/evabci/network/v1/params" 2>/dev/null || echo "{}")
    epoch_length=$(echo "$params" | jq -r '.params.epoch_length // "unknown"' 2>/dev/null || echo "unknown")
    sign_mode=$(echo "$params" | jq -r '.params.sign_mode // "unknown"' 2>/dev/null || echo "unknown")
    
    echo "   Epoch length: $epoch_length"
    echo "   Sign mode: $sign_mode"
else
    echo "   ❌ Network module is not accessible"
fi

exit 1