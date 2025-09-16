# Evolve Attester (beta)

> Early, simplified attester mode. Work in progress – expect breaking changes and incomplete feature coverage.

This command-line helper joins the attester set of an Evolve-based chain and streams attestations for new blocks. It is the first public beta of the attester workflow and intentionally omits a number of production-hardening features (automatic key management, batching, robust persistence, etc.). The goal is to unblock experimentation on top of the upcoming attester mode.

## What it does
- Configures the Cosmos SDK Bech32 prefixes required by the target chain.
- Derives an operator account key from a mnemonic and loads the validator consensus key from the local `priv_validator_key.json`.
- Submits a `MsgJoinAttesterSet` transaction so the validator's consensus key is registered as an attester.
- Polls the node's RPC (`/status` and `/block`) to detect new heights and rebuilds the Evolve header and original `BlockID` for each block.
- Signs a precommit-style vote with the consensus key and wraps it in `MsgAttest` transactions paid for by the operator account.
- Retries failed submissions a few times and keeps catching up when the attester falls behind.

## Prerequisites
- An Evolve node exposing RPC on `tcp://HOST:PORT` and REST API on `http://HOST:PORT`.
- Access to the validator home directory so the attester can read `config/priv_validator_key.json` and `data/priv_validator_state.json`.
- A funded Cosmos SDK account mnemonic that will cover the attestation fees.
- The chain ID and Bech32 prefixes used by your deployment.

## Running the attester
Build with Go 1.22+:

```bash
cd attester
go build -o attester
```

Run it against your node (adjust the sample values):

```bash
./attester \
  --chain-id evmallus-1 \
  --node tcp://localhost:26657 \
  --api-addr http://localhost:1317 \
  --home /path/to/validator/home \
  --mnemonic "word1 word2 … word24" \
  --verbose
```

### Important flags
- `--home` – validator home directory containing the CometBFT private validator files.
- `--mnemonic` – Cosmos SDK mnemonic for the operator account that pays fees.
- `--bech32-account-prefix` and friends – override if your chain does not use the defaults (`gm`, `gmvaloper`, etc.).
- `--node` / `--api-addr` – RPC and REST endpoints used for account queries and block data.

## Operational notes
- The attester assumes the node accepts `MsgAttest` at every height. Current networks only process attestations at checkpoint heights (multiples of the epoch length); out-of-window submissions will be rejected with code 18 (`invalid request`).
- Sequence numbers are cached in memory. Restarting the process while transactions are pending can still produce sequence mismatches.
- HTTP polling currently drives block detection. There is no WebSocket subscription or backoff logic yet, so running it against remote nodes may require proxying/caching to avoid throttling.
- Logging is verbose by default to aid debugging. Remove `--verbose` once the setup is stable.

## Limitations and future work
- No automatic key rotation or secure storage – mnemonic and consensus keys must be provided manually.
- No persistence of attestation state across restarts beyond what the chain tracks.
- Lacks production monitoring hooks, metrics, and alerting.
- Error handling focuses on known happy paths; unexpected RPC responses will cause retries or exits.

Feedback is welcome. Please treat this module as beta software and be prepared for rapid iteration as the attester mode matures.
