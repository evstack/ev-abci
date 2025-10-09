package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	abci "github.com/cometbft/cometbft/abci/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/evstack/ev-abci/modules/migrationmngr/types"
)

// IBCSmoothingFactor is the factor used to smooth the migration process when IBC is enabled. It determines how many blocks the migration will take.
var IBCSmoothingFactor uint64 = 30

// migrateNow migrates the chain to evolve immediately.
// this method is used when ibc is not enabled, so no migration smoothing is needed.
func (k Keeper) migrateNow(
	ctx context.Context,
	migrationData types.EvolveMigration,
	lastValidatorSet []stakingtypes.Validator,
) (initialValUpdates []abci.ValidatorUpdate, err error) {
	// ensure sequencer pubkey Any is unpacked and cached for TmConsPublicKey() to work correctly
	if err := migrationData.Sequencer.UnpackInterfaces(k.cdc); err != nil {
		return nil, sdkerrors.ErrInvalidRequest.Wrapf("failed to unpack sequencer pubkey: %v", err)
	}
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
	// it will be used by the evolve migration command when using attesters
	seq := migrationData.Sequencer
	if err := k.Sequencer.Set(ctx, seq); err != nil {
		return nil, sdkerrors.ErrInvalidRequest.Wrapf("failed to set sequencer: %v", err)
	}

	return initialValUpdates, nil
}

// migrateToSequencer migrates the chain to a single sequencer.
// the validator set is updated to include the sequencer and remove all other validators.
func migrateToSequencer(
	migrationData types.EvolveMigration,
	lastValidatorSet []stakingtypes.Validator,
) (initialValUpdates []abci.ValidatorUpdate, err error) {
	seq := &migrationData.Sequencer

	pk, err := seq.TmConsPublicKey()
	if err != nil {
		return nil, err
	}
	sequencerUpdate := abci.ValidatorUpdate{
		PubKey: pk,
		Power:  1,
	}

	for _, val := range lastValidatorSet {
		// skip the sequencer - we'll add it at the end with power 1
		if val.ConsensusPubkey.Equal(seq.ConsensusPubkey) {
			continue
		}
		// use ABCIValidatorUpdateZero() to get the proper CometBFT representation
		// this ensures the pubkey bytes match what CometBFT expects
		powerUpdate := val.ABCIValidatorUpdateZero()
		initialValUpdates = append(initialValUpdates, powerUpdate)
	}

	return append(initialValUpdates, sequencerUpdate), nil
}

