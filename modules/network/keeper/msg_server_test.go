package keeper

import (
	"bytes"
	"context"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil/integration"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/evstack/ev-abci/modules/network/types"
)

func TestJoinAttesterSet(t *testing.T) {
	myValAddr := sdk.ValAddress("validator4")

	type testCase struct {
		setup  func(t *testing.T, ctx sdk.Context, keeper *Keeper, sk *MockStakingKeeper)
		msg    *types.MsgJoinAttesterSet
		expErr error
		expSet bool
	}

	tests := map[string]testCase{
		"valid": {
			setup: func(t *testing.T, ctx sdk.Context, keeper *Keeper, sk *MockStakingKeeper) {
				validator := stakingtypes.Validator{
					OperatorAddress: myValAddr.String(),
					Status:          stakingtypes.Bonded,
				}
				err := sk.SetValidator(ctx, validator)
				require.NoError(t, err, "failed to set validator")
			},
			msg:    &types.MsgJoinAttesterSet{Authority: myValAddr.String(), ConsensusAddress: myValAddr.String()},
			expSet: true,
		},
		"invalid_addr": {
			setup:  func(t *testing.T, ctx sdk.Context, keeper *Keeper, sk *MockStakingKeeper) {},
			msg:    &types.MsgJoinAttesterSet{Authority: "invalidAddr", ConsensusAddress: "invalidAddr"},
			expErr: sdkerrors.ErrInvalidAddress,
		},
		"already set": {
			setup: func(t *testing.T, ctx sdk.Context, keeper *Keeper, sk *MockStakingKeeper) {
				validator := stakingtypes.Validator{
					OperatorAddress: myValAddr.String(),
					Status:          stakingtypes.Bonded,
				}
				require.NoError(t, sk.SetValidator(ctx, validator))
				require.NoError(t, keeper.SetAttesterSetMember(ctx, myValAddr.String()))
			},
			msg:    &types.MsgJoinAttesterSet{Authority: myValAddr.String(), ConsensusAddress: myValAddr.String()},
			expErr: sdkerrors.ErrInvalidRequest,
			expSet: true,
		},
	}

	for name, spec := range tests {
		t.Run(name, func(t *testing.T) {
			sk := NewMockStakingKeeper()
			server, keeper, ctx := newTestServer(t, &sk)

			spec.setup(t, ctx, &keeper, &sk)

			// when
			rsp, err := server.JoinAttesterSet(ctx, spec.msg)
			// then
			if spec.expErr != nil {
				require.ErrorIs(t, err, spec.expErr)
				require.Nil(t, rsp)
				exists, gotErr := keeper.AttesterSet.Has(ctx, spec.msg.ConsensusAddress)
				require.NoError(t, gotErr)
				assert.Equal(t, exists, spec.expSet)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, rsp)
			exists, gotErr := keeper.AttesterSet.Has(ctx, spec.msg.ConsensusAddress)
			require.NoError(t, gotErr)
			assert.True(t, exists)

			// Verify authority is stored correctly in AttesterInfo
			info, infoErr := keeper.GetAttesterInfo(ctx, spec.msg.ConsensusAddress)
			require.NoError(t, infoErr)
			assert.Equal(t, spec.msg.Authority, info.Authority)
		})
	}
}

func TestJoinAttesterSetMaxCap(t *testing.T) {
	// Verify the constant is set to a sane value that is within uint16 range
	require.LessOrEqual(t, MaxAttesters, int(^uint16(0)),
		"MaxAttesters must fit in uint16 to avoid index overflow in BuildValidatorIndexMap")

	t.Run("join succeeds under cap", func(t *testing.T) {
		sk := NewMockStakingKeeper()
		server, keeper, ctx := newTestServer(t, &sk)

		// With an empty set, join should succeed
		newAddr := sdk.ValAddress("new_attester")
		msg := &types.MsgJoinAttesterSet{
			Authority:        newAddr.String(),
			ConsensusAddress: newAddr.String(),
		}

		rsp, err := server.JoinAttesterSet(ctx, msg)
		require.NoError(t, err)
		require.NotNil(t, rsp)

		// Verify the attester was added
		exists, err := keeper.AttesterSet.Has(ctx, newAddr.String())
		require.NoError(t, err)
		assert.True(t, exists)
	})
}

