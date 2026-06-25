package core

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"sort"

	abci "github.com/cometbft/cometbft/abci/types"
	cmbytes "github.com/cometbft/cometbft/libs/bytes"
	cmquery "github.com/cometbft/cometbft/libs/pubsub/query"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	ctypes "github.com/cometbft/cometbft/rpc/core/types"
	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
	cmttypes "github.com/cometbft/cometbft/types"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	"github.com/cosmos/gogoproto/proto"

	storepkg "github.com/evstack/ev-node/pkg/store"
	rlktypes "github.com/evstack/ev-node/types"

	networktypes "github.com/evstack/ev-abci/modules/network/types"
	"github.com/evstack/ev-abci/pkg/adapter"
)

// BlockSearch searches for a paginated set of blocks matching PreBlock and
// EndBlock event search criteria.
func BlockSearch(
	ctx *rpctypes.Context,
	query string,
	pagePtr, perPagePtr *int,
	orderBy string,
) (*ctypes.ResultBlockSearch, error) {
	wrappedCtx := ctx.Context()

	q, err := cmquery.New(query)
	if err != nil {
		return nil, err
	}

	results, err := env.BlockIndexer.Search(wrappedCtx, q)
	if err != nil {
		return nil, err
	}

	// Sort the results
	switch orderBy {
	case "desc":
		sort.Slice(results, func(i, j int) bool {
			return results[i] > results[j]
		})

	case "asc", "":
		slices.Sort(results)
	default:
		return nil, errors.New("expected order_by to be either `asc` or `desc` or empty")
	}

	// Paginate
	totalCount := len(results)
	perPageVal := validatePerPage(perPagePtr)

	pageVal, err := validatePage(pagePtr, perPageVal, totalCount)
	if err != nil {
		return nil, err
	}

	skipCount := validateSkipCount(pageVal, perPageVal)
	pageSize := min(perPageVal, totalCount-skipCount)

	blocks := make([]*ctypes.ResultBlock, 0, pageSize)
	for i := skipCount; i < skipCount+pageSize; i++ {
		header, data, err := env.Adapter.RollkitStore.GetBlockData(wrappedCtx, uint64(results[i]))
		if err != nil {
			return nil, err
		}

		lastCommit, err := env.Adapter.GetLastCommit(wrappedCtx, uint64(results[i]))
		if err != nil {
			return nil, fmt.Errorf("failed to get last commit for block %d: %w", results[i], err)
		}

		abciHeader, err := adapter.ToABCIHeader(header.Header, lastCommit)
		if err != nil {
			return nil, fmt.Errorf("failed to convert header to ABCI format: %w", err)
		}

		abciBlock, err := adapter.ToABCIBlock(abciHeader, lastCommit, data)
		if err != nil {
			return nil, err
		}

		blockParts, err := abciBlock.MakePartSet(cmttypes.BlockPartSizeBytes)
		if err != nil {
			return nil, fmt.Errorf("make part set: %w", err)
		}

		blocks = append(blocks, &ctypes.ResultBlock{
			Block: abciBlock,
			BlockID: cmttypes.BlockID{
				Hash:          abciHeader.Hash(),
				PartSetHeader: blockParts.Header(),
			},
		})
	}

	return &ctypes.ResultBlockSearch{Blocks: blocks, TotalCount: totalCount}, nil
}

