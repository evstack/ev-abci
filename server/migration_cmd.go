package server

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	goheaderstore "github.com/celestiaorg/go-header/store"
	cmtdbm "github.com/cometbft/cometbft-db"
	cometbftcmd "github.com/cometbft/cometbft/cmd/cometbft/commands"
	cfg "github.com/cometbft/cometbft/config"
	"github.com/cometbft/cometbft/crypto"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	"github.com/cometbft/cometbft/state"
	cmtstore "github.com/cometbft/cometbft/store"
	cmttypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	ds "github.com/ipfs/go-datastore"
	ktds "github.com/ipfs/go-datastore/keytransform"
	"github.com/spf13/cobra"

	rollkitnode "github.com/evstack/ev-node/node"
	rollkitgenesis "github.com/evstack/ev-node/pkg/genesis"
	rollkitstore "github.com/evstack/ev-node/pkg/store"
	rollkittypes "github.com/evstack/ev-node/types"

	migrationmngr "github.com/evstack/ev-abci/modules/migrationmngr"
	migrationmngrtypes "github.com/evstack/ev-abci/modules/migrationmngr/types"
	execstore "github.com/evstack/ev-abci/pkg/store"
)

var (
	flagDaHeight   = "da-height"
	maxMissedBlock = 50
)

// evolveMigrationGenesis represents the minimal genesis for ev-abci migration.
type evolveMigrationGenesis struct {
	ChainID         string        `json:"chain_id"`
	InitialHeight   uint64        `json:"initial_height"`
	GenesisTime     int64         `json:"genesis_time"`
	SequencerAddr   []byte        `json:"sequencer_address"`
	SequencerPubKey crypto.PubKey `json:"sequencer_pub_key,omitempty"`
}

// ToEVGenesis converts the rollkit migration genesis to a ev-node genesis.
func (g evolveMigrationGenesis) ToEVGenesis() *rollkitgenesis.Genesis {
	genesis := rollkitgenesis.NewGenesis(
		g.ChainID,
		g.InitialHeight,
		time.Unix(0, g.GenesisTime),
		g.SequencerAddr,
	)

	// A migrated chain wont have yet a DaStartHeight.

	return &genesis
}

