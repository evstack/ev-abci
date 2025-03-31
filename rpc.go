package goexecution

import (
	"context"
	"errors"
	"fmt"
	"sort"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtbytes "github.com/cometbft/cometbft/libs/bytes"
	cmtmath "github.com/cometbft/cometbft/libs/math"
	cmtquery "github.com/cometbft/cometbft/libs/pubsub/query"
	"github.com/cometbft/cometbft/p2p"
	rpcclient "github.com/cometbft/cometbft/rpc/client"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	"github.com/cometbft/cometbft/state/indexer"
	blockidxnull "github.com/cometbft/cometbft/state/indexer/block/null"

	"github.com/cometbft/cometbft/state/txindex"
	"github.com/cometbft/cometbft/state/txindex/null"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/rollkit/go-execution-abci/mempool"

	"github.com/cosmos/cosmos-sdk/client"
)

const (
	// maxQueryLength is the maximum length of a query string that will be
	// accepted. This is just a safety check to avoid outlandish queries.
	maxQueryLength = 512

	defaultPerPage = 30
	maxPerPage     = 100
)

type RPCServer struct {
	adapter      *Adapter
	txIndexer    txindex.TxIndexer
	blockIndexer indexer.BlockIndexer
}

var _ client.CometRPC = &RPCServer{}

// ABCIInfo implements client.CometRPC.
func (r *RPCServer) ABCIInfo(context.Context) (*coretypes.ResultABCIInfo, error) {
	info, err := r.adapter.app.Info(&abci.RequestInfo{})
	if err != nil {
		return nil, err
	}
	return &coretypes.ResultABCIInfo{
		Response: *info,
	}, nil
}

// ABCIQuery implements client.CometRPC.
func (r *RPCServer) ABCIQuery(ctx context.Context, path string, data cmtbytes.HexBytes) (*coretypes.ResultABCIQuery, error) {
	resp, err := r.adapter.app.Query(ctx, &abci.RequestQuery{
		Data: data,
		Path: path,
	})
	if err != nil {
		return nil, err
	}
	return &coretypes.ResultABCIQuery{
		Response: *resp,
	}, nil
}

// ABCIQueryWithOptions implements client.CometRPC.
func (r *RPCServer) ABCIQueryWithOptions(ctx context.Context, path string, data cmtbytes.HexBytes, opts rpcclient.ABCIQueryOptions) (*coretypes.ResultABCIQuery, error) {
	resp, err := r.adapter.app.Query(ctx, &abci.RequestQuery{
		Data:   data,
		Path:   path,
		Height: opts.Height,
		Prove:  opts.Prove,
	})
	if err != nil {
		return nil, err
	}
	return &coretypes.ResultABCIQuery{
		Response: *resp,
	}, nil
}

// Block implements client.CometRPC.
func (r *RPCServer) Block(ctx context.Context, height *int64) (*coretypes.ResultBlock, error) {
	var heightValue uint64

	switch {
	// block tag = included
	case height != nil && *height == -1:
		// heightValue = r.adapter.store.GetDAIncludedHeight()
		// TODO: implement
		return nil, errors.New("DA included height not implemented")
	default:
		heightValue = r.normalizeHeight(height)
	}
	header, data, err := r.adapter.store.GetBlockData(ctx, heightValue)
	if err != nil {
		return nil, err
	}

	hash := header.Hash()
	abciBlock, err := ToABCIBlock(header, data)
	if err != nil {
		return nil, err
	}
	return &coretypes.ResultBlock{
		BlockID: cmttypes.BlockID{
			Hash: cmtbytes.HexBytes(hash),
			PartSetHeader: cmttypes.PartSetHeader{
				Total: 0,
				Hash:  nil,
			},
		},
		Block: abciBlock,
	}, nil
}

// BlockByHash implements client.CometRPC.
func (r *RPCServer) BlockByHash(ctx context.Context, hash []byte) (*coretypes.ResultBlock, error) {
	header, data, err := r.adapter.store.GetBlockByHash(ctx, hash)
	if err != nil {
		return nil, err
	}

	abciBlock, err := ToABCIBlock(header, data)
	if err != nil {
		return nil, err
	}
	return &coretypes.ResultBlock{
		BlockID: cmttypes.BlockID{
			Hash: cmtbytes.HexBytes(hash),
			PartSetHeader: cmttypes.PartSetHeader{
				Total: 0,
				Hash:  nil,
			},
		},
		Block: abciBlock,
	}, nil
}

