package keeper

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/evstack/ev-abci/modules/network/types"
)

// BeginBlocker handles begin block logic for the network module
func (k Keeper) BeginBlocker(ctx sdk.Context) error {
	params := k.GetParams(ctx)

	// Only process if sign mode is IBC_ONLY and we have outbound IBC packets
	if params.SignMode == types.SignMode_SIGN_MODE_IBC_ONLY {
		return errors.New("IBC only sign mode not yet implemented")
	}
	return nil
}

// EndBlocker handles end block logic for the network module
func (k Keeper) EndBlocker(ctx sdk.Context) error {
	height := ctx.BlockHeight()
	params := k.GetParams(ctx)

	// Handle checkpoint heights
	if k.IsCheckpointHeight(ctx, height) {
		if err := k.processCheckpoint(ctx, height); err != nil {
			return fmt.Errorf("processing checkpoint at height %d: %w", height, err)
		}
	}

	// Handle epoch end
	epoch := k.GetCurrentEpoch(ctx)
	nextHeight := height + 1
	nextEpoch := uint64(nextHeight) / params.EpochLength

	if epoch != nextEpoch {
		if err := k.processEpochEnd(ctx, epoch); err != nil {
			return fmt.Errorf("processing epoch end %d: %w", epoch, err)
		}
	}
	return nil
}

// processCheckpoint handles checkpoint processing
func (k Keeper) processCheckpoint(ctx sdk.Context, height int64) error {
	bitmapBytes, err := k.GetAttestationBitmap(ctx, height)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return fmt.Errorf("get attestation bitmap: %w", err)
	}
	if bitmapBytes == nil {
		return nil
	}

	votedPower, err := k.CalculateVotedPower(ctx, bitmapBytes)
	if err != nil {
		return err
	}
	totalPower, err := k.GetTotalPower(ctx)
	if err != nil {
		return err
	}

	validatorHash := sha256.Sum256(bitmapBytes)

	commitHash := sha256.Sum256([]byte("placeholder"))

	softConfirmed, err := k.CheckQuorum(ctx, votedPower, totalPower)
	if err != nil {
		return err
	}

	attestationInfoToStore := types.AttestationBitmap{
		Height:        height,
		Bitmap:        bitmapBytes,
		VotedPower:    votedPower,
		TotalPower:    totalPower,
		SoftConfirmed: softConfirmed,
	}

	if err := k.StoredAttestationInfo.Set(ctx, height, attestationInfoToStore); err != nil {
		return fmt.Errorf("storing attestation info at height %d: %w", height, err)
	}

	// Emit hashes
	k.emitCheckpointHashes(ctx, height, validatorHash[:], commitHash[:], softConfirmed)
	return nil
}

func (k Keeper) processEpochEnd(ctx sdk.Context, epoch uint64) error {
	params := k.GetParams(ctx)
	epochBitmap := k.GetEpochBitmap(ctx, epoch)

	if epochBitmap != nil {
		validators, err := k.stakingKeeper.GetLastValidators(ctx)
		if err != nil {
			return fmt.Errorf("getting last validators: %w", err)
		}
		totalBondedValidators := 0
		for _, v := range validators {
			if v.IsBonded() {
				totalBondedValidators++
			}
		}

		if totalBondedValidators > 0 {
			participated := k.bitmapHelper.PopCount(epochBitmap)
			minParticipation, err := math.LegacyNewDecFromStr(params.MinParticipation)
			if err != nil {
				return fmt.Errorf("parsing MinParticipation parameter: %w", err)
			}
			participationRate := math.LegacyNewDec(int64(participated)).QuoInt64(int64(totalBondedValidators))
			if participationRate.LT(minParticipation) {
				k.ejectLowParticipants(ctx, epochBitmap)
			}
		}
	}

	if !params.EmergencyMode {
		epochStartHeight := int64(epoch * params.EpochLength)
		checkpointsInEpoch := 0
		softConfirmedCheckpoints := 0

		for h := epochStartHeight; h < epochStartHeight+int64(params.EpochLength); h++ {
			if h > ctx.BlockHeight() {
				break
			}
			if k.IsCheckpointHeight(ctx, h) {
				checkpointsInEpoch++
				if q, err := k.IsSoftConfirmed(ctx, h); q && err == nil {
					softConfirmedCheckpoints++
				}
			}
		}

		if checkpointsInEpoch > 0 && softConfirmedCheckpoints == 0 {
			// todo (Alex): should we really fail?
			//return fmt.Errorf("no checkpoints achieved quorum in epoch: %d", epoch)
			k.Logger(ctx).Info("No checkpoints achieved quorum in epoch", "epoch", epoch)
		}
	}

	// todo (Alex): find a way to prune only bitmaps that are not used anymore
	//if err := k.PruneOldBitmaps(ctx, epoch); err != nil {
	//	return fmt.Errorf("pruning old data at epoch %d: %w", epoch, err)
	//}

	if err := k.BuildValidatorIndexMap(ctx); err != nil {
		return fmt.Errorf("rebuilding validator index map at epoch %d: %w", epoch, err)
	}
	return nil
}

// ejectLowParticipants ejects validators with low participation
func (k Keeper) ejectLowParticipants(ctx sdk.Context, epochBitmap []byte) {
	// TODO: Implement validator ejection logic
	k.Logger(ctx).Info("Low participation detected, ejection logic not yet implemented")
}

// emitCheckpointHashes emits checkpoint hashes
func (k Keeper) emitCheckpointHashes(ctx sdk.Context, height int64, validatorHash, commitHash []byte, softConfirmed bool) {
	var softConfirmedSt string

	if softConfirmed {
		softConfirmedSt = "true"
	} else {
		softConfirmedSt = "false"
	}
	ctx.EventManager().EmitEvent(
		sdk.NewEvent(
			"checkpoint",
			sdk.NewAttribute("height", math.NewInt(height).String()),
			sdk.NewAttribute("validator_hash", base64.StdEncoding.EncodeToString(validatorHash)),
			sdk.NewAttribute("commit_hash", base64.StdEncoding.EncodeToString(commitHash)),
			sdk.NewAttribute("soft_confirmed", softConfirmedSt),
		),
	)
}
