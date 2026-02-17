package store_test

import (
	"testing"

	ds "github.com/ipfs/go-datastore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/evstack/ev-abci/pkg/store"
)

func TestStateIO(t *testing.T) {
	db := ds.NewMapDatastore()
	abciStore := store.NewExecABCIStore(db)
	myState := store.TestingStateFixture()
	require.NoError(t, abciStore.SaveState(t.Context(), myState))
	gotState, gotErr := abciStore.LoadState(t.Context())
	require.NoError(t, gotErr)
	assert.Equal(t, myState, gotState)
	exists, gotErr := db.Has(t.Context(), ds.NewKey("/abci/s"))
	require.NoError(t, gotErr)
	assert.True(t, exists)
}

func TestPrune_RemovesPerHeightABCIKeysUpToTarget(t *testing.T) {
	db := ds.NewMapDatastore()
	absciStore := store.NewExecABCIStore(db)

	ctx := t.Context()

	// Seed per-height block ID and block response keys for heights 1..5.
	for h := 1; h <= 5; h++ {
		heightKey := ds.NewKey("/abci/bid").ChildString(string(rune(h + '0')))
		require.NoError(t, db.Put(ctx, heightKey, []byte("bid")))

		respKey := ds.NewKey("/abci/br").ChildString(string(rune(h + '0')))
		require.NoError(t, db.Put(ctx, respKey, []byte("br")))
	}

	// Seed state to ensure it is not affected by pruning.
	require.NoError(t, db.Put(ctx, ds.NewKey("/abci/s"), []byte("state")))

	// Prune up to height 3.
	require.NoError(t, absciStore.Prune(ctx, 3))

	// Heights 1..3 should be deleted.
	for h := 1; h <= 3; h++ {
		bidKey := ds.NewKey("/abci/bid").ChildString(string(rune(h + '0')))
		exists, err := db.Has(ctx, bidKey)
		require.NoError(t, err)
		assert.False(t, exists)

		respKey := ds.NewKey("/abci/br").ChildString(string(rune(h + '0')))
		exists, err = db.Has(ctx, respKey)
		require.NoError(t, err)
		assert.False(t, exists)
	}

	// Heights 4..5 should remain.
	for h := 4; h <= 5; h++ {
		bidKey := ds.NewKey("/abci/bid").ChildString(string(rune(h + '0')))
		exists, err := db.Has(ctx, bidKey)
		require.NoError(t, err)
		assert.True(t, exists)

		respKey := ds.NewKey("/abci/br").ChildString(string(rune(h + '0')))
		exists, err = db.Has(ctx, respKey)
		require.NoError(t, err)
		assert.True(t, exists)
	}

	// State should not be pruned.
	exists, err := db.Has(ctx, ds.NewKey("/abci/s"))
	require.NoError(t, err)
	assert.True(t, exists)
}