// BlockResults implements client.CometRPC.
func (r *RPCServer) BlockResults(ctx context.Context, height *int64) (*coretypes.ResultBlockResults, error) {
	var h uint64
	if height == nil {
		h = r.adapter.store.Height()
	} else {
		h = uint64(*height)
	}
	header, _, err := r.adapter.store.GetBlockData(ctx, h)
	if err != nil {
		return nil, err
	}
	resp, err := r.adapter.store.GetBlockResponses(ctx, h)
	if err != nil {
		return nil, err
	}

	return &coretypes.ResultBlockResults{
		Height:                int64(h), //nolint:gosec
		TxsResults:            resp.TxResults,
		FinalizeBlockEvents:   resp.Events,
		ValidatorUpdates:      resp.ValidatorUpdates,
		ConsensusParamUpdates: resp.ConsensusParamUpdates,
		AppHash:               header.Header.AppHash,
	}, nil
}

// BlockSearch implements client.CometRPC.
func (r *RPCServer) BlockSearch(ctx context.Context, query string, pagePtr *int, perPagePtr *int, orderBy string) (*coretypes.ResultBlockSearch, error) {
	// skip if block indexing is disabled
	if _, ok := r.blockIndexer.(*blockidxnull.BlockerIndexer); ok {
		return nil, errors.New("block indexing is disabled")
	}

	q, err := cmtquery.New(query)
	if err != nil {
		return nil, err
	}

	results, err := r.blockIndexer.Search(ctx, q)
	if err != nil {
		return nil, err
	}

	// sort results (must be done before pagination)
	switch orderBy {
	case "desc", "":
		sort.Slice(results, func(i, j int) bool { return results[i] > results[j] })

	case "asc":
		sort.Slice(results, func(i, j int) bool { return results[i] < results[j] })

	default:
		return nil, errors.New("expected order_by to be either `asc` or `desc` or empty")
	}

	// paginate results
	totalCount := len(results)
	perPage := validatePerPage(perPagePtr)

	page, err := validatePage(pagePtr, perPage, totalCount)
	if err != nil {
		return nil, err
	}

	skipCount := validateSkipCount(page, perPage)
	pageSize := cmtmath.MinInt(perPage, totalCount-skipCount)

	apiResults := make([]*coretypes.ResultBlock, 0, pageSize)
	for i := skipCount; i < skipCount+pageSize; i++ {
		header, data, err := r.adapter.store.GetBlockData(ctx, uint64(results[i]))
		if err != nil {
			return nil, err
		}
		block, err := ToABCIBlock(header, data)
		if err != nil {
			return nil, err
		}
		apiResults = append(apiResults, &coretypes.ResultBlock{
			Block: block,
			BlockID: cmttypes.BlockID{
				Hash: block.Hash(),
			},
		})
	}

	return &coretypes.ResultBlockSearch{Blocks: apiResults, TotalCount: totalCount}, nil
}

// BlockchainInfo implements client.CometRPC.
func (r *RPCServer) BlockchainInfo(ctx context.Context, minHeight int64, maxHeight int64) (*coretypes.ResultBlockchainInfo, error) {
	const limit int64 = 20

	// Currently blocks are not pruned and are synced linearly so the base height is 0
	minHeight, maxHeight, err := filterMinMax(
		0,
		int64(r.adapter.store.Height()), //nolint:gosec
		minHeight,
		maxHeight,
		limit)
	if err != nil {
		return nil, err
	}

	blocks := make([]*cmttypes.BlockMeta, 0, maxHeight-minHeight+1)
	for height := maxHeight; height >= minHeight; height-- {
		header, data, err := r.adapter.store.GetBlockData(ctx, uint64(height))
		if err != nil {
			return nil, err
		}
		if header != nil && data != nil {
			cmblockmeta, err := ToABCIBlockMeta(header, data)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, cmblockmeta)
		}
	}

	return &coretypes.ResultBlockchainInfo{
		LastHeight: int64(r.adapter.store.Height()), //nolint:gosec
		BlockMetas: blocks,
	}, nil
}

// BroadcastTxAsync implements client.CometRPC.
func (r *RPCServer) BroadcastTxAsync(ctx context.Context, tx cmttypes.Tx) (*coretypes.ResultBroadcastTx, error) {
	err := r.adapter.mempool.CheckTx(tx, nil, mempool.TxInfo{})
	if err != nil {
		return nil, err
	}
	return &coretypes.ResultBroadcastTx{
		Code: abci.CodeTypeOK,
		Hash: tx.Hash(),
	}, nil
}