// MigrateToEvolveCmd returns a command that migrates the data from the CometBFT chain to Evolve.
func MigrateToEvolveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "evolve-migrate",
		Short: "Migrate the data from the CometBFT chain to Evolve",
		Long: `Migrate the data from the CometBFT chain to Evolve. This command should be used to migrate nodes or the sequencer.

This command will:
1. Migrate all blocks from the CometBFT blockstore to the Evolve store
2. Convert the CometBFT state to Evolve state format
3. Create a minimal evolve_genesis.json file for subsequent startups

After migration, start the node normally - it will automatically detect and use the ev_genesis.json.json file.`,
		Args: cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			config, err := cometbftcmd.ParseConfig(cmd)
			if err != nil {
				return err
			}

			cometBlockStore, cometStateStore, err := loadStateAndBlockStore(config)
			if err != nil {
				return err
			}

			cometBFTstate, err := cometStateStore.Load()
			if err != nil {
				return err
			}

			lastBlockHeight := cometBFTstate.LastBlockHeight
			cmd.Printf("Last block height: %d\n", lastBlockHeight)

			rollkitStores, err := loadRollkitStores(config.RootDir)
			if err != nil {
				return err
			}

			// Track whether sync stores were started
			var syncStoresStarted bool

			// Ensure all stores are stopped/closed in the right order on any exit path.
			// Order matters: stop header/data sync stores first, then close rollkit store,
			// then close Comet stores.
			defer func() {
				// Only stop sync stores if they were started
				if syncStoresStarted {
					_ = rollkitStores.headerSyncStore.Stop(ctx)
					_ = rollkitStores.dataSyncStore.Stop(ctx)
				}
				_ = rollkitStores.rollkitStore.Close()
				_ = cometBlockStore.Close()
				_ = cometStateStore.Close()
			}()

			// save current CometBFT state to the ABCI exec store
			if err = rollkitStores.abciExecStore.SaveState(ctx, &cometBFTstate); err != nil {
				return fmt.Errorf("failed to save CometBFT state to ABCI exec store: %w", err)
			}

			daHeight, err := cmd.Flags().GetUint64(flagDaHeight)
			if err != nil {
				return fmt.Errorf("error reading %s flag: %w", flagDaHeight, err)
			}

			rollkitState, err := cometbftStateToRollkitState(cometBFTstate, daHeight)
			if err != nil {
				return err
			}

			// create minimal rollkit genesis file for future startups
			if err := createRollkitMigrationGenesis(config.RootDir, cometBFTstate); err != nil {
				return fmt.Errorf("failed to create evolve migration genesis: %w", err)
			}

			cmd.Println("Created ev_genesis.json for migration - the node will use this on subsequent startups")

			// migrate all the blocks from the CometBFT block store to the evolve store
			// the migration is done in reverse order, starting from the last block
			missedBlocks := make(map[int64]bool)

			batch, err := rollkitStores.rollkitStore.NewBatch(ctx)
			if err != nil {
				return fmt.Errorf("failed to create batch: %w", err)
			}

			for height := lastBlockHeight; height > 0; height-- {
				cmd.Printf("Migrating block %d...\n", height)

				if len(missedBlocks) >= maxMissedBlock {
					cmd.Println("Too many missed blocks, the node was probably pruned... Stopping now, everything should be fine.")
					break
				}

				block := cometBlockStore.LoadBlock(height)
				if block == nil {
					missedBlocks[height] = true
					cmd.Printf("Block %d not found in CometBFT block store, skipping...\n", height)
					continue
				}

				header, data, signature := cometBlockToRollkit(block)

				if err = batch.SaveBlockData(header, data, &signature); err != nil {
					return fmt.Errorf("failed to save block data: %w", err)
				}

				// Save BlockID for this height so execution can build last commit post-migration
				blockParts, err := block.MakePartSet(cmttypes.BlockPartSizeBytes)
				if err != nil {
					return fmt.Errorf("failed to build part set for block %d: %w", height, err)
				}
				blockID := cmttypes.BlockID{
					Hash:          block.Header.Hash(),
					PartSetHeader: blockParts.Header(),
				}
				if err := rollkitStores.abciExecStore.SaveBlockID(ctx, uint64(block.Height), &blockID); err != nil {
					return fmt.Errorf("failed to save BlockID for height %d: %w", height, err)
				}

				// Only save extended commit info if vote extensions are enabled
				if enabled := cometBFTstate.ConsensusParams.ABCI.VoteExtensionsEnabled(block.Height); enabled {
					cmd.Printf("⚠️⚠️⚠️ Vote extensions were enabled at height %d ⚠️⚠️⚠️\n", block.Height)
					cmd.Println("⚠️⚠️⚠️ Vote extensions have no effect when using Evolve ⚠️⚠️⚠️")
					cmd.Println("⚠️⚠️⚠️ Please consult the docs ⚠️⚠️⚠️")
				}

				cmd.Println("Block", height, "migrated")
			}

			// set the last height in the Rollkit store
			if err = batch.SetHeight(uint64(lastBlockHeight)); err != nil {
				return fmt.Errorf("failed to set last height in Evolve store: %w", err)
			}

			// persist the rollkit state at the after SetHeight is called.
			if err = batch.UpdateState(rollkitState); err != nil {
				return fmt.Errorf("failed to update evolve state at height %d: %w", lastBlockHeight, err)
			}

			if err = batch.Commit(); err != nil {
				return fmt.Errorf("failed to commit batch: %w", err)
			}

			cmd.Println("Seeding sync stores head with latest migrated header/data ...")
			if err := rollkitStores.headerSyncStore.Start(ctx); err != nil {
				return fmt.Errorf("failed to start header sync store: %w", err)
			}
			if err := rollkitStores.dataSyncStore.Start(ctx); err != nil {
				return fmt.Errorf("failed to start data sync store: %w", err)
			}
			// Mark that sync stores were started successfully
			syncStoresStarted = true

			currentSyncHeight := rollkitStores.headerSyncStore.Height()
			if currentSyncHeight == 0 {
				header, data, err := rollkitStores.rollkitStore.GetBlockData(ctx, uint64(lastBlockHeight))
				if err != nil {
					return fmt.Errorf("failed to get block data for seeding sync stores at height %d: %w", lastBlockHeight, err)
				}
				if err := rollkitStores.headerSyncStore.Append(ctx, header); err != nil {
					return fmt.Errorf("failed to append header to sync store at height %d: %w", lastBlockHeight, err)
				}
				if err := rollkitStores.dataSyncStore.Append(ctx, data); err != nil {
					return fmt.Errorf("failed to append data to sync store at height %d: %w", lastBlockHeight, err)
				}
				cmd.Printf("Seeded sync stores head at height %d\n", lastBlockHeight)
			} else {
				cmd.Println("Sync stores already initialized. Skipping seeding")
			}

			// clean up the migration state from the application database to prevent halt on restart
			if err := cleanupMigrationState(config.RootDir); err != nil {
				return fmt.Errorf("failed to cleanup migration state: %w", err)
			}
			cmd.Println("Cleaned up migration state from application database")

			// Defer cleanup above will stop/close stores in correct order
			cmd.Println("Migration completed successfully")
			return nil
		},
	}

	cmd.Flags().Uint64(flagDaHeight, 1, "The DA height to set in the Evolve state. Defaults to 1.")

	return cmd
}

