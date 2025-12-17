# Post Transaction Command

The `post-tx` command allows you to post a signed transaction directly to a Celestia namespace using the Evolve node's DA configuration.

## Overview

This command submits signed transactions in JSON format to the configured Celestia Data Availability (DA) layer. The transaction is automatically decoded from JSON to bytes before submission.

It's useful for:

- Testing DA submission without running a full node
- Manually posting transaction data to specific namespaces
- Debugging DA layer connectivity and configuration
- Submitting signed transactions from external sources

## Usage

```bash
evabcid post-tx <json-file-or-string> [flags]
```

- The first argument contains a transaction as JSON file path or JSON string (required)
  - Accepts either a path to a JSON file containing the transaction
  - Or a JSON string directly
  - The command automatically detects whether input is a file or JSON string
  - JSON must follow Cosmos SDK transaction format

## Optional Flags

### Transaction-Specific Flags

- `--namespace`: Celestia namespace ID to post to
  - If not provided, uses the namespace from config (`evnode.da.namespace`)
  - Format: 58-character hex string representing the namespace

- `--gas-price`: Gas price for DA submission
  - Default: `-1` (uses config value)
  - If config also uses auto pricing, DA layer determines the price

- `--timeout`: Timeout duration for the submission
  - Default: `60s` (1 minute)
  - Examples: `30s`, `2m`, `1m30s`

- `--submit-options`: Additional submit options for DA layer
  - If not provided, uses `evnode.da.submit_options` from config

### Configuration Flags

The command also accepts all Evolve configuration flags (prefixed with `--evnode.`):

- `--evnode.da.address`: DA layer RPC address (default: `http://localhost:7980`)
- `--evnode.da.auth_token`: Authentication token for DA layer
- `--evnode.da.block_time`: DA chain block time (default: `6s`)
- And many more... (see `--help` for complete list)

## Examples

### Basic Usage (File)

Post a transaction from a JSON file:

```bash
evabcid post-tx transaction.json
```

### Basic Usage (JSON String)

Post a transaction from a JSON string:

```bash
evabcid post-tx '{
  "body": {
    "messages": [],
    "memo": "test transaction",
    "timeout_height": "0",
    "extension_options": [],
    "non_critical_extension_options": []
  },
  "auth_info": {
    "signer_infos": [],
    "fee": {
      "amount": [{"denom": "stake", "amount": "200"}],
      "gas_limit": "200000",
      "payer": "",
      "granter": ""
    }
  },
  "signatures": []
}'
```

### With Custom Namespace

Post to a specific Celestia namespace:

```bash
evabcid post-tx \
  signed_tx.json \
  --namespace 0000000000000000000000000000000000000000000001
```

### With Custom Gas Price

Post with explicit gas price:

```bash
evabcid post-tx \
  signed_tx.json \
  --gas-price 0.025
```

### With DA Configuration Override

Post using a specific DA endpoint:

```bash
evabcid post-tx \
  signed_tx.json \
  --evnode.da.address http://celestia-node:7980 \
  --evnode.da.auth_token my-secret-token
```

### With Custom Timeout

Post with extended timeout:

```bash
evabcid post-tx \
  signed_tx.json \
  --timeout 5m
```

## Transaction JSON Format

The transaction data must be provided in Cosmos SDK transaction JSON format:

```json
{
  "body": {
    "messages": [
      {
        "@type": "/evstack.network.v1.MsgAttest",
        "authority": "evstack1...",
        "consensus_address": "evstackvalcons1...",
        "height": "100",
        "vote": "base64-encoded-vote"
      }
    ],
    "memo": "Optional memo text",
    "timeout_height": "0",
    "extension_options": [],
    "non_critical_extension_options": []
  },
  "auth_info": {
    "signer_infos": [
      {
        "public_key": {
          "@type": "/cosmos.crypto.secp256k1.PubKey",
          "key": "base64-encoded-pubkey"
        },
        "mode_info": {
          "single": {
            "mode": "SIGN_MODE_DIRECT"
          }
        },
        "sequence": "0"
      }
    ],
    "fee": {
      "amount": [
        {
          "denom": "stake",
          "amount": "200"
        }
      ],
      "gas_limit": "200000",
      "payer": "",
      "granter": ""
    }
  },
  "signatures": ["base64-encoded-signature"]
}
```

### Auto-Detection

The command automatically detects whether arg is a file path or JSON string:

1. Check if the value is a valid file path that exists
2. If yes → read file contents and decode as JSON
3. If no → treat value as JSON string and decode directly

## Output

### Success

On successful submission, the command outputs:

```
✓ Transaction posted successfully

Namespace:  0000000000000000000000000000000000000000000001
DA Height:  12345
Gas Price:  0.02
Data Size:  256 bytes
```

Where:

- **Namespace**: The Celestia namespace where the transaction was posted
- **DA Height**: The height of the DA block containing the transaction
- **Gas Price**: The gas price used for the submission
- **Data Size**: Size of the submitted transaction data in bytes

### Already in Mempool

If the transaction is already in the mempool:

```
⚠ Transaction already in mempool
  DA Height: 12345
```

### Errors

Common error cases:

1. **Invalid JSON syntax**:

   ```
   Error: failed to decode transaction from JSON: parsing JSON: invalid character...
   ```

2. **File not found**:

   ```
   Error: failed to decode transaction from file: reading file: no such file or directory
   ```

3. **Transaction too large**:

   ```
   Error: transaction too large for DA layer: blob size exceeds maximum
   ```

4. **Connection failure**:

   ```
   Error: failed to create DA client: dial tcp: connection refused
   ```

5. **Timeout**:

   ```
   Error: submission canceled: context deadline exceeded
   ```

6. **Empty transaction**:
   ```
   Error: transaction cannot be empty
   ```

## Configuration File

The command reads configuration from `~/.evabci/config/evnode.yaml` by default. You can override the location with the `--home` flag:

```bash
evabcid post-tx --tx transaction.json --home /custom/path
```

Example `evnode.yaml` configuration:

```yaml
da:
  address: "http://localhost:7980"
  auth_token: "your-token-here"
  namespace: "M21eldetxV"
  gas_price: 0.025
  block_time: 6s
  submit_options: ""
```

## Workflow

### Creating and Submitting a Transaction

1. **Create unsigned transaction**:

   ```bash
   mychaind tx bank send alice bob 100stake \
     --generate-only \
     --chain-id mychain \
     > unsigned_tx.json
   ```

2. **Sign the transaction**:

   ```bash
   mychaind tx sign unsigned_tx.json \
     --from alice \
     --chain-id mychain \
     > signed_tx.json
   ```

3. **Submit to DA layer**:
   ```bash
   evabcid post-tx --tx signed_tx.json
   ```
