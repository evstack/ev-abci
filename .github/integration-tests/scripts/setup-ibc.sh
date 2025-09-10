#!/bin/bash
set -e  # Exit on any error

# Configuration for chains - adapted for Docker containers
GM_CHAIN_ID="gm"
GAIA_CHAIN_ID="gaia-local"
GM_RPC="http://gm-chain:26757"
GAIA_RPC="http://gaia-chain:26657"
GM_GRPC="http://gm-chain:9190"
GAIA_GRPC="http://gaia-chain:9090"

HERMES_CONFIG_DIR="$HOME/.hermes"
HERMES_CONFIG="$HERMES_CONFIG_DIR/config.toml"

# Mnemonic for relayer accounts (separate from validators)
RELAYER_MNEMONIC="abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art"

echo "🔗 Setting up IBC connection between GM and Gaia chains..."

# Check if hermes is installed
if ! command -v hermes &> /dev/null; then
    echo "❌ Error: hermes is not installed or not in PATH"
    echo "Install it with: cargo install ibc-relayer-cli --bin hermes"
    exit 1
fi

# Create hermes config directory
echo "📁 Creating hermes config directory..."
mkdir -p "$HERMES_CONFIG_DIR"

# Create hermes configuration
echo "⚙️ Creating hermes configuration..."
cat > "$HERMES_CONFIG" << 'EOF'
[global]
log_level = 'debug'

[mode]

[mode.clients]
enabled = true
refresh = true
misbehaviour = false

[mode.connections]
enabled = false

[mode.channels]
enabled = false

[mode.packets]
enabled = true
clear_interval = 100
clear_on_start = true
tx_confirmation = false

[rest]
enabled = false
host = '127.0.0.1'
port = 3000

[telemetry]
enabled = false
host = '127.0.0.1'
port = 3001

[[chains]]
id = 'gm'
type = 'CosmosSdk'
rpc_addr = 'http://gm-chain:26757'
grpc_addr = 'http://gm-chain:9190'
rpc_timeout = '10s'
trusted_node = false
account_prefix = 'gm'
key_name = 'gm-relayer'
key_store_type = 'Test'
store_prefix = 'ibc'
default_gas = 200000
max_gas = 2000000
gas_multiplier = 1.5
max_msg_num = 30
max_tx_size = 2097152
max_grpc_decoding_size = 33554432
clock_drift = '5s'
max_block_time = '30s'
ccv_consumer_chain = false
memo_prefix = ''
sequential_batch_tx = false

[chains.event_source]
mode = 'pull'
interval = '1s'

[chains.trust_threshold]
numerator = '1'
denominator = '3'

[chains.gas_price]
price = 10.0
denom = 'stake'

[chains.packet_filter]
policy = 'allow'
list = [
  ['transfer', 'channel-*'],
]

[chains.address_type]
derivation = 'cosmos'

[[chains]]
id = 'gaia-local'
type = 'CosmosSdk'
rpc_addr = 'http://gaia-chain:26657'
grpc_addr = 'http://gaia-chain:9090'
rpc_timeout = '10s'
trusted_node = false
account_prefix = 'cosmos'
key_name = 'gaia-relayer'
key_store_type = 'Test'
store_prefix = 'ibc'
default_gas = 200000
max_gas = 2000000
gas_multiplier = 1.5
max_msg_num = 30
max_tx_size = 2097152
max_grpc_decoding_size = 33554432
clock_drift = '5s'
max_block_time = '30s'
ccv_consumer_chain = false
memo_prefix = ''
sequential_batch_tx = false

[chains.event_source]
mode = 'pull'
interval = '1s'

[chains.trust_threshold]
numerator = '1'
denominator = '3'

[chains.gas_price]
price = 10.0
denom = 'stake'

[chains.packet_filter]
policy = 'allow'
list = [
  ['transfer', 'channel-*'],
]

[chains.address_type]
derivation = 'cosmos'
EOF

echo "✅ Hermes configuration created at $HERMES_CONFIG"
echo "📋 Validating Hermes configuration..."
hermes config validate || {
    echo "❌ Hermes configuration validation failed!"
    echo "📄 Configuration file content:"
    cat "$HERMES_CONFIG"
    exit 1
}
echo "✅ Hermes configuration is valid"

# Add relayer keys to hermes
echo "🔑 Adding relayer keys to hermes..."

