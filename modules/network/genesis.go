package network

import (
	"bytes"
	"fmt"
	"sort"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/evstack/ev-abci/modules/network/keeper"
	"github.com/evstack/ev-abci/modules/network/types"
)

// InitGenesis initializes the network module's state from a provided genesis state.
func InitGenesis(ctx sdk.Context, k keeper.Keeper, genState types.GenesisState) error {
	if err := k.SetParams(ctx, genState.Params); err != nil {
		return fmt.Errorf("set params: %s", err)
	}

	// Load attesters: validate pubkey/address match, then insert and assign indices.
	attesters := make([]types.AttesterInfo, len(genState.AttesterInfos))
	copy(attesters, genState.AttesterInfos)

	for i := range attesters {
		info := attesters[i]
		pk, err := info.GetPubKey()
		if err != nil {
			return fmt.Errorf("attester %d: %w", i, err)
		}
		derived := sdk.ConsAddress(pk.Address()).String()
		if derived != info.ConsensusAddress {
			return fmt.Errorf("attester %d: pubkey address mismatch (derived %s, stated %s)",
				i, derived, info.ConsensusAddress)
		}
	}

	// Order by pubkey.Address() bytes ascending to match cmttypes.NewValidatorSet.
	sort.Slice(attesters, func(i, j int) bool {
		pki, _ := attesters[i].GetPubKey()
		pkj, _ := attesters[j].GetPubKey()
		return bytes.Compare(pki.Address(), pkj.Address()) < 0
	})

	for idx, info := range attesters {
		if err := k.SetAttesterInfo(ctx, info.ConsensusAddress, &info); err != nil {
			return fmt.Errorf("set attester info: %w", err)
		}
		if err := k.SetAttesterSetMember(ctx, info.ConsensusAddress); err != nil {
			return fmt.Errorf("set attester set member: %w", err)
		}
		if err := k.SetValidatorIndex(ctx, info.ConsensusAddress, uint16(idx), 1); err != nil {
			return fmt.Errorf("set validator index: %w", err)
		}
	}

	// Still load historical bitmaps if provided (upgrade/dump scenarios).
	for _, ab := range genState.AttestationBitmaps {
		if err := k.SetAttestationBitmap(ctx, ab.Height, ab.Bitmap); err != nil {
			return err
		}
		if err := k.StoredAttestationInfo.Set(ctx, ab.Height, ab); err != nil {
			return err
		}
		if ab.SoftConfirmed {
			if err := setSoftConfirmed(ctx, k, ab.Height); err != nil {
				return err
			}
		}
	}

	// Legacy: genState.ValidatorIndices is now derived from AttesterInfos and
	// ignored. Warn if non-empty so operators notice.
	if len(genState.ValidatorIndices) > 0 {
		k.Logger(ctx).Error("genesis.validator_indices is deprecated and ignored; use attester_infos")
	}

	return nil
}

// ExportGenesis returns the network module's exported genesis.
func ExportGenesis(ctx sdk.Context, k keeper.Keeper) *types.GenesisState {
	genesis := types.DefaultGenesisState()
	genesis.Params = k.GetParams(ctx)

	var attesters []types.AttesterInfo
	if err := k.AttesterInfo.Walk(ctx, nil, func(_ string, info types.AttesterInfo) (bool, error) {
		attesters = append(attesters, info)
		return false, nil
	}); err != nil {
		panic(err)
	}
	sort.Slice(attesters, func(i, j int) bool {
		pki, _ := attesters[i].GetPubKey()
		pkj, _ := attesters[j].GetPubKey()
		return bytes.Compare(pki.Address(), pkj.Address()) < 0
	})
	genesis.AttesterInfos = attesters

	var attestationBitmaps []types.AttestationBitmap
	if err := k.StoredAttestationInfo.Walk(ctx, nil, func(_ int64, ab types.AttestationBitmap) (bool, error) {
		attestationBitmaps = append(attestationBitmaps, ab)
		return false, nil
	}); err != nil {
		panic(err)
	}
	genesis.AttestationBitmaps = attestationBitmaps

	// ValidatorIndices no longer exported: they are derived deterministically
	// from AttesterInfos order.
	genesis.ValidatorIndices = nil

	return genesis
}

func setSoftConfirmed(ctx sdk.Context, k keeper.Keeper, height int64) error {
	ab, err := k.StoredAttestationInfo.Get(ctx, height)
	if err != nil {
		return err
	}
	ab.SoftConfirmed = true
	return k.StoredAttestationInfo.Set(ctx, height, ab)
}