func TestAttestVotePayloadValidation(t *testing.T) {
	chainID := "test-chain"
	priv := cmted25519.GenPrivKey()
	pub := priv.PubKey().(cmted25519.PubKey)
	blockHash := bytes.Repeat([]byte{0x01}, 32)

	consAddr := sdk.ConsAddress(pub.Address()).String()
	authorityAddr := sdk.AccAddress(pub.Address()).String()

	specs := map[string]struct {
		vote   []byte
		expErr error
	}{
		"empty vote rejected": {
			vote:   []byte{},
			expErr: sdkerrors.ErrInvalidRequest,
		},
		"nil vote rejected": {
			vote:   nil,
			expErr: sdkerrors.ErrInvalidRequest,
		},
		"random bytes rejected": {
			vote:   bytes.Repeat([]byte{0x01}, 64),
			expErr: sdkerrors.ErrInvalidRequest,
		},
		"valid signed vote accepted": {
			vote: nil, // populated below per-subtest
		},
	}

	for name, spec := range specs {
		t.Run(name, func(t *testing.T) {
			sk := NewMockStakingKeeper()
			cdc := moduletestutil.MakeTestEncodingConfig().Codec
			keys := storetypes.NewKVStoreKeys(types.StoreKey)
			logger := log.NewTestLogger(t)
			cms := integration.CreateMultiStore(keys, logger)
			authority := authtypes.NewModuleAddress("gov")
			keeper := NewKeeper(cdc, runtime.NewKVStoreService(keys[types.StoreKey]), &sk, nil, nil, authority.String())
			server := msgServer{Keeper: keeper}
			ctx := sdk.NewContext(cms, cmtproto.Header{
				ChainID: chainID,
				Time:    time.Now().UTC(),
				Height:  10,
			}, false, logger).WithContext(t.Context())

			require.NoError(t, keeper.SetParams(ctx, types.DefaultParams()))

			sdkPk, err := cryptocodec.FromCmtPubKeyInterface(pub)
			require.NoError(t, err)
			info, err := types.NewAttesterInfo(authorityAddr, sdkPk, 0)
			require.NoError(t, err)
			require.NoError(t, keeper.SetAttesterInfo(ctx, consAddr, info))
			require.NoError(t, keeper.SetAttesterSetMember(ctx, consAddr))
			require.NoError(t, keeper.SetValidatorIndex(ctx, consAddr, 0, 1))

			vote := spec.vote
			if name == "valid signed vote accepted" {
				vote = signTestVote(t, chainID, 10, priv, blockHash)
			}

			msg := &types.MsgAttest{
				Authority:        authorityAddr,
				ConsensusAddress: consAddr,
				Height:           10,
				Vote:             vote,
			}

			rsp, err := server.Attest(ctx, msg)
			if spec.expErr != nil {
				require.ErrorIs(t, err, spec.expErr)
				require.Nil(t, rsp)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, rsp)
		})
	}
}