// Block gets block at a given height.
// If no height is provided, it will fetch the latest block.
// More: https://docs.cometbft.com/v0.37/rpc/#/Info/block
func Block(ctx *rpctypes.Context, heightPtr *int64) (*ctypes.ResultBlock, error) {
	var (
		heightValue uint64
		err         error
	)

	switch {
	case heightPtr != nil && *heightPtr == -1:
		rawVal, err := env.Adapter.RollkitStore.GetMetadata(
			ctx.Context(),
			storepkg.DAIncludedHeightKey,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to get DA included height: %w", err)
		}

		if len(rawVal) != 8 {
			return nil, fmt.Errorf("invalid finalized height data length: %d", len(rawVal))
		}

		heightValue = binary.LittleEndian.Uint64(rawVal)
	default:
		heightValue, err = normalizeHeight(ctx.Context(), heightPtr)
		if err != nil {
			return nil, err
		}
	}

	blockMeta, block := getBlockMeta(ctx.Context(), heightValue)
	if blockMeta == nil {
		return &ctypes.ResultBlock{
			BlockID: cmttypes.BlockID{},
			Block:   block,
		}, nil
	}

	// Use the same BlockID that the sequencer uses from store
	// The sequencer uses store.GetBlockID() which has the original PartSetHeader
	storedBlockID, err := env.Adapter.Store.GetBlockID(ctx.Context(), heightValue)
	var actualBlockID cmttypes.BlockID
	if err != nil || heightValue <= 1 {
		// For height <= 1 or errors, use empty BlockID like sequencer does
		actualBlockID = cmttypes.BlockID{}
	} else {
		// Convert from proto to types like the sequencer does
		protoBlockID := storedBlockID.ToProto()
		actualBlockID = cmttypes.BlockID{
			Hash: protoBlockID.Hash,
			PartSetHeader: cmttypes.PartSetHeader{
				Total: protoBlockID.PartSetHeader.Total,
				Hash:  protoBlockID.PartSetHeader.Hash,
			},
		}
	}

	return &ctypes.ResultBlock{
		BlockID: actualBlockID,
		Block:   block,
	}, nil
}

// BlockByHash gets block by hash.
// More: https://docs.cometbft.com/v0.37/rpc/#/Info/block_by_hash
func BlockByHash(ctx *rpctypes.Context, hash []byte) (*ctypes.ResultBlock, error) {
	header, data, err := env.Adapter.RollkitStore.GetBlockByHash(ctx.Context(), rlktypes.Hash(hash))
	if err != nil {
		return nil, err
	}

	lastCommit, err := env.Adapter.GetLastCommit(ctx.Context(), header.Height())
	if err != nil {
		return nil, fmt.Errorf("failed to get last commit for block %d: %w", header.Height(), err)
	}

	abciHeader, err := adapter.ToABCIHeader(header.Header, lastCommit)
	if err != nil {
		return nil, fmt.Errorf("failed to convert header to ABCI format: %w", err)
	}

	abciBlock, err := adapter.ToABCIBlock(abciHeader, lastCommit, data)
	if err != nil {
		return nil, err
	}

	blockParts, err := abciBlock.MakePartSet(cmttypes.BlockPartSizeBytes)
	if err != nil {
		return nil, fmt.Errorf("make part set: %w", err)
	}

	return &ctypes.ResultBlock{
		BlockID: cmttypes.BlockID{
			Hash:          abciHeader.Hash(),
			PartSetHeader: blockParts.Header(),
		},
		Block: abciBlock,
	}, nil
}

// Commit gets block commit at a given height.
// If no height is provided, it will fetch the commit for the latest block.
// More: https://docs.cometbft.com/main/rpc/#/Info/commit
func Commit(ctx *rpctypes.Context, heightPtr *int64) (*ctypes.ResultCommit, error) {
	height, err := normalizeHeight(ctx.Context(), heightPtr)
	if err != nil {
		return nil, err
	}

	blockMeta, _ := getBlockMeta(ctx.Context(), height)
	if blockMeta == nil {
		return nil, nil
	}
	abciHeader := blockMeta.Header

	// get current commit (use attester signatures if in attester mode)
	commit, err := getCommitForHeight(ctx.Context(), height)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit for height %d: %w", height, err)
	}

	return ctypes.NewResultCommit(&abciHeader, commit, true), nil
}

// BlockResults gets block results at a given height.
// If no height is provided, it will fetch the results for the latest block.
func BlockResults(ctx *rpctypes.Context, heightPtr *int64) (*ctypes.ResultBlockResults, error) {
	height, err := normalizeHeight(ctx.Context(), heightPtr)
	if err != nil {
		return nil, err
	}

	resp, err := env.Adapter.Store.GetBlockResponse(ctx.Context(), height)
	if err != nil {
		return nil, err
	}

	return &ctypes.ResultBlockResults{
		Height:                int64(height),
		TxsResults:            resp.TxResults,
		FinalizeBlockEvents:   resp.Events,
		ValidatorUpdates:      resp.ValidatorUpdates,
		ConsensusParamUpdates: resp.ConsensusParamUpdates,
		AppHash:               resp.AppHash,
	}, nil
}

