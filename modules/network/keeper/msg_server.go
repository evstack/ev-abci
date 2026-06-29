package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdkerr "cosmossdk.io/errors"
	"cosmossdk.io/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	"github.com/cosmos/gogoproto/proto"

	"github.com/evstack/ev-abci/modules/network/types"
)

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

	attesterSet, err := k.GetAttesterSetForHeight(ctx, msg.Height)
	if err != nil {
		return nil, sdkerr.Wrap(err, "get attester set")
	}
	index, found := attesterIndex(attesterSet, msg.ConsensusAddress)
	if !found {
		return nil, sdkerr.Wrapf(sdkerrors.ErrUnauthorized, "consensus address %s not in attester set", msg.ConsensusAddress)
	}

	if err := k.assertValidValidatorAuthority(ctx, msg.ConsensusAddress, msg.Authority); err != nil {
		return nil, err
	}

	// Verify the vote: decode, internal checks, signature check.
	if _, err := k.verifyVote(ctx, msg.ConsensusAddress, msg.Vote, msg.Height); err != nil {
		return nil, err
	}

	// Height bounds
	currentHeight := ctx.BlockHeight()
	maxFutureHeight := currentHeight + 1
	if msg.Height > maxFutureHeight {
		return nil, sdkerr.Wrapf(sdkerrors.ErrInvalidRequest, "attestation height %d exceeds max allowed height %d", msg.Height, maxFutureHeight)
	}

	// Txs run before EndBlocker, so the current epoch's pruning has not run yet.
	currentEpoch := k.GetCurrentEpoch(ctx)
	if currentEpoch > 0 {
		boundary := k.attestationRetentionBoundary(ctx, currentEpoch-1)
		if boundary != nil && boundary.prunesHeight(msg.Height) {
			return nil, sdkerr.Wrapf(sdkerrors.ErrInvalidRequest, "attestation height %d is below retention window (min %d)", msg.Height, boundary.firstRetainedHeight)
		}
	}
	bitmap, err := k.GetAttestationBitmap(ctx, msg.Height)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return nil, sdkerr.Wrap(err, "get attestation bitmap")
	}
	if bitmap == nil {
		bitmap = k.bitmapHelper.NewBitmap(len(attesterSet))
	}

	if k.bitmapHelper.IsSet(bitmap, index) {
		return nil, sdkerr.Wrapf(sdkerrors.ErrInvalidRequest, "consensus address %s already attested for height %d", msg.ConsensusAddress, msg.Height)
	}

	k.bitmapHelper.SetBit(bitmap, index)
	if err := k.SetAttestationBitmap(ctx, msg.Height, bitmap); err != nil {
		return nil, sdkerr.Wrap(err, "set attestation bitmap")
	}
	if err := k.SetSignature(ctx, msg.Height, msg.ConsensusAddress, msg.Vote); err != nil {
		return nil, sdkerr.Wrap(err, "store signature")
	}

	votedPower := k.CalculateVotedPowerForAttesterSet(bitmap, attesterSet)
	totalPower := uint64(len(attesterSet))
	quorumReached, err := k.CheckQuorum(ctx, votedPower, totalPower)
	if err != nil {
		return nil, sdkerr.Wrap(err, "check quorum")
	}
	if err := k.StoredAttestationInfo.Set(ctx, msg.Height, types.AttestationBitmap{
		Height:        msg.Height,
		Bitmap:        bitmap,
		VotedPower:    votedPower,
		TotalPower:    totalPower,
		SoftConfirmed: quorumReached,
	}); err != nil {
		return nil, sdkerr.Wrap(err, "store attestation info")
	}
	if quorumReached {
		if err := k.UpdateLastAttestedHeight(ctx, msg.Height); err != nil {
			return nil, sdkerr.Wrap(err, "update last attested height")
		}
		k.Logger(ctx).Info("block reached quorum and is now soft confirmed",
			"height", msg.Height, "voted_power", votedPower, "total_power", totalPower)
	}

	epoch := k.GetCurrentEpoch(ctx)
	epochBitmap := k.GetEpochBitmap(ctx, epoch)
	if epochBitmap == nil {
		epochBitmap = k.bitmapHelper.NewBitmap(len(attesterSet))
	}
	k.bitmapHelper.SetBit(epochBitmap, index)
	if err := k.SetEpochBitmap(ctx, epoch, epochBitmap); err != nil {
		return nil, sdkerr.Wrap(err, "set epoch bitmap")
	}

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

	pubKey, err := unpackMsgPubKey(msg.Pubkey)
	if err != nil {
		return nil, sdkerr.Wrap(sdkerrors.ErrInvalidRequest, err.Error())
	}
	derivedConsensusAddress := sdk.ConsAddress(pubKey.Address()).String()
	if msg.ConsensusAddress != derivedConsensusAddress {
		return nil, sdkerr.Wrapf(sdkerrors.ErrInvalidRequest,
			"consensus address %s does not match pubkey address %s",
			msg.ConsensusAddress, derivedConsensusAddress)
	}
	if inSet, err := k.IsInAttesterSet(ctx, msg.ConsensusAddress); err != nil {
		return nil, sdkerr.Wrap(err, "check attester set")
	} else if inSet {
		return nil, sdkerr.Wrapf(sdkerrors.ErrInvalidRequest, "validator already in attester set: %s", msg.ConsensusAddress)
	}

	info, err := types.NewAttesterInfo(msg.Authority, pubKey, ctx.BlockHeight())
	if err != nil {
		return nil, sdkerr.Wrap(sdkerrors.ErrInvalidRequest, err.Error())
	}
	if err := k.SetAttesterInfo(ctx, msg.ConsensusAddress, info); err != nil {
		return nil, sdkerr.Wrap(err, "set attester info")
	}
	nextIndex, err := k.NextValidatorIndex(ctx)
	if err != nil {
		return nil, sdkerr.Wrap(err, "get next validator index")
	}
	if err := k.SetValidatorIndex(ctx, msg.ConsensusAddress, nextIndex, 1); err != nil {
		return nil, sdkerr.Wrap(err, "set validator index")
	}
	if err := k.SetAttesterSetMember(ctx, msg.ConsensusAddress); err != nil {
		return nil, sdkerr.Wrap(err, "set attester set member")
	}
	if err := k.SetAttesterSetSnapshot(ctx, ctx.BlockHeight()+1); err != nil {
		return nil, sdkerr.Wrap(err, "set attester set snapshot")
	}
	return &types.MsgJoinAttesterSetResponse{}, nil
}

