package keeper

import (
	"context"
)

// BeginBlock performs the begin block operations for the symslashing module.
// In the context of ev-node (single sequencer), there is no CometBFT consensus engine
// to provide double-sign or downtime evidence natively via ABCI BeginBlock.
// Any slashing actions are triggered externally (e.g., via the relay or specific transactions).
func (k Keeper) BeginBlock(ctx context.Context) error {
	return nil
}
