package core

import (
	"errors"
	"fmt"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtlog "github.com/cometbft/cometbft/libs/log"
	cmtquery "github.com/cometbft/cometbft/libs/pubsub/query"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/rollkit/go-execution-abci/pkg/adapter"
)

// TestTx tests the Tx function
func TestTx(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	ctx := newTestRPCContext() // Assumes newTestRPCContext is available or define it

	mockTxIndexer := new(MockTxIndexer)
	env = &Environment{
		TxIndexer: mockTxIndexer,
		Logger:    cmtlog.NewNopLogger(),
		Adapter:   &adapter.Adapter{}, // Minimal adapter needed? Add mocks if GetBlockData is used (for prove=true)
	}

	sampleTx := cmttypes.Tx("sample_tx_data")
	sampleHash := sampleTx.Hash()
	sampleHeight := int64(10)
	sampleIndex := uint32(1)
	sampleResult := abci.ExecTxResult{
		Code: 0,
		Data: []byte("result_data"),
		Log:  "success",
	}
	sampleTxResult := &abci.TxResult{
		Height: sampleHeight,
		Index:  sampleIndex,
		Tx:     sampleTx,
		Result: sampleResult,
	}

	t.Run("Success", func(t *testing.T) {
		mockTxIndexer.On("Get", sampleHash).Return(sampleTxResult, nil).Once()

		result, err := Tx(ctx, sampleHash, false) // prove = false

		require.NoError(err)
		require.NotNil(result)
		assert.Equal(sampleHash, []byte(result.Hash))
		assert.Equal(sampleHeight, result.Height)
		assert.Equal(sampleIndex, result.Index)
		assert.Equal(sampleResult, result.TxResult)
		assert.Equal(sampleTx, result.Tx)
		// Proof is expected to be empty when prove is false
		assert.Empty(result.Proof.Proof) // Check specific fields if needed

		mockTxIndexer.AssertExpectations(t)
	})

	t.Run("NotFound", func(t *testing.T) {
		mockTxIndexer.On("Get", sampleHash).Return(nil, nil).Once()

		result, err := Tx(ctx, sampleHash, false)

		require.Error(err)
		assert.Nil(result)
		assert.Contains(err.Error(), "not found")

		mockTxIndexer.AssertExpectations(t)
	})

	t.Run("IndexerError", func(t *testing.T) {
		expectedErr := errors.New("indexer database error")
		mockTxIndexer.On("Get", sampleHash).Return(nil, expectedErr).Once()

		result, err := Tx(ctx, sampleHash, false)

		require.Error(err)
		assert.Nil(result)
		assert.Equal(expectedErr, err) // Should return the original error

		mockTxIndexer.AssertExpectations(t)
	})

	// TODO: Add test case for prove = true once the proof logic is implemented
	// t.Run("Success_WithProof", func(t *testing.T) { ... })

}

