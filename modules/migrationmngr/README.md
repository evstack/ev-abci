# Migration Manager Module

## Introduction

The `migrationmngr` module is a specialized module for orchestrating a one-time, coordinated transition of the chain's consensus participants. It is designed to migrate a standard proof-of-stake (PoS) validator set to a new topology, which can be either a single-sequencer setup or a sequencer-and-attester network. This is a fundamental component for evolving the chain from a sovereign Cosmos chain to a rollup.

The migration is designed to be robust and safe, with mechanisms to handle different scenarios, including whether the chain has active IBC connections.

## Initiating a Migration

The migration process is not automatic; it must be explicitly triggered by a governance proposal. This ensures that the chain's stakeholders have approved the transition.

The proposal must contain a `MsgMigrateToEvolve` message.

### `MsgMigrateToEvolve`

This message instructs the `migrationmngr` module to begin the migration process at a specified block height.

-   `authority`: The address of the governance module. This is set automatically when submitted via a proposal.
-   `block_height`: The block number at which the migration will start.
-   `sequencer`: An object defining the new sequencer.
    -   `name`: A human-readable name for the sequencer.
    -   `consensus_pubkey`: The consensus public key of the sequencer.
-   `attesters`: An optional list of objects defining the attesters. If this list is empty, the chain will migrate to a single-sequencer mode.
    -   `name`: A human-readable name for the attester.
    -   `consensus_pubkey`: The consensus public key of the attester.

### Example Governance Proposals

Below are examples of the `messages` array within a `submit-proposal` JSON file.

#### Example 1: Migrating to a Single Sequencer

This proposal will migrate the chain to be validated by a single sequencer, starting at block `1234567`.

```json
{
  "messages": [
    {
      "@type": "/evabci.migrationmngr.v1.MsgMigrateToEvolve",
      "authority": "cosmos10d07y265gmmuvt4z0w9aw880j2r6426u005ev2",
      "block_height": "1234567",
      "sequencer": {
        "name": "sequencer-01",
        "consensus_pubkey": {
          "@type": "/cosmos.crypto.ed25519.PubKey",
          "key": "YOUR_SEQUENCER_PUBKEY_BASE64"
        }
      },
      "attesters": []
    }
  ],
  "metadata": "...",
  "deposit": "...",
  "title": "Proposal to Migrate to a Single Sequencer",
  "summary": "This proposal initiates the migration to a single-sequencer chain operated by sequencer-01."
}
```

#### Example 2: Migrating to a Sequencer and Attesters

This proposal will migrate the chain to a new consensus set composed of one sequencer and two attesters, starting at block `1234567`.

```json
{
  "messages": [
    {
      "@type": "/evabci.migrationmngr.v1.MsgMigrateToEvolve",
      "authority": "cosmos10d07y265gmmuvt4z0w9aw880j2r6426u005ev2",
      "block_height": "1234567",
      "sequencer": {
        "name": "sequencer-01",
        "consensus_pubkey": {
          "@type": "/cosmos.crypto.ed25519.PubKey",
          "key": "YOUR_SEQUENCER_PUBKEY_BASE64"
        }
      },
      "attesters": [
        {
          "name": "attester-01",
          "consensus_pubkey": {
            "@type": "/cosmos.crypto.ed25519.PubKey",
            "key": "ATTESTER_01_PUBKEY_BASE64"
          }
        },
        {
          "name": "attester-02",
          "consensus_pubkey": {
            "@type": "/cosmos.crypto.ed25519.PubKey",
            "key": "ATTESTER_02_PUBKEY_BASE64"
          }
        }
      ]
    }
  ],
  "metadata": "...",
  "deposit": "...",
  "title": "Proposal to Migrate to a Sequencer and Attester Network",
  "summary": "This proposal initiates the migration to a new network with one sequencer and two attesters."
}
```

## How Migrations Work Under the Hood

The migration process is managed through state machine logic that is triggered at a specific block height, defined by a governance proposal. Once a migration is approved and the block height is reached, the module's `EndBlock` and `PreBlock` logic take over.

There are two primary migration scenarios:

1.  **Migration to a Single Sequencer**: The entire existing validator set is replaced by a single, designated sequencer. This entity becomes the sole block producer for the rollup.
2.  **Migration to a Sequencer and Attesters**: The validator set is replaced by a new set of actors, including a primary sequencer and a group of "attesters" who provide additional validation or data availability services.

### The Migration Mechanism

The core of the migration is handled by returning `abci.ValidatorUpdate` messages to the underlying consensus engine (CometBFT). These updates change the voting power of validators.

-   **Removing Old Validators**: The existing validators that are not part of the new sequencer/attester set have their power reduced to `0`.
-   **Adding New Participants**: The new sequencer and/or attesters are added to the consensus set with a power of `1`.

### IBC-Aware Migrations

