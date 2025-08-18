package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/address"
	"github.com/evstack/ev-abci/modules/migrationmngr/types"
	"github.com/stretchr/testify/require"
)

func TestMigrateToRollkit_AuthorityError(t *testing.T) {
	s := initFixture(t)
	msg := &types.MsgMigrateToEvolve{Authority: "bad"}
	_, err := s.msgServer.MigrateToEvolve(s.ctx, msg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid authority")
}

func TestMigrateToRollkit_BlockHeightError(t *testing.T) {
	s := initFixture(t)
	auth := sdk.AccAddress(address.Module(types.ModuleName)).String()
	msg := &types.MsgMigrateToEvolve{Authority: auth, BlockHeight: 1}
	s.ctx = s.ctx.WithBlockHeight(2)
	_, err := s.msgServer.MigrateToEvolve(s.ctx, msg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "block height")
}

func TestMigrateToRollkit_Success(t *testing.T) {
	s := initFixture(t)
	auth := sdk.AccAddress(address.Module(types.ModuleName)).String()
	msg := &types.MsgMigrateToEvolve{Authority: auth, BlockHeight: 10}
	resp, err := s.msgServer.MigrateToEvolve(s.ctx, msg)
	require.NoError(t, err)
	require.NotNil(t, resp)
}
