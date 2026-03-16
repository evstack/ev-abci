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

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/evstack/ev-abci/modules/symstaking/relay"
	"github.com/evstack/ev-abci/modules/symstaking/types"
)

// Keeper of the symstaking store
type Keeper struct {
	cdc         codec.BinaryCodec
	storeService store.KVStoreService
	relayClient types.RelayClient
	authority   string
	hooks       types.SymStakingHooks

	// Collections for state management
	CurrentEpoch     collections.Item[uint64]
	LastValidatorSet collections.Map[string, int64] // pubkey hex -> power
	Schema           collections.Schema
}

// NewKeeper creates a new symstaking Keeper instance
func NewKeeper(
	cdc codec.BinaryCodec,
	storeService store.KVStoreService,
	relayCfg relay.Config,
	authority string,
) (Keeper, error) {
	// Create relay client
	client, err := relay.NewClient(relayCfg)
	if err != nil {
		return Keeper{}, fmt.Errorf("failed to create relay client: %w", err)
	}

	sb := collections.NewSchemaBuilder(storeService)
	keeper := Keeper{
		cdc:          cdc,
		storeService: storeService,
		relayClient:  client,
		authority:    authority,

		CurrentEpoch:     collections.NewItem(sb, types.CurrentEpochKey, "current_epoch", collections.Uint64Value),
		LastValidatorSet: collections.NewMap(sb, types.LastValidatorSetPrefix, "last_validator_set", collections.StringKey, collections.Int64Value),
	}

	schema, err := sb.Build()
	if err != nil {
		return Keeper{}, fmt.Errorf("failed to build schema: %w", err)
	}
	keeper.Schema = schema
	return keeper, nil
}

// SetHooks sets the module hooks
func (k *Keeper) SetHooks(hooks types.SymStakingHooks) {
	k.hooks = hooks
}

// GetAuthority returns the module authority
func (k Keeper) GetAuthority() string {
	return k.authority
}

// Logger returns a module-specific logger
func (k Keeper) Logger(ctx sdk.Context) log.Logger {
	return ctx.Logger().With("module", "x/symstaking")
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

// GetCurrentEpoch returns the current epoch
func (k Keeper) GetCurrentEpoch(ctx sdk.Context) uint64 {
	epoch, err := k.CurrentEpoch.Get(ctx)
	if err != nil {
		return 0
	}
	return epoch
}

// SetCurrentEpoch sets the current epoch
func (k Keeper) SetCurrentEpoch(ctx sdk.Context, epoch uint64) error {
	return k.CurrentEpoch.Set(ctx, epoch)
}

// GetValidatorSetFromRelay fetches the validator set from the relay
func (k Keeper) GetValidatorSetFromRelay(ctx context.Context, epoch uint64) ([]abci.ValidatorUpdate, error) {
	return k.relayClient.GetValidatorSet(ctx, epoch)
}

// GetCurrentEpochFromRelay fetches the current epoch from the relay
func (k Keeper) GetCurrentEpochFromRelay(ctx context.Context) (uint64, error) {
	return k.relayClient.GetCurrentEpoch(ctx)
}

// SignMessageViaRelay signs a message via the relay
func (k Keeper) SignMessageViaRelay(ctx context.Context, keyTag uint32, message []byte) (string, error) {
	return k.relayClient.SignMessage(ctx, keyTag, message)
}

// GetLastValidatorPower returns the last known power for a validator
func (k Keeper) GetLastValidatorPower(ctx sdk.Context, pubkeyHex string) (int64, bool) {
	power, err := k.LastValidatorSet.Get(ctx, pubkeyHex)
	if err != nil {
		return 0, false
	}
	return power, true
}

// SetLastValidatorPower sets the power for a validator
func (k Keeper) SetLastValidatorPower(ctx sdk.Context, pubkeyHex string, power int64) error {
	return k.LastValidatorSet.Set(ctx, pubkeyHex, power)
}

// DeleteLastValidatorPower removes a validator from the last set
func (k Keeper) DeleteLastValidatorPower(ctx sdk.Context, pubkeyHex string) error {
	return k.LastValidatorSet.Remove(ctx, pubkeyHex)
}

// IterateLastValidatorSet iterates over the last validator set
func (k Keeper) IterateLastValidatorSet(ctx sdk.Context, cb func(pubkeyHex string, power int64) bool) {
	iter, err := k.LastValidatorSet.Iterate(ctx, nil)
	if err != nil {
		return
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		kv, err := iter.KeyValue()
		if err != nil {
			return
		}
		if cb(kv.Key, kv.Value) {
			return
		}
	}
}

// Close closes the relay client connection
func (k Keeper) Close() error {
	if k.relayClient != nil {
		return k.relayClient.Close()
	}
	return nil
}

// SlashWithInfractionReason requests the relay to sign a slash message
func (k Keeper) SlashWithInfractionReason(
	ctx context.Context,
	validatorPubKey []byte,
	infractionType types.Infraction,
	height int64,
) (string, error) {
	params := k.GetParams(sdk.UnwrapSDKContext(ctx))

	// Create slash message payload
	// Format: infraction_type (1 byte) | height (8 bytes) | pubkey (32 bytes)
	message := make([]byte, 0, 41)
	message = append(message, byte(infractionType))
	message = append(message, encodeHeight(height)...)
	message = append(message, validatorPubKey...)

	requestID, err := k.relayClient.SignMessage(ctx, params.SigningKeyTag, message)
	if err != nil {
		return "", fmt.Errorf("failed to sign slash message: %w", err)
	}

	k.Logger(sdk.UnwrapSDKContext(ctx)).Info(
		"slash message signed",
		"request_id", requestID,
		"validator", hex.EncodeToString(validatorPubKey),
		"infraction", infractionType,
	)

	return requestID, nil
}

func encodeHeight(height int64) []byte {
	bz := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		bz[i] = byte(height & 0xff)
		height >>= 8
	}
	return bz
}
