# Attester Command (beta)

The `attester` command is a standalone command that can be used with the ev-abci server to join the attester set and attest to blocks on an Evolve-based chain.

## Overview

The attester command:

- Joins the attester set by submitting a `MsgJoinAttesterSet` transaction
- Polls for new blocks via RPC
- Signs attestations using the validator's consensus key
- Submits `MsgAttest` transactions for each block
- Handles retries for failed attestations

## Usage

```bash
appd attester \
  --chain-id <chain-id> \
  --node tcp://localhost:26657 \
  --api-addr http://localhost:1317 \
  --home <validator-home-dir> \
  --mnemonic "your twelve or twenty four word mnemonic" \
  --verbose
```

## Flags

### Required Flags

- `--chain-id` - Chain ID of the blockchain

### Optional Flags

- `--node` - RPC node address (default: `tcp://localhost:26657`)
- `--api-addr` - API node address (default: `http://localhost:1317`)
- `--home` - Directory for config and data (validator home directory)
- `--verbose` - Enable verbose output (default: `false`)
- `--mnemonic` - Mnemonic for the operator private key
- `--priv-key-armor` - ASCII armored private key (alternative to mnemonic)
- `--bech32-account-prefix` - Bech32 prefix for account addresses (default: `gm`)
- `--bech32-account-pubkey` - Bech32 prefix for account public keys (default: `gmpub`)
- `--bech32-validator-prefix` - Bech32 prefix for validator addresses (default: `gmvaloper`)
- `--bech32-validator-pubkey` - Bech32 prefix for validator public keys (default: `gmvaloperpub`)

## Key Management

The attester requires two keys:

1. **Operator Key** - Used to pay transaction fees and submit transactions
   - Provided via `--mnemonic` or `--priv-key-armor`
   - Must have sufficient balance to pay for attestation transactions

2. **Consensus Key** - Used to sign attestations
   - Loaded from `$HOME/config/priv_validator_key.json`
   - Must match a validator registered on the chain

## How It Works

1. **Initialization**
   - Configures Cosmos SDK Bech32 prefixes
   - Loads operator private key from mnemonic or armored key
   - Loads consensus private key from validator home directory

2. **Join Attester Set**
   - Submits `MsgJoinAttesterSet` transaction
   - Waits for transaction confirmation
   - Handles case where validator is already in attester set

3. **Historical Attestations**
   - Fetches current blockchain height
   - Attests to all blocks from height 1 to current height
   - Retries failed attestations up to 3 times

4. **Live Attestations**
   - Polls for new blocks every 50ms
   - Attests to new blocks as they arrive
   - Catches up on any missed blocks

## Example

```bash
# Using mnemonic
appd attester \
  --chain-id evmallus-1 \
  --node tcp://localhost:26657 \
  --api-addr http://localhost:1317 \
  --home ~/.evnode \
  --mnemonic "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about" \
  --verbose

# Using custom Bech32 prefixes
appd attester \
  --chain-id cosmos-hub-1 \
  --node tcp://localhost:26657 \
  --home ~/.evnode \
  --mnemonic "your mnemonic here" \
  --bech32-account-prefix cosmos \
  --bech32-account-pubkey cosmospub \
  --bech32-validator-prefix cosmosvaloper \
  --bech32-validator-pubkey cosmosvaloperpub
```

## Error Handling

The attester handles several error scenarios:

- **Already in attester set** - Continues with attestations
- **Insufficient funds** - Reports error and exits
- **Sequence mismatch** - Syncs sequence number and retries
- **Block attestation failure** - Retries up to 3 times with backoff
- **RPC connection issues** - Retries with 100ms delay

## Operational Notes

- Sequence numbers are cached in memory to avoid duplicate transactions
- The attester automatically syncs sequence numbers if drift is detected
- Verbose mode provides detailed transaction logs and debugging information
- The attester gracefully shuts down on SIGINT/SIGTERM signals

## Limitations

- No persistent state across restarts (except on-chain state)
- Sequence numbers reset on restart, requiring sync with chain
- HTTP polling (no WebSocket subscription)
- Basic retry logic without exponential backoff
- Keys must be provided manually (no secure key management)
