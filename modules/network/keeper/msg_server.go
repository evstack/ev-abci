package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdkerr "cosmossdk.io/errors"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"

	"github.com/evstack/ev-abci/modules/network/types"
)

// MinVoteLen is the minimum vote payload length in bytes.
// 64 is the size of a Ed25519 signature
const MinVoteLen = 64

type msgServer struct {
	Keeper
}

// NewMsgServerImpl returns an implementation of the MsgServer interface
func NewMsgServerImpl(keeper Keeper) types.MsgServer {
	return &msgServer{Keeper: keeper}
}

var _ types.MsgServer = msgServer{}

// Attest handles MsgAttest
func (k msgServer) Attest(goCtx context.Context, msg *types.MsgAttest) (*types.MsgAttestResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	if k.GetParams(ctx).SignMode == types.SignMode_SIGN_MODE_CHECKPOINT &&
		!k.IsCheckpointHeight(ctx, msg.Height) {
		return nil, sdkerr.Wrapf(sdkerrors.ErrInvalidRequest, "height %d is not a checkpoint", msg.Height)
	}

	if len(msg.Vote) < MinVoteLen {
		return nil, sdkerr.Wrapf(sdkerrors.ErrInvalidRequest, "vote payload too short: got %d bytes, minimum %d", len(msg.Vote), MinVoteLen)
	}

	if err := k.assertValidValidatorAuthority(ctx, msg.ConsensusAddress, msg.Authority); err != nil {
		return nil, err
	}

	index, found := k.GetValidatorIndex(ctx, msg.ConsensusAddress)
	if !found {
		return nil, sdkerr.Wrapf(sdkerrors.ErrNotFound, "validator index not found for %s", msg.ConsensusAddress)
	}

	// todo (Alex): we need to set a limit to not have validators attest old blocks. Also make sure that this relates with
	// the retention period for pruning
	bitmap, err := k.GetAttestationBitmap(ctx, msg.Height)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return nil, sdkerr.Wrap(err, "get attestation bitmap")
	}
	if bitmap == nil {
		attesters, err := k.GetAllAttesters(ctx)
		if err != nil {
			return nil, err
		}
		numAttesters := len(attesters)
		bitmap = k.bitmapHelper.NewBitmap(numAttesters)
	}

	if k.bitmapHelper.IsSet(bitmap, int(index)) {
		return nil, sdkerr.Wrapf(sdkerrors.ErrInvalidRequest, "consensus address %s already attested for height %d", msg.ConsensusAddress, msg.Height)
	}

	// Set the bit
	k.bitmapHelper.SetBit(bitmap, int(index))
	if err := k.SetAttestationBitmap(ctx, msg.Height, bitmap); err != nil {
		return nil, sdkerr.Wrap(err, "set attestation bitmap")
	}

	// Store signature using the consensus address (this is the key fix for IBC)
	if err := k.SetSignature(ctx, msg.Height, msg.ConsensusAddress, msg.Vote); err != nil {
		return nil, sdkerr.Wrap(err, "store signature")
	}

	// Check if quorum is reached after this attestation
	votedPower, err := k.CalculateVotedPower(ctx, bitmap)
	if err != nil {
		return nil, sdkerr.Wrap(err, "calculate voted power")
	}

	totalPower, err := k.GetTotalPower(ctx)
	if err != nil {
		return nil, sdkerr.Wrap(err, "get total power")
	}

	quorumReached, err := k.CheckQuorum(ctx, votedPower, totalPower)
	if err != nil {
		return nil, sdkerr.Wrap(err, "check quorum")
	}

	// If quorum is reached, update the last attested height
	if quorumReached {
		if err := k.UpdateLastAttestedHeight(ctx, msg.Height); err != nil {
			return nil, sdkerr.Wrap(err, "update last attested height")
		}

		k.Logger(ctx).Info("block reached quorum and is now soft confirmed",
			"height", msg.Height,
			"voted_power", votedPower,
			"total_power", totalPower)
	}

	epoch := k.GetCurrentEpoch(ctx)
	epochBitmap := k.GetEpochBitmap(ctx, epoch)
	if epochBitmap == nil {
		attesters, err := k.GetAllAttesters(ctx)
		if err != nil {
			return nil, err
		}
		numAttesters := len(attesters)
		epochBitmap = k.bitmapHelper.NewBitmap(numAttesters)
	}
	k.bitmapHelper.SetBit(epochBitmap, int(index))
	if err := k.SetEpochBitmap(ctx, epoch, epochBitmap); err != nil {
		return nil, sdkerr.Wrap(err, "set epoch bitmap")
	}

	// Emit event
	ctx.EventManager().EmitEvent(
		sdk.NewEvent(
			types.TypeMsgAttest,
			sdk.NewAttribute("consensus_address", msg.ConsensusAddress),
			sdk.NewAttribute("authority", msg.Authority),
			sdk.NewAttribute("height", math.NewInt(msg.Height).String()),
		),
	)

	return &types.MsgAttestResponse{}, nil
}

