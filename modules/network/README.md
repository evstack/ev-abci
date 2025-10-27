# Network Module

## Introduction

The `network` module provides the framework for an off-chain consensus mechanism driven by a dynamic set of participants called **Attesters**. This module allows for blocks to be confirmed much faster than the underlying consensus engine's finality by gathering votes from attesters. When enough votes are collected for a specific block, it is considered "soft confirmed."

This module is responsible for:
- Managing the set of active attesters.
- Collecting and recording attestations (votes) for blocks.
- Determining when a block has reached a quorum of votes.
- Emitting events that other off-chain components can use to reason about the state of the network.

## Core Concepts

### Attesters

Attesters are validators who have explicitly opted-in to participate in the off-chain attestation process. They are responsible for validating blocks and submitting signed votes to the network module.

### Soft Confirmation

A soft confirmation is a signal that a block has been validated by a sufficient number of attesters. It is achieved when a quorum of votes is reached for a specific block height. This provides a faster, off-chain layer of consensus that can be used by light clients and other services that require quick confirmation, without waiting for the finality of the underlying L1.

### Epochs

An epoch is a defined period of blocks. At the end of each epoch, the module can perform accounting tasks, such as updating the attester set based on who has joined or left.

## Workflow

1.  **Joining:** A validator operator submits a `MsgJoinAttesterSet` transaction to add their validator to the active attester set.
2.  **Attesting:** For each relevant block, an attester's off-chain process signs a vote and submits it to the chain via a `MsgAttest` transaction.
3.  **Recording:** The `network` module receives the attestation, records the vote, and updates a bitmap for that block height to track which attesters have voted.
4.  **Confirmation:** At the end of every block, the module's `EndBlocker` checks if the total voting power of the attesters for any recent block has passed the quorum threshold.
5.  **Notification:** If quorum is reached, the module emits a `checkpoint` event containing the block hash and other details. Downstream services listen for this event to act on the soft-confirmed block.

## Messages

Interaction with the network module is done through the following messages:

### MsgJoinAttesterSet

Allows a validator to opt-in to the attester set. The transaction must be signed by the `authority` account, but the `consensus_address` specifies which validator is being added.

**Example:**
```json
{
  "messages": [
    {
      "@type": "/evabci.network.v1.MsgJoinAttesterSet",
      "authority": "cosmos1...",
      "consensus_address": "cosmosvalcons1...",
      "pubkey": {
        "@type": "/cosmos.crypto.ed25519.PubKey",
        "key": "VALIDATOR_CONSENSUS_PUBKEY_BASE64"
      }
    }
  ],
  ...
}
```

### MsgLeaveAttesterSet

Allows a validator to opt-out of the attester set.

**Example:**
```json
{
  "messages": [
    {
      "@type": "/evabci.network.v1.MsgLeaveAttesterSet",
      "authority": "cosmos1...",
      "consensus_address": "cosmosvalcons1..."
    }
  ],
  ...
}
```

### MsgAttest

Submits a signed vote for a specific block height. This is the primary transaction used by active attesters.

- `authority`: The account submitting the transaction and paying the fee.
- `consensus_address`: The consensus address of the validator that is attesting.
- `height`: The block height being voted on.
- `vote`: The base64-encoded vote payload.

**Example:**
```json
{
  "messages": [
    {
      "@type": "/evabci.network.v1.MsgAttest",
      "authority": "cosmos1...",
      "consensus_address": "cosmosvalcons1...",
      "height": "12345",
      "vote": "BASE64_ENCODED_VOTE_BYTES"
    }
  ],
  ...
}
```

### MsgUpdateParams

Allows a governance proposal to update the parameters of the network module.

**Example:**
```json
{
  "messages": [
    {
      "@type": "/evabci.network.v1.MsgUpdateParams",
      "authority": "cosmos10d07y265gmmuvt4z0w9aw880j2r6426u005ev2",
      "params": {
        "epoch_length": "100",
        "min_attester_stake_amount": "1000000"
      }
    }
  ],
  ...
}
```

## EndBlocker Logic

The module's `EndBlocker` is executed at the end of every block and performs the following critical functions:

1.  **Quorum Evaluation**: It iterates through recent blocks that have received attestations and checks if the cumulative voting power of the attesters has reached the required quorum.
2.  **Checkpoint Emission**: If quorum is met for a block, it emits an `EventSoftCheckpoint` with the `height`, `block_hash`, a bitmap of the participating attesters, and the total voting power.
3.  **Epoch Processing**: It checks if the current block is the end of an epoch. If it is, it performs accounting tasks, such as updating the attester index map for the next epoch.

## Genesis and Queries

The module supports standard genesis import/export, allowing the attester set, parameters, and historical attestation data to be included in a chain's genesis file.

The state can be inspected via gRPC queries, which allow you to query for the current attester set, module parameters, and the attestation status of specific blocks.