// migrateToAttesters migrates the chain to attesters.
// the validator set is updated to include the attesters and remove all other validators.
func migrateToAttesters(
	migrationData types.EvolveMigration,
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
	for i := range migrationData.Attesters {
		pk, err := migrationData.Attesters[i].TmConsPublicKey()
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

// migrateOver migrates the chain to evolve over a period of blocks.
// this is to ensure ibc light client verification keep working while changing the whole validator set.
// the migration step is tracked in store.
func (k Keeper) migrateOver(
	ctx context.Context,
	migrationData types.EvolveMigration,
	lastValidatorSet []stakingtypes.Validator,
) (initialValUpdates []abci.ValidatorUpdate, err error) {
	// Ensure pubkey Any fields are unpacked before use during progressive migrations
	if err := migrationData.Sequencer.UnpackInterfaces(k.cdc); err != nil {
		return nil, sdkerrors.ErrInvalidRequest.Wrapf("failed to unpack sequencer interfaces: %v", err)
	}
	for i := range migrationData.Attesters {
		if err := migrationData.Attesters[i].UnpackInterfaces(k.cdc); err != nil {
			return nil, sdkerrors.ErrInvalidRequest.Wrapf("failed to unpack attester interfaces: %v", err)
		}
	}
	step, err := k.MigrationStep.Get(ctx)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return nil, sdkerrors.ErrInvalidRequest.Wrapf("failed to get migration step: %v", err)
	}

	if step >= IBCSmoothingFactor {
		// migration complete, just return the final set (same as migrateNow)
		if err := k.MigrationStep.Remove(ctx); err != nil {
			return nil, sdkerrors.ErrInvalidRequest.Wrapf("failed to remove migration step: %v", err)
		}
		return k.migrateNow(ctx, migrationData, lastValidatorSet)
	}

	switch len(migrationData.Attesters) {
	case 0:
		// no attesters, migrate to a single sequencer over smoothing period
		// remove all validators except the sequencer, add sequencer at the end
		seq := migrationData.Sequencer
		var oldValsToRemove []stakingtypes.Validator
		for _, val := range lastValidatorSet {
			if !val.ConsensusPubkey.Equal(seq.ConsensusPubkey) {
				oldValsToRemove = append(oldValsToRemove, val)
			}
		}
		removePerStep := (len(oldValsToRemove) + int(IBCSmoothingFactor) - 1) / int(IBCSmoothingFactor)
		startRemove := int(step) * removePerStep
		endRemove := min(startRemove+removePerStep, len(oldValsToRemove))
		for _, val := range oldValsToRemove[startRemove:endRemove] {
			powerUpdate := val.ABCIValidatorUpdateZero()
			initialValUpdates = append(initialValUpdates, powerUpdate)
		}
	default:
		// attesters present, migrate as before
		attesterPubKeys := make(map[string]struct{})
		for _, attester := range migrationData.Attesters {
			attesterPubKeys[attester.ConsensusPubkey.String()] = struct{}{}
		}
		var oldValsToRemove []stakingtypes.Validator
		for _, val := range lastValidatorSet {
			if _, ok := attesterPubKeys[val.ConsensusPubkey.String()]; !ok {
				oldValsToRemove = append(oldValsToRemove, val)
			}
		}
		lastValPubKeys := make(map[string]struct{})
		for _, val := range lastValidatorSet {
			lastValPubKeys[val.ConsensusPubkey.String()] = struct{}{}
		}
		var newAttestersToAdd []types.Attester
		for _, attester := range migrationData.Attesters {
			if _, ok := lastValPubKeys[attester.ConsensusPubkey.String()]; !ok {
				newAttestersToAdd = append(newAttestersToAdd, attester)
			}
		}
		removePerStep := (len(oldValsToRemove) + int(IBCSmoothingFactor) - 1) / int(IBCSmoothingFactor)
		addPerStep := (len(newAttestersToAdd) + int(IBCSmoothingFactor) - 1) / int(IBCSmoothingFactor)
		startRemove := int(step) * removePerStep
		endRemove := min(startRemove+removePerStep, len(oldValsToRemove))
		for _, val := range oldValsToRemove[startRemove:endRemove] {
			powerUpdate := val.ABCIValidatorUpdateZero()
			initialValUpdates = append(initialValUpdates, powerUpdate)
		}
		startAdd := int(step) * addPerStep
		endAdd := min(startAdd+addPerStep, len(newAttestersToAdd))
		for i := startAdd; i < endAdd; i++ {
			pk, err := newAttestersToAdd[i].TmConsPublicKey()
			if err != nil {
				return nil, sdkerrors.ErrInvalidRequest.Wrapf("failed to get attester pubkey: %v", err)
			}
			attesterUpdate := abci.ValidatorUpdate{
				PubKey: pk,
				Power:  1,
			}
			initialValUpdates = append(initialValUpdates, attesterUpdate)
		}
	}

	// increment and persist the step
	if err := k.MigrationStep.Set(ctx, step+1); err != nil {
		return nil, sdkerrors.ErrInvalidRequest.Wrapf("failed to set migration step: %v", err)
	}

	// the first time, we set the whole validator set to the same validator power. This is to avoid a validator ends up with >= 33% or worse >= 66%
	// vp during the migration.
	// TODO: add a test
	if step == 0 {
		// Create a map of existing updates for O(1) lookup
		existingUpdates := make(map[string]bool)
		for _, powerUpdate := range initialValUpdates {
			existingUpdates[powerUpdate.PubKey.String()] = true
		}

		// set the whole validator set to the same power
		for _, val := range lastValidatorSet {
			valPubKey, err := val.CmtConsPublicKey()
			if err != nil {
				return nil, sdkerrors.ErrInvalidRequest.Wrapf("failed to get validator pubkey: %v", err)
			}

			if !existingUpdates[valPubKey.String()] {
				powerUpdate := abci.ValidatorUpdate{
					PubKey: valPubKey,
					Power:  1,
				}
				initialValUpdates = append(initialValUpdates, powerUpdate)
			}
		}
	}

	return initialValUpdates, nil
}