// cometBlockToRollkit converts a cometBFT block to a rollkit block
func cometBlockToRollkit(block *cmttypes.Block) (*rollkittypes.SignedHeader, *rollkittypes.Data, rollkittypes.Signature) {
	var (
		header    *rollkittypes.SignedHeader
		data      *rollkittypes.Data
		signature rollkittypes.Signature
	)

	// find proposer signature
	for _, sig := range block.LastCommit.Signatures {
		if bytes.Equal(sig.ValidatorAddress.Bytes(), block.ProposerAddress.Bytes()) {
			signature = sig.Signature
			break
		}
	}

	header = &rollkittypes.SignedHeader{
		Header: rollkittypes.Header{
			BaseHeader: rollkittypes.BaseHeader{
				Height:  uint64(block.Height),
				Time:    uint64(block.Time.UnixNano()),
				ChainID: block.ChainID,
			},
			Version: rollkittypes.Version{
				Block: block.Version.Block,
				App:   block.Version.App,
			},
			LastHeaderHash:  block.Header.Hash().Bytes(),
			DataHash:        block.DataHash.Bytes(),
			AppHash:         block.AppHash.Bytes(),
			ValidatorHash:   block.ValidatorsHash.Bytes(),
			ProposerAddress: block.ProposerAddress.Bytes(),
		},
		Signature: signature,
	}

	data = &rollkittypes.Data{
		Metadata: &rollkittypes.Metadata{
			ChainID:      block.ChainID,
			Height:       uint64(block.Height),
			Time:         uint64(block.Time.UnixNano()),
			LastDataHash: block.DataHash.Bytes(),
		},
	}

	for _, tx := range block.Txs {
		data.Txs = append(data.Txs, rollkittypes.Tx(tx))
	}

	return header, data, signature
}

func loadStateAndBlockStore(config *cfg.Config) (*cmtstore.BlockStore, state.Store, error) {
	dbType := cmtdbm.BackendType(config.DBBackend)

	if ok, err := fileExists(filepath.Join(config.DBDir(), "blockstore.db")); !ok || err != nil {
		return nil, nil, fmt.Errorf("no blockstore found in %v: %w", config.DBDir(), err)
	}

	// Get BlockStore
	blockStoreDB, err := cmtdbm.NewDB("blockstore", dbType, config.DBDir())
	if err != nil {
		return nil, nil, err
	}
	blockStore := cmtstore.NewBlockStore(blockStoreDB)

	if ok, err := fileExists(filepath.Join(config.DBDir(), "state.db")); !ok || err != nil {
		return nil, nil, fmt.Errorf("no statestore found in %v: %w", config.DBDir(), err)
	}

	// Get StateStore
	stateDB, err := cmtdbm.NewDB("state", dbType, config.DBDir())
	if err != nil {
		return nil, nil, err
	}
	stateStore := state.NewStore(stateDB, state.StoreOptions{
		DiscardABCIResponses: config.Storage.DiscardABCIResponses,
	})

	return blockStore, stateStore, nil
}

