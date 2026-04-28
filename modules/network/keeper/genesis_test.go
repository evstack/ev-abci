package keeper_test

import (
	"bytes"
	"sort"
	"testing"
	"time"

	"cosmossdk.io/log"
	storetypes "cosmossdk.io/store/types"
	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/codec"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil/integration"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"

	"github.com/evstack/ev-abci/modules/network"
	"github.com/evstack/ev-abci/modules/network/keeper"
	"github.com/evstack/ev-abci/modules/network/types"
)

func newKeeperForGenesis(t *testing.T) (keeper.Keeper, sdk.Context, codec.BinaryCodec) {
	t.Helper()
	cdc := moduletestutil.MakeTestEncodingConfig().Codec
	keys := storetypes.NewKVStoreKeys(types.StoreKey)
	logger := log.NewTestLogger(t)
	cms := integration.CreateMultiStore(keys, logger)
	authority := authtypes.NewModuleAddress("gov")
	k := keeper.NewKeeper(cdc, runtime.NewKVStoreService(keys[types.StoreKey]), nil, nil, nil, authority.String())
	ctx := sdk.NewContext(cms, cmtproto.Header{ChainID: "test-chain", Time: time.Now().UTC(), Height: 1}, false, logger).
		WithContext(t.Context())
	return k, ctx, cdc
}

func mustAnyPubKey(t *testing.T, cmtPk cmted25519.PubKey) *types.AttesterInfo {
	t.Helper()
	sdkPk, err := cryptocodec.FromCmtPubKeyInterface(cmtPk)
	require.NoError(t, err)
	info, err := types.NewAttesterInfo(
		sdk.AccAddress(cmtPk.Address()).String(),
		sdkPk,
		0,
	)
	require.NoError(t, err)
	return info
}

func TestInitGenesisLoadsAttesters(t *testing.T) {
	k, ctx, _ := newKeeperForGenesis(t)

	pk1 := cmted25519.GenPrivKey().PubKey().(cmted25519.PubKey)
	pk2 := cmted25519.GenPrivKey().PubKey().(cmted25519.PubKey)
	info1 := mustAnyPubKey(t, pk1)
	info1.ConsensusAddress = sdk.ConsAddress(pk1.Address()).String()
	info2 := mustAnyPubKey(t, pk2)
	info2.ConsensusAddress = sdk.ConsAddress(pk2.Address()).String()

	gs := types.GenesisState{
		Params:        types.DefaultParams(),
		AttesterInfos: []types.AttesterInfo{*info1, *info2},
	}

	require.NoError(t, network.InitGenesis(ctx, k, gs))

	// Both in AttesterSet
	for _, info := range []*types.AttesterInfo{info1, info2} {
		has, err := k.AttesterSet.Has(ctx, info.ConsensusAddress)
		require.NoError(t, err)
		require.True(t, has)

		stored, err := k.GetAttesterInfo(ctx, info.ConsensusAddress)
		require.NoError(t, err)
		require.Equal(t, info.Authority, stored.Authority)
	}

	// Validator indices assigned in ascending pubkey-address order, power=1
	expectedOrder := []cmted25519.PubKey{pk1, pk2}
	sort.Slice(expectedOrder, func(i, j int) bool {
		return bytes.Compare(expectedOrder[i].Address(), expectedOrder[j].Address()) < 0
	})
	for i, pk := range expectedOrder {
		consAddr := sdk.ConsAddress(pk.Address()).String()
		idx, found := k.GetValidatorIndex(ctx, consAddr)
		require.True(t, found, "consensus address %s missing index", consAddr)
		require.Equal(t, uint16(i), idx)
		power, err := k.GetValidatorPower(ctx, idx)
		require.NoError(t, err)
		require.Equal(t, uint64(1), power)
	}
}

func TestInitGenesisRejectsPubkeyAddressMismatch(t *testing.T) {
	k, ctx, _ := newKeeperForGenesis(t)

	pk := cmted25519.GenPrivKey().PubKey().(cmted25519.PubKey)
	info := mustAnyPubKey(t, pk)
	info.ConsensusAddress = sdk.ConsAddress([]byte("not-matching-20-bytes")).String()

	gs := types.GenesisState{
		Params:        types.DefaultParams(),
		AttesterInfos: []types.AttesterInfo{*info},
	}

	err := network.InitGenesis(ctx, k, gs)
	require.Error(t, err)
	require.Contains(t, err.Error(), "pubkey address mismatch")
}

func TestExportGenesisRoundtripsAttesters(t *testing.T) {
	k, ctx, _ := newKeeperForGenesis(t)

	pk := cmted25519.GenPrivKey().PubKey().(cmted25519.PubKey)
	info := mustAnyPubKey(t, pk)
	info.ConsensusAddress = sdk.ConsAddress(pk.Address()).String()

	gs := types.GenesisState{
		Params:        types.DefaultParams(),
		AttesterInfos: []types.AttesterInfo{*info},
	}
	require.NoError(t, network.InitGenesis(ctx, k, gs))

	exported := network.ExportGenesis(ctx, k)
	require.Len(t, exported.AttesterInfos, 1)
	require.Equal(t, info.ConsensusAddress, exported.AttesterInfos[0].ConsensusAddress)
	require.Equal(t, info.Authority, exported.AttesterInfos[0].Authority)
}
