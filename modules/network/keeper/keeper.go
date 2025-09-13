package keeper

import (
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/store"
	"cosmossdk.io/log"
	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/evstack/ev-abci/modules/network/types"
)

// Keeper of the network store
type Keeper struct {
	cdc           codec.BinaryCodec
	stakingKeeper types.StakingKeeper
	accountKeeper types.AccountKeeper
	bankKeeper    types.BankKeeper
	authority     string
	bitmapHelper  *BitmapHelper

	// Collections for state management
	ValidatorIndex        collections.Map[string, uint16]
	ValidatorPower        collections.Map[uint16, uint64]
	AttestationBitmap     collections.Map[int64, []byte]
	EpochBitmap           collections.Map[uint64, []byte]
	AttesterSet           collections.KeySet[string]
	AttesterInfo          collections.Map[string, types.AttesterInfo]
	Signatures            collections.Map[collections.Pair[int64, string], []byte]
	StoredAttestationInfo collections.Map[int64, types.AttestationBitmap]
	LastAttestedHeight    collections.Item[int64]
	Params                collections.Item[types.Params]
	Schema                collections.Schema
}

// NewKeeper creates a new network Keeper instance
func NewKeeper(
	cdc codec.BinaryCodec,
	storeService store.KVStoreService, // Changed from sdk.StoreKey
	sk types.StakingKeeper,
	ak types.AccountKeeper,
	bk types.BankKeeper,
	authority string,
) Keeper {

	sb := collections.NewSchemaBuilder(storeService)
	keeper := Keeper{
		cdc:           cdc,
		stakingKeeper: sk,
		accountKeeper: ak,
		bankKeeper:    bk,
		authority:     authority,
		bitmapHelper:  NewBitmapHelper(),

		ValidatorIndex:        collections.NewMap(sb, types.ValidatorIndexPrefix, "validator_index", collections.StringKey, collections.Uint16Value),
		ValidatorPower:        collections.NewMap(sb, types.ValidatorPowerPrefix, "validator_power", collections.Uint16Key, collections.Uint64Value),
		AttestationBitmap:     collections.NewMap(sb, types.AttestationBitmapPrefix, "attestation_bitmap", collections.Int64Key, collections.BytesValue),
		EpochBitmap:           collections.NewMap(sb, types.EpochBitmapPrefix, "epoch_bitmap", collections.Uint64Key, collections.BytesValue),
		AttesterSet:           collections.NewKeySet(sb, types.AttesterSetPrefix, "attester_set", collections.StringKey),
		AttesterInfo:          collections.NewMap(sb, types.AttesterInfoPrefix, "attester_info", collections.StringKey, codec.CollValue[types.AttesterInfo](cdc)),
		Signatures:            collections.NewMap(sb, types.SignaturePrefix, "signatures", collections.PairKeyCodec(collections.Int64Key, collections.StringKey), collections.BytesValue),
		StoredAttestationInfo: collections.NewMap(sb, types.StoredAttestationInfoPrefix, "stored_attestation_info", collections.Int64Key, codec.CollValue[types.AttestationBitmap](cdc)), // Initialize new collection
		LastAttestedHeight:    collections.NewItem(sb, types.LastAttestedHeightKey, "last_attested_height", collections.Int64Value),
		Params:                collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
	}

	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	keeper.Schema = schema
	return keeper
}

// GetAuthority returns the module authority
func (k Keeper) GetAuthority() string {
	return k.authority
}

// Logger returns a module-specific logger
func (k Keeper) Logger(ctx sdk.Context) log.Logger {
	return ctx.Logger().With("module", "network")
}

// GetParams get all parameters as types.Params
func (k Keeper) GetParams(ctx sdk.Context) types.Params {
	params, err := k.Params.Get(ctx)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		panic(err)
	}
	return params
}

// SetParams set the params
func (k Keeper) SetParams(ctx sdk.Context, params types.Params) error {
	return k.Params.Set(ctx, params)
}

// SetValidatorIndex stores the validator index mapping and power
func (k Keeper) SetValidatorIndex(ctx sdk.Context, addr string, index uint16, power uint64) error {
	if err := k.ValidatorIndex.Set(ctx, addr, index); err != nil {
		return err
	}
	return k.ValidatorPower.Set(ctx, index, power)
}

