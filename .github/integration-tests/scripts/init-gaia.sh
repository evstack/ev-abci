#!/bin/bash
set -euo pipefail

# Configuration
CHAIN_ID="${CHAIN_ID:-gaia-local}"
MONIKER="${MONIKER:-gaia-local}"
GAIA_HOME="${GAIA_HOME:-$HOME/.gaia_local}"
KEY_NAME="${KEY_NAME:-validator}"
KEYRING_BACKEND="${KEYRING_BACKEND:-test}"
MIN_GAS_PRICE="${MIN_GAS_PRICE:-0.01stake}"
STAKE_AMOUNT="${STAKE_AMOUNT:-500000000stake}"
GENESIS_COINS="${GENESIS_COINS:-100000000000stake}"

# Validator mnemonic (same as your script)
MNEMONIC="abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
# Relayer mnemonic (different from validator)
RELAYER_MNEMONIC="abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art"

echo "🚀 Initializing Gaia chain..."
echo "   Chain ID: $CHAIN_ID"
echo "   Moniker: $MONIKER"
echo "   Home: $GAIA_HOME"

# Initialize chain
if [[ ! -f "$GAIA_HOME/config/genesis.json" ]]; then
    echo "🔧 Initializing chain..."
    gaiad init "$MONIKER" --chain-id "$CHAIN_ID" --home "$GAIA_HOME"

    echo "🔑 Creating validator key..."
    echo "$MNEMONIC" | gaiad keys add "$KEY_NAME" --keyring-backend "$KEYRING_BACKEND" --home "$GAIA_HOME" --recover
    VALIDATOR_ADDRESS=$(gaiad keys show "$KEY_NAME" -a --keyring-backend "$KEYRING_BACKEND" --home "$GAIA_HOME")

    echo "🔑 Creating relayer key..."
    echo "$RELAYER_MNEMONIC" | gaiad keys add "relayer" --keyring-backend "$KEYRING_BACKEND" --home "$GAIA_HOME" --recover
    RELAYER_ADDRESS=$(gaiad keys show "relayer" -a --keyring-backend "$KEYRING_BACKEND" --home "$GAIA_HOME")

    echo "💰 Adding genesis accounts..."
    gaiad genesis add-genesis-account "$VALIDATOR_ADDRESS" "$GENESIS_COINS" --home "$GAIA_HOME"
    gaiad genesis add-genesis-account "$RELAYER_ADDRESS" "100000000000stake" --home "$GAIA_HOME"

    echo "🧾 Generating genesis transaction..."
    gaiad genesis gentx "$KEY_NAME" "$STAKE_AMOUNT" \
        --chain-id "$CHAIN_ID" \
        --keyring-backend "$KEYRING_BACKEND" \
        --home "$GAIA_HOME"

    echo "📦 Collecting genesis transactions..."
    gaiad genesis collect-gentxs --home "$GAIA_HOME"

    # Configure app.toml
    APP_TOML="$GAIA_HOME/config/app.toml"
    if [[ -f "$APP_TOML" ]]; then
        echo "⚙️ Configuring minimum gas prices..."
        sed -i "s|^minimum-gas-prices = \".*\"|minimum-gas-prices = \"$MIN_GAS_PRICE\"|" "$APP_TOML"
        
        # Enable API server
        sed -i 's|^enable = false|enable = true|' "$APP_TOML"
        sed -i 's|^address = "tcp://localhost:1317"|address = "tcp://0.0.0.0:1317"|' "$APP_TOML"
    fi

    # Configure config.toml for container networking
    CONFIG_TOML="$GAIA_HOME/config/config.toml"
    if [[ -f "$CONFIG_TOML" ]]; then
        echo "⚙️ Configuring networking..."
        # Set RPC to listen on all interfaces
        sed -i 's|^laddr = "tcp://127.0.0.1:26657"|laddr = "tcp://0.0.0.0:26657"|' "$CONFIG_TOML"
        # Set P2P to listen on all interfaces  
        sed -i 's|^laddr = "tcp://0.0.0.0:26656"|laddr = "tcp://0.0.0.0:26656"|' "$CONFIG_TOML"
        # Disable strict address book
        sed -i 's|^addr_book_strict = true|addr_book_strict = false|' "$CONFIG_TOML"
    fi

    echo "✅ Gaia chain initialized successfully"
else
    echo "ℹ️ Chain already initialized, using existing configuration"
fi

echo "🚀 Starting Gaia node..."
echo "   RPC: http://0.0.0.0:26657"
echo "   gRPC: 0.0.0.0:9090"
echo "   API: http://0.0.0.0:1317"

# Start the node
exec gaiad start --home "$GAIA_HOME" \
    --p2p.laddr tcp://0.0.0.0:26656 \
    --rpc.laddr tcp://0.0.0.0:26657 \
    --grpc.address 0.0.0.0:9090