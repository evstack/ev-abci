package keeper

import (
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
		//{
		//	name: "failed to set attester set member",
		//	setup: func(t *testing.T, ctx sdk.Context, keeper *Keeper, sk *MockStakingKeeper) {
		//		validatorAddr := sdk.ValAddress([]byte("validator5"))
		//		validator := stakingtypes.Validator{
		//			OperatorAddress: validatorAddr.String(),
		//			Status:          stakingtypes.Bonded,
		//		}
		//		err := sk.SetValidator(ctx, validator)
		//		require.NoError(t, err, "failed to set validator")
		//		keeper.forceError = true
		//	},
		//	msg: &types.MsgJoinAttesterSet{
		//		Validator: "validator5",
		//	},
		//	expErr:  sdkerrors.ErrInternal,
		//	expectResponse: false,
		//},
	}

	for name, spec := range tests {
		t.Run(name, func(t *testing.T) {
			sk := NewMockStakingKeeper()

			cdc := moduletestutil.MakeTestEncodingConfig().Codec

			keys := storetypes.NewKVStoreKeys(types.StoreKey)

			logger := log.NewTestLogger(t)
			cms := integration.CreateMultiStore(keys, logger)
			authority := authtypes.NewModuleAddress("gov")
			keeper := NewKeeper(cdc, runtime.NewKVStoreService(keys[types.StoreKey]), sk, nil, nil, authority.String())
			server := msgServer{Keeper: keeper}
			ctx := sdk.NewContext(cms, cmtproto.Header{ChainID: "test-chain", Time: time.Now().UTC(), Height: 10}, false, logger).
				WithContext(t.Context())

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
		})
	}
}

func TestJoinAttesterSetMaxCap(t *testing.T) {
	// Verify the constant is set to a sane value that is within uint16 range
	require.LessOrEqual(t, MaxAttesters, int(^uint16(0)),
		"MaxAttesters must fit in uint16 to avoid index overflow in BuildValidatorIndexMap")

	t.Run("join succeeds under cap", func(t *testing.T) {
		sk := NewMockStakingKeeper()
		cdc := moduletestutil.MakeTestEncodingConfig().Codec
		keys := storetypes.NewKVStoreKeys(types.StoreKey)
		logger := log.NewTestLogger(t)
		cms := integration.CreateMultiStore(keys, logger)
		authority := authtypes.NewModuleAddress("gov")
		keeper := NewKeeper(cdc, runtime.NewKVStoreService(keys[types.StoreKey]), sk, nil, nil, authority.String())
		server := msgServer{Keeper: keeper}
		ctx := sdk.NewContext(cms, cmtproto.Header{ChainID: "test-chain", Time: time.Now().UTC(), Height: 10}, false, logger).
			WithContext(t.Context())

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
				Height:  10,
			}, false, logger).WithContext(t.Context())

			require.NoError(t, keeper.SetParams(ctx, types.DefaultParams()))
			require.NoError(t, keeper.SetAttesterSetMember(ctx, myValAddr.String()))
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

func TestAttestHeightBounds(t *testing.T) {
	myValAddr := sdk.ValAddress("validator1")
	// With DefaultParams: EpochLength=1, PruneAfter=7
	// At blockHeight=100: currentEpoch=100, minHeight=(100-7)*1=93

	specs := map[string]struct {
		blockHeight int64
		attestH     int64
		params      *types.Params // nil → DefaultParams()
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
			attestH:     92, // minHeight = 93
			expErr:      sdkerrors.ErrInvalidRequest,
		},
		"at retention boundary accepted": {
			blockHeight: 100,
			attestH:     93, // exactly minHeight
		},
		"early chain no stale rejection": {
			// blockHeight=5, currentEpoch=5, PruneAfter=7 → currentEpoch <= PruneAfter → minHeight=1
			blockHeight: 5,
			attestH:     1,
		},
		// Cases with EpochLength>1 and blockHeight NOT on an epoch boundary.
		// EpochLength=10, PruneAfter=2, blockHeight=35 (mid epoch 3):
		//   GetCurrentEpoch = 35/10 = 3  (integer floor — not aligned to boundary)
		//   currentEpoch(3) > PruneAfter(2) → minHeight = (3-2)*10 = 10
		// attestH must be a checkpoint (multiple of EpochLength) to bypass the
		// SIGN_MODE_CHECKPOINT guard and reach the lower-bound check.
		// This asserts that the floor-to-epoch-start rounding from GetCurrentEpoch
		// is preserved and consistent with PruneOldBitmaps.
		"multi-epoch mid-block below retention rejected": {
			blockHeight: 35, // epoch 3 (floor), mid-epoch
			attestH:     0,  // checkpoint (0%10==0), but < minHeight(10) → rejected
			params: func() *types.Params {
				p := types.NewParams(10, types.DefaultQuorumFraction, types.DefaultMinParticipation, 2, types.DefaultSignMode)
				return &p
			}(),
			expErr: sdkerrors.ErrInvalidRequest,
		},
		"multi-epoch mid-block at retention boundary accepted": {
			blockHeight: 35, // epoch 3 (floor), mid-epoch
			attestH:     10, // checkpoint (10%10==0) and == minHeight(10) → accepted
			params: func() *types.Params {
				p := types.NewParams(10, types.DefaultQuorumFraction, types.DefaultMinParticipation, 2, types.DefaultSignMode)
				return &p
			}(),
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

			p := types.DefaultParams()
			if spec.params != nil {
				p = *spec.params
			}
			require.NoError(t, keeper.SetParams(ctx, p))

			// Setup: add attester and build index map
			require.NoError(t, keeper.SetAttesterSetMember(ctx, myValAddr.String()))
			require.NoError(t, keeper.BuildValidatorIndexMap(ctx))

			msg := &types.MsgAttest{
				Authority:        myValAddr.String(),
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
