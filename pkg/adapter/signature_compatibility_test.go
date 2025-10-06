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

	"github.com/evstack/ev-node/types"

	"github.com/evstack/ev-abci/pkg/store"
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

	require.NoError(t, err)

	// Save the properly generated BlockID to the store
	err = storeOnlyAdapter.Store.SaveBlockID(context.Background(), header.Height(), &cmttypes.BlockID{})
	require.NoError(t, err)

	signBytes2, err := provider(header)
	require.NoError(t, err)
	require.NotEmpty(t, signBytes2)

	// The equal as height 1 has an empty block ID
	require.Equal(t, signBytes, signBytes2)

	// Test 4: Verify that height 2 uses a real BlockID and produces different signature bytes
	header2 := &types.Header{
		BaseHeader: types.BaseHeader{
			Height:  2,
			Time:    uint64(time.Now().UnixNano()),
			ChainID: chainID,
		},
		ProposerAddress: validatorAddress,
	}

	// Save header 1 so GetLastCommit can retrieve it for height 2
	signedHeader1 := &types.SignedHeader{
		Header:    *header,
		Signature: types.Signature(make([]byte, 64)),
	}
	err = storeOnlyAdapter.RollkitStore.SaveBlockData(context.Background(), signedHeader1, &types.Data{}, &types.Signature{})
	require.NoError(t, err)

	// Generate and save BlockID for height 2
	lastCommit2, err := storeOnlyAdapter.GetLastCommit(context.Background(), header2.Height())
	require.NoError(t, err)

	abciHeader2, err := ToABCIHeader(*header2, lastCommit2)
	require.NoError(t, err)

	cmtTxs := make(cmttypes.Txs, 0)
	_, blockID2, err := MakeABCIBlock(header2.Height(), cmtTxs, currentState, abciHeader2, lastCommit2)
	require.NoError(t, err)

	err = storeOnlyAdapter.Store.SaveBlockID(context.Background(), header2.Height(), blockID2)
	require.NoError(t, err)

	signBytes3, err := provider(header2)
	require.NoError(t, err)
	require.NotEmpty(t, signBytes3)

	// Height 2 should have different signature bytes than height 1 (different BlockID)
	require.NotEqual(t, signBytes, signBytes3)
}