# Add GM relayer key
echo "Adding GM relayer key..."
echo "$RELAYER_MNEMONIC" | hermes keys add --chain "$GM_CHAIN_ID" --mnemonic-file /dev/stdin --key-name "gm-relayer" --hd-path "m/44'/118'/0'/0/0" || echo "⚠️ GM key already exists"

# Add Gaia relayer key  
echo "Adding Gaia relayer key..."
echo "$RELAYER_MNEMONIC" | hermes keys add --chain "$GAIA_CHAIN_ID" --mnemonic-file /dev/stdin --key-name "gaia-relayer" --hd-path "m/44'/118'/0'/0/0" || echo "⚠️ Gaia key already exists"

# Wait for chains to be running
echo "⏳ Waiting for chains to be ready..."
sleep 5

# Check chain connectivity
echo "🔍 Checking chain connectivity..."
echo "🔍 Testing individual chain connections..."

echo "🔍 Testing GM chain ($GM_CHAIN_ID) at $GM_RPC..."
if command -v curl >/dev/null 2>&1; then
  if curl -s --connect-timeout 5 "$GM_RPC/status" | grep -q "result"; then
      echo "✅ GM chain is accessible via RPC"
      GM_BLOCK_HEIGHT=$(curl -s "$GM_RPC/status" | grep -o '"latest_block_height":"[0-9]*"' | grep -o '[0-9]*')
      echo "   Latest block height: $GM_BLOCK_HEIGHT"
  else
      echo "⚠️  GM RPC check failed (curl). Continuing..."
  fi
else
  echo "ℹ️  curl not available; skipping GM RPC check"
fi

# Test Gaia chain connection  
echo "🔍 Testing Gaia chain ($GAIA_CHAIN_ID) at $GAIA_RPC..."
if command -v curl >/dev/null 2>&1; then
  if curl -s --connect-timeout 5 "$GAIA_RPC/status" | grep -q "result"; then
      echo "✅ Gaia chain is accessible via RPC"
      GAIA_BLOCK_HEIGHT=$(curl -s "$GAIA_RPC/status" | grep -o '"latest_block_height":"[0-9]*"' | grep -o '[0-9]*')
      echo "   Latest block height: $GAIA_BLOCK_HEIGHT"
  else
      echo "⚠️  Gaia RPC check failed (curl). Continuing..."
  fi
else
  echo "ℹ️  curl not available; skipping Gaia RPC check"
fi

echo "🔍 Running Hermes health check..."
hermes health-check || {
    echo "⚠️ Health check had warnings, continuing anyway..."
    echo "   - GM chain: $GM_RPC"  
    echo "   - Gaia chain: $GAIA_RPC"
    echo "   - Check the logs above for specific connectivity issues"
}

# Wait until chains report catching_up=false (especially GM in attester mode)
echo "⏳ Waiting for chains to finish catching up..."

wait_for_not_catching_up() {
  local name="$1"; local url="$2"; local max_wait="${CATCHUP_MAX_WAIT:-180}"; local waited=0
  # If curl is not available (as in Hermes official image), skip this wait.
  if ! command -v curl >/dev/null 2>&1; then
    echo "ℹ️  curl not available; skipping catch-up wait for $name"
    return 0
  fi
  while true; do
    local status_json
    status_json=$(curl -s "$url/status" || echo "")
    if echo "$status_json" | grep -q '"catching_up":false'; then
      echo "✅ $name not catching up"
      return 0
    fi
    if [ "$waited" -ge "$max_wait" ]; then
      echo "⚠️ $name still catching_up after ${max_wait}s; proceeding anyway"
      return 0
    fi
    sleep 3
    waited=$((waited+3))
    echo "   $name catching_up=true... (${waited}/${max_wait}s)"
  done
}

wait_for_not_catching_up "GM chain" "$GM_RPC"
wait_for_not_catching_up "Gaia chain" "$GAIA_RPC"

# Create IBC client for GM on Gaia
echo "🔗 Creating IBC client for GM on Gaia..."
echo "📋 Pre-flight checks for client creation..."

