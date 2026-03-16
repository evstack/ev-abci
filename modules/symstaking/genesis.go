package symstaking

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/evstack/ev-abci/modules/symstaking/keeper"
	"github.com/evstack/ev-abci/modules/symstaking/types"
)

// InitGenesis initializes the symstaking module's state from a provided genesis state.
func InitGenesis(ctx sdk.Context, k keeper.Keeper, genState types.GenesisState) error {
	// Set module params
	if err := k.SetParams(ctx, genState.Params); err != nil {
		return fmt.Errorf("set params: %w", err)
	}

	// Set initial epoch
	if err := k.SetCurrentEpoch(ctx, genState.Epoch); err != nil {
		return fmt.Errorf("set epoch: %w", err)
	}

	return nil
}

// ExportGenesis returns the symstaking module's exported genesis.
func ExportGenesis(ctx sdk.Context, k keeper.Keeper) *types.GenesisState {
	genesis := types.DefaultGenesisState()
	genesis.Params = k.GetParams(ctx)
	genesis.Epoch = k.GetCurrentEpoch(ctx)
	return genesis
}