func TestLeaveAttesterSet(t *testing.T) {
	ownerAddr := sdk.ValAddress("owner1")
	otherAddr := sdk.ValAddress("other1")

	type testCase struct {
		setup  func(t *testing.T, ctx sdk.Context, keeper *Keeper, server msgServer)
		msg    *types.MsgLeaveAttesterSet
		expErr error
	}

	tests := map[string]testCase{
		"valid": {
			setup: func(t *testing.T, ctx sdk.Context, keeper *Keeper, server msgServer) {
				t.Helper()
				joinMsg := &types.MsgJoinAttesterSet{
					Authority:        ownerAddr.String(),
					ConsensusAddress: ownerAddr.String(),
				}
				_, err := server.JoinAttesterSet(ctx, joinMsg)
				require.NoError(t, err)
			},
			msg: &types.MsgLeaveAttesterSet{
				Authority:        ownerAddr.String(),
				ConsensusAddress: ownerAddr.String(),
			},
		},
		"not_in_set": {
			setup: func(t *testing.T, ctx sdk.Context, keeper *Keeper, server msgServer) {
				t.Helper()
			},
			msg: &types.MsgLeaveAttesterSet{
				Authority:        ownerAddr.String(),
				ConsensusAddress: ownerAddr.String(),
			},
			expErr: sdkerrors.ErrUnauthorized,
		},
		"wrong_authority": {
			setup: func(t *testing.T, ctx sdk.Context, keeper *Keeper, server msgServer) {
				t.Helper()
				joinMsg := &types.MsgJoinAttesterSet{
					Authority:        ownerAddr.String(),
					ConsensusAddress: ownerAddr.String(),
				}
				_, err := server.JoinAttesterSet(ctx, joinMsg)
				require.NoError(t, err)
			},
			msg: &types.MsgLeaveAttesterSet{
				Authority:        otherAddr.String(),
				ConsensusAddress: ownerAddr.String(),
			},
			expErr: sdkerrors.ErrUnauthorized,
		},
	}

	for name, spec := range tests {
		t.Run(name, func(t *testing.T) {
			sk := NewMockStakingKeeper()
			server, keeper, ctx := newTestServer(t, &sk)

			spec.setup(t, ctx, &keeper, server)

			rsp, err := server.LeaveAttesterSet(ctx, spec.msg)
			if spec.expErr != nil {
				require.ErrorIs(t, err, spec.expErr)
				require.Nil(t, rsp)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, rsp)

			// Verify actually removed from attester set
			exists, gotErr := keeper.AttesterSet.Has(ctx, spec.msg.ConsensusAddress)
			require.NoError(t, gotErr)
			assert.False(t, exists)
		})
	}
}

func TestAttest(t *testing.T) {
	chainID := "test-chain"
	priv := cmted25519.GenPrivKey()
	pub := priv.PubKey().(cmted25519.PubKey)
	consAddr := sdk.ConsAddress(pub.Address()).String()
	authorityAddr := sdk.AccAddress(pub.Address()).String()
	otherAddr := sdk.ValAddress("other_sender")
	blockHash := bytes.Repeat([]byte{0x01}, 32)

	type testCase struct {
		setup  func(t *testing.T, ctx sdk.Context, keeper *Keeper)
		msg    func() *types.MsgAttest
		expErr error
	}

	tests := map[string]testCase{
		"valid": {
			setup: func(t *testing.T, ctx sdk.Context, keeper *Keeper) {
				t.Helper()
				require.NoError(t, keeper.SetParams(ctx, types.DefaultParams()))
				sdkPk, err := cryptocodec.FromCmtPubKeyInterface(pub)
				require.NoError(t, err)
				info, err := types.NewAttesterInfo(authorityAddr, sdkPk, 0)
				require.NoError(t, err)
				require.NoError(t, keeper.SetAttesterInfo(ctx, consAddr, info))
				require.NoError(t, keeper.SetAttesterSetMember(ctx, consAddr))
				require.NoError(t, keeper.SetValidatorIndex(ctx, consAddr, 0, 1))
			},
			msg: func() *types.MsgAttest {
				return &types.MsgAttest{
					Authority:        authorityAddr,
					ConsensusAddress: consAddr,
					Height:           10,
					Vote:             signTestVote(t, chainID, 10, priv, blockHash),
				}
			},
		},
		"not_in_set": {
			setup: func(t *testing.T, ctx sdk.Context, keeper *Keeper) {
				t.Helper()
			},
			msg: func() *types.MsgAttest {
				return &types.MsgAttest{
					Authority:        authorityAddr,
					ConsensusAddress: consAddr,
					Height:           10,
					Vote:             bytes.Repeat([]byte{0x01}, 64),
				}
			},
			expErr: sdkerrors.ErrUnauthorized,
		},
		"wrong_authority": {
			setup: func(t *testing.T, ctx sdk.Context, keeper *Keeper) {
				t.Helper()
				sdkPk, err := cryptocodec.FromCmtPubKeyInterface(pub)
				require.NoError(t, err)
				info, err := types.NewAttesterInfo(authorityAddr, sdkPk, 0)
				require.NoError(t, err)
				require.NoError(t, keeper.SetAttesterInfo(ctx, consAddr, info))
				require.NoError(t, keeper.SetAttesterSetMember(ctx, consAddr))
				require.NoError(t, keeper.SetValidatorIndex(ctx, consAddr, 0, 1))
			},
			msg: func() *types.MsgAttest {
				return &types.MsgAttest{
					Authority:        otherAddr.String(),
					ConsensusAddress: consAddr,
					Height:           10,
					Vote:             bytes.Repeat([]byte{0x01}, 64),
				}
			},
			expErr: sdkerrors.ErrUnauthorized,
		},
	}
	for name, spec := range tests {
		t.Run(name, func(t *testing.T) {
			sk := NewMockStakingKeeper()
			server, keeper, ctx := newTestServer(t, &sk)

			spec.setup(t, ctx, &keeper)

			rsp, err := server.Attest(ctx, spec.msg())
			if spec.expErr != nil {
				require.ErrorIs(t, err, spec.expErr)
				require.Nil(t, rsp)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, rsp)
		})
	}
}