type rollkitStores struct {
	rollkitStore    rollkitstore.Store
	abciExecStore   *execstore.Store
	dataSyncStore   *goheaderstore.Store[*rollkittypes.Data]
	headerSyncStore *goheaderstore.Store[*rollkittypes.SignedHeader]
}

func loadRollkitStores(rootDir string) (rollkitStores, error) {
	store, err := rollkitstore.NewDefaultKVStore(rootDir, "data", "evolve")
	if err != nil {
		return rollkitStores{}, fmt.Errorf("failed to create rollkit store: %w", err)
	}

	rollkitPrefixStore := ktds.Wrap(store, &ktds.PrefixTransform{
		Prefix: ds.NewKey(rollkitnode.EvPrefix),
	})

	ds, err := goheaderstore.NewStore[*rollkittypes.Data](
		rollkitPrefixStore,
		goheaderstore.WithStorePrefix("dataSync"),
		goheaderstore.WithMetrics(),
	)
	if err != nil {
		return rollkitStores{}, err
	}

	hs, err := goheaderstore.NewStore[*rollkittypes.SignedHeader](
		rollkitPrefixStore,
		goheaderstore.WithStorePrefix("headerSync"),
		goheaderstore.WithMetrics(),
	)
	if err != nil {
		return rollkitStores{}, err
	}

	return rollkitStores{
		rollkitStore:    rollkitstore.New(rollkitPrefixStore),
		abciExecStore:   execstore.NewExecABCIStore(store),
		dataSyncStore:   ds,
		headerSyncStore: hs,
	}, nil
}

func cometbftStateToRollkitState(cometBFTState state.State, daHeight uint64) (rollkittypes.State, error) {
	return rollkittypes.State{
		Version: rollkittypes.Version{
			Block: cometBFTState.Version.Consensus.Block,
			App:   cometBFTState.Version.Consensus.App,
		},
		ChainID:         cometBFTState.ChainID,
		InitialHeight:   uint64(cometBFTState.LastBlockHeight), // The initial height is the migration height
		LastBlockHeight: uint64(cometBFTState.LastBlockHeight),
		LastBlockTime:   cometBFTState.LastBlockTime,

		DAHeight: daHeight,

		AppHash: cometBFTState.AppHash,
	}, nil
}

// createRollkitMigrationGenesis creates a minimal rollkit genesis file for migration.
// This creates a lightweight genesis containing only the essential information needed
// for rollkit to start after migration. The full state is stored separately in the
// rollkit state store.
func createRollkitMigrationGenesis(rootDir string, cometBFTState state.State) error {
	var (
		sequencerAddr   []byte
		sequencerPubKey crypto.PubKey
	)

	// use the first validator as sequencer (assuming single validator setup for migration)
	if len(cometBFTState.LastValidators.Validators) == 1 {
		sequencerAddr = cometBFTState.LastValidators.Validators[0].Address.Bytes()
		sequencerPubKey = cometBFTState.LastValidators.Validators[0].PubKey
	} else if len(cometBFTState.LastValidators.Validators) > 1 {
		sequencer, err := getSequencerFromMigrationMngrState(rootDir, cometBFTState)
		if err != nil {
			return fmt.Errorf("failed to get sequencer from migrationmngr state: %w", err)
		}

		sequencerAddr = sequencer.Address
		sequencerPubKey = sequencer.PubKey
	} else {
		return fmt.Errorf("no validators found in the last validators, cannot determine sequencer address")
	}

	migrationGenesis := evolveMigrationGenesis{
		ChainID:         cometBFTState.ChainID,
		InitialHeight:   uint64(cometBFTState.InitialHeight),
		GenesisTime:     cometBFTState.LastBlockTime.UnixNano(),
		SequencerAddr:   sequencerAddr,
		SequencerPubKey: sequencerPubKey,
	}

	// using cmtjson for marshalling to ensure compatibility with cometbft genesis format
	genesisBytes, err := cmtjson.MarshalIndent(migrationGenesis, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal evolve migration genesis: %w", err)
	}

	genesisPath := filepath.Join(rootDir, evolveGenesisFilename)
	if err := os.WriteFile(genesisPath, genesisBytes, 0o644); err != nil {
		return fmt.Errorf("failed to write evolve migration genesis to %s: %w", genesisPath, err)
	}

	return nil
}

