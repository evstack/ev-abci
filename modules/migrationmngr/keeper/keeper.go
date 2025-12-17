package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	corestore "cosmossdk.io/core/store"
	"cosmossdk.io/log"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/evstack/ev-abci/modules/migrationmngr/types"
)

type Keeper struct {
	storeService corestore.KVStoreService
	cdc          codec.BinaryCodec
	addressCodec address.Codec
	authority    string

	stakingKeeper types.StakingKeeper

	Schema        collections.Schema
	Sequencer     collections.Item[types.Sequencer]
	Migration     collections.Item[types.EvolveMigration]
	MigrationStep collections.Item[uint64]
}

// NewKeeper creates a new sequencer Keeper instance.
func NewKeeper(
	cdc codec.BinaryCodec,
	storeService corestore.KVStoreService,
	addressCodec address.Codec,
	stakingKeeper types.StakingKeeper,
	authority string,
) Keeper {
	// ensure that authority is a valid account address
	if _, err := addressCodec.StringToBytes(authority); err != nil {
		panic("authority is not a valid acc address")
	}

	sb := collections.NewSchemaBuilder(storeService)
	k := Keeper{
		storeService:  storeService,
		cdc:           cdc,
		authority:     authority,
		addressCodec:  addressCodec,
		stakingKeeper: stakingKeeper,
		Sequencer: collections.NewItem(
			sb,
			types.SequencerKey,
			"sequencer",
			codec.CollValue[types.Sequencer](cdc),
		),
		Migration: collections.NewItem(
			sb,
			types.MigrationKey,
			"evolve_migration",
			codec.CollValue[types.EvolveMigration](cdc),
		),
		MigrationStep: collections.NewItem(
			sb,
			types.MigrationStepKey,
			"migration_step",
			collections.Uint64Value,
		),
	}

	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema

	return k
}

// Logger returns a module-specific logger.
func (k Keeper) Logger(ctx context.Context) log.Logger {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	return sdkCtx.Logger().With("module", "x/"+types.ModuleName)
}

// IsMigrating checks if the migration to Evolve is in progress.
// It checks if the EvolveMigration item exists in the store.
// And if it does, it verifies we are past the block height that started the migration.
func (k Keeper) IsMigrating(ctx context.Context) (start, end uint64, ok bool) {
	migration, err := k.Migration.Get(ctx)
	if errors.Is(err, collections.ErrNotFound) {
		return 0, 0, false
	} else if err != nil {
		k.Logger(ctx).Error("failed to get evolve migration state", "error", err)
		return 0, 0, false
	}

	// Migration is performed in a single step.
	migrationEndHeight := migration.BlockHeight + 1

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	currentHeight := uint64(sdkCtx.BlockHeight())
	migrationInProgress := currentHeight >= migration.BlockHeight && currentHeight <= migrationEndHeight

	return migration.BlockHeight, migrationEndHeight, migrationInProgress
}
