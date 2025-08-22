package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/cometbft/cometbft/crypto/ed25519"
	cmttypes "github.com/cometbft/cometbft/types"
	ds "github.com/ipfs/go-datastore"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/stretchr/testify/require"

	"github.com/evstack/ev-abci/pkg/store"
	"github.com/evstack/ev-node/types"
)

func TestSignatureCompatibility_HeaderAndCommit(t *testing.T) {
	// Create test key pair
	cmtPrivKey := ed25519.GenPrivKey()
	p2pPrivKey, err := crypto.UnmarshalEd25519PrivateKey(cmtPrivKey.Bytes())
	require.NoError(t, err)

	// Create a test store
	dsStore := ds.NewMapDatastore()
	storeOnlyAdapter := NewABCIExecutor(nil, dsStore, nil, nil, nil, nil, nil)

	validatorAddress := make([]byte, 20)
	chainID := "test-chain"

	// Create a test header
	header := &types.Header{
		BaseHeader: types.BaseHeader{
			Height:  1,
			Time:    uint64(time.Now().UnixNano()),
			ChainID: chainID,
		},
		ProposerAddress: validatorAddress,
	}

	// Test 1: AggregatorNodeSignatureBytesProvider should work without BlockID in store
	provider := AggregatorNodeSignatureBytesProvider(storeOnlyAdapter)
	signBytes, err := provider(header)
	require.NoError(t, err)
	require.NotEmpty(t, signBytes)

	syncProvider := SyncNodeSignatureBytesProvider(storeOnlyAdapter)
	signBytesSync, err := syncProvider(context.Background(), header, &types.Data{})
	require.NoError(t, err)
	require.NotEmpty(t, signBytesSync)

	require.Equal(t, signBytes, signBytesSync)

	// Test 2: Should be able to sign the payload
	signature, err := p2pPrivKey.Sign(signBytes)
	require.NoError(t, err)
	require.NotEmpty(t, signature)
	require.Equal(t, 64, len(signature)) // Ed25519 signature length

	// Test 3: Set up proper state and create consistent BlockID for both providers (at height > 2)
	state := store.TestingStateFixture()
	state.LastBlockHeight = 2
	err = storeOnlyAdapter.Store.SaveState(context.Background(), store.TestingStateFixture())
	require.NoError(t, err)

	// Create BlockID using the same method that SyncNodeSignatureBytesProvider uses
	currentState, err := storeOnlyAdapter.Store.LoadState(context.Background())
	require.NoError(t, err)

	lastCommit, err := storeOnlyAdapter.GetLastCommit(context.Background(), header.Height())
	require.NoError(t, err)

	abciHeader, err := ToABCIHeader(*header, lastCommit)
	require.NoError(t, err)

	cmtTxs := make(cmttypes.Txs, 0)
	_, blockID, err := MakeABCIBlock(header.Height(), cmtTxs, currentState, abciHeader, lastCommit)
	require.NoError(t, err)

	// Save the properly generated BlockID to the store
	err = storeOnlyAdapter.Store.SaveBlockID(context.Background(), header.Height(), blockID)
	require.NoError(t, err)

	signBytes2, err := provider(header)
	require.NoError(t, err)
	require.NotEmpty(t, signBytes2)

	// The sign bytes should be different now that we have a real BlockID
	require.NotEqual(t, signBytes, signBytes2)
}