func newTestServer(t *testing.T, sk *MockStakingKeeper) (msgServer, Keeper, sdk.Context) {
	t.Helper()
	cdc := moduletestutil.MakeTestEncodingConfig().Codec
	keys := storetypes.NewKVStoreKeys(types.StoreKey)
	logger := log.NewTestLogger(t)
	cms := integration.CreateMultiStore(keys, logger)
	authority := authtypes.NewModuleAddress("gov")
	keeper := NewKeeper(cdc, runtime.NewKVStoreService(keys[types.StoreKey]), sk, nil, nil, authority.String())
	server := msgServer{Keeper: keeper}
	ctx := sdk.NewContext(cms, cmtproto.Header{ChainID: "test-chain", Time: time.Now().UTC(), Height: 10}, false, logger).
		WithContext(t.Context())
	return server, keeper, ctx
}

func TestAttestHeightBounds(t *testing.T) {
	// With DefaultParams: EpochLength=1, PruneAfter=15
	// At blockHeight=100: currentEpoch=100, minHeight=(100-7)*1=93
	specs := map[string]struct {
		blockHeight int64
		attestH     int64
		expErr      error
	}{
		"future height rejected": {
			blockHeight: 100,
			attestH:     200,
			expErr:      sdkerrors.ErrInvalidRequest,
		},
		"two-ahead rejected": {
			blockHeight: 100,
			attestH:     102,
			expErr:      sdkerrors.ErrInvalidRequest,
		},
		"current height accepted": {
			blockHeight: 100,
			attestH:     100,
		},
		"next height accepted": {
			blockHeight: 100,
			attestH:     101,
		},
		"stale height rejected": {
			blockHeight: 100,
			attestH:     1,
			expErr:      sdkerrors.ErrInvalidRequest,
		},
		"below retention window rejected": {
			blockHeight: 100,
			attestH:     84, // minHeight = 85
			expErr:      sdkerrors.ErrInvalidRequest,
		},
		"at retention boundary accepted": {
			blockHeight: 100,
			attestH:     93, // exactly minHeight
		},
		"early chain no stale rejection": {
			blockHeight: 16,
			attestH:     1,
		},
	}
	for name, spec := range specs {
		t.Run(name, func(t *testing.T) {
			chainID := "test-chain"
			priv := cmted25519.GenPrivKey()
			pub := priv.PubKey().(cmted25519.PubKey)
			consAddr := sdk.ConsAddress(pub.Address()).String()
			authorityAddr := sdk.AccAddress(pub.Address()).String()

			sk := NewMockStakingKeeper()
			cdc := moduletestutil.MakeTestEncodingConfig().Codec
			keys := storetypes.NewKVStoreKeys(types.StoreKey)
			logger := log.NewTestLogger(t)
			cms := integration.CreateMultiStore(keys, logger)
			authority := authtypes.NewModuleAddress("gov")
			keeper := NewKeeper(cdc, runtime.NewKVStoreService(keys[types.StoreKey]), &sk, nil, nil, authority.String())
			server := msgServer{Keeper: keeper}
			ctx := sdk.NewContext(cms, cmtproto.Header{
				ChainID: chainID,
				Time:    time.Now().UTC(),
				Height:  spec.blockHeight,
			}, false, logger).WithContext(t.Context())

			require.NoError(t, keeper.SetParams(ctx, types.DefaultParams()))

			// Register the attester directly via keeper (no MsgJoin)
			sdkPk, err := cryptocodec.FromCmtPubKeyInterface(pub)
			require.NoError(t, err)
			info, err := types.NewAttesterInfo(authorityAddr, sdkPk, 0)
			require.NoError(t, err)
			require.NoError(t, keeper.SetAttesterInfo(ctx, consAddr, info))
			require.NoError(t, keeper.SetAttesterSetMember(ctx, consAddr))
			require.NoError(t, keeper.SetValidatorIndex(ctx, consAddr, 0, 1))

			// Build a signed vote for the expected height
			blockHash := bytes.Repeat([]byte{0x01}, 32)
			voteBytes := signTestVote(t, chainID, spec.attestH, priv, blockHash)

			msg := &types.MsgAttest{
				Authority:        authorityAddr,
				ConsensusAddress: consAddr,
				Height:           spec.attestH,
				Vote:             voteBytes,
			}
			rsp, err := server.Attest(ctx, msg)
			if spec.expErr != nil {
				require.ErrorIs(t, err, spec.expErr)
				require.Nil(t, rsp)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, rsp)
		})
	}
}