// LeaveAttesterSet handles MsgLeaveAttesterSet
func (k msgServer) LeaveAttesterSet(goCtx context.Context, msg *types.MsgLeaveAttesterSet) (*types.MsgLeaveAttesterSetResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	if err := k.assertValidValidatorAuthority(ctx, msg.ConsensusAddress, msg.Authority); err != nil {
		return nil, err
	}
	if err := k.RemoveAttesterSetMember(ctx, msg.ConsensusAddress); err != nil {
		return nil, sdkerr.Wrap(err, "remove attester set member")
	}
	if err := k.SetAttesterSetSnapshot(ctx, ctx.BlockHeight()+1); err != nil {
		return nil, sdkerr.Wrap(err, "set attester set snapshot")
	}
	return &types.MsgLeaveAttesterSetResponse{}, nil
}

func unpackMsgPubKey(pubkey *codectypes.Any) (cryptotypes.PubKey, error) {
	if pubkey == nil {
		return nil, fmt.Errorf("pubkey not set")
	}
	if pk, ok := pubkey.GetCachedValue().(cryptotypes.PubKey); ok {
		return pk, nil
	}
	var pk cryptotypes.PubKey
	if err := types.ModuleCdc.InterfaceRegistry().UnpackAny(pubkey, &pk); err != nil {
		return nil, fmt.Errorf("unpack pubkey: %w", err)
	}
	return pk, nil
}