// sequencerInfo holds the sequencer information extracted from migrationmngr state
type sequencerInfo struct {
	Address []byte
	PubKey  crypto.PubKey
}

// getSequencerFromMigrationMngrState attempts to load the sequencer information from the migrationmngr module state
func getSequencerFromMigrationMngrState(rootDir string, cometBFTState state.State) (*sequencerInfo, error) {
	config := cfg.DefaultConfig()
	config.SetRoot(rootDir)

	dbType := dbm.BackendType(config.DBBackend)

	// Check if application database exists
	appDBPath := filepath.Join(config.DBDir(), "application.db")
	if ok, err := fileExists(appDBPath); err != nil {
		return nil, fmt.Errorf("error checking application database in %v: %w", config.DBDir(), err)
	} else if !ok {
		return nil, fmt.Errorf("no application database found in %v", config.DBDir())
	}

	// Open application database
	appDB, err := dbm.NewDB("application", dbType, config.DBDir())
	if err != nil {
		return nil, fmt.Errorf("failed to open application database: %w", err)
	}
	defer func() { _ = appDB.Close() }()

	encCfg := moduletestutil.MakeTestEncodingConfig(migrationmngr.AppModuleBasic{})

	// Register crypto types so UnpackInterfaces can properly decode the consensus pubkey
	cryptocodec.RegisterInterfaces(encCfg.InterfaceRegistry)

	// The migrationmngr module data is stored with the prefix: s/k:migrationmngr/
	// Collections library adds 0x66 ('f') prefix for Item type collections
	// For the Sequencer collection, the full key is: s/k:migrationmngr/ + 0x66 + SequencerKey
	modulePrefix := fmt.Sprintf("s/k:%s/", migrationmngrtypes.ModuleName)

	// Collections library uses 0x66 ('f') as a deterministic prefix for Item types
	collectionsItemPrefix := byte(0x66)

	// Build the full key: module prefix + collections Item prefix + sequencer key
	fullKey := append([]byte(modulePrefix), collectionsItemPrefix)
	fullKey = append(fullKey, migrationmngrtypes.SequencerKey...)

	// read directly from the database
	sequencerBytes, err := appDB.Get(fullKey)
	if err != nil {
		return nil, fmt.Errorf("failed to read sequencer from database: %w", err)
	}
	if len(sequencerBytes) == 0 {
		return nil, fmt.Errorf("sequencer not found in migrationmngr state at key %q", string(fullKey))
	}

	// The collections library wraps values in a protobuf container with a field tag and length.
	// First byte: field tag (e.g., 0x6a = field 13, wire type 2)
	// Second byte: length varint
	// We skip these 2 wrapper bytes to get to the actual Sequencer proto
	// TODO: properly decode the varint length instead of hardcoding 2 bytes
	if len(sequencerBytes) < 2 {
		return nil, fmt.Errorf("sequencer bytes too short: %d", len(sequencerBytes))
	}
	actualProtoBytes := sequencerBytes[2:]

	var sequencer migrationmngrtypes.Sequencer
	if err := encCfg.Codec.Unmarshal(actualProtoBytes, &sequencer); err != nil {
		return nil, fmt.Errorf("failed to unmarshal sequencer: %w", err)
	}

	if err := sequencer.UnpackInterfaces(encCfg.InterfaceRegistry); err != nil {
		return nil, fmt.Errorf("failed to unpack sequencer interfaces: %w", err)
	}

	// Extract the public key from the sequencer
	pubKeyAny := sequencer.ConsensusPubkey
	if pubKeyAny == nil {
		return nil, fmt.Errorf("sequencer consensus public key is nil")
	}

	// Convert from Cosmos SDK crypto type to CometBFT crypto type
	// The cached value is a Cosmos SDK cryptotypes.PubKey, we need CometBFT crypto.PubKey
	sdkPubKey, ok := pubKeyAny.GetCachedValue().(cryptotypes.PubKey)
	if !ok {
		return nil, fmt.Errorf("failed to get SDK pubkey from cached value, got type %T", pubKeyAny.GetCachedValue())
	}

	pubKey, err := cryptocodec.ToCmtPubKeyInterface(sdkPubKey)
	if err != nil {
		return nil, fmt.Errorf("failed to convert public key to CometBFT format: %w", err)
	}

	// Get the address from the public key
	addr := pubKey.Address()

	// Validate that this sequencer is actually one of the validators
	validatorFound := false
	for _, validator := range cometBFTState.LastValidators.Validators {
		if bytes.Equal(validator.Address.Bytes(), addr) {
			validatorFound = true
			break
		}
	}

	if !validatorFound {
		return nil, fmt.Errorf("sequencer from migrationmngr state (address: %x) is not found in the validator set", addr)
	}

	return &sequencerInfo{
		Address: addr,
		PubKey:  pubKey,
	}, nil
}