# Check if GM chain is producing blocks
echo "   Checking if GM chain is producing blocks..."
if command -v curl >/dev/null 2>&1; then
  GM_HEIGHT_1=$(curl -s "$GM_RPC/status" | grep -o '"latest_block_height":"[0-9]*"' | grep -o '[0-9]*' || echo "0")
  sleep 3
  GM_HEIGHT_2=$(curl -s "$GM_RPC/status" | grep -o '"latest_block_height":"[0-9]*"' | grep -o '[0-9]*' || echo "0")
  if [[ "$GM_HEIGHT_2" -gt "$GM_HEIGHT_1" ]]; then
      echo "✅ GM chain is producing blocks ($GM_HEIGHT_1 -> $GM_HEIGHT_2)"
  else
      echo "⚠️ GM chain may not be producing blocks (height: $GM_HEIGHT_1)"
  fi
else
  echo "ℹ️  curl not available; skipping GM block production check"
fi

# Check chain consensus info
echo "   Getting GM chain consensus info..."
if command -v curl >/dev/null 2>&1; then
  GM_CONSENSUS=$(curl -s "$GM_RPC/consensus_state" 2>/dev/null || echo "failed")
  if [[ "$GM_CONSENSUS" != "failed" ]]; then
      echo "✅ GM chain consensus state accessible"
  else
      echo "⚠️ Could not get GM chain consensus state"
  fi
fi

echo "🚀 Attempting to create IBC client for GM on Gaia..."
echo "   Command: RUST_LOG=debug hermes create client --host-chain $GAIA_CHAIN_ID --reference-chain $GM_CHAIN_ID --trust-threshold 1/3"
echo "📝 Running with verbose logging..."

set +e  # Temporarily disable exit on error

# Create a temporary file to capture all output
TEMP_LOG_1=$(mktemp)
echo "🔍 Capturing output to temporary file: $TEMP_LOG_1"

# Run hermes with verbose logging and capture everything
RUST_LOG=debug hermes create client --host-chain "$GAIA_CHAIN_ID" --reference-chain "$GM_CHAIN_ID" --trust-threshold 1/3 > "$TEMP_LOG_1" 2>&1
CREATE_CLIENT_EXIT_CODE=$?

# Read the output
CLIENT_OUTPUT_1=$(cat "$TEMP_LOG_1")
CLIENT_GM_ON_GAIA=$(echo "$CLIENT_OUTPUT_1" | grep -o '07-tendermint-[0-9]*' | tail -1)

# Clean up temp file
rm -f "$TEMP_LOG_1"
set -e  # Re-enable exit on error

echo "📄 Full client creation output:"
echo "$CLIENT_OUTPUT_1"
echo "📊 Exit code: $CREATE_CLIENT_EXIT_CODE"

if [[ $CREATE_CLIENT_EXIT_CODE -eq 0 && -n "$CLIENT_GM_ON_GAIA" ]]; then
    echo "✅ Successfully created client for GM on Gaia: $CLIENT_GM_ON_GAIA"
else
    echo "❌ Failed to create client for GM on Gaia"
    echo "🔍 Troubleshooting information:"
    echo "   - Host chain: $GAIA_CHAIN_ID (should be running and accessible)"
    echo "   - Reference chain: $GM_CHAIN_ID (should be producing blocks)"
    echo "   - GM chain RPC: $GM_RPC"
    echo "   - Gaia chain RPC: $GAIA_RPC"
    echo "   - Check if both chains have recent blocks and are synced"
fi

# Create IBC client for Gaia on GM
echo "🔗 Creating IBC client for Gaia on GM..."
echo "📋 Pre-flight checks for client creation..."

# Check if Gaia chain is producing blocks
echo "   Checking if Gaia chain is producing blocks..."
if command -v curl >/dev/null 2>&1; then
  GAIA_HEIGHT_1=$(curl -s "$GAIA_RPC/status" | grep -o '"latest_block_height":"[0-9]*"' | grep -o '[0-9]*' || echo "0")
  sleep 3
  GAIA_HEIGHT_2=$(curl -s "$GAIA_RPC/status" | grep -o '"latest_block_height":"[0-9]*"' | grep -o '[0-9]*' || echo "0")
  if [[ "$GAIA_HEIGHT_2" -gt "$GAIA_HEIGHT_1" ]]; then
      echo "✅ Gaia chain is producing blocks ($GAIA_HEIGHT_1 -> $GAIA_HEIGHT_2)"
  else
      echo "⚠️ Gaia chain may not be producing blocks (height: $GAIA_HEIGHT_1)"
  fi
