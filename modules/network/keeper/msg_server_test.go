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
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil/integration"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
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
	myValAddr := sdk.ValAddress("validator1")

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
		"short vote rejected": {
			vote:   make([]byte, MinVoteLen-1),
			expErr: sdkerrors.ErrInvalidRequest,
		},
		"min-length vote accepted": {
			vote: make([]byte, MinVoteLen),
		},
		"valid-length vote accepted": {
			vote: make([]byte, 96),
		},
	}

	for name, spec := range specs {
		t.Run(name, func(t *testing.T) {
			sk := NewMockStakingKeeper()
			server, keeper, ctx := newTestServer(t, &sk)
			require.NoError(t, keeper.SetParams(ctx, types.DefaultParams()))
			require.NoError(t, keeper.SetAttesterSetMember(ctx, myValAddr.String()))
			require.NoError(t, keeper.SetAttesterInfo(ctx, myValAddr.String(), &types.AttesterInfo{Authority: myValAddr.String()}))
			require.NoError(t, keeper.BuildValidatorIndexMap(ctx))

			msg := &types.MsgAttest{
				Authority:        myValAddr.String(),
				ConsensusAddress: myValAddr.String(),
				Height:           10,
				Vote:             spec.vote,
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
	ownerAddr := sdk.ValAddress("attester_owner")
	otherAddr := sdk.ValAddress("other_sender")

	type testCase struct {
		setup  func(t *testing.T, ctx sdk.Context, keeper *Keeper, server msgServer)
		msg    *types.MsgAttest
		expErr error
	}

	tests := map[string]testCase{
		"valid": {
			setup: func(t *testing.T, ctx sdk.Context, keeper *Keeper, server msgServer) {
				t.Helper()
				require.NoError(t, keeper.SetParams(ctx, types.DefaultParams()))
				joinMsg := &types.MsgJoinAttesterSet{
					Authority:        ownerAddr.String(),
					ConsensusAddress: ownerAddr.String(),
				}
				_, err := server.JoinAttesterSet(ctx, joinMsg)
				require.NoError(t, err)
				require.NoError(t, keeper.BuildValidatorIndexMap(ctx))
			},
			msg: &types.MsgAttest{
				Authority:        ownerAddr.String(),
				ConsensusAddress: ownerAddr.String(),
				Height:           10,
				Vote:             bytes.Repeat([]byte{0x01}, 64),
			},
		},
		"not_in_set": {
			setup: func(t *testing.T, ctx sdk.Context, keeper *Keeper, server msgServer) {
				t.Helper()
			},
			msg: &types.MsgAttest{
				Authority:        ownerAddr.String(),
				ConsensusAddress: ownerAddr.String(),
				Height:           10,
				Vote:             bytes.Repeat([]byte{0x01}, 64),
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
				require.NoError(t, keeper.BuildValidatorIndexMap(ctx))
			},
			msg: &types.MsgAttest{
				Authority:        otherAddr.String(),
				ConsensusAddress: ownerAddr.String(),
				Height:           10,
				Vote:             bytes.Repeat([]byte{0x01}, 64),
			},
			expErr: sdkerrors.ErrUnauthorized,
		},
	}

	for name, spec := range tests {
		t.Run(name, func(t *testing.T) {
			sk := NewMockStakingKeeper()
			server, keeper, ctx := newTestServer(t, &sk)

			spec.setup(t, ctx, &keeper, server)

			rsp, err := server.Attest(ctx, spec.msg)
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

// helpers

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
