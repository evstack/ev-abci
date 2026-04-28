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
	"github.com/stretchr/testify/require"

	"github.com/evstack/ev-abci/modules/network/types"
)

func TestJoinAttesterSetDisabled(t *testing.T) {
	sk := NewMockStakingKeeper()
	server, _, ctx := newTestServer(t, &sk)

	msg := &types.MsgJoinAttesterSet{
		Authority:        sdk.AccAddress([]byte("any-authority-20b")).String(),
		ConsensusAddress: sdk.ConsAddress([]byte("any-cons-addr-20-b")).String(),
	}
	rsp, err := server.JoinAttesterSet(ctx, msg)
	require.ErrorIs(t, err, sdkerrors.ErrInvalidRequest)
	require.Contains(t, err.Error(), "attester set changes disabled")
	require.Nil(t, rsp)
}

func TestLeaveAttesterSetDisabled(t *testing.T) {
	sk := NewMockStakingKeeper()
	server, _, ctx := newTestServer(t, &sk)

	msg := &types.MsgLeaveAttesterSet{
		Authority:        sdk.AccAddress([]byte("any-authority-20b")).String(),
		ConsensusAddress: sdk.ConsAddress([]byte("any-cons-addr-20-b")).String(),
	}
	rsp, err := server.LeaveAttesterSet(ctx, msg)
	require.ErrorIs(t, err, sdkerrors.ErrInvalidRequest)
	require.Nil(t, rsp)
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
			require.NoError(t, keeper.SetAttesterInfo(ctx, myValAddr.String(), &types.AttesterInfo{Authority: myValAddr.String()}))
			require.NoError(t, keeper.SetAttesterSetMember(ctx, myValAddr.String()))
			require.NoError(t, keeper.SetValidatorIndex(ctx, myValAddr.String(), 0, 1))

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
				require.NoError(t, keeper.SetAttesterInfo(ctx, ownerAddr.String(), &types.AttesterInfo{Authority: ownerAddr.String()}))
				require.NoError(t, keeper.SetAttesterSetMember(ctx, ownerAddr.String()))
				require.NoError(t, keeper.SetValidatorIndex(ctx, ownerAddr.String(), 0, 1))
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
				require.NoError(t, keeper.SetAttesterInfo(ctx, ownerAddr.String(), &types.AttesterInfo{Authority: ownerAddr.String()}))
				require.NoError(t, keeper.SetAttesterSetMember(ctx, ownerAddr.String()))
				require.NoError(t, keeper.SetValidatorIndex(ctx, ownerAddr.String(), 0, 1))
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
	myValAddr := sdk.ValAddress("validator1")
	ownerAddr := sdk.ValAddress("attester_owner")
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
			sk := NewMockStakingKeeper()
			cdc := moduletestutil.MakeTestEncodingConfig().Codec
			keys := storetypes.NewKVStoreKeys(types.StoreKey)
			logger := log.NewTestLogger(t)
			cms := integration.CreateMultiStore(keys, logger)
			authority := authtypes.NewModuleAddress("gov")
			keeper := NewKeeper(cdc, runtime.NewKVStoreService(keys[types.StoreKey]), sk, nil, nil, authority.String())
			server := msgServer{Keeper: keeper}
			ctx := sdk.NewContext(cms, cmtproto.Header{
				ChainID: "test-chain",
				Time:    time.Now().UTC(),
				Height:  spec.blockHeight,
			}, false, logger).WithContext(t.Context())

			require.NoError(t, keeper.SetParams(ctx, types.DefaultParams()))

			require.NoError(t, keeper.SetAttesterInfo(ctx, myValAddr.String(), &types.AttesterInfo{Authority: ownerAddr.String()}))
			require.NoError(t, keeper.SetAttesterSetMember(ctx, myValAddr.String()))
			require.NoError(t, keeper.SetValidatorIndex(ctx, myValAddr.String(), 0, 1))

			// when
			msg := &types.MsgAttest{
				Authority:        ownerAddr.String(),
				ConsensusAddress: myValAddr.String(),
				Height:           spec.attestH,
				Vote:             make([]byte, MinVoteLen),
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
