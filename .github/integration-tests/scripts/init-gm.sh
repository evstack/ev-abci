#!/bin/bash
set -euo pipefail
# Enable shell tracing when DEBUG=1 for easier CI debugging
if [[ "${DEBUG:-0}" == "1" ]]; then
  set -x
fi

# Configuration
CHAIN_ID="${CHAIN_ID:-gm}"
MONIKER="${MONIKER:-gm}"
GM_HOME="${GM_HOME:-$HOME/.gm}"
KEY_NAME="${KEY_NAME:-validator}"
KEYRING_BACKEND="${KEYRING_BACKEND:-test}"
BINARY="${BINARY:-gmd}"
ATTESTER_MODE="${ATTESTER_MODE:-false}"

# Mnemonics
MNEMONIC="abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
RELAYER_MNEMONIC="abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art"

echo "🚀 Initializing GM chain..."
echo "   Chain ID: $CHAIN_ID"
echo "   Moniker: $MONIKER" 
echo "   Home: $GM_HOME"
echo "   Attester Mode: $ATTESTER_MODE"
echo "   Ignite: $(command -v ignite || echo 'ignite not found')"
if command -v ignite >/dev/null 2>&1; then
  ignite version || true
fi

# Wait for local-da to be available
echo "⏳ Waiting for local-da to be available..."
./wait-for-da.sh

# Reset and initialize chain
if [[ -d "$GM_HOME" ]]; then
    # If it's a mount point (Docker volume), just clear contents instead of removing directory
    if mountpoint -q "$GM_HOME" 2>/dev/null || [[ $(stat -c %d "$GM_HOME" 2>/dev/null || echo "0") != $(stat -c %d "$GM_HOME/.." 2>/dev/null || echo "1") ]]; then
        echo "🧹 Clearing existing chain state from volume..."
        rm -rf "$GM_HOME"/* "$GM_HOME"/.* 2>/dev/null || true
    else
        rm -rf "$GM_HOME"
        echo "🧹 Removed existing chain state"
    fi
fi

echo "🔧 Initializing chain with ignite evolve..."
cd /home/gm/gm
ls -la || true
ignite evolve init

echo "🔑 Setting up keys..."
# Add validator key (same key will be used for attester)
echo "$MNEMONIC" | "$BINARY" keys add "$KEY_NAME" --keyring-backend "$KEYRING_BACKEND" --home "$GM_HOME" --recover --hd-path "m/44'/118'/0'/0/0"
VALIDATOR_ADDRESS=$("$BINARY" keys show "$KEY_NAME" -a --keyring-backend "$KEYRING_BACKEND" --home "$GM_HOME")
ATTESTER_ADDRESS="$VALIDATOR_ADDRESS"

# Add relayer key (different mnemonic)
echo "$RELAYER_MNEMONIC" | "$BINARY" keys add "relayer" --keyring-backend "$KEYRING_BACKEND" --home "$GM_HOME" --recover --hd-path "m/44'/118'/0'/0/0"
RELAYER_ADDRESS=$("$BINARY" keys show "relayer" -a --keyring-backend "$KEYRING_BACKEND" --home "$GM_HOME")

# Ensure client.toml has the correct RPC node (127.0.0.1:26757 instead of localhost:26657)
mkdir -p "$GM_HOME/config"
if [ -f "$GM_HOME/config/client.toml" ]; then
  sed -i 's|^node *=.*|node = "tcp://127.0.0.1:26757"|' "$GM_HOME/config/client.toml"
else
  cat > "$GM_HOME/config/client.toml" <<EOF
[client]
node = "tcp://127.0.0.1:26757"
EOF
fi

echo "💰 Adding genesis accounts..."
"$BINARY" genesis add-genesis-account "$VALIDATOR_ADDRESS" "100000000stake,10000token" --home "$GM_HOME"
"$BINARY" genesis add-genesis-account "$RELAYER_ADDRESS" "100000000stake,10000token" --home "$GM_HOME"

echo "✅ GM chain initialized successfully"
echo "   Validator: $VALIDATOR_ADDRESS"
echo "   Attester: $ATTESTER_ADDRESS"
echo "   Relayer: $RELAYER_ADDRESS"

# Copy configuration files to shared volume for attester access
echo "📋 Copying config files to shared volume..."
# Fix shared directory permissions and create structure
sudo chown -R gm:gm /shared 2>/dev/null || chown -R gm:gm /shared 2>/dev/null || true
mkdir -p /shared/config /shared/data 2>/dev/null || true
if [[ -d "/shared" && -w "/shared" ]]; then
    cp -r "$GM_HOME/config"/* /shared/config/ 2>/dev/null || true
    cp -r "$GM_HOME/data"/* /shared/data/ 2>/dev/null || true
    # Copy other important files to root of shared volume
    cp "$GM_HOME"/keyring-test-* /shared/ 2>/dev/null || true
    echo "   Config files available for attester"
else
    echo "   ⚠️  Cannot write to /shared - attester may not have access to keys"
fi

# Build start command
START_CMD=("$BINARY" start --rollkit.node.aggregator \
    --rollkit.da.address "http://local-da:7980" \
    --home "$GM_HOME" \
    --rpc.laddr tcp://0.0.0.0:26757 \
    --grpc.address 0.0.0.0:9190 \
    --api.address tcp://0.0.0.0:1417)

# Add attester-specific flag if in attester mode
if [[ "$ATTESTER_MODE" == "true" ]]; then
    START_CMD+=(--evnode.attester-mode=true)
    echo "🚀 Starting GM chain in ATTESTER MODE..."
else
    echo "🚀 Starting GM chain in NORMAL MODE..."
fi

echo "   RPC: http://0.0.0.0:26757"
echo "   gRPC: 0.0.0.0:9190"  
echo "   API: http://0.0.0.0:1417"

# Copy configuration files to shared volume before starting (in case of restart)
echo "📋 Ensuring config files are available in shared volume..."
# Ensure permissions are correct and directory exists
sudo chown -R gm:gm /shared 2>/dev/null || chown -R gm:gm /shared 2>/dev/null || true
mkdir -p /shared/config /shared/data 2>/dev/null || true
if [[ -d "/shared" && -w "/shared" ]]; then
    cp -r "$GM_HOME/config"/* /shared/config/ 2>/dev/null || true
    cp -r "$GM_HOME/data"/* /shared/data/ 2>/dev/null || true
    # Copy other important files to root of shared volume
    cp "$GM_HOME"/keyring-test-* /shared/ 2>/dev/null || true
    echo "   Config files synced for attester"
else
    echo "   ⚠️  Cannot write to /shared - attester may not have access to keys"
fi

# Execute the start command
exec "${START_CMD[@]}"