A critical feature of the migration manager is its awareness of the Inter-Blockchain Communication (IBC) protocol. A sudden, drastic change in the validator set can cause IBC light clients on other chains to fail their verification checks, leading to a broken connection.

To prevent this, the module first checks if IBC is enabled by verifying the existence of the IBC module's store key.

-   **If IBC is Enabled**: The migration is "smoothed" over a period of blocks (currently `30` blocks, defined by `IBCSmoothingFactor`). In each block during this period, a fraction of the old validators are removed, and a fraction of the new attesters are added. This gradual change ensures that IBC light clients can safely update their trusted validator sets without interruption. On the first block of the migration, all validators are set to have an equal power of `1` to prevent any single validator from having a disproportionate amount of power during the transition.
-   **If IBC is Not Enabled**: The migration can be performed "immediately" in a single block. All old validators are removed, and the new sequencer/attesters are added in one atomic update at the migration start height.

### The Chain Halt: A Coordinated Upgrade

The most critical part of the process is the final step. One block *after* the migration period ends (`migration_end_height + 1`), the `PreBlock` logic is designed to panic and halt the chain.

This is an intentional design choice to force a coordinated upgrade. When the chain halts, node operators will see a specific error message in their logs:

```
MIGRATE: chain migration to evolve is complete. Switch to the evolve binary and run 'gmd evolve-migrate' to complete the migration.
```

This halt ensures that all participants must manually intervene to switch to the new software, preventing a chain split or other inconsistencies. Before halting, the module cleans up the migration state from the store to ensure the chain does not halt again on restart.

## What Validators and Node Operators Need to Do

If you are a validator or node operator on a chain using this module, you must be prepared for the migration event.

1.  **Monitor Governance**: The migration will be initiated by a governance proposal. Stay informed about upcoming proposals. The proposal will define the target block height for the migration.

2.  **Prepare for the Chain Halt**: At `migration_end_height + 1`, your node **will stop**. This is expected. Check your node's logs for the specific "MIGRATE" error message.

3.  **Perform the Upgrade**: Once the chain has halted, you must perform the following steps:
    a. **Install the New Binary**: You will need to replace your current node software (e.g., `gmd`) with the new version required for the rollup.
    b. **Run the Migration Command**: The error message will instruct you to run a command like `gmd evolve-migrate`. This command will handle any necessary state or configuration changes on your local node to make it compatible with the new network topology.
    c. **Restart Your Node**: After running the migration command, you can restart your node. It will now be operating as part of the new rollup network (either as a sequencer or attester, depending on the new configuration).

Your role will fundamentally change from a proof-of-stake validator to a participant in the rollup's new consensus mechanism.

## Application Wiring (`app.go`)

To ensure the `migrationmngr` module can correctly take control during a migration, you must make modifications to your application's main `app.go` file. This is crucial to prevent the standard `staking` module from sending conflicting validator set updates.

### 1. Add the Migration Manager Keeper to the `App` Struct

First, make the `migrationmngr`'s keeper available in your `App` struct. Find the struct definition (e.g., `type App struct { ... }`) and add the `MigrationmngrKeeper` field. You will also need to ensure it is properly instantiated in your app's constructor (e.g., `NewApp(...)`).

```go
import (
	// ... other imports
	migrationmngrkeeper "github.com/evstack/ev-abci/modules/migrationmngr/keeper"
	// ... other imports
)

type App struct {
	// ... other keepers
	StakingKeeper        *stakingkeeper.Keeper
	MigrationmngrKeeper  migrationmngrkeeper.Keeper // Add this line
	// ... other keepers
}
```

### 2. Modify the `EndBlocker` Method

Next, you must modify the `EndBlocker` (or `EndBlock`) method of your `App`. Wrap the existing call to the module manager's `EndBlock` function with logic that checks if a migration is in progress.

**Replace the standard `EndBlocker` implementation:**

```go
func (app *App) EndBlocker(ctx sdk.Context) (abci.ResponseEndBlock, error) {
	return app.mm.EndBlock(ctx)
}
```

**With this conditional logic:**

```go
func (app *App) EndBlocker(ctx sdk.Context) (abci.ResponseEndBlock, error) {
	// Check if a migration is in progress.
	_, _, isMigrating := app.MigrationmngrKeeper.IsMigrating(ctx)
	if isMigrating {
		// If migrating, only run the migrationmngr EndBlocker.
		// This prevents the staking module from sending conflicting validator updates.
		valUpdates, err := app.MigrationmngrKeeper.EndBlock(ctx)
		if err != nil {
			return abci.ResponseEndBlock{}, err
		}
		return abci.ResponseEndBlock{
			ValidatorUpdates: valUpdates,
		}, nil
	}

	// If no migration is in progress, run the default EndBlocker logic.
	return app.mm.EndBlock(ctx)
}
```

These changes guarantee that during the migration, the `migrationmngr` module is the sole source of truth for validator set changes, ensuring a smooth and predictable transition.
