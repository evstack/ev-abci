#!/bin/bash
set -euo pipefail

echo "💸 Testing IBC transfers..."
echo ""

# Configuration
GM_CHAIN_ID="gm"
GAIA_CHAIN_ID="gaia-local"
GM_RPC="${GM_RPC:-http://gm-chain:26757}"
GAIA_RPC="${GAIA_RPC:-http://gaia-chain:26657}"
GM_HOME="/tmp/gm-test-home"
GAIA_HOME="/tmp/gaia-test-home"

# Relayer mnemonic (same as used in setup-ibc.sh)
RELAYER_MNEMONIC="abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art"

# Load connection info
if [[ ! -f /tmp/ibc-connection-info ]]; then
    echo "❌ IBC connection info not found. Run setup-ibc.sh first."
    exit 1
fi

source /tmp/ibc-connection-info

echo "📋 Using IBC channels:"
echo "   GM Channel:   $CHANNEL_GM"  
echo "   Gaia Channel: $CHANNEL_GAIA"
echo ""

# Setup test homes and keyrings
echo "🔧 Setting up test environment..."
mkdir -p "$GM_HOME" "$GAIA_HOME"

# Initialize keyring for GM chain
echo "$RELAYER_MNEMONIC" | gmd keys add relayer --keyring-backend test --recover --home "$GM_HOME" >/dev/null 2>&1 || true

# Initialize keyring for Gaia chain - using 'validator' key like in your script
echo "$RELAYER_MNEMONIC" | gaiad keys add validator --keyring-backend test --recover --home "$GAIA_HOME" >/dev/null 2>&1 || true

# Get addresses using binaries
GM_RELAYER_ADDR=$(gmd keys show relayer -a --keyring-backend test --home "$GM_HOME")
GAIA_VALIDATOR_ADDR=$(gaiad keys show validator -a --keyring-backend test --home "$GAIA_HOME")

echo "📋 Test addresses:"
echo "   GM Relayer:     $GM_RELAYER_ADDR"
echo "   Gaia Validator: $GAIA_VALIDATOR_ADDR"  
echo ""

# Function to get all balances
get_all_balances() {
    local chain="$1"
    local address="$2"
    
    case "$chain" in
        "gm")
            gmd query bank balances "$address" --node "$GM_RPC" --home "$GM_HOME" --output json 2>/dev/null | jq -r '.balances[]? | .denom + ": " + .amount' || echo "No balances"
            ;;
        "gaia")
            gaiad query bank balances "$address" --chain-id "$GAIA_CHAIN_ID" --node "$GAIA_RPC" --home "$GAIA_HOME" --output json 2>/dev/null | jq -r '.balances[]? | .denom + ": " + .amount' || echo "No balances"
            ;;
    esac
}

# Check initial balances
echo "🔍 Checking initial balances..."
echo "GM relayer balances:"
get_all_balances "gm" "$GM_RELAYER_ADDR" | sed 's/^/   /'

echo ""
echo "Gaia validator balances:"  
get_all_balances "gaia" "$GAIA_VALIDATOR_ADDR" | sed 's/^/   /'
echo ""

# Test 1: Transfer from GM to Gaia
echo "💸 Test 1: Transferring 1000stake from GM to Gaia..."
echo "   Executing real IBC transfer..."

gmd tx ibc-transfer transfer transfer "$CHANNEL_GM" "$GAIA_VALIDATOR_ADDR" 1000stake \
    --from relayer \
    --chain-id "$GM_CHAIN_ID" \
    --keyring-backend test \
    --home "$GM_HOME" \
    --fees 500stake \
    --node "$GM_RPC" \
    --output json \
    -y

if [[ $? -eq 0 ]]; then
    echo "✅ GM to Gaia transfer submitted successfully"
else
    echo "❌ GM to Gaia transfer failed"
fi

# Test 2: Transfer from Gaia to GM  
echo ""
echo "💸 Test 2: Transferring 1000stake from Gaia to GM..."
echo "   Executing real IBC transfer..."

gaiad tx ibc-transfer transfer transfer "$CHANNEL_GAIA" "$GM_RELAYER_ADDR" 1000stake \
    --from validator \
    --chain-id "$GAIA_CHAIN_ID" \
    --keyring-backend test \
    --home "$GAIA_HOME" \
    --fees 200000stake \
    --node "$GAIA_RPC" \
    --output json \
    -y

if [[ $? -eq 0 ]]; then
    echo "✅ Gaia to GM transfer submitted successfully"
else
    echo "❌ Gaia to GM transfer failed"
fi

echo ""
echo "⏳ Waiting for transfers to be relayed..."

