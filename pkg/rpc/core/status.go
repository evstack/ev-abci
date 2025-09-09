package core

import (
	"fmt"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/config"
	cmbytes "github.com/cometbft/cometbft/libs/bytes"
	corep2p "github.com/cometbft/cometbft/p2p"
	ctypes "github.com/cometbft/cometbft/rpc/core/types"
	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cometbft/cometbft/version"
	networktypes "github.com/evstack/ev-abci/modules/network/types"
)

// Status returns CometBFT status including node info, pubkey, latest block
// hash, app hash, block height and time.
// More: https://docs.cometbft.com/v0.37/rpc/#/Info/status
func Status(ctx *rpctypes.Context) (*ctypes.ResultStatus, error) {
	unwrappedCtx := ctx.Context()

	var (
		latestBlockHash cmbytes.HexBytes
		latestAppHash   cmbytes.HexBytes
		latestBlockTime time.Time
		catchingUp      = false // default to not catching up
	)

	// Determine latest height based on node mode
	var latestHeight uint64
	var err error

	// Debug: Log attester mode status
	isAttester := isAttesterNode()
	env.Logger.Info("Status endpoint - node mode detection",
		"isAttester", isAttester,
		"AttesterMode", env.AttesterMode)

	if isAttester {
		// If attester node, get last attested height for this validator
		latestHeight, err = getLastAttestedHeight(ctx)
		if err != nil {
			// Fallback to store height if attestation query fails
			latestHeight, err = env.Adapter.RollkitStore.Height(ctx.Context())
			if err != nil {
				return nil, fmt.Errorf("failed to get latest height: %w", err)
			}
		}
	} else {
		// If sequencer node, use normal behavior
		latestHeight, err = env.Adapter.RollkitStore.Height(ctx.Context())
		if err != nil {
			return nil, fmt.Errorf("failed to get latest height: %w", err)
		}
	}

	if latestHeight != 0 {
		header, err := env.Adapter.RollkitStore.GetHeader(unwrappedCtx, latestHeight)
		if err != nil {
			return nil, fmt.Errorf("failed to find latest block: %w", err)
		}
		latestBlockHash = cmbytes.HexBytes(header.DataHash)
		latestAppHash = cmbytes.HexBytes(header.AppHash)
		latestBlockTime = header.Time()

		// Consider node to be catching up if latest block is more than twice the block time old
		blockTime := env.EVNodeConfig.Node.BlockTime.Duration
		catchingUpThreshold := 2 * blockTime
		timeSinceLatestBlock := time.Since(latestBlockTime)
		catchingUp = timeSinceLatestBlock > catchingUpThreshold
	}

	// TODO: For attester mode, we temporarily report CatchingUp=false
	// to unblock relayer clients while we refine the semantics for
	// "catching up" based on attestation lag vs. block production.
	// Revisit to implement a more accurate condition for attester nodes.
	if env.AttesterMode {
		catchingUp = false
	}

	initialHeader, err := env.Adapter.RollkitStore.GetHeader(unwrappedCtx, uint64(env.Adapter.AppGenesis.InitialHeight))
	if err != nil {
		return nil, fmt.Errorf("failed to find earliest block: %w", err)
	}

	genesisValidators := env.Adapter.AppGenesis.Consensus.Validators
	if len(genesisValidators) != 1 {
		return nil, fmt.Errorf("there should be exactly one validator in genesis")
	}

	// Changed behavior to get this from genesis
	genesisValidator := genesisValidators[0]
	validator := cmttypes.Validator{
		Address:     genesisValidator.Address,
		PubKey:      genesisValidator.PubKey,
		VotingPower: int64(1),
	}

	state, err := env.Adapter.RollkitStore.GetState(unwrappedCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to load the last saved state: %w", err)
	}
	defaultProtocolVersion := corep2p.NewProtocolVersion(
		version.P2PProtocol,
		version.BlockProtocol,
		state.Version.App,
	)
	id, addr, network, err := env.Adapter.P2PClient.Info()
	if err != nil {
		return nil, fmt.Errorf("failed to load node p2p2 info: %w", err)
	}

	processedID, err := TruncateNodeID(id)
	if err != nil {
		return nil, fmt.Errorf("failed to process node ID: %w", err)
	}
	id = processedID

	txIndexerStatus := "on"

	result := &ctypes.ResultStatus{
		NodeInfo: corep2p.DefaultNodeInfo{
			ProtocolVersion: defaultProtocolVersion,
			DefaultNodeID:   corep2p.ID(id),
			ListenAddr:      addr,
			Network:         network,
			Version:         version.TMCoreSemVer,
			Moniker:         config.DefaultBaseConfig().Moniker,
			Other: corep2p.DefaultNodeInfoOther{
				TxIndex:    txIndexerStatus,
				RPCAddress: env.RPCConfig.ListenAddress,
			},
		},
		SyncInfo: ctypes.SyncInfo{
			LatestBlockHash:     latestBlockHash,
			LatestAppHash:       latestAppHash,
			LatestBlockHeight:   int64(latestHeight),
			LatestBlockTime:     latestBlockTime,
			EarliestBlockHash:   cmbytes.HexBytes(initialHeader.DataHash),
			EarliestAppHash:     cmbytes.HexBytes(initialHeader.AppHash),
			EarliestBlockHeight: int64(initialHeader.Height()),
			EarliestBlockTime:   initialHeader.Time(),
			CatchingUp:          catchingUp,
		},
		ValidatorInfo: ctypes.ValidatorInfo{
			Address:     validator.Address,
			PubKey:      validator.PubKey,
			VotingPower: validator.VotingPower,
		},
	}

	return result, nil
}

// isAttesterNode checks if this node is running in attester mode
func isAttesterNode() bool {
	return env.AttesterMode
}

// getLastAttestedHeight returns the highest block height attested by this validator
func getLastAttestedHeight(ctx *rpctypes.Context) (uint64, error) {
	// Create protobuf request for LastAttestedHeight query (O(1) operation)
	req := &networktypes.QueryLastAttestedHeightRequest{}

	// Marshal the protobuf request
	reqBytes, err := req.Marshal()
	if err != nil {
		return 0, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Use ABCI Query with the new endpoint
	res, err := env.Adapter.App.Query(ctx.Context(), &abci.RequestQuery{
		Path: "/evabci.network.v1.Query/LastAttestedHeight",
		Data: reqBytes,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to query last attested height: %w", err)
	}
	if res.Code != 0 {
		return 0, fmt.Errorf("query failed with code %d: %s", res.Code, res.Log)
	}

	// Unmarshal the protobuf response
	if len(res.Value) > 0 {
		var response networktypes.QueryLastAttestedHeightResponse
		if err := response.Unmarshal(res.Value); err != nil {
			return 0, fmt.Errorf("failed to unmarshal response: %w", err)
		}
		return uint64(response.Height), nil
	}

	// If no response value, return 0
	return 0, nil
}
