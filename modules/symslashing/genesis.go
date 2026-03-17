package symslashing

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/evstack/ev-abci/modules/symslashing/keeper"
	"github.com/evstack/ev-abci/modules/symslashing/types"
)

// InitGenesis initializes the symslashing module's state from a provided genesis state.
func InitGenesis(ctx sdk.Context, k keeper.Keeper, genState types.GenesisState) error {
	// Set module params
	if err := k.SetParams(ctx, genState.Params); err != nil {
		return fmt.Errorf("set params: %w", err)
	}

	return nil
}

// ExportGenesis returns the symslashing module's exported genesis.
func ExportGenesis(ctx sdk.Context, k keeper.Keeper) *types.GenesisState {
	genesis := types.DefaultGenesisState()
	genesis.Params = k.GetParams(ctx)
	return genesis
}
