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
  --mnemonic "your twelve or twenty four word mnemonic" \
  [--verbose]
```

The command uses the standard Cosmos SDK client flags for configuration:

```bash
appd attester \
  --chain-id <chain-id> \
  --node tcp://localhost:26657 \
  --home <validator-home-dir> \
  --mnemonic "your mnemonic" \
  --verbose
```

## Flags

### Attester-Specific Flags

- `--mnemonic` - Mnemonic for the operator private key (required if `--priv-key-armor` not provided)
- `--priv-key-armor` - ASCII armored private key (alternative to mnemonic)
- `--verbose` - Enable verbose output (default: `false`)

### Standard Client Flags

The command inherits standard Cosmos SDK client flags:

- `--chain-id` - Chain ID of the blockchain (required)
- `--node` - RPC node address (default: `tcp://localhost:26657`)
- `--home` - Directory for config and data (validator home directory)

For a full list of available client flags, run:

```bash
appd attester --help
```

## Key Management

The attester requires two keys:

1. **Operator Key** - Used to pay transaction fees and submit transactions
   - Provided via `--mnemonic` or `--priv-key-armor`
   - Must have sufficient balance to pay for attestation transactions
   - Derived using standard Cosmos SDK HD path (m/44'/118'/0'/0/0)

2. **Consensus Key** - Used to sign attestations
   - Loaded from `$HOME/config/priv_validator_key.json`
   - Must match a validator registered on the chain
   - Uses the validator's CometBFT consensus private key

## How It Works

1. **Initialization**
   - Reads configuration from client context (chain-id, node, home)
   - Configures Cosmos SDK Bech32 prefixes from the client context
   - Loads operator private key from mnemonic or armored key
   - Loads consensus private key from validator home directory

2. **Join Attester Set**
   - Converts consensus public key to Cosmos SDK format
   - Creates and submits `MsgJoinAttesterSet` transaction
   - Waits for transaction confirmation (up to 10 retries with 500ms delay)
   - Handles case where validator is already in attester set (code 18)

3. **Historical Attestations**
   - Queries current blockchain height via `/status` endpoint
   - Attests to all blocks from height 1 to current height
   - Reports progress every 10 blocks
   - Retries failed attestations up to 3 times with 300ms delay
   - Small delay (100ms) between attestations to avoid overwhelming the node

4. **Live Attestations**
   - Polls for new blocks via `/block` endpoint every 50ms
   - Attests to new blocks as they arrive
   - Catches up on any missed blocks automatically
   - Continues until interrupted by SIGINT/SIGTERM

## Example

```bash
# Basic usage with mnemonic
appd attester \
  --chain-id evmallus-1 \
  --node tcp://localhost:26657 \
  --home ~/.evnode \
  --mnemonic "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about" \
  --verbose

# Using ASCII armored key instead of mnemonic
appd attester \
  --chain-id evmallus-1 \
  --node tcp://localhost:26657 \
  --home ~/.evnode \
  --priv-key-armor "$(cat operator_key.txt)" \
  --verbose

# Using remote node
appd attester \
  --chain-id evmallus-1 \
  --node tcp://rpc.example.com:26657 \
  --home ~/.evnode \
  --mnemonic "your mnemonic here"
```

## Transaction Details

### Gas and Fees

- Gas limit: 200,000 per transaction
- Fees: 200 tokens (using the chain's default bond denomination)
- Uses `SIGN_MODE_DIRECT` for transaction signing

### Sequence Management

- Sequence numbers are cached in memory to avoid duplicate transactions
- Automatically syncs with on-chain sequence if drift is detected
- Sequence number is incremented after each successful transaction broadcast
- On restart, sequence is re-initialized from the chain

## Error Handling

The attester handles several error scenarios:

### Join Attester Set Errors

- **Code 4 (Unauthorized)** - Address may not be a valid validator
- **Code 5 (Insufficient funds)** - Operator account needs more tokens
- **Code 11 (Out of gas)** - Gas limit needs to be increased
- **Code 18 (Already in attester set)** - Continues with attestations (not an error)
- **Code 18 (Invalid request)** - Generic invalid request error

### Attestation Errors

- **Block attestation failure** - Retries up to 3 times with 300ms delay between attempts
- **RPC connection issues** - Retries with 100ms delay
- **Transaction not found** - Retries query up to 5 times with 500ms delay
- **Sequence mismatch** - Automatically syncs sequence number from chain

### Signal Handling

- **SIGINT/SIGTERM** - Gracefully shuts down the attester
- Cleans up resources and exits

## Operational Notes

- The attester uses HTTP polling (not WebSocket) to detect new blocks
- Sequence numbers are cached in memory and reset on restart
- Verbose mode provides detailed transaction logs and debugging information
- The attester gracefully handles transaction failures and retries automatically
- Uses the validator's consensus key directly from the file system (no key rotation)
- All attestation votes use `PrecommitType` with round 0

## Block ID Construction

The attester reconstructs the original BlockID for each height:

- For height ≤ 1: Uses empty BlockID
- For height > 1: Queries `/block?height=N` to get BlockID with:
  - Block hash
  - PartSetHeader hash and total

This ensures signatures match the original block structure used by the sequencer.

## Attestation Vote Structure

Each attestation includes:

- Vote type: `PrecommitType`
- Validator address: From consensus private key
- Height: Block height being attested
- Round: 0 (always)
- BlockID: Reconstructed from original block
- Timestamp: From block header
- Signature: Signed with consensus private key

## Limitations

- No persistent state across restarts (except on-chain state)
- Sequence numbers reset on restart, requiring sync with chain
- HTTP polling only (no WebSocket subscription)
- Basic retry logic without exponential backoff
- Keys must be provided manually (no secure key management)
- No automatic key rotation or refresh
- Fixed gas limit and fees (not dynamically calculated)
- Bech32 prefixes are configured via client context, not exposed as flags

## Troubleshooting

### "either --mnemonic or --priv-key-armor must be provided"

One of these flags is required to provide the operator private key.

### "failed to create private key from mnemonic"

The mnemonic is invalid or incorrectly formatted. Ensure it's a valid 12 or 24-word BIP39 mnemonic.

### "transaction failed in mempool with code X"

The transaction was rejected during mempool validation. Check the error code and message for details.

### "Could not find transaction after N attempts"

The transaction may still be pending or was not included in a block. Check the node logs.

### Sequence Mismatch

The attester automatically detects and corrects sequence drift. If issues persist, restart the attester to re-sync.

## Debug Output

When `--verbose` is enabled, the attester outputs:

- Bech32 configuration (account and validator prefixes)
- Operator account and validator addresses
- Transaction submission details
- Transaction hashes and confirmation status
- Gas usage information
- Validator address debugging information
- Sequence number synchronization messages

This helps diagnose configuration and connectivity issues.