// Header gets block header at a given height.
// If no height is provided, it will fetch the latest header.
// More: https://docs.cometbft.com/v0.37/rpc/#/Info/header
func Header(ctx *rpctypes.Context, heightPtr *int64) (*ctypes.ResultHeader, error) {
	height, err := normalizeHeight(ctx.Context(), heightPtr)
	if err != nil {
		return nil, err
	}

	blockMeta, _ := getBlockMeta(ctx.Context(), height)
	if blockMeta == nil {
		return nil, fmt.Errorf("block at height %d not found", height)
	}

	return &ctypes.ResultHeader{Header: &blockMeta.Header}, nil
}

// HeaderByHash gets header by hash.
// More: https://docs.cometbft.com/v0.37/rpc/#/Info/header_by_hash
func HeaderByHash(ctx *rpctypes.Context, hash cmbytes.HexBytes) (*ctypes.ResultHeader, error) {
	// N.B. The hash parameter is HexBytes so that the reflective parameter
	// decoding logic in the HTTP service will correctly translate from JSON.
	// See https://github.com/cometbft/cometbft/issues/6802 for context.

	header, _, err := env.Adapter.RollkitStore.GetBlockByHash(ctx.Context(), rlktypes.Hash(hash))
	if err != nil {
		return nil, err
	}

	lastCommit, err := env.Adapter.GetLastCommit(ctx.Context(), header.Height())
	if err != nil {
		return nil, fmt.Errorf("failed to get last commit for block %d: %w", header.Height(), err)
	}

	abciHeader, err := adapter.ToABCIHeader(header.Header, lastCommit)
	if err != nil {
		return nil, fmt.Errorf("failed to convert header to ABCI format: %w", err)
	}

	return &ctypes.ResultHeader{Header: &abciHeader}, nil
}

// BlockchainInfo gets block headers for minHeight <= height <= maxHeight.
// Block headers are returned in descending order (highest first).
// More: https://docs.cometbft.com/v0.37/rpc/#/Info/blockchain
func BlockchainInfo(ctx *rpctypes.Context, minHeight, maxHeight int64) (*ctypes.ResultBlockchainInfo, error) {
	const limit int64 = 20

	height, err := env.Adapter.RollkitStore.Height(ctx.Context())
	if err != nil {
		return nil, err
	}

	// Currently blocks are not pruned and are synced linearly so the base height is 0.
	minHeight, maxHeight, err = filterMinMax(
		0,
		int64(height), //nolint:gosec
		minHeight,
		maxHeight,
		limit)
	if err != nil {
		return nil, err
	}
	env.Logger.Debug("BlockchainInfo", "maxHeight", maxHeight, "minHeight", minHeight)

	blockMetas := []*cmttypes.BlockMeta{}
	for height := maxHeight; height >= minHeight; height-- {
		blockMeta, _ := getBlockMeta(ctx.Context(), uint64(height))
		blockMetas = append(blockMetas, blockMeta)
	}

	return &ctypes.ResultBlockchainInfo{
		LastHeight: int64(height), //nolint:gosec
		BlockMetas: blockMetas,
	}, nil
}

// getCommitForHeight returns a deterministic cmttypes.Commit for height.
// In attester mode it builds the commit from the ordered attester set, placing
// BlockIDFlagAbsent for non-signers and refusing to return until 2/3 quorum is met.
func getCommitForHeight(ctx context.Context, height uint64) (*cmttypes.Commit, error) {
	if !env.AttesterMode {
		return env.Adapter.GetLastCommit(ctx, height+1)
	}

	blockID, err := env.Adapter.Store.GetBlockID(ctx, height)
	if err != nil {
		return nil, fmt.Errorf("get block ID for height %d: %w", height, err)
	}

	entries, err := getAttesterSet(ctx)
	if err != nil {
		return nil, fmt.Errorf("get attester set: %w", err)
	}
	signatures, err := getAttesterSignatures(ctx, int64(height))
	if err != nil {
		return nil, fmt.Errorf("get attester signatures: %w", err)
	}

	commitSigs := make([]cmttypes.CommitSig, 0, len(entries))
	signedCount := 0
	for _, e := range entries {
		voteBytes, ok := signatures[e.ConsensusAddress]
		if !ok {
			commitSigs = append(commitSigs, cmttypes.CommitSig{BlockIDFlag: cmttypes.BlockIDFlagAbsent})
			continue
		}
		var vote cmtproto.Vote
		if err := proto.Unmarshal(voteBytes, &vote); err != nil {
			commitSigs = append(commitSigs, cmttypes.CommitSig{BlockIDFlag: cmttypes.BlockIDFlagAbsent})
			continue
		}
		commitSigs = append(commitSigs, cmttypes.CommitSig{
			BlockIDFlag:      cmttypes.BlockIDFlagCommit,
			ValidatorAddress: e.ValidatorAddress,
			Timestamp:        vote.Timestamp,
			Signature:        vote.Signature,
		})
		signedCount++
	}

	total := len(entries)
	if signedCount*3 <= total*2 {
		return nil, fmt.Errorf("height %d not yet attested (signed %d of %d)", height, signedCount, total)
	}

	return &cmttypes.Commit{
		Height:     int64(height), //nolint:gosec
		Round:      0,
		BlockID:    *blockID,
		Signatures: commitSigs,
	}, nil
}