// BroadcastTxCommit implements client.CometRPC.
func (r *RPCServer) BroadcastTxCommit(ctx context.Context, tx cmttypes.Tx) (*coretypes.ResultBroadcastTxCommit, error) {
	panic("unimplemented")
}

// BroadcastTxSync implements client.CometRPC.
func (r *RPCServer) BroadcastTxSync(ctx context.Context, tx cmttypes.Tx) (*coretypes.ResultBroadcastTx, error) {
	resCh := make(chan *abci.ResponseCheckTx, 1)
	err := r.adapter.mempool.CheckTx(tx, func(res *abci.ResponseCheckTx) {
		select {
		case <-ctx.Done():
			return
		case resCh <- res:
		}
	}, mempool.TxInfo{})
	if err != nil {
		return nil, err
	}
	res := <-resCh

	// gossip the transaction if it's in the mempool.
	// Note: we have to do this here because, unlike the tendermint mempool reactor, there
	// is no routine that gossips transactions after they enter the pool
	if res.Code == abci.CodeTypeOK {
		// TODO: implement gossiping
		// err = r.adapter.p2pClient.GossipTx(ctx, tx)
		if err != nil {
			// the transaction must be removed from the mempool if it cannot be gossiped.
			// if this does not occur, then the user will not be able to try again using
			// this node, as the CheckTx call above will return an error indicating that
			// the tx is already in the mempool
			_ = r.adapter.mempool.RemoveTxByKey(tx.Key())
			return nil, fmt.Errorf("failed to gossip tx: %w", err)
		}
	}

	return &coretypes.ResultBroadcastTx{
		Code:      res.Code,
		Data:      res.Data,
		Log:       res.Log,
		Codespace: res.Codespace,
		Hash:      tx.Hash(),
	}, nil
}

// Commit implements client.CometRPC.
func (r *RPCServer) Commit(ctx context.Context, height *int64) (*coretypes.ResultCommit, error) {
	heightValue := r.normalizeHeight(height)
	header, data, err := r.adapter.store.GetBlockData(ctx, heightValue)
	if err != nil {
		return nil, err
	}

	// we should have a single validator
	if len(header.Validators.Validators) == 0 {
		return nil, errors.New("empty validator set found in block")
	}

	val := header.Validators.Validators[0].Address
	commit := GetABCICommit(heightValue, header.Hash(), val, header.Time(), header.Signature)

	block, err := ToABCIBlock(header, data)
	if err != nil {
		return nil, err
	}

	return coretypes.NewResultCommit(&block.Header, commit, true), nil
}

// Status implements client.CometRPC.
func (r *RPCServer) Status(ctx context.Context) (*coretypes.ResultStatus, error) {
	info, err := r.adapter.app.Info(&abci.RequestInfo{})
	if err != nil {
		return nil, err
	}

	s := r.adapter.state.Load()

	return &coretypes.ResultStatus{
		NodeInfo: p2p.DefaultNodeInfo{}, // TODO: fill this in
		SyncInfo: coretypes.SyncInfo{
			LatestBlockHash:   cmtbytes.HexBytes(info.LastBlockAppHash),
			LatestBlockHeight: info.LastBlockHeight,
			// LatestBlockTime:   s.LastBlockTime, // TODO: fill this in
		},
		ValidatorInfo: coretypes.ValidatorInfo{
			Address:     s.Validators.Proposer.Address,
			PubKey:      s.Validators.Proposer.PubKey,
			VotingPower: s.Validators.Proposer.VotingPower,
		},
	}, nil
}

// Tx implements client.CometRPC.
func (r *RPCServer) Tx(ctx context.Context, hash []byte, prove bool) (*coretypes.ResultTx, error) {
	res, err := r.txIndexer.Get(hash)
	if err != nil {
		return nil, err
	}

	if res == nil {
		return nil, fmt.Errorf("tx (%X) not found", hash)
	}

	height := res.Height
	index := res.Index

	var proof cmttypes.TxProof
	if prove {
		_, data, _ := r.adapter.store.GetBlockData(ctx, uint64(height))
		blockProof := data.Txs.Proof(int(index)) // XXX: overflow on 32-bit machines
		proof = cmttypes.TxProof{
			RootHash: blockProof.RootHash,
			Data:     cmttypes.Tx(blockProof.Data),
			Proof:    blockProof.Proof,
		}
	}

	return &coretypes.ResultTx{
		Hash:     hash,
		Height:   height,
		Index:    index,
		TxResult: res.Result,
		Tx:       res.Tx,
		Proof:    proof,
	}, nil
}

