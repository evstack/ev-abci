package core

import (
	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/libs/bytes"
	ctypes "github.com/cometbft/cometbft/rpc/core/types"
	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
)

// ABCIQuery queries the application for some information.
// More: https://docs.cometbft.com/v0.37/rpc/#/ABCI/abci_query
func ABCIQuery(
	ctx *rpctypes.Context,
	path string,
	data bytes.HexBytes,
	height int64,
	prove bool,
) (*ctypes.ResultABCIQuery, error) {
	resp, err := env.Adapter.App.Query(ctx.Context(), &abci.RequestQuery{
		Data:   data,
		Path:   path,
		Height: height,
		Prove:  prove,
	})
	if err != nil {
		return nil, err
	}
	return &ctypes.ResultABCIQuery{
		Response: *resp,
	}, nil
}

// ABCIInfo gets some info about the application.
// More: https://docs.cometbft.com/v0.37/rpc/#/ABCI/abci_info
func ABCIInfo(ctx *rpctypes.Context) (*ctypes.ResultABCIInfo, error) {
	info, err := env.Adapter.App.Info(&abci.RequestInfo{})
	if err != nil {
		return nil, err
	}

	// In attester mode, override last_block_height with last attested height
	if env.NetworkSoftConfirmation {
		lastAttestedHeight, err := getLastAttestedHeight(ctx)
		if err != nil {
			// Log warning but don't fail the request - fall back to original height
			env.Logger.Error("Failed to get last attested height for ABCIInfo", "error", err)
		} else {
			// Override the last block height with the last attested height
			originalHeight := info.LastBlockHeight
			info.LastBlockHeight = int64(lastAttestedHeight)
			env.Logger.Debug("ABCIInfo using last attested height", 
				"lastAttestedHeight", lastAttestedHeight,
				"originalHeight", originalHeight)
		}
	}

	return &ctypes.ResultABCIInfo{
		Response: *info,
	}, nil
}