var _ types.StakingKeeper = &MockStakingKeeper{}

type MockStakingKeeper struct {
	activeSet map[string]stakingtypes.Validator
}

func NewMockStakingKeeper() MockStakingKeeper {
	return MockStakingKeeper{
		activeSet: make(map[string]stakingtypes.Validator),
	}
}

func (m *MockStakingKeeper) SetValidator(ctx context.Context, validator stakingtypes.Validator) error {
	m.activeSet[validator.GetOperator()] = validator
	return nil

}
func (m MockStakingKeeper) GetAllValidators(ctx context.Context) (validators []stakingtypes.Validator, err error) {
	return slices.SortedFunc(maps.Values(m.activeSet), func(v1 stakingtypes.Validator, v2 stakingtypes.Validator) int {
		return strings.Compare(v1.OperatorAddress, v2.OperatorAddress)
	}), nil
}

func (m MockStakingKeeper) GetValidator(ctx context.Context, addr sdk.ValAddress) (validator stakingtypes.Validator, err error) {
	validator, found := m.activeSet[addr.String()]
	if !found {
		return validator, sdkerrors.ErrNotFound
	}
	return validator, nil
}

func (m MockStakingKeeper) GetLastValidators(ctx context.Context) (validators []stakingtypes.Validator, err error) {
	for _, validator := range m.activeSet {
		if validator.IsBonded() { // Assuming IsBonded() identifies if a validator is in the last validators
			validators = append(validators, validator)
		}
	}
	return
}

func (m MockStakingKeeper) GetLastTotalPower(ctx context.Context) (math.Int, error) {
	return math.NewInt(int64(len(m.activeSet))), nil
}