else
  echo "ℹ️  curl not available; skipping Gaia block production check"
fi

# Check chain consensus info
echo "   Getting Gaia chain consensus info..."
if command -v curl >/dev/null 2>&1; then
  GAIA_CONSENSUS=$(curl -s "$GAIA_RPC/consensus_state" 2>/dev/null || echo "failed")
  if [[ "$GAIA_CONSENSUS" != "failed" ]]; then
      echo "✅ Gaia chain consensus state accessible"
  else
      echo "⚠️ Could not get Gaia chain consensus state"
  fi
fi

echo "🚀 Attempting to create IBC client for Gaia on GM..."
echo "   Command: RUST_LOG=debug hermes create client --host-chain $GM_CHAIN_ID --reference-chain $GAIA_CHAIN_ID --trust-threshold 1/3"
echo "📝 Running with verbose logging..."

set +e

# Create a temporary file to capture all output
TEMP_LOG_2=$(mktemp)
echo "🔍 Capturing output to temporary file: $TEMP_LOG_2"

# Run hermes with verbose logging and capture everything
RUST_LOG=debug hermes create client --host-chain "$GM_CHAIN_ID" --reference-chain "$GAIA_CHAIN_ID" --trust-threshold 1/3 > "$TEMP_LOG_2" 2>&1
CREATE_CLIENT_EXIT_CODE_2=$?

# Read the output
CLIENT_OUTPUT_2=$(cat "$TEMP_LOG_2")
CLIENT_GAIA_ON_GM=$(echo "$CLIENT_OUTPUT_2" | grep -o '07-tendermint-[0-9]*' | tail -1)

# Clean up temp file
rm -f "$TEMP_LOG_2"
set -e

echo "📄 Full client creation output:"
echo "$CLIENT_OUTPUT_2"
echo "📊 Exit code: $CREATE_CLIENT_EXIT_CODE_2"

if [[ $CREATE_CLIENT_EXIT_CODE_2 -eq 0 && -n "$CLIENT_GAIA_ON_GM" ]]; then
    echo "✅ Successfully created client for Gaia on GM: $CLIENT_GAIA_ON_GM"
else
    echo "❌ Failed to create client for Gaia on GM"
    echo "🔍 Troubleshooting information:"
    echo "   - Host chain: $GM_CHAIN_ID (should be running and accessible)"
    echo "   - Reference chain: $GAIA_CHAIN_ID (should be producing blocks)"
    echo "   - GM chain RPC: $GM_RPC"
    echo "   - Gaia chain RPC: $GAIA_RPC"
    echo "   - Check if both chains have recent blocks and are synced"
fi

# Verify we have both clients
echo "🔍 Verifying client creation results..."
if [[ -z "$CLIENT_GM_ON_GAIA" ]]; then
    echo "❌ Missing client ID for GM on Gaia chain"
    echo "🔍 Raw output from first client creation:"
    echo "$CLIENT_OUTPUT_1"
fi

if [[ -z "$CLIENT_GAIA_ON_GM" ]]; then
    echo "❌ Missing client ID for Gaia on GM chain"
    echo "🔍 Raw output from second client creation:"
    echo "$CLIENT_OUTPUT_2"
fi

if [[ -z "$CLIENT_GM_ON_GAIA" || -z "$CLIENT_GAIA_ON_GM" ]]; then
    echo "❌ Failed to create IBC clients. Cannot proceed with connection setup."
    echo "🛠️ Possible issues:"
    echo "   1. Chains are not running or not accessible"
    echo "   2. Chains are not producing blocks"
    echo "   3. Hermes configuration is incorrect"
    echo "   4. Network connectivity issues between chains"
    echo "   5. Trust threshold or other IBC parameters are invalid"
    echo "📝 Check the debug logs above for specific error messages"
    exit 1
fi

echo "✅ Both IBC clients created successfully:"
echo "   - GM on Gaia: $CLIENT_GM_ON_GAIA"
echo "   - Gaia on GM: $CLIENT_GAIA_ON_GM"

# Create connection
echo "🔗 Creating IBC connection..."
echo "   Using clients: GM->$CLIENT_GAIA_ON_GM, Gaia->$CLIENT_GM_ON_GAIA"
echo "   Command: RUST_LOG=debug hermes create connection --a-chain $GM_CHAIN_ID --a-client $CLIENT_GAIA_ON_GM --b-client $CLIENT_GM_ON_GAIA"