// JoinAttesterSet handles MsgJoinAttesterSet
func (k msgServer) JoinAttesterSet(goCtx context.Context, msg *types.MsgJoinAttesterSet) (*types.MsgJoinAttesterSetResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	// Validate the consensus address format
	_, err := sdk.ValAddressFromBech32(msg.ConsensusAddress)
	if err != nil {
		return nil, sdkerr.Wrapf(sdkerrors.ErrInvalidAddress, "invalid consensus address: %s", err)
	}

	// NOTE: Removed bonded validator requirement to allow any address to join attester set
	// This allows external attesters that are not part of the validator set

	// Check if already in attester set (use consensus address)
	has, err := k.IsInAttesterSet(ctx, msg.ConsensusAddress)
	if err != nil {
		return nil, sdkerr.Wrapf(err, "in attester set")
	}
	if has {
		return nil, sdkerr.Wrapf(sdkerrors.ErrInvalidRequest, "consensus address already in attester set")
	}

	// Enforce maximum attester set size to prevent unbounded growth and uint16 index overflow
	attesters, err := k.GetAllAttesters(ctx)
	if err != nil {
		return nil, sdkerr.Wrap(err, "get all attesters")
	}
	if len(attesters) >= MaxAttesters {
		return nil, sdkerr.Wrapf(sdkerrors.ErrInvalidRequest, "attester set is full: %d/%d", len(attesters), MaxAttesters)
	}

	// Store the attester information including pubkey (key by consensus address)
	attesterInfo := &types.AttesterInfo{
		Authority:    msg.Authority,
		Pubkey:       msg.Pubkey,
		JoinedHeight: ctx.BlockHeight(),
	}

	if err := k.SetAttesterInfo(ctx, msg.ConsensusAddress, attesterInfo); err != nil {
		return nil, sdkerr.Wrap(err, "set attester info")
	}

	// TODO (Alex): the valset should be updated at the end of an epoch only
	if err := k.SetAttesterSetMember(ctx, msg.ConsensusAddress); err != nil {
		return nil, sdkerr.Wrap(err, "set attester set member")
	}

	ctx.EventManager().EmitEvent(
		sdk.NewEvent(
			types.TypeMsgJoinAttesterSet,
			sdk.NewAttribute("consensus_address", msg.ConsensusAddress),
			sdk.NewAttribute("authority", msg.Authority),
		),
	)
	k.Logger(ctx).Info("+++ joined attester set", "consensus_address", msg.ConsensusAddress, "authority", msg.Authority)
	return &types.MsgJoinAttesterSetResponse{}, nil
}

// LeaveAttesterSet handles MsgLeaveAttesterSet
func (k msgServer) LeaveAttesterSet(goCtx context.Context, msg *types.MsgLeaveAttesterSet) (*types.MsgLeaveAttesterSetResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	if err := k.assertValidValidatorAuthority(ctx, msg.ConsensusAddress, msg.Authority); err != nil {
		return nil, err
	}

	// TODO (Alex): the valset should be updated at the end of an epoch only
	if err := k.RemoveAttesterSetMember(ctx, msg.ConsensusAddress); err != nil {
		return nil, sdkerr.Wrap(err, "remove attester set member")
	}

	ctx.EventManager().EmitEvent(
		sdk.NewEvent(
			types.TypeMsgLeaveAttesterSet,
			sdk.NewAttribute("consensus_address", msg.ConsensusAddress),
			sdk.NewAttribute("authority", msg.Authority),
		),
	)

	return &types.MsgLeaveAttesterSetResponse{}, nil
}

func (k msgServer) assertValidValidatorAuthority(ctx sdk.Context, consensusAddress, authority string) error {
	v, err := k.AttesterInfo.Get(ctx, consensusAddress)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return sdkerr.Wrapf(sdkerrors.ErrUnauthorized, "consensus address %s not in attester set", consensusAddress)
		}
		return sdkerr.Wrapf(err, "attester set")
	}
	if v.Authority != authority {
		return sdkerr.Wrapf(sdkerrors.ErrUnauthorized, "address %s", authority)
	}
	return nil
}

// UpdateParams handles MsgUpdateParams
func (k msgServer) UpdateParams(goCtx context.Context, msg *types.MsgUpdateParams) (*types.MsgUpdateParamsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	if k.GetAuthority() != msg.Authority {
		return nil, sdkerr.Wrapf(govtypes.ErrInvalidSigner, "invalid authority; expected %s, got %s", k.GetAuthority(), msg.Authority)
	}

	if err := msg.Params.Validate(); err != nil {
		return nil, err
	}

	if err := k.SetParams(ctx, msg.Params); err != nil {
		return nil, fmt.Errorf("set params: %w", err)
	}

	ctx.EventManager().EmitEvent(
		sdk.NewEvent(
			types.TypeMsgUpdateParams,
			sdk.NewAttribute("authority", msg.Authority),
		),
	)

	return &types.MsgUpdateParamsResponse{}, nil
}
