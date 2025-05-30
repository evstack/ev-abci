package keeper

import (
	"context"

	abci "github.com/cometbft/cometbft/abci/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/rollkit/go-execution-abci/modules/rollkitmngr/types"
)

// IBCSmoothingFactor is the factor used to smooth the migration process when IBC is enabled. It determines how many blocks the migration will take.
var IBCSmoothingFactor uint64 = 30

// migrateNow migrates the chain to rollkit immediately.
// this method is used when ibc is not enabled, so no migration smoothing is needed.
func (k Keeper) migrateNow(
	ctx context.Context,
	migrationData types.RollkitMigration,
	lastValidatorSet []stakingtypes.Validator,
) (initialValUpdates []abci.ValidatorUpdate, err error) {
	switch len(migrationData.Attesters) {
	case 0:
		// no attesters, we are migrating to a single sequencer
		initialValUpdates, err = migrateToSequencer(migrationData, lastValidatorSet)
		if err != nil {
			return nil, sdkerrors.ErrInvalidRequest.Wrapf("failed to migrate to sequencer: %v", err)
		}
	default:
		// we are migrating the validator set to attesters
		initialValUpdates, err = migrateToAttesters(migrationData, lastValidatorSet)
		if err != nil {
			return nil, sdkerrors.ErrInvalidRequest.Wrapf("failed to migrate to sequencer & attesters: %v", err)
		}
	}

	// set new sequencer in the store
	// it will be used by the rollkit migration command when using attesters
	seq := migrationData.Sequencer
	if err := k.Sequencer.Set(ctx, seq); err != nil {
		return nil, sdkerrors.ErrInvalidRequest.Wrapf("failed to set sequencer: %v", err)
	}

	return initialValUpdates, nil
}

// migrateToSequencer migrates the chain to a single sequencer.
// the validator set is updated to include the sequencer and remove all other validators.
func migrateToSequencer(
	migrationData types.RollkitMigration,
	lastValidatorSet []stakingtypes.Validator,
) (initialValUpdates []abci.ValidatorUpdate, err error) {
	seq := migrationData.Sequencer

	pk, err := seq.TmConsPublicKey()
	if err != nil {
		return nil, err
	}
	sequencerUpdate := abci.ValidatorUpdate{
		PubKey: pk,
		Power:  1,
	}

	for _, val := range lastValidatorSet {
		powerUpdate := val.ABCIValidatorUpdateZero()
		if val.ConsensusPubkey.Equal(seq.ConsensusPubkey) {
			continue
		}
		initialValUpdates = append(initialValUpdates, powerUpdate)
	}

	return append(initialValUpdates, sequencerUpdate), nil
}

// migrateToAttesters migrates the chain to attesters.
// the validator set is updated to include the attesters and remove all other validators.
func migrateToAttesters(
	migrationData types.RollkitMigration,
	lastValidatorSet []stakingtypes.Validator,
) (initialValUpdates []abci.ValidatorUpdate, err error) {
	// First, remove all existing validators that are not attesters
	attesterPubKeys := make(map[string]bool)
	for _, attester := range migrationData.Attesters {
		key := attester.ConsensusPubkey.String()
		attesterPubKeys[key] = true
	}

	// Remove validators that are not attesters
	for _, val := range lastValidatorSet {
		if !attesterPubKeys[val.ConsensusPubkey.String()] {
			powerUpdate := val.ABCIValidatorUpdateZero()
			initialValUpdates = append(initialValUpdates, powerUpdate)
		}
	}

	// Add attesters with power 1
	for _, attester := range migrationData.Attesters {
		pk, err := attester.TmConsPublicKey()
		if err != nil {
			return nil, err
		}
		attesterUpdate := abci.ValidatorUpdate{
			PubKey: pk,
			Power:  1,
		}
		initialValUpdates = append(initialValUpdates, attesterUpdate)
	}

	return initialValUpdates, nil
}

// migrateOver migrates the chain to rollkit over a period of blocks.
// this is to ensure ibc light client verification keep working while changing the whole validator set.
func (k Keeper) migrateOver(ctx context.Context, migrationData types.RollkitMigration, lastValidatorSet []stakingtypes.Validator) (initialValUpdates []abci.ValidatorUpdate, err error) {
	panic("not implemented yet")
}