// attesterSetEntry holds an ordered attester set entry used for commit reconstruction.
type attesterSetEntry struct {
	ConsensusAddress string
	ValidatorAddress []byte
	Pubkey           cryptotypes.PubKey
}

// getAttesterSet fetches the ordered attester set from the network module via ABCI query.
func getAttesterSet(ctx context.Context) ([]attesterSetEntry, error) {
	req, err := proto.Marshal(&networktypes.QueryAttesterSetRequest{})
	if err != nil {
		return nil, err
	}
	result, err := env.Adapter.App.Query(ctx, &abci.RequestQuery{
		Path: "/evabci.network.v1.Query/AttesterSet",
		Data: req,
	})
	if err != nil {
		return nil, err
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("query AttesterSet failed: %s", result.Log)
	}
	var resp networktypes.QueryAttesterSetResponse
	if err := proto.Unmarshal(result.Value, &resp); err != nil {
		return nil, err
	}
	sort.Slice(resp.Entries, func(i, j int) bool { return resp.Entries[i].Index < resp.Entries[j].Index })

	out := make([]attesterSetEntry, 0, len(resp.Entries))
	for _, e := range resp.Entries {
		var pk cryptotypes.PubKey
		if err := networktypes.ModuleCdc.InterfaceRegistry().UnpackAny(e.Pubkey, &pk); err != nil {
			return nil, fmt.Errorf("unpack pubkey for %s: %w", e.ConsensusAddress, err)
		}
		out = append(out, attesterSetEntry{
			ConsensusAddress: e.ConsensusAddress,
			ValidatorAddress: pk.Address(),
			Pubkey:           pk,
		})
	}
	return out, nil
}

// getAttesterSignatures queries the network module to get all attester signatures for a height.
func getAttesterSignatures(ctx context.Context, height int64) (map[string][]byte, error) {
	signaturesReq, err := proto.Marshal(&networktypes.QueryAttesterSignaturesRequest{Height: height})
	if err != nil {
		return nil, fmt.Errorf("marshal attester signatures request: %w", err)
	}

	result, err := env.Adapter.App.Query(ctx, &abci.RequestQuery{
		Path: "/evabci.network.v1.Query/AttesterSignatures",
		Data: signaturesReq,
	})
	if err != nil {
		return make(map[string][]byte), nil
	}

	if result.Code != 0 {
		return make(map[string][]byte), nil
	}

	var signaturesResp networktypes.QueryAttesterSignaturesResponse
	if err := proto.Unmarshal(result.Value, &signaturesResp); err != nil {
		return nil, fmt.Errorf("unmarshal attester signatures response: %w", err)
	}

	signatures := make(map[string][]byte)
	for _, sig := range signaturesResp.Signatures {
		signatures[sig.ValidatorAddress] = sig.Signature
	}

	return signatures, nil
}

// GetCommitForHeightForTest is exported only for tests in this package.
func GetCommitForHeightForTest(ctx context.Context, e *Environment, height uint64) (*cmttypes.Commit, error) {
	previousEnv := env
	env = e
	defer func() { env = previousEnv }()
	return getCommitForHeight(ctx, height)
}