// cleanupMigrationState removes the migration state from the application database
// to prevent the chain from halting on restart after migration is complete.
func cleanupMigrationState(rootDir string) error {
	config := cfg.DefaultConfig()
	config.SetRoot(rootDir)

	dbType := dbm.BackendType(config.DBBackend)

	// check if application database exists
	appDBPath := filepath.Join(config.DBDir(), "application.db")
	if ok, err := fileExists(appDBPath); err != nil {
		return fmt.Errorf("error checking application database in %v: %w", config.DBDir(), err)
	} else if !ok {
		return fmt.Errorf("no application database found in %v", config.DBDir())
	}

	// open application database
	appDB, err := dbm.NewDB("application", dbType, config.DBDir())
	if err != nil {
		return fmt.Errorf("failed to open application database: %w", err)
	}
	defer func() { _ = appDB.Close() }()

	// the migrationmngr module data is stored with the prefix: s/k:migrationmngr/
	// collections library adds 0x66 ('f') prefix for Item type collections
	// for the Migration collection, the full key is: s/k:migrationmngr/ + 0x66 + MigrationKey
	modulePrefix := fmt.Sprintf("s/k:%s/", migrationmngrtypes.ModuleName)

	// collections library uses 0x66 ('f') as a deterministic prefix for Item types
	collectionsItemPrefix := byte(0x66)

	// build the full key for migration state: module prefix + collections Item prefix + migration key
	migrationFullKey := append([]byte(modulePrefix), collectionsItemPrefix)
	migrationFullKey = append(migrationFullKey, migrationmngrtypes.MigrationKey...)

	// delete the migration state from the database
	if err := appDB.Delete(migrationFullKey); err != nil {
		return fmt.Errorf("failed to delete migration state from database: %w", err)
	}

	// also remove the sequencer state as it's no longer needed after migration
	sequencerFullKey := append([]byte(modulePrefix), collectionsItemPrefix)
	sequencerFullKey = append(sequencerFullKey, migrationmngrtypes.SequencerKey...)

	if err := appDB.Delete(sequencerFullKey); err != nil {
		return fmt.Errorf("failed to delete sequencer state from database: %w", err)
	}

	return nil
}

// fileExists checks if a file/directory exists.
func fileExists(filename string) (bool, error) {
	_, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("error checking file %s: %w", filename, err)
	}

	return true, nil
}
