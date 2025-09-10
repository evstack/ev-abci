#!/bin/bash
set -euo pipefail

# Wrapper to run gm chain inside the container.
# - If GM_HOME already initialized, just start gmd.
# - Otherwise, delegate to init-gm.sh (which initializes and starts).

GM_HOME="${GM_HOME:-$HOME/.gm}"
CHAIN_ID="${CHAIN_ID:-gm}"
MONIKER="${MONIKER:-gm-local}"
ATTESTER_MODE="${ATTESTER_MODE:-false}"

echo "GM_HOME: $GM_HOME"
echo "CHAIN_ID: $CHAIN_ID"
echo "MONIKER: $MONIKER"
echo "ATTESTER_MODE: $ATTESTER_MODE"

# Ensure DA is reachable before doing anything
if [ -x "/home/gm/wait-for-da.sh" ]; then
  /home/gm/wait-for-da.sh
else
  echo "Waiting for local-da on local-da:7980..."
  for i in $(seq 1 30); do
    if nc -z local-da 7980 >/dev/null 2>&1; then echo "local-da is up"; break; fi
    sleep 2
  done
fi

# If GM_HOME already has a genesis, skip full init and start directly
if [ -f "$GM_HOME/config/genesis.json" ]; then
  echo "Existing genesis found. Starting gmd without re-initialization."
  exec gmd start \
    --rollkit.node.aggregator \
    --rollkit.da.address "http://local-da:7980" \
    --home "$GM_HOME" \
    --rpc.laddr tcp://0.0.0.0:26757 \
    --grpc.address 0.0.0.0:9190 \
    --api.address tcp://0.0.0.0:1417 \
    --log_format=json
fi

echo "No existing genesis. Running full initialization via init-gm.sh..."
exec /home/gm/init-gm.sh

