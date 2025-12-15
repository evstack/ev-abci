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

### Migration Execution

The migration is performed in a single, atomic step. At the migration start height, all old validators are removed, and the new sequencer/attesters are added in one atomic update.

**Note:** IBC light client updates must be performed at height H+1 (one block after the migration) to ensure proper verification of the validator set changes.

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

2.  **Prepare for the Chain Halt**: Your node **will stop** at a predictable block height, calculated from the migration's start height (`block_height` in the proposal). The halt occurs at `block_height + 2`. This is expected. Check your node's logs for the specific "MIGRATE" error message.

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

### 2. Use the Staking Wrapper Module

To prevent the standard `staking` module from sending conflicting validator updates during migration, you must use the staking wrapper module provided in the `modules/staking` directory instead of the standard Cosmos SDK staking module.

The staking wrapper module automatically nullifies validator updates from the standard staking module by returning an empty validator update list in its `EndBlock` method. This ensures that the `migrationmngr` module remains the sole source of validator set changes during the migration process.

**Update your application's module dependencies:**

Replace imports of the standard Cosmos SDK staking module:

```go
import (
	// OLD - remove this
	"github.com/cosmos/cosmos-sdk/x/staking"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)
```

**With imports of the ev-abci staking wrapper module:**

```go
import (
	// NEW - use the wrapper module
	"github.com/evstack/ev-abci/modules/staking"
	stakingkeeper "github.com/evstack/ev-abci/modules/staking/keeper"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types" // types remain the same
)
```

If you're using depinject, the staking wrapper module will be automatically wired through the dependency injection system and will prevent validator updates from the staking module while allowing the `migrationmngr` module to control validator set changes during migrations.