// GetValidatorIndex retrieves the validator index
func (k Keeper) GetValidatorIndex(ctx sdk.Context, addr string) (uint16, bool) {
	index, err := k.ValidatorIndex.Get(ctx, addr)
	if err != nil {
		// For 'not found', collections.ErrNotFound can be checked specifically if needed.
		return 0, false
	}
	return index, true
}

// GetValidatorPower retrieves the validator power by index
func (k Keeper) GetValidatorPower(ctx sdk.Context, index uint16) (uint64, error) {
	power, err := k.ValidatorPower.Get(ctx, index)
	return power, err
}

// SetAttestationBitmap stores the attestation bitmap for a height
func (k Keeper) SetAttestationBitmap(ctx sdk.Context, height int64, bitmap []byte) error {
	return k.AttestationBitmap.Set(ctx, height, bitmap)
}

// GetAttestationBitmap retrieves the attestation bitmap for a height
func (k Keeper) GetAttestationBitmap(ctx sdk.Context, height int64) ([]byte, error) {
	bitmap, err := k.AttestationBitmap.Get(ctx, height)
	return bitmap, err
}

// SetEpochBitmap stores the epoch participation bitmap
func (k Keeper) SetEpochBitmap(ctx sdk.Context, epoch uint64, bitmap []byte) error {
	return k.EpochBitmap.Set(ctx, epoch, bitmap)
}

// GetEpochBitmap retrieves the epoch participation bitmap
func (k Keeper) GetEpochBitmap(ctx sdk.Context, epoch uint64) []byte {
	bitmap, err := k.EpochBitmap.Get(ctx, epoch)
	if err != nil {
		// Consider logging err or returning (nil, error)
		return nil
	}
	return bitmap
}

// IsInAttesterSet checks if a validator is in the attester set
func (k Keeper) IsInAttesterSet(ctx sdk.Context, addr string) (bool, error) {
	has, err := k.AttesterSet.Has(ctx, addr)
	return has, err
}

// SetAttesterSetMember adds a validator to the attester set
func (k Keeper) SetAttesterSetMember(ctx sdk.Context, addr string) error {
	return k.AttesterSet.Set(ctx, addr)
}

// RemoveAttesterSetMember removes a validator from the attester set
func (k Keeper) RemoveAttesterSetMember(ctx sdk.Context, addr string) error {
	return k.AttesterSet.Remove(ctx, addr)
}

// GetAllAttesters returns all validators in the attester set
func (k Keeper) GetAllAttesters(ctx sdk.Context) ([]string, error) {
	var attesters []string
	iterator, err := k.AttesterSet.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = iterator.Close() }()

	for ; iterator.Valid(); iterator.Next() {
		addr, err := iterator.Key()
		if err != nil {
			return nil, err
		}
		attesters = append(attesters, addr)
	}
	return attesters, nil
}

// BuildValidatorIndexMap rebuilds the validator index mapping
func (k Keeper) BuildValidatorIndexMap(ctx sdk.Context) error {
	// Get all attesters instead of bonded validators
	attesters, err := k.GetAllAttesters(ctx)
	if err != nil {
		return err
	}

	// Clear existing indices and powers
	// The `nil` range clears all entries in the collection.
	if err := k.ValidatorIndex.Clear(ctx, nil); err != nil {
		k.Logger(ctx).Error("failed to clear validator index", "error", err)
		return err
	}
	if err := k.ValidatorPower.Clear(ctx, nil); err != nil {
		k.Logger(ctx).Error("failed to clear validator power", "error", err)
		return err
	}

	// Build new indices for all attesters with voting power of 1
	index := uint16(0)
	for _, attesterAddr := range attesters {
		power := uint64(1) // Assign voting power of 1 to all attesters
		if err := k.SetValidatorIndex(ctx, attesterAddr, index, power); err != nil {
			// Consider how to handle partial failures; potentially log and continue or return error.
			k.Logger(ctx).Error("failed to set validator index during build", "attester", attesterAddr, "error", err)
			return err
		}
		k.Logger(ctx).Debug("assigned index to attester", "attester", attesterAddr, "index", index, "power", power)
		index++
	}
	k.Logger(ctx).Info("rebuilt validator index map for attesters", "count", len(attesters))
	return nil
}

