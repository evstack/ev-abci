package keeper

import (
	"encoding/hex"

	abci "github.com/cometbft/cometbft/abci/types"
	cryptoProto "github.com/cometbft/cometbft/proto/tendermint/crypto"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
)

// EndBlock updates the validator set from the relay at the end of each block.
// It compares the current validator set with the relay's and returns updates.
func (k Keeper) EndBlock(ctx sdk.Context) ([]abci.ValidatorUpdate, error) {
	params := k.GetParams(ctx)

	// Only check for epoch updates at specified intervals
	if ctx.BlockHeight()%params.EpochCheckInterval != 0 {
		return nil, nil
	}

	// Get current epoch from relay
	currentEpoch, err := k.GetCurrentEpochFromRelay(ctx)
	if err != nil {
		k.Logger(ctx).Error("failed to get current epoch from relay", "error", err)
		return nil, err
	}

	// Check if epoch has changed
	lastEpoch := k.GetCurrentEpoch(ctx)
	if currentEpoch == lastEpoch {
		// No epoch change, no validator updates needed
		return nil, nil
	}

	// Update stored epoch
	if err := k.SetCurrentEpoch(ctx, currentEpoch); err != nil {
		return nil, err
	}

	// Get validator set from relay
	validatorSet, err := k.GetValidatorSetFromRelay(ctx, currentEpoch)
	if err != nil {
		k.Logger(ctx).Error("failed to get validator set from relay", "error", err)
		return nil, err
	}

	// Build a map of new validators for easy lookup
	newValidators := make(map[string]int64)
	for _, val := range validatorSet {
		pubKeyHex := pubKeyToHex(val.PubKey)
		newValidators[pubKeyHex] = val.Power
	}

	// Track all updates to return
	var updates []abci.ValidatorUpdate

	// Check for removed or modified validators from last set
	k.IterateLastValidatorSet(ctx, func(pubkeyHex string, oldPower int64) bool {
		newPower, exists := newValidators[pubkeyHex]
		if !exists {
			// Validator was removed - power 0
			pubKey := hexToPubKey(pubkeyHex)
			updates = append(updates, abci.ValidatorUpdate{
				PubKey: pubKey,
				Power:  0,
			})
			// Call hook for removed validator
			if k.hooks != nil {
				edKey := &ed25519.PubKey{Key: hexToBytes(pubkeyHex)}
				if err := k.hooks.AfterValidatorRemoved(ctx, edKey); err != nil {
					k.Logger(ctx).Error("hook AfterValidatorRemoved failed", "error", err)
				}
			}
			// Remove from stored set
			_ = k.DeleteLastValidatorPower(ctx, pubkeyHex)
		} else if newPower != oldPower {
			// Power changed
			pubKey := hexToPubKey(pubkeyHex)
			updates = append(updates, abci.ValidatorUpdate{
				PubKey: pubKey,
				Power:  newPower,
			})
			// Call hook for modified validator
			if k.hooks != nil {
				edKey := &ed25519.PubKey{Key: hexToBytes(pubkeyHex)}
				if err := k.hooks.AfterValidatorModified(ctx, edKey); err != nil {
					k.Logger(ctx).Error("hook AfterValidatorModified failed", "error", err)
				}
			}
			// Update stored power
			_ = k.SetLastValidatorPower(ctx, pubkeyHex, newPower)
		}
		return false
	})

	// Check for new validators
	for _, val := range validatorSet {
		pubKeyHex := pubKeyToHex(val.PubKey)
		_, exists := k.GetLastValidatorPower(ctx, pubKeyHex)
		if !exists {
			// New validator
			updates = append(updates, val)
			// Call hook for new validator
			if k.hooks != nil {
				edKey := &ed25519.PubKey{Key: hexToBytes(pubKeyHex)}
				if err := k.hooks.AfterValidatorCreated(ctx, edKey); err != nil {
					k.Logger(ctx).Error("hook AfterValidatorCreated failed", "error", err)
				}
			}
			// Store new validator
			_ = k.SetLastValidatorPower(ctx, pubKeyHex, val.Power)
		}
	}

	k.Logger(ctx).Info("validator set updated from relay",
		"epoch", currentEpoch,
		"total_validators", len(validatorSet),
		"updates", len(updates),
	)

	return updates, nil
}

// pubKeyToHex converts a protobuf PublicKey to hex string
func pubKeyToHex(pk cryptoProto.PublicKey) string {
	switch sum := pk.Sum.(type) {
	case *cryptoProto.PublicKey_Ed25519:
		return hex.EncodeToString(sum.Ed25519)
	case *cryptoProto.PublicKey_Secp256K1:
		return hex.EncodeToString(sum.Secp256K1)
	default:
		return ""
	}
}

// hexToBytes converts hex string to bytes
func hexToBytes(s string) []byte {
	bz, _ := hex.DecodeString(s)
	return bz
}

// hexToPubKey converts hex string back to protobuf PublicKey
func hexToPubKey(s string) cryptoProto.PublicKey {
	bz := hexToBytes(s)
	if len(bz) == 32 {
		// Ed25519 key
		return cryptoProto.PublicKey{Sum: &cryptoProto.PublicKey_Ed25519{Ed25519: bz}}
	}
	// Assume Secp256K1 for other lengths
	return cryptoProto.PublicKey{Sum: &cryptoProto.PublicKey_Secp256K1{Secp256K1: bz}}
}