func attesterIndex(entries []types.AttesterSetEntry, consensusAddress string) (int, bool) {
	for _, entry := range entries {
		if entry.ConsensusAddress == consensusAddress {
			return int(entry.Index), true
		}
	}
	return 0, false
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

// verifyVote decodes vote bytes, performs internal-consistency checks, and
// verifies the signature against the pubkey registered for consensusAddress.
// Returns the decoded vote on success.
func (k msgServer) verifyVote(ctx sdk.Context, consensusAddress string, voteBytes []byte, msgHeight int64) (*cmtproto.Vote, error) {
	var v cmtproto.Vote
	if err := proto.Unmarshal(voteBytes, &v); err != nil {
		return nil, sdkerr.Wrapf(sdkerrors.ErrInvalidRequest, "unmarshal vote: %v", err)
	}
	if v.Type != cmtproto.PrecommitType {
		return nil, sdkerr.Wrapf(sdkerrors.ErrInvalidRequest, "vote type must be Precommit, got %s", v.Type)
	}
	if v.Height != msgHeight {
		return nil, sdkerr.Wrapf(sdkerrors.ErrInvalidRequest, "vote height %d != msg height %d", v.Height, msgHeight)
	}
	if v.Round != 0 {
		return nil, sdkerr.Wrapf(sdkerrors.ErrInvalidRequest, "vote round must be 0, got %d", v.Round)
	}

	info, err := k.AttesterInfo.Get(ctx, consensusAddress)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, sdkerr.Wrapf(sdkerrors.ErrNotFound, "consensus address %s not registered", consensusAddress)
		}
		return nil, sdkerr.Wrap(err, "get attester info")
	}
	pk, err := info.GetPubKey()
	if err != nil {
		return nil, sdkerr.Wrap(err, "decode pubkey")
	}
	if !bytes.Equal(v.ValidatorAddress, pk.Address()) {
		return nil, sdkerr.Wrapf(sdkerrors.ErrUnauthorized,
			"vote validator address %X does not match registered pubkey address %X",
			v.ValidatorAddress, pk.Address())
	}
	voteBlockID, err := cmttypes.BlockIDFromProto(&v.BlockID)
	if err != nil {
		return nil, sdkerr.Wrapf(sdkerrors.ErrInvalidRequest,
			"invalid vote BlockID: %v", err)
	}

	sig := v.Signature
	v.Signature = nil
	signBytes := cmttypes.VoteSignBytes(ctx.ChainID(), &v)
	if !pk.VerifySignature(signBytes, sig) {
		return nil, sdkerr.Wrapf(sdkerrors.ErrUnauthorized, "invalid vote signature")
	}
	v.Signature = sig

	// Pin the full signed BlockID to the sequencer's real BlockID for this
	// height. CometBFT sign bytes include both Hash and PartSetHeader; accepting
	// a vote over a different PartSetHeader would later fail VerifyCommitLight.
	provider := k.blockIDProvider()
	if provider == nil {
		return nil, sdkerr.Wrap(sdkerrors.ErrLogic,
			"block ID provider not wired; cannot verify vote BlockID")
	}
	storedID, err := provider.GetBlockID(ctx, uint64(msgHeight))
	if err != nil {
		return nil, sdkerr.Wrapf(sdkerrors.ErrInvalidRequest,
			"get block ID for height %d: %v", msgHeight, err)
	}
	if storedID == nil {
		return nil, sdkerr.Wrapf(sdkerrors.ErrInvalidRequest,
			"block ID for height %d not found", msgHeight)
	}
	if !storedID.Equals(*voteBlockID) {
		return nil, sdkerr.Wrapf(sdkerrors.ErrInvalidRequest,
			"vote BlockID %v does not match sequencer BlockID %v at height %d",
			voteBlockID, storedID, msgHeight)
	}
	return &v, nil
}

// VerifyVoteForTest exposes verifyVote for unit testing. Not for production use.
func (k Keeper) VerifyVoteForTest(ctx sdk.Context, consensusAddress string, voteBytes []byte, msgHeight int64) (*cmtproto.Vote, error) {
	return msgServer{Keeper: k}.verifyVote(ctx, consensusAddress, voteBytes, msgHeight)
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