// GetCurrentEpoch returns the current epoch number
func (k Keeper) GetCurrentEpoch(ctx sdk.Context) uint64 {
	params := k.GetParams(ctx)
	height := uint64(ctx.BlockHeight())
	return height / params.EpochLength
}

// IsCheckpointHeight checks if a height is a checkpoint
func (k Keeper) IsCheckpointHeight(ctx sdk.Context, height int64) bool {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return false
	}
	params := p
	return uint64(height)%params.EpochLength == 0
}

// CalculateVotedPower calculates the total voted power from a bitmap
func (k Keeper) CalculateVotedPower(ctx sdk.Context, bitmap []byte) (uint64, error) {
	var votedPower uint64
	for i := 0; i < len(bitmap)*8; i++ {
		if k.bitmapHelper.IsSet(bitmap, i) {
			power, err := k.GetValidatorPower(ctx, uint16(i))
			if err != nil {
				return 0, fmt.Errorf("get validator power: %w", err)
			}

			votedPower += power
		}
	}
	return votedPower, nil
}

// GetTotalPower returns the total attester power (all attesters have power 1)
func (k Keeper) GetTotalPower(ctx sdk.Context) (uint64, error) {
	attesters, err := k.GetAllAttesters(ctx)
	if err != nil {
		return 0, err
	}
	// Each attester has power 1, so total power equals number of attesters
	return uint64(len(attesters)), nil
}

// CheckQuorum checks if the voted power meets quorum
func (k Keeper) CheckQuorum(ctx sdk.Context, votedPower, totalPower uint64) (bool, error) {
	params := k.GetParams(ctx)
	quorumFrac, err := math.LegacyNewDecFromStr(params.QuorumFraction)
	if err != nil {
		return false, fmt.Errorf("invalid quorum fraction: %w", err)
	}

	requiredPower := math.LegacyNewDec(int64(totalPower)).Mul(quorumFrac).TruncateInt().Uint64()
	return votedPower >= requiredPower, nil
}

// IsSoftConfirmed checks if a block at a given height is soft-confirmed
// based on the attestation bitmap and quorum rules.
func (k Keeper) IsSoftConfirmed(ctx sdk.Context, height int64) (bool, error) {
	bitmap, err := k.GetAttestationBitmap(ctx, height)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return false, fmt.Errorf("get attestation bitmap: %w", err)
	}
	if bitmap == nil {
		return false, nil // No bitmap, so cannot be soft-confirmed
	}
	votedPower, err := k.CalculateVotedPower(ctx, bitmap)
	if err != nil {
		return false, err
	}
	totalPower, err := k.GetTotalPower(ctx) // Assuming this gets the relevant total power for the height
	if err != nil {
		return false, err
	}

	return k.CheckQuorum(ctx, votedPower, totalPower)
}

// PruneOldBitmaps removes bitmaps older than PruneAfter epochs
func (k Keeper) PruneOldBitmaps(ctx sdk.Context, currentEpoch uint64) error {
	params := k.GetParams(ctx)
	if params.PruneAfter == 0 { // Avoid pruning if PruneAfter is zero or not set
		return nil
	}
	if currentEpoch <= params.PruneAfter {
		return nil
	}

	pruneBeforeEpoch := currentEpoch - params.PruneAfter
	pruneHeight := int64(pruneBeforeEpoch * params.EpochLength) // Assuming EpochLength defines blocks per epoch

	// Prune attestation bitmaps (raw bitmaps)
	attestationRange := new(collections.Range[int64]).StartInclusive(0).EndExclusive(pruneHeight)
	if err := k.AttestationBitmap.Clear(ctx, attestationRange); err != nil {
		return fmt.Errorf("clearing attestation bitmaps before height %d: %w", pruneHeight, err)
	}
	// Prune stored attestation info (full AttestationBitmap objects)
	storedAttestationInfoRange := new(collections.Range[int64]).StartInclusive(0).EndExclusive(pruneHeight)
	if err := k.StoredAttestationInfo.Clear(ctx, storedAttestationInfoRange); err != nil {
		return fmt.Errorf("clearing stored attestation info before height %d: %w", pruneHeight, err)
	}

	// Prune epoch bitmaps
	epochRange := new(collections.Range[uint64]).StartInclusive(0).EndExclusive(pruneBeforeEpoch)
	if err := k.EpochBitmap.Clear(ctx, epochRange); err != nil {
		return fmt.Errorf("clearing epoch bitmaps before epoch %d: %w", pruneBeforeEpoch, err)
	}

	// TODO: Consider pruning signatures associated with pruned heights.
	// This would involve iterating k.Signatures and removing entries where height < pruneHeight.

	k.Logger(ctx).Info("Pruned old bitmaps and attestation info", "prunedBeforeEpoch", pruneBeforeEpoch, "prunedBeforeHeight", pruneHeight)
	return nil
}

