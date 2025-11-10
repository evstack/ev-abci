# Post Transaction Command

The `post-tx` command allows you to post a signed transaction directly to a Celestia namespace using the Evolve node's DA configuration.

## Overview

This command submits raw transaction bytes to the configured Celestia Data Availability (DA) layer. The transaction bytes are the same format that would be passed to `ExecuteTxs` in the ABCI adapter.

It's useful for:

- Testing DA submission without running a full node
- Manually posting transaction data to specific namespaces
- Debugging DA layer connectivity and configuration
- Submitting raw transaction bytes from external sources

## Usage

```bash
evabcid post-tx --tx <hex-encoded-transaction> [flags]
```

## Required Flags

- `--tx`: Hex-encoded raw transaction bytes (required)
  - The hex string represents the raw transaction bytes that will be submitted to Celestia
  - These are the same bytes that would be passed to `ExecuteTxs` in the ABCI adapter
  - Hex encoding is only for CLI convenience; raw bytes are decoded and submitted

## Optional Flags

### Transaction-Specific Flags

- `--namespace`: Celestia namespace ID to post to
  - If not provided, uses the namespace from config (`evnode.da.namespace`)
  - Format: 58-character hex string representing the namespace
- `--gas-price`: Gas price for DA submission
  - Default: `-1` (uses config value)
  - If set to `-1`, uses `evnode.da.gas_price` from config
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
- `--evnode.da.gas_multiplier`: Gas price multiplier for retries
- And many more... (see `--help` for complete list)

## Examples

### Basic Usage

Post a transaction using config defaults:

```bash
evabcid post-tx --tx deadbeef1234567890abcdef
```

### With Custom Namespace

Post to a specific Celestia namespace:

```bash
evabcid post-tx \
  --tx 0xdeadbeef1234567890abcdef \
  --namespace 0000000000000000000000000000000000000000000001
```

### With Custom Gas Price

Post with explicit gas price:

```bash
evabcid post-tx \
  --tx deadbeef1234567890abcdef \
  --gas-price 0.025
```

### With DA Configuration Override

Post using a specific DA endpoint:

```bash
evabcid post-tx \
  --tx deadbeef1234567890abcdef \
  --evnode.da.address http://celestia-node:7980 \
  --evnode.da.auth_token my-secret-token
```

### With Custom Timeout

Post with extended timeout:

```bash
evabcid post-tx \
  --tx deadbeef1234567890abcdef \
  --timeout 5m
```

## Transaction Data Format

The transaction data (`--tx` flag) must be provided as a hex-encoded string for CLI convenience. The command will:

1. Decode the hex string to raw bytes
2. Submit those raw bytes to Celestia as a blob
3. These bytes are the same format that `ExecuteTxs` expects in the ABCI adapter

Requirements:

- With or without `0x` prefix: Both `0xdeadbeef` and `deadbeef` are accepted
- Must be valid hexadecimal characters (0-9, a-f, A-F)
- Cannot be empty after decoding
- The decoded bytes should be valid transaction data in your chain's format

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

1. **Invalid hex encoding**:

   ```
   Error: failed to decode hex transaction: encoding/hex: invalid byte: U+006E 'n'
   ```

2. **Transaction too large**:

   ```
   Error: transaction too large for DA layer: blob size exceeds maximum
   ```

3. **Connection failure**:

   ```
   Error: failed to create DA client: dial tcp: connection refused
   ```

4. **Timeout**:
   ```
   Error: submission canceled: context deadline exceeded
   ```

## Configuration File

The command reads configuration from `~/.evabci/config/evnode.yaml` by default. You can override the location with the `--home` flag:

```bash
evabcid post-tx --tx deadbeef --home /custom/path
```

Example `evnode.yaml` configuration:

```yaml
da:
  address: "http://localhost:7980"
  auth_token: "your-token-here"
  namespace: "M21eldetxV"
  gas_price: 0.025
  gas_multiplier: 1.1
  block_time: 6s
  submit_options: ""
```

