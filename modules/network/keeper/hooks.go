package keeper

import (
	"context"
	"encoding/hex"

	sdk "github.com/cosmos/cosmos-sdk/types"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"

	symstakingtypes "github.com/evstack/ev-abci/modules/symstaking/types"
)

// SymStakingHooksImpl implements SymStakingHooks for the network module.
// It updates the attester set when validators are added/removed via the relay.
type SymStakingHooksImpl struct {
	keeper Keeper
}

// NewSymStakingHooks creates a new SymStakingHooks implementation.
func NewSymStakingHooks(k Keeper) symstakingtypes.SymStakingHooks {
	return &SymStakingHooksImpl{keeper: k}
}

// AfterValidatorCreated is called when a new validator is added from the relay.
func (h *SymStakingHooksImpl) AfterValidatorCreated(ctx context.Context, consPubKey cryptotypes.PubKey) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	// Get the hex-encoded pubkey as the attester address
	pubKeyHex := hex.EncodeToString(consPubKey.Bytes())

	// Check if we're at the maximum attester limit
	attesters, err := h.keeper.GetAllAttesters(sdkCtx)
	if err != nil {
		h.keeper.Logger(sdkCtx).Error("failed to get attesters", "error", err)
		return err
	}
	if len(attesters) >= MaxAttesters {
		h.keeper.Logger(sdkCtx).Warn("attester set at maximum capacity, cannot add validator",
			"pubkey", pubKeyHex, "max", MaxAttesters)
		return nil // Don't fail, just log
	}

	// Add to attester set
	if err := h.keeper.SetAttesterSetMember(sdkCtx, pubKeyHex); err != nil {
		h.keeper.Logger(sdkCtx).Error("failed to add attester", "pubkey", pubKeyHex, "error", err)
		return err
	}

	// Rebuild validator index map to include the new attester
	if err := h.keeper.BuildValidatorIndexMap(sdkCtx); err != nil {
		h.keeper.Logger(sdkCtx).Error("failed to rebuild validator index map", "error", err)
		return err
	}

	h.keeper.Logger(sdkCtx).Info("added validator to attester set via relay",
		"pubkey", pubKeyHex,
		"total_attesters", len(attesters)+1,
	)

	return nil
}

// AfterValidatorModified is called when a validator's power changes from the relay.
func (h *SymStakingHooksImpl) AfterValidatorModified(ctx context.Context, consPubKey cryptotypes.PubKey) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	// Get the hex-encoded pubkey
	pubKeyHex := hex.EncodeToString(consPubKey.Bytes())

	// For now, we just log the modification. Power changes are handled
	// directly by the symstaking module returning ValidatorUpdates.
	h.keeper.Logger(sdkCtx).Info("validator power modified via relay", "pubkey", pubKeyHex)

	return nil
}

// AfterValidatorRemoved is called when a validator is removed from the relay.
func (h *SymStakingHooksImpl) AfterValidatorRemoved(ctx context.Context, consPubKey cryptotypes.PubKey) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	// Get the hex-encoded pubkey
	pubKeyHex := hex.EncodeToString(consPubKey.Bytes())

	// Remove from attester set
	if err := h.keeper.RemoveAttesterSetMember(sdkCtx, pubKeyHex); err != nil {
		h.keeper.Logger(sdkCtx).Error("failed to remove attester", "pubkey", pubKeyHex, "error", err)
		return err
	}

	// Rebuild validator index map without the removed attester
	if err := h.keeper.BuildValidatorIndexMap(sdkCtx); err != nil {
		h.keeper.Logger(sdkCtx).Error("failed to rebuild validator index map", "error", err)
		return err
	}

	attesters, _ := h.keeper.GetAllAttesters(sdkCtx)
	h.keeper.Logger(sdkCtx).Info("removed validator from attester set via relay",
		"pubkey", pubKeyHex,
		"remaining_attesters", len(attesters),
	)

	return nil
}
