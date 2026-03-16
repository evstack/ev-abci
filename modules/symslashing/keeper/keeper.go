package keeper

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/store"
	"cosmossdk.io/log"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/evstack/ev-abci/modules/symslashing/types"
	symstakingtypes "github.com/evstack/ev-abci/modules/symstaking/types"
)

// Keeper of the symslashing store
type Keeper struct {
	cdc              codec.BinaryCodec
	storeService     store.KVStoreService
	symStakingKeeper types.SymStakingKeeper
	authority        string

	// Collections for state management
	InfractionRecords collections.Map[string, []byte] // pubkey hex -> JSON record
	Schema            collections.Schema
}

// NewKeeper creates a new symslashing Keeper instance
func NewKeeper(
	cdc codec.BinaryCodec,
	storeService store.KVStoreService,
	symStakingKeeper types.SymStakingKeeper,
	authority string,
) Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	keeper := Keeper{
		cdc:              cdc,
		storeService:     storeService,
		symStakingKeeper: symStakingKeeper,
		authority:        authority,

		InfractionRecords: collections.NewMap(sb, types.InfractionRecordsPrefix, "infraction_records", collections.StringKey, collections.BytesValue),
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
	return ctx.Logger().With("module", "x/symslashing")
}

// GetParams get all parameters as types.Params
func (k Keeper) GetParams(ctx sdk.Context) types.Params {
	store := k.storeService.OpenKVStore(ctx)
	bz, err := store.Get(types.ParamsKey)
	if err != nil || bz == nil {
		return types.DefaultParams()
	}
	var params types.Params
	if err := json.Unmarshal(bz, &params); err != nil {
		return types.DefaultParams()
	}
	return params
}

// SetParams set the params
func (k Keeper) SetParams(ctx sdk.Context, params types.Params) error {
	store := k.storeService.OpenKVStore(ctx)
	bz, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return store.Set(types.ParamsKey, bz)
}

// SlashValidator slashes a validator via the relay
func (k Keeper) SlashValidator(
	ctx context.Context,
	validatorPubKey []byte,
	infractionType symstakingtypes.Infraction,
	height int64,
) (string, error) {
	if len(validatorPubKey) == 0 {
		return "", types.ErrEmptyValidatorPubKey
	}

	// Validate infraction type
	if infractionType != symstakingtypes.Infraction_INFRACTION_DOUBLE_SIGN &&
		infractionType != symstakingtypes.Infraction_INFRACTION_DOWNTIME {
		return "", types.ErrInvalidInfraction
	}

	// Request slash via relay
	requestID, err := k.symStakingKeeper.SlashWithInfractionReason(ctx, validatorPubKey, infractionType, height)
	if err != nil {
		return "", fmt.Errorf("%w: %w", types.ErrSignFailed, err)
	}

	// Record the infraction
	pubKeyHex := hex.EncodeToString(validatorPubKey)
	record := types.InfractionRecord{
		ValidatorPubKey: validatorPubKey,
		InfractionType:  infractionType,
		Height:          height,
		RequestID:       requestID,
	}
	recordBz, err := json.Marshal(record)
	if err != nil {
		k.Logger(sdk.UnwrapSDKContext(ctx)).Error("failed to marshal infraction record", "error", err)
	} else {
		sdkCtx := sdk.UnwrapSDKContext(ctx)
		if err := k.InfractionRecords.Set(sdkCtx, pubKeyHex, recordBz); err != nil {
			k.Logger(sdkCtx).Error("failed to store infraction record", "error", err)
		}
	}

	k.Logger(sdk.UnwrapSDKContext(ctx)).Info(
		"validator slashed via relay",
		"validator", pubKeyHex,
		"infraction", infractionType,
		"height", height,
		"request_id", requestID,
	)

	return requestID, nil
}

// GetInfractionRecord retrieves an infraction record by validator pubkey
func (k Keeper) GetInfractionRecord(ctx sdk.Context, pubkeyHex string) (types.InfractionRecord, bool) {
	recordBz, err := k.InfractionRecords.Get(ctx, pubkeyHex)
	if err != nil {
		return types.InfractionRecord{}, false
	}
	var record types.InfractionRecord
	if err := json.Unmarshal(recordBz, &record); err != nil {
		return types.InfractionRecord{}, false
	}
	return record, true
}

// DeleteInfractionRecord removes an infraction record
func (k Keeper) DeleteInfractionRecord(ctx sdk.Context, pubkeyHex string) error {
	return k.InfractionRecords.Remove(ctx, pubkeyHex)
}