// TestTxSearch tests the TxSearch function
func TestTxSearch(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	ctx := newTestRPCContext()

	mockTxIndexer := new(MockTxIndexer)
	env = &Environment{
		TxIndexer: mockTxIndexer,
		Logger:    cmtlog.NewNopLogger(),
		Adapter:   &adapter.Adapter{},
	}

	// Sample transactions for search results
	tx1 := cmttypes.Tx("tx_data_1")
	tx2 := cmttypes.Tx("tx_data_2_longer")
	tx3 := cmttypes.Tx("tx_data_3")

	res1 := &abci.TxResult{Height: 10, Index: 1, Tx: tx1, Result: abci.ExecTxResult{Code: 0}}
	res2 := &abci.TxResult{Height: 11, Index: 0, Tx: tx2, Result: abci.ExecTxResult{Code: 1}} // Different height
	res3 := &abci.TxResult{Height: 10, Index: 0, Tx: tx3, Result: abci.ExecTxResult{Code: 0}} // Same height as res1, lower index

	searchResults := []*abci.TxResult{res1, res2, res3} // Unsorted initially

	defaultPage := 1
	defaultPerPage := 30

	t.Run("Success_Ascending", func(t *testing.T) {
		query := "tx.height >= 10"
		orderBy := "asc"
		mockTxIndexer.On("Search", mock.Anything, mock.AnythingOfType("*query.Query")).Run(func(args mock.Arguments) {
			// Basic check if the query seems right (optional)
			q := args.Get(1).(*cmtquery.Query)
			require.NotNil(q)
		}).Return(searchResults, nil).Once()

		result, err := TxSearch(ctx, query, false, &defaultPage, &defaultPerPage, orderBy)

		require.NoError(err)
		require.NotNil(result)
		assert.Equal(3, result.TotalCount)
		require.Len(result.Txs, 3)

		// Check order: (h10, i0), (h10, i1), (h11, i0)
		assert.Equal(int64(10), result.Txs[0].Height)
		assert.Equal(uint32(0), result.Txs[0].Index)
		assert.Equal(tx3.Hash(), []byte(result.Txs[0].Hash))

		assert.Equal(int64(10), result.Txs[1].Height)
		assert.Equal(uint32(1), result.Txs[1].Index)
		assert.Equal(tx1.Hash(), []byte(result.Txs[1].Hash))

		assert.Equal(int64(11), result.Txs[2].Height)
		assert.Equal(uint32(0), result.Txs[2].Index)
		assert.Equal(tx2.Hash(), []byte(result.Txs[2].Hash))

		mockTxIndexer.AssertExpectations(t)
	})

	t.Run("Success_Descending", func(t *testing.T) {
		query := "tx.height >= 10"
		orderBy := "desc"
		mockTxIndexer.On("Search", mock.Anything, mock.AnythingOfType("*query.Query")).Return(searchResults, nil).Once()

		result, err := TxSearch(ctx, query, false, &defaultPage, &defaultPerPage, orderBy)

		require.NoError(err)
		require.NotNil(result)
		assert.Equal(3, result.TotalCount)
		require.Len(result.Txs, 3)

		// Check order: (h11, i0), (h10, i1), (h10, i0)
		assert.Equal(int64(11), result.Txs[0].Height)
		assert.Equal(uint32(0), result.Txs[0].Index)
		assert.Equal(tx2.Hash(), []byte(result.Txs[0].Hash))

		assert.Equal(int64(10), result.Txs[1].Height)
		assert.Equal(uint32(1), result.Txs[1].Index)
		assert.Equal(tx1.Hash(), []byte(result.Txs[1].Hash))

		assert.Equal(int64(10), result.Txs[2].Height)
		assert.Equal(uint32(0), result.Txs[2].Index)
		assert.Equal(tx3.Hash(), []byte(result.Txs[2].Hash))

		mockTxIndexer.AssertExpectations(t)
	})

	t.Run("Success_Pagination", func(t *testing.T) {
		query := "tx.height >= 10"
		orderBy := "asc" // Use ascending for predictable pagination
		page := 2
		perPage := 2
		mockTxIndexer.On("Search", mock.Anything, mock.AnythingOfType("*query.Query")).Return(searchResults, nil).Once()

		result, err := TxSearch(ctx, query, false, &page, &perPage, orderBy)

		require.NoError(err)
		require.NotNil(result)
		assert.Equal(3, result.TotalCount) // Total count remains the same
		require.Len(result.Txs, 1)         // Only the last item should be on page 2

		// Check the single item on page 2 (which is the 3rd item overall in ascending order)
		assert.Equal(int64(11), result.Txs[0].Height)
		assert.Equal(uint32(0), result.Txs[0].Index)
		assert.Equal(tx2.Hash(), []byte(result.Txs[0].Hash))

		mockTxIndexer.AssertExpectations(t)
	})

	t.Run("Error_InvalidQuery", func(t *testing.T) {
		invalidQuery := "invalid query string!!!"
		orderBy := "asc"

		// No mock expectation for Search, as it should fail before calling the indexer

		result, err := TxSearch(ctx, invalidQuery, false, &defaultPage, &defaultPerPage, orderBy)

		require.Error(err)
		assert.Nil(result)
		// Check if the error comes from the query parser
		assert.Contains(err.Error(), "got tag, wanted") // More specific check based on actual error
	})

	t.Run("Error_IndexerSearch", func(t *testing.T) {
		query := "tx.height >= 10"
		orderBy := "asc"
		expectedErr := errors.New("indexer search failed")
		mockTxIndexer.On("Search", mock.Anything, mock.AnythingOfType("*query.Query")).Return(nil, expectedErr).Once()

		result, err := TxSearch(ctx, query, false, &defaultPage, &defaultPerPage, orderBy)

		require.Error(err)
		assert.Nil(result)
		assert.Equal(expectedErr, err)

		mockTxIndexer.AssertExpectations(t)
	})

	t.Run("Error_InvalidOrderBy", func(t *testing.T) {
		query := "tx.height >= 10"
		invalidOrderBy := "random"
		mockTxIndexer.On("Search", mock.Anything, mock.AnythingOfType("*query.Query")).Return(searchResults, nil).Once() // Search succeeds initially

		result, err := TxSearch(ctx, query, false, &defaultPage, &defaultPerPage, invalidOrderBy)

		require.Error(err)
		assert.Nil(result)
		assert.Contains(err.Error(), "order_by") // Check for specific error message

		mockTxIndexer.AssertExpectations(t)
	})

	t.Run("Error_InvalidPage", func(t *testing.T) {
		query := "tx.height >= 10"
		orderBy := "asc"
		invalidPage := 0 // Page must be >= 1
		perPage := 2
		mockTxIndexer.On("Search", mock.Anything, mock.AnythingOfType("*query.Query")).Return(searchResults, nil).Once() // Search succeeds initially

		result, err := TxSearch(ctx, query, false, &invalidPage, &perPage, orderBy)

		require.Error(err)
		assert.Nil(result)
		assert.Contains(err.Error(), "page") // Check for specific pagination error

		mockTxIndexer.AssertExpectations(t)
	})

	// TODO: Add test case for prove = true once the proof logic is implemented
	// t.Run("Success_WithProof", func(t *testing.T) { ... })
}