func TestVerifyVote(t *testing.T) {
	chainID := "test-chain"
	priv := cmted25519.GenPrivKey()
	pub := priv.PubKey().(cmted25519.PubKey)
	consAddr := sdk.ConsAddress(pub.Address()).String()
	// 32-byte block hash (CanonicalizeBlockID requires 32 bytes or empty)
	blockHash := bytes.Repeat([]byte{0xbb}, 32)

	sk := NewMockStakingKeeper()
	_, keeper, ctx := newTestServer(t, &sk)

	sdkPk, err := cryptocodec.FromCmtPubKeyInterface(pub)
	require.NoError(t, err)
	info, err := types.NewAttesterInfo(sdk.AccAddress(pub.Address()).String(), sdkPk, 0)
	require.NoError(t, err)
	require.NoError(t, keeper.SetAttesterInfo(ctx, consAddr, info))

	validBytes := signTestVote(t, chainID, 42, priv, blockHash)

	specs := map[string]struct {
		consAddr string
		vote     []byte
		msgH     int64
		expErr   error
	}{
		"valid": {
			consAddr: consAddr,
			vote:     validBytes,
			msgH:     42,
		},
		"wrong chain id": {
			consAddr: consAddr,
			vote:     signTestVote(t, "other-chain", 42, priv, blockHash),
			msgH:     42,
			expErr:   sdkerrors.ErrUnauthorized,
		},
		"wrong height": {
			consAddr: consAddr,
			vote:     validBytes,
			msgH:     99,
			expErr:   sdkerrors.ErrInvalidRequest,
		},
		"random 64 bytes": {
			consAddr: consAddr,
			vote:     bytes.Repeat([]byte{0x01}, 64),
			msgH:     42,
			expErr:   sdkerrors.ErrInvalidRequest, // unmarshal may succeed but checks fail
		},
		"signed by different key": {
			consAddr: consAddr,
			vote:     signTestVote(t, chainID, 42, cmted25519.GenPrivKey(), blockHash),
			msgH:     42,
			expErr:   sdkerrors.ErrUnauthorized,
		},
		"prevote type": {
			consAddr: consAddr,
			vote: func() []byte {
				v := cmtproto.Vote{
					Type:             cmtproto.PrevoteType,
					Height:           42,
					Round:            0,
					BlockID:          cmtproto.BlockID{Hash: blockHash, PartSetHeader: cmtproto.PartSetHeader{}},
					Timestamp:        testTimeUTC(),
					ValidatorAddress: pub.Address(),
				}
				sb := cmttypes.VoteSignBytes(chainID, &v)
				sig, _ := priv.Sign(sb)
				v.Signature = sig
				bz, _ := proto.Marshal(&v)
				return bz
			}(),
			msgH:   42,
			expErr: sdkerrors.ErrInvalidRequest,
		},
		"non-zero round": {
			consAddr: consAddr,
			vote: func() []byte {
				v := cmtproto.Vote{
					Type:             cmtproto.PrecommitType,
					Height:           42,
					Round:            1,
					BlockID:          cmtproto.BlockID{Hash: blockHash, PartSetHeader: cmtproto.PartSetHeader{}},
					Timestamp:        testTimeUTC(),
					ValidatorAddress: pub.Address(),
				}
				sb := cmttypes.VoteSignBytes(chainID, &v)
				sig, _ := priv.Sign(sb)
				v.Signature = sig
				bz, _ := proto.Marshal(&v)
				return bz
			}(),
			msgH:   42,
			expErr: sdkerrors.ErrInvalidRequest,
		},
		"unknown consensus address": {
			consAddr: sdk.ConsAddress(bytes.Repeat([]byte{0x77}, 20)).String(),
			vote:     validBytes,
			msgH:     42,
			expErr:   sdkerrors.ErrNotFound,
		},
	}

	for name, spec := range specs {
		t.Run(name, func(t *testing.T) {
			sdkCtx := ctx.WithBlockHeader(cmtproto.Header{ChainID: chainID})
			_, err := keeper.VerifyVoteForTest(sdkCtx, spec.consAddr, spec.vote, spec.msgH)
			if spec.expErr != nil {
				require.ErrorIs(t, err, spec.expErr)
				return
			}
			require.NoError(t, err)
		})
	}
}