## Return Codes

- `0`: Success - transaction posted successfully
- `1`: Error - invalid input, configuration error, or DA submission failure

## Technical Details

### DA Submission Flow

1. Parse hex-encoded input from CLI
2. Decode hex to raw transaction bytes
3. Load Evolve configuration
4. Create DA client with configured parameters
5. Submit raw bytes as a blob to Celestia
6. Return submission result with DA height

**Note**: The raw bytes submitted are exactly what would be passed to `ExecuteTxs` in the ABCI adapter. Hex encoding is only used for CLI input convenience.

### Retry Behavior

The command uses the DA client's built-in retry logic:

- Retries on transient failures (network issues, mempool full)
- Increases gas price on retries (based on `gas_multiplier`)
- Respects the configured `max_submit_attempts`
- Backs off exponentially between retries

### Blob Size Limits

The maximum blob size is determined by the DA layer (Celestia). Currently:

- Default max blob size: ~1.5 MB
- If transaction exceeds this, you'll receive a `StatusTooBig` error

## Use Cases

### 1. Testing DA Connectivity

Quickly verify your DA configuration works:

```bash
# Convert raw text to hex and submit
echo -n "test" | xxd -p | evabcid post-tx --tx $(cat -)

# Or submit any raw bytes
evabcid post-tx --tx $(echo "test data" | xxd -p -c 0)
```

### 2. Manual Transaction Submission

Submit raw transaction bytes from a file:

```bash
# If you have hex-encoded transaction
TX_HEX=$(cat transaction.hex)
evabcid post-tx --tx $TX_HEX --namespace <your-namespace>

# If you have raw bytes, encode to hex first
TX_HEX=$(xxd -p -c 0 transaction.bin)
evabcid post-tx --tx $TX_HEX --namespace <your-namespace>
```

### 3. CI/CD Integration

Automate transaction posting in scripts:

```bash
#!/bin/bash
TX_HEX="deadbeef1234567890"
if evabcid post-tx --tx $TX_HEX; then
  echo "Transaction posted successfully"
  exit 0
else
  echo "Failed to post transaction"
  exit 1
fi
```

### 4. Posting Arbitrary Data

Post any raw data to Celestia:

```bash
# Convert any file to hex and post
FILE_HEX=$(xxd -p -c 0 data.bin)
evabcid post-tx --tx $FILE_HEX --namespace <data-namespace>

# Post JSON data
JSON_HEX=$(echo '{"key":"value"}' | xxd -p -c 0)
evabcid post-tx --tx $JSON_HEX --namespace <json-namespace>
```

## Troubleshooting

### "required flag(s) 'tx' not set"

You must provide the `--tx` flag with hex-encoded transaction bytes.

### "failed to decode hex transaction"

Ensure your input is valid hexadecimal:

- Remove any spaces, newlines, or non-hex characters
- Valid characters: 0-9, a-f, A-F
- Optional `0x` prefix is accepted

If you have raw bytes in a file, encode them to hex first:

```bash
xxd -p -c 0 transaction.bin
```

### "failed to load config"

Verify that:

1. The config file exists at `~/.evabci/config/evnode.yaml`
2. The YAML syntax is valid
3. You have read permissions

### "invalid config: namespace cannot be empty"

Set the namespace either via:

- `--namespace` flag
- `evnode.da.namespace` in config file

### "failed to create DA client: connection refused"

Check that:

1. The DA node is running
2. The address in config is correct
3. Firewall allows the connection

## Related Commands

- `evabcid init`: Initialize Evolve configuration
- `evabcid start`: Start the Evolve node (includes DA submission)
- `evabcid evolve-migrate`: Migrate from CometBFT to Evolve

## See Also

- [Evolve Documentation](https://docs.evstack.org)
- [Celestia Documentation](https://docs.celestia.org)
- [ev-node DA Submitter](https://github.com/evstack/ev-node/tree/main/block/internal/submitting)