// Helper functions for pagination validation (copied from cometbft/rpc/core/tx.go as they are not exported)
// Consider moving these to a shared test utility if used frequently.
const (
	defaultPerPage_test = 30
	maxPerPage_test     = 100
)

func validatePerPage_test(perPagePtr *int) int {
	if perPagePtr == nil {
		return defaultPerPage_test
	}

	perPage := *perPagePtr
	if perPage < 1 {
		return defaultPerPage_test
	} else if perPage > maxPerPage_test {
		return maxPerPage_test
	}
	return perPage
}

func validatePage_test(pagePtr *int, perPage, totalCount int) (int, error) {
	if perPage < 1 {
		panic(fmt.Sprintf("zero or negative perPage: %d", perPage))
	}

	if pagePtr == nil { // no page specified
		return 1, nil
	}

	page := *pagePtr
	if page < 1 {
		return 1, fmt.Errorf("page must be greater than 0")
	}

	pages := ((totalCount - 1) / perPage) + 1
	if pages == 0 {
		pages = 1 // one page of zero results
	}
	if page > pages {
		return pages, fmt.Errorf("page should be less than or equal to %d", pages)
	}

	return page, nil
}

func validateSkipCount_test(page, perPage int) int {
	skipCount := (page - 1) * perPage
	if skipCount < 0 {
		return 0
	}

	return skipCount
}

func min_test(a, b int) int {
	if a < b {
		return a
	}
	return b
}