# Wait until BOTH chains show IBC tokens or timeout
MAX_RELAY_WAIT="${MAX_RELAY_WAIT:-300}"
WAITED=0
INTERVAL=5
while true; do
  # Recompute IBC token counts on each chain
  GM_IBC_TOKENS=$(gmd query bank balances "$GM_RELAYER_ADDR" --node "$GM_RPC" --home "$GM_HOME" --output json 2>/dev/null | jq -r '.balances[]? | select(.denom | startswith("ibc/")) | .denom + ": " + .amount' | wc -l | tr -d ' ')
  GAIA_IBC_TOKENS=$(gaiad query bank balances "$GAIA_VALIDATOR_ADDR" --chain-id "$GAIA_CHAIN_ID" --node "$GAIA_RPC" --home "$GAIA_HOME" --output json 2>/dev/null | jq -r '.balances[]? | select(.denom | startswith("ibc/")) | .denom + ": " + .amount' | wc -l | tr -d ' ')

  if [[ ${GM_IBC_TOKENS:-0} -gt 0 && ${GAIA_IBC_TOKENS:-0} -gt 0 ]]; then
    echo "✅ Both chains show IBC tokens (GM: ${GM_IBC_TOKENS}, Gaia: ${GAIA_IBC_TOKENS})"
    break
  fi

  if [[ $WAITED -ge $MAX_RELAY_WAIT ]]; then
    echo "⚠️  Transfers not fully relayed after ${MAX_RELAY_WAIT}s (GM ibc tokens: ${GM_IBC_TOKENS:-0}, Gaia ibc tokens: ${GAIA_IBC_TOKENS:-0})"
    break
  fi

  sleep "$INTERVAL"
  WAITED=$((WAITED+INTERVAL))
  echo "   ...waiting (${WAITED}/${MAX_RELAY_WAIT}s) | GM ibc: ${GM_IBC_TOKENS:-0}, Gaia ibc: ${GAIA_IBC_TOKENS:-0}"
done

# Check final balances
echo ""
echo "🔍 Checking final balances after transfer..."
echo "GM relayer balances:"
get_all_balances "gm" "$GM_RELAYER_ADDR" | sed 's/^/   /'

echo ""
echo "Gaia validator balances:"  
get_all_balances "gaia" "$GAIA_VALIDATOR_ADDR" | sed 's/^/   /'

# Look specifically for IBC tokens (they have ibc/ prefix)
echo ""
echo "🔍 Checking for IBC tokens..."

GM_IBC_TOKENS=${GM_IBC_TOKENS:-$(gmd query bank balances "$GM_RELAYER_ADDR" --node "$GM_RPC" --home "$GM_HOME" --output json 2>/dev/null | jq -r '.balances[]? | select(.denom | startswith("ibc/")) | .denom + ": " + .amount' | wc -l | tr -d ' ')}
GAIA_IBC_TOKENS=${GAIA_IBC_TOKENS:-$(gaiad query bank balances "$GAIA_VALIDATOR_ADDR" --chain-id "$GAIA_CHAIN_ID" --node "$GAIA_RPC" --home "$GAIA_HOME" --output json 2>/dev/null | jq -r '.balances[]? | select(.denom | startswith("ibc/")) | .denom + ": " + .amount' | wc -l | tr -d ' ')}

if [[ ${GM_IBC_TOKENS:-0} -gt 0 ]]; then
    echo "✅ GM chain has IBC tokens:"
    gmd query bank balances "$GM_RELAYER_ADDR" --node "$GM_RPC" --home "$GM_HOME" --output json 2>/dev/null | jq -r '.balances[]? | select(.denom | startswith("ibc/")) | "   " + .denom + ": " + .amount'
else
    echo "ℹ️  GM chain has no IBC tokens yet"
fi

if [[ ${GAIA_IBC_TOKENS:-0} -gt 0 ]]; then
    echo "✅ Gaia chain has IBC tokens:"
    gaiad query bank balances "$GAIA_VALIDATOR_ADDR" --chain-id "$GAIA_CHAIN_ID" --node "$GAIA_RPC" --home "$GAIA_HOME" --output json 2>/dev/null | jq -r '.balances[]? | select(.denom | startswith("ibc/")) | "   " + .denom + ": " + .amount'
else
    echo "ℹ️  Gaia chain has no IBC tokens yet"
fi

echo ""

# Final validation
echo ""
echo "🔍 Final validation..."

# Check if chains are still healthy
echo "   Checking chain health..."
if ! curl -f -s "$GM_RPC/status" >/dev/null; then
    echo "❌ GM chain is not healthy"
    exit 1
fi

if ! curl -f -s "$GAIA_RPC/status" >/dev/null; then
    echo "❌ Gaia chain is not healthy"
    exit 1
fi

echo "✅ Both chains are healthy"

# Test if balance queries are working
echo "   Testing balance query functionality..."
if get_all_balances "gm" "$GM_RELAYER_ADDR" >/dev/null && get_all_balances "gaia" "$GAIA_VALIDATOR_ADDR" >/dev/null; then
    echo "✅ Balance queries working correctly"
else
    echo "❌ Balance queries failed"
    exit 1
fi

echo ""

# Require tokens on BOTH chains to pass
if [[ ${GM_IBC_TOKENS:-0} -eq 0 || ${GAIA_IBC_TOKENS:-0} -eq 0 ]]; then
  echo "❌ IBC transfers incomplete: GM ibc tokens=${GM_IBC_TOKENS:-0}, Gaia ibc tokens=${GAIA_IBC_TOKENS:-0}"
  exit 1
fi

echo "🎉 IBC Transfer Tests Completed!"
echo "================================"
echo ""
echo "✅ Test Summary:"
echo "   ✅ Chain connectivity maintained"
echo "   ✅ Real IBC transfers executed"
echo "   ✅ Balance queries operational"
echo "   ✅ IBC channels functional"
echo "   ✅ IBC tokens successfully transferred on BOTH chains"
echo ""
echo "🚀 Your IBC integration system is fully operational!"