set +e
# Create temporary file for connection output
TEMP_LOG_3=$(mktemp)
RUST_LOG=debug hermes create connection --a-chain "$GM_CHAIN_ID" --a-client "$CLIENT_GAIA_ON_GM" --b-client "$CLIENT_GM_ON_GAIA" > "$TEMP_LOG_3" 2>&1
CONNECTION_EXIT_CODE=$?
CONNECTION_OUTPUT=$(cat "$TEMP_LOG_3")
CONNECTION_ID=$(echo "$CONNECTION_OUTPUT" | grep -o 'connection-[0-9]*' | tail -1)
rm -f "$TEMP_LOG_3"
set -e

echo "📄 Full connection creation output:"
echo "$CONNECTION_OUTPUT"
echo "📊 Exit code: $CONNECTION_EXIT_CODE"

if [[ $CONNECTION_EXIT_CODE -eq 0 && -n "$CONNECTION_ID" ]]; then
    echo "✅ Successfully created connection: $CONNECTION_ID"
else
    echo "❌ Failed to create connection"
    echo "🔍 Connection troubleshooting:"
    echo "   - Verify both clients exist and are active"
    echo "   - Check that both chains are still running"
    echo "   - Ensure clients haven't expired"
fi

if [[ -z "$CONNECTION_ID" ]]; then
    echo "❌ Failed to create IBC connection. Cannot proceed with channel setup."
    echo "📝 Raw connection output analysis:"
    echo "$CONNECTION_OUTPUT"
    exit 1
fi

# Create transfer channel
echo "🔗 Creating transfer channel..."
echo "   Using connection: $CONNECTION_ID"
echo "   Command: RUST_LOG=debug hermes create channel --a-chain $GM_CHAIN_ID --a-connection $CONNECTION_ID --a-port transfer --b-port transfer"

set +e
# Create temporary file for channel output
TEMP_LOG_4=$(mktemp)
RUST_LOG=debug hermes create channel --a-chain "$GM_CHAIN_ID" --a-connection "$CONNECTION_ID" --a-port "transfer" --b-port "transfer" > "$TEMP_LOG_4" 2>&1
CHANNEL_EXIT_CODE=$?
CHANNEL_OUTPUT=$(cat "$TEMP_LOG_4")
CHANNEL_GM=$(echo "$CHANNEL_OUTPUT" | grep -o 'channel-[0-9]*' | head -1)
CHANNEL_GAIA=$(echo "$CHANNEL_OUTPUT" | grep -o 'channel-[0-9]*' | tail -1)
rm -f "$TEMP_LOG_4"
set -e

echo "📄 Full channel creation output:"
echo "$CHANNEL_OUTPUT"
echo "📊 Exit code: $CHANNEL_EXIT_CODE"

if [[ $CHANNEL_EXIT_CODE -eq 0 && -n "$CHANNEL_GM" && -n "$CHANNEL_GAIA" ]]; then
    echo "✅ Successfully created transfer channel: GM=$CHANNEL_GM, Gaia=$CHANNEL_GAIA"
else
    echo "❌ Failed to create transfer channel"
    echo "🔍 Channel troubleshooting:"
    echo "   - Verify connection $CONNECTION_ID exists and is active"
    echo "   - Check that transfer modules are enabled on both chains"
    echo "   - Ensure both chains support ICS-20 token transfers"
fi

# Save connection details for test script
cat > /tmp/ibc-connection-info << EOF
CLIENT_GM_ON_GAIA=$CLIENT_GM_ON_GAIA
CLIENT_GAIA_ON_GM=$CLIENT_GAIA_ON_GM
CONNECTION_ID=$CONNECTION_ID
CHANNEL_GM=$CHANNEL_GM
CHANNEL_GAIA=$CHANNEL_GAIA
EOF

echo "🎉 IBC setup completed successfully!"
echo ""
echo "📊 Connection Details:"
echo "   GM Chain:    $GM_CHAIN_ID ($GM_RPC)"
echo "   Gaia Chain:  $GAIA_CHAIN_ID ($GAIA_RPC)"
echo "   Connection:  $CONNECTION_ID"
echo "   GM Channel:  $CHANNEL_GM"
echo "   Gaia Channel: $CHANNEL_GAIA"
echo ""