// SetSignature stores the vote signature for a given height and validator
func (k Keeper) SetSignature(ctx sdk.Context, height int64, validatorAddr string, signature []byte) error {
	return k.Signatures.Set(ctx, collections.Join(height, validatorAddr), signature)
}

// GetSignature retrieves the vote signature for a given height and validator
func (k Keeper) GetSignature(ctx sdk.Context, height int64, validatorAddr string) ([]byte, error) {
	return k.Signatures.Get(ctx, collections.Join(height, validatorAddr))
}

// HasSignature checks if a signature exists for a given height and validator
func (k Keeper) HasSignature(ctx sdk.Context, height int64, validatorAddr string) (bool, error) {
	return k.Signatures.Has(ctx, collections.Join(height, validatorAddr))
}

// GetAllSignaturesForHeight returns all attester signatures for a specific height
func (k Keeper) GetAllSignaturesForHeight(ctx sdk.Context, height int64) (map[string][]byte, error) {
	signatures := make(map[string][]byte)

	// Get the attestation bitmap to see which validators attested
	bitmap, err := k.GetAttestationBitmap(ctx, height)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return nil, fmt.Errorf("get attestation bitmap: %w", err)
	}
	if bitmap == nil {
		return signatures, nil // No attestations for this height
	}

	// Get all attesters to map indices to addresses
	attesters, err := k.GetAllAttesters(ctx)
	if err != nil {
		return nil, fmt.Errorf("get all attesters: %w", err)
	}

	// Check each attester to see if they signed
	for i, attesterAddr := range attesters {
		if i >= len(bitmap)*8 {
			break // Don't go beyond bitmap size
		}

		// Check if this attester signed (bit is set in bitmap)
		if k.bitmapHelper.IsSet(bitmap, i) {
			signature, err := k.GetSignature(ctx, height, attesterAddr)
			if err != nil && !errors.Is(err, collections.ErrNotFound) {
				k.Logger(ctx).Error("failed to get signature for attester",
					"height", height, "attester", attesterAddr, "error", err)
				continue
			}
			if signature != nil {
				signatures[attesterAddr] = signature
			}
		}
	}

	return signatures, nil
}

// GetLastAttestedHeight returns the last attested height
func (k Keeper) GetLastAttestedHeight(ctx sdk.Context) (int64, error) {
	height, err := k.LastAttestedHeight.Get(ctx)
	if err != nil && errors.Is(err, collections.ErrNotFound) {
		return 0, nil // Return 0 if not set yet
	}
	return height, err
}

// SetLastAttestedHeight sets the last attested height
func (k Keeper) SetLastAttestedHeight(ctx sdk.Context, height int64) error {
	return k.LastAttestedHeight.Set(ctx, height)
}

// UpdateLastAttestedHeight updates the last attested height if the new height is greater
func (k Keeper) UpdateLastAttestedHeight(ctx sdk.Context, height int64) error {
	currentHeight, err := k.GetLastAttestedHeight(ctx)
	if err != nil {
		return err
	}

	if height > currentHeight {
		return k.SetLastAttestedHeight(ctx, height)
	}

	return nil
}

// SetAttesterInfo stores the attester information including pubkey
func (k Keeper) SetAttesterInfo(ctx sdk.Context, addr string, info *types.AttesterInfo) error {
	return k.AttesterInfo.Set(ctx, addr, *info)
}

// GetAttesterInfo retrieves the attester information including pubkey
func (k Keeper) GetAttesterInfo(ctx sdk.Context, addr string) (*types.AttesterInfo, error) {
	info, err := k.AttesterInfo.Get(ctx, addr)
	if err != nil {
		return nil, err
	}
	return &info, nil
}