// TxSearch implements client.CometRPC.
func (r *RPCServer) TxSearch(ctx context.Context, query string, prove bool, pagePtr *int, perPagePtr *int, orderBy string) (*coretypes.ResultTxSearch, error) {
	// if index is disabled, return error
	if _, ok := r.txIndexer.(*null.TxIndex); ok {
		return nil, errors.New("transaction indexing is disabled")
	} else if len(query) > maxQueryLength {
		return nil, errors.New("maximum query length exceeded")
	}

	q, err := cmtquery.New(query)
	if err != nil {
		return nil, err
	}

	results, err := r.txIndexer.Search(ctx, q)
	if err != nil {
		return nil, err
	}

	// sort results (must be done before pagination)
	switch orderBy {
	case "desc":
		sort.Slice(results, func(i, j int) bool {
			if results[i].Height == results[j].Height {
				return results[i].Index > results[j].Index
			}
			return results[i].Height > results[j].Height
		})
	case "asc", "":
		sort.Slice(results, func(i, j int) bool {
			if results[i].Height == results[j].Height {
				return results[i].Index < results[j].Index
			}
			return results[i].Height < results[j].Height
		})
	default:
		return nil, errors.New("expected order_by to be either `asc` or `desc` or empty")
	}

	// paginate results
	totalCount := len(results)
	perPage := validatePerPage(perPagePtr)

	page, err := validatePage(pagePtr, perPage, totalCount)
	if err != nil {
		return nil, err
	}

	skipCount := validateSkipCount(page, perPage)
	pageSize := cmtmath.MinInt(perPage, totalCount-skipCount)

	apiResults := make([]*coretypes.ResultTx, 0, pageSize)
	for i := skipCount; i < skipCount+pageSize; i++ {
		r := results[i]

		var proof cmttypes.TxProof
		if prove {
			// TODO: implement proofs?
			// block := env.BlockStore.LoadBlock(r.Height)
			// if block != nil {
			// 	proof = block.Data.Txs.Proof(int(r.Index))
			// }
		}

		apiResults = append(apiResults, &coretypes.ResultTx{
			Hash:     cmttypes.Tx(r.Tx).Hash(),
			Height:   r.Height,
			Index:    r.Index,
			TxResult: r.Result,
			Tx:       r.Tx,
			Proof:    proof,
		})
	}

	return &coretypes.ResultTxSearch{Txs: apiResults, TotalCount: totalCount}, nil
}

// Validators implements client.CometRPC.
func (r *RPCServer) Validators(ctx context.Context, height *int64, page *int, perPage *int) (*coretypes.ResultValidators, error) {
	s := r.adapter.state.Load()

	validators := s.Validators.Validators
	totalCount := len(validators)

	// Handle pagination
	start := 0
	end := totalCount
	if page != nil && perPage != nil {
		start = (*page - 1) * *perPage
		end = cmtmath.MinInt(start+*perPage, totalCount)

		if start >= totalCount {
			return &coretypes.ResultValidators{
				BlockHeight: int64(r.adapter.store.Height()),
				Validators:  []*cmttypes.Validator{},
				Total:       totalCount,
			}, nil
		}
		validators = validators[start:end]
	}

	return &coretypes.ResultValidators{
		BlockHeight: int64(r.adapter.store.Height()),
		Validators:  validators,
		Total:       totalCount,
	}, nil
}

//----------------------------------------------

func validateSkipCount(page, perPage int) int {
	skipCount := (page - 1) * perPage
	if skipCount < 0 {
		return 0
	}

	return skipCount
}

func validatePerPage(perPagePtr *int) int {
	if perPagePtr == nil { // no per_page parameter
		return defaultPerPage
	}

	perPage := *perPagePtr
	if perPage < 1 {
		return defaultPerPage
	} else if perPage > maxPerPage {
		return maxPerPage
	}
	return perPage
}

func validatePage(pagePtr *int, perPage, totalCount int) (int, error) {
	if perPage < 1 {
		panic(fmt.Sprintf("zero or negative perPage: %d", perPage))
	}

	if pagePtr == nil { // no page parameter
		return 1, nil
	}

	pages := ((totalCount - 1) / perPage) + 1
	if pages == 0 {
		pages = 1 // one page (even if it's empty)
	}
	page := *pagePtr
	if page <= 0 || page > pages {
		return 1, fmt.Errorf("page should be within [1, %d] range, given %d", pages, page)
	}

	return page, nil
}

func (r *RPCServer) normalizeHeight(height *int64) uint64 {
	var heightValue uint64
	if height == nil {
		heightValue = r.adapter.store.Height()
	} else {
		heightValue = uint64(*height)
	}

	return heightValue
}
