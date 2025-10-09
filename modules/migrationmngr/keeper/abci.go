package keeper

import (
	"context"
	"errors"

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

// BeginBlock makes sure the chain halts at block height + 1 after the migration end.
// This is to ensure that the migration is completed with a binary switch.
func (k Keeper) BeginBlock(ctx context.Context) error {
	start, end, _ := k.IsMigrating(ctx)
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	currentHeight := uint64(sdkCtx.BlockHeight())
	shouldHalt := end > 0 && currentHeight == end+1

	k.Logger(ctx).Info("BeginBlock migration check",
		"height", currentHeight,
		"start", start,
		"end", end,
		"shouldHalt", shouldHalt)

	// one block after the migration end, we halt forcing a binary switch
	if shouldHalt {
		k.Logger(ctx).Info("HALTING CHAIN - migration complete, binary switch required")

		// remove the migration state from the store
		// this is to ensure at restart we won't halt the chain again
		if err := k.Migration.Remove(ctx); err != nil {
			return sdkerrors.ErrLogic.Wrapf("failed to delete migration state: %v", err)
		}

		return errors.New("app migration to evolve is in progress, switch to the new binary and run the evolve migration command to complete the migration")
	}

	return nil
}

// EndBlocker is called at the end of every block and returns sequencer updates.
func (k Keeper) EndBlock(ctx context.Context) ([]abci.ValidatorUpdate, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	start, end, shouldBeMigrating := k.IsMigrating(ctx)
	k.Logger(ctx).Info("EndBlock migration check", "height", sdkCtx.BlockHeight(), "start", start, "end", end, "shouldBeMigrating", shouldBeMigrating)

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
		} else {
			// subsequent blocks during migration: return empty updates
			updates = []abci.ValidatorUpdate{}
		}
	} else {
		updates, err = k.migrateOver(sdkCtx, migration, validatorSet)
		if err != nil {
			return nil, err
		}
	}

	k.Logger(ctx).Info("EndBlock migration updates", "height", sdkCtx.BlockHeight(), "updates", len(updates))
	return updates, nil
}
