package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtstateproto "github.com/cometbft/cometbft/proto/tendermint/state"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmtstate "github.com/cometbft/cometbft/state"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/gogoproto/proto"
	ds "github.com/ipfs/go-datastore"
	kt "github.com/ipfs/go-datastore/keytransform"
)

const (
	// keyPrefix is the prefix used for all ABCI-related keys in the datastore
	keyPrefix = "abci"
	// stateKey is the key used for storing state
	stateKey = "s"
	// blockResponseKey is the key used for storing block responses
	blockResponseKey = "br"
	// blockIDKey is the key used for storing block IDs
	blockIDKey = "bid"
	// lastPrunedHeightKey tracks the highest height that has been pruned.
	// This makes pruning idempotent and allows incremental pruning across
	// multiple calls.
	lastPrunedHeightKey = "lph"
)

// Store wraps a datastore with ABCI-specific functionality
type Store struct {
	prefixedStore ds.Batching
}

// NewExecABCIStore creates a new Store with the ABCI prefix.
// The data is stored under ev-node database and not in the app's database.
func NewExecABCIStore(store ds.Batching) *Store {
	return &Store{
		prefixedStore: kt.Wrap(store, &kt.PrefixTransform{
			Prefix: ds.NewKey(keyPrefix),
		}),
	}
}

// LoadState loads the state from disk.
// When the state does not exist, it returns an empty state.
func (s *Store) LoadState(ctx context.Context) (*cmtstate.State, error) {
	data, err := s.prefixedStore.Get(ctx, ds.NewKey(stateKey))
	if err != nil {
		return nil, fmt.Errorf("failed to get state metadata: %w", err)
	}

	if data == nil {
		return &cmtstate.State{}, nil
	}

	stateProto := &cmtstateproto.State{}
	if err := proto.Unmarshal(data, stateProto); err != nil {
		return nil, fmt.Errorf("failed to unmarshal state: %w", err)
	}

	return cmtstate.FromProto(stateProto)
}

// SaveState saves the state to disk
func (s *Store) SaveState(ctx context.Context, state *cmtstate.State) error {
	stateProto, err := state.ToProto()
	if err != nil {
		return fmt.Errorf("failed to convert state to proto: %w", err)
	}

	data, err := proto.Marshal(stateProto)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	return s.prefixedStore.Put(ctx, ds.NewKey(stateKey), data)
}

// SaveBlockID saves the block ID to disk per height.
// This is used to store the block ID for the block execution
func (s *Store) SaveBlockID(ctx context.Context, height uint64, blockID *cmttypes.BlockID) error {
	blockIDProto := blockID.ToProto()
	data, err := proto.Marshal(&blockIDProto)
	if err != nil {
		return fmt.Errorf("failed to marshal block ID: %w", err)
	}

	key := ds.NewKey(blockIDKey).ChildString(strconv.FormatUint(height, 10))
	return s.prefixedStore.Put(ctx, key, data)
}

// GetBlockID loads the block ID from disk for a specific height.
func (s *Store) GetBlockID(ctx context.Context, height uint64) (*cmttypes.BlockID, error) {
	key := ds.NewKey(blockIDKey).ChildString(strconv.FormatUint(height, 10))
	data, err := s.prefixedStore.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get block ID %d: %w", height, err)
	}

	if data == nil {
		return nil, fmt.Errorf("block ID not found for height %d", height)
	}

	protoBlockID := &cmtproto.BlockID{}
	if err := proto.Unmarshal(data, protoBlockID); err != nil {
		return nil, fmt.Errorf("failed to unmarshal block ID: %w", err)
	}

	blockID, err := cmttypes.BlockIDFromProto(protoBlockID)
	if err != nil {
		return nil, fmt.Errorf("failed to convert block ID from proto: %w", err)
	}

	return blockID, nil
}

// SaveBlockResponse saves the block response to disk per height
// This is used to store the results of the block execution
// so that they can be retrieved later, e.g., for querying transaction results.
func (s *Store) SaveBlockResponse(ctx context.Context, height uint64, resp *abci.ResponseFinalizeBlock) error {
	data, err := proto.Marshal(resp)
	if err != nil {
		return fmt.Errorf("failed to marshal block response: %w", err)
	}

	key := ds.NewKey(blockResponseKey).ChildString(strconv.FormatUint(height, 10))
	return s.prefixedStore.Put(ctx, key, data)
}

// GetBlockResponse loads the block response from disk for a specific height
// If the block response does not exist, it returns an error.
func (s *Store) GetBlockResponse(ctx context.Context, height uint64) (*abci.ResponseFinalizeBlock, error) {
	key := ds.NewKey(blockResponseKey).ChildString(strconv.FormatUint(height, 10))
	data, err := s.prefixedStore.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get block response: %w", err)
	}

	if data == nil {
		return nil, fmt.Errorf("block response not found for height %d", height)
	}

	resp := &abci.ResponseFinalizeBlock{}
	if err := proto.Unmarshal(data, resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal block response: %w", err)
	}

	return resp, nil
}

// Prune deletes per-height ABCI execution metadata (block IDs and block
// responses) for all heights up to and including the provided target
// height. The current ABCI state (stored under stateKey) is never pruned,
// as it is maintained separately by the application.
//
// Pruning is idempotent: the store tracks the highest pruned height and
// will skip work for already-pruned ranges.
func (s *Store) Prune(ctx context.Context, height uint64) error {
	// Load the last pruned height, if any.
	data, err := s.prefixedStore.Get(ctx, ds.NewKey(lastPrunedHeightKey))
	if err != nil {
		if !errors.Is(err, ds.ErrNotFound) {
			return fmt.Errorf("failed to get last pruned height: %w", err)
		}
	}

	var lastPruned uint64
	if len(data) > 0 {
		lastPruned, err = strconv.ParseUint(string(data), 10, 64)
		if err != nil {
			return fmt.Errorf("invalid last pruned height value %q: %w", string(data), err)
		}
	}

	// Nothing to do if we've already pruned up to at least this height.
	if height <= lastPruned {
		return nil
	}

	// Use a batch to atomically delete all per-height ABCI metadata and
	// update the last pruned height in a single transaction. This avoids
	// leaving the store in a partially pruned state if an error occurs
	// midway through the operation.
	batch, err := s.prefixedStore.Batch(ctx)
	if err != nil {
		return fmt.Errorf("failed to create batch for pruning: %w", err)
	}

	// Delete per-height ABCI metadata (block IDs and block responses) for
	// heights in (lastPruned, height]. Missing keys are ignored.
	for h := lastPruned + 1; h <= height; h++ {
		hStr := strconv.FormatUint(h, 10)
		bidKey := ds.NewKey(blockIDKey).ChildString(hStr)
		if err := batch.Delete(ctx, bidKey); err != nil && !errors.Is(err, ds.ErrNotFound) {
			return fmt.Errorf("failed to add block ID deletion to batch at height %d: %w", h, err)
		}

		brKey := ds.NewKey(blockResponseKey).ChildString(hStr)
		if err := batch.Delete(ctx, brKey); err != nil && !errors.Is(err, ds.ErrNotFound) {
			return fmt.Errorf("failed to add block response deletion to batch at height %d: %w", h, err)
		}
	}

	// Persist the updated last pruned height in the same batch.
	if err := batch.Put(ctx, ds.NewKey(lastPrunedHeightKey), []byte(strconv.FormatUint(height, 10))); err != nil {
		return fmt.Errorf("failed to add last pruned height update to batch: %w", err)
	}

	if err := batch.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit pruning batch: %w", err)
	}

	return nil
}
