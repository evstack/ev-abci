package keeper

import (
	"context"

	"cosmossdk.io/core/appmodule"
	abci "github.com/cometbft/cometbft/abci/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
)

// 1. Migrate to sequencer only
/*
	- Slowly migrate the validator set to the sequencer
	- We need to check if the sequencer is already in the validator set, otherwise it would need to be added to the validator set (check what are the implications of this)
	- If IBC enabled and vp of sequencer 2/3, migration can be done immediately
*/

// 2. Migrate to sequencer with attesters network
/*
	- If IBC enabled and vp of attesters network 2/3, migration can be done immediately
	- Otherwise, slowly migrate the validator set to the attesters
*/

// PreBlock makes sure the chain halts at block height + 1 after the migration end.
// This is to ensure that the migration is completed with a binary switch.
func (k Keeper) PreBlock(ctx context.Context) (appmodule.ResponsePreBlock, error) {
	start, end, _ := k.IsMigrating(ctx)
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	currentHeight := uint64(sdkCtx.BlockHeight())
	shouldHalt := end > 0 && currentHeight == end+1

	k.Logger(ctx).Debug("PreBlock migration check",
		"height", currentHeight,
		"start", start,
		"end", end,
		"shouldHalt", shouldHalt)

	// one block after the migration end, we halt forcing a binary switch
	if shouldHalt {
		migration, err := k.Migration.Get(ctx)
		if err != nil {
			k.Logger(ctx).Error("failed to get migration state", "error", err)
			return nil, sdkerrors.ErrLogic.Wrapf("failed to get migration state: %v", err)
		}

		if migration.StayOnComet {
			k.Logger(ctx).Info("Migration complete, staying on CometBFT")

			// remove the migration state from the store
			if err := k.Migration.Remove(ctx); err != nil {
				k.Logger(ctx).Error("failed to remove migration state", "error", err)
			}

			// continue running on CometBFT - do not halt
			return &sdk.ResponsePreBlock{}, nil
		}

		k.Logger(ctx).Info("HALTING CHAIN - migration complete, binary switch required")

		// remove the migration state from the store
		// this is to ensure at restart we won't halt the chain again
		if err := k.Migration.Remove(ctx); err != nil {
			k.Logger(ctx).Error("failed to remove migration state", "error", err)
		}

		// return error to halt the chain,this causes all validators to halt
		// the node operator will see this error message and know to switch to the new binary
		return nil, sdkerrors.ErrLogic.Wrap("MIGRATE: chain migration to evolve is complete. Switch to the evolve binary and run 'gmd evolve-migrate' to complete the migration.")
	}

	return &sdk.ResponsePreBlock{}, nil
}

// EndBlock is called at the end of every block and returns sequencer updates.
func (k Keeper) EndBlock(ctx context.Context) ([]abci.ValidatorUpdate, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	start, end, shouldBeMigrating := k.IsMigrating(ctx)
	k.Logger(ctx).Debug("EndBlock migration check", "height", sdkCtx.BlockHeight(), "start", start, "end", end, "shouldBeMigrating", shouldBeMigrating)

	if !shouldBeMigrating || start > uint64(sdkCtx.BlockHeight()) {
		// no migration in progress, return empty updates
		return []abci.ValidatorUpdate{}, nil
	}

	migration, err := k.Migration.Get(ctx)
	if err != nil {
		return nil, sdkerrors.ErrLogic.Wrapf("failed to get migration state: %v", err)
	}

	validatorSet, err := k.stakingKeeper.GetLastValidators(sdkCtx)
	if err != nil {
		return nil, err
	}

	var updates []abci.ValidatorUpdate
	if !k.isIBCEnabled(ctx) {
		// if IBC is not enabled, we can migrate immediately
		// but only return updates on the first block of migration (start height)
		if uint64(sdkCtx.BlockHeight()) == start {
			updates, err = k.migrateNow(ctx, migration, validatorSet)
			if err != nil {
				return nil, err
			}
		}
	} else {
		updates, err = k.migrateOver(sdkCtx, migration, validatorSet)
		if err != nil {
			return nil, err
		}
	}

	k.Logger(ctx).Debug("EndBlock migration updates", "height", sdkCtx.BlockHeight(), "updates", len(updates))
	return updates, nil
}
