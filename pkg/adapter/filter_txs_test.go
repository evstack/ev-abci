package adapter

import (
	"context"
	"errors"
	"testing"

	"cosmossdk.io/log"
	abci "github.com/cometbft/cometbft/abci/types"
	ds "github.com/ipfs/go-datastore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/evstack/ev-node/core/execution"
)

func TestFilterTxs(t *testing.T) {
	tx10bytes := make([]byte, 10)
	tx20bytes := make([]byte, 20)
	tx50bytes := make([]byte, 50)

	okCheckTx := func(gasWanted int64) func(*abci.RequestCheckTx) (*abci.ResponseCheckTx, error) {
		return func(req *abci.RequestCheckTx) (*abci.ResponseCheckTx, error) {
			return &abci.ResponseCheckTx{Code: abci.CodeTypeOK, GasWanted: gasWanted}, nil
		}
	}

	// checkTxWithGasPerByte returns GasWanted proportional to tx size for deterministic gas values.
	checkTxWithGasPerByte := func(gasPerByte int64) func(*abci.RequestCheckTx) (*abci.ResponseCheckTx, error) {
		return func(req *abci.RequestCheckTx) (*abci.ResponseCheckTx, error) {
			return &abci.ResponseCheckTx{
				Code:      abci.CodeTypeOK,
				GasWanted: int64(len(req.Tx)) * gasPerByte,
			}, nil
		}
	}

	failCheckTx := func(req *abci.RequestCheckTx) (*abci.ResponseCheckTx, error) {
		return &abci.ResponseCheckTx{Code: 1, Log: "invalid tx"}, nil
	}

	errCheckTx := func(req *abci.RequestCheckTx) (*abci.ResponseCheckTx, error) {
		return nil, errors.New("app error")
	}

	specs := map[string]struct {
		txs                 [][]byte
		maxBytes            uint64
		maxGas              uint64
		hasForceIncludedTx  bool
		checkTxFn           func(*abci.RequestCheckTx) (*abci.ResponseCheckTx, error)
		expStatuses         []execution.FilterStatus
		expCheckTxNotCalled bool
	}{
		// --- No force-included txs: short-circuit, return all OK ---
		"no force included - returns all OK": {
			txs:                 [][]byte{tx10bytes, tx20bytes, tx50bytes},
			maxBytes:            1, // would normally filter, but short-circuit applies
			maxGas:              1,
			hasForceIncludedTx:  false,
			expStatuses:         []execution.FilterStatus{execution.FilterOK, execution.FilterOK, execution.FilterOK},
			expCheckTxNotCalled: true,
		},
		"no force included - empty list": {
			txs:                 [][]byte{},
			maxBytes:            100,
			maxGas:              100,
			hasForceIncludedTx:  false,
			expStatuses:         []execution.FilterStatus{},
			expCheckTxNotCalled: true,
		},

		// --- Empty txs ---
		"empty tx removed": {
			txs:                [][]byte{{}, tx10bytes},
			maxBytes:           0,
			maxGas:             0,
			hasForceIncludedTx: true,
			checkTxFn:          okCheckTx(100),
			expStatuses:        []execution.FilterStatus{execution.FilterRemove, execution.FilterOK},
		},

		// --- Size filtering ---
		"single tx exceeds maxBytes - removed": {
			txs:                [][]byte{tx50bytes},
			maxBytes:           30,
			maxGas:             0,
			hasForceIncludedTx: true,
			checkTxFn:          okCheckTx(100),
			expStatuses:        []execution.FilterStatus{execution.FilterRemove},
		},
		"cumulative bytes exceeded - postpone": {
			txs:                [][]byte{tx20bytes, tx20bytes, tx20bytes},
			maxBytes:           50,
			maxGas:             0,
			hasForceIncludedTx: true,
			checkTxFn:          okCheckTx(0),
			expStatuses: []execution.FilterStatus{
				execution.FilterOK,
				execution.FilterOK,
				execution.FilterPostpone,
			},
		},
		"remaining txs postponed after limit reached": {
			txs:                [][]byte{tx20bytes, tx20bytes, tx10bytes, tx10bytes},
			maxBytes:           50,
			maxGas:             0,
			hasForceIncludedTx: true,
			checkTxFn:          okCheckTx(0),
			expStatuses: []execution.FilterStatus{
				execution.FilterOK,
				execution.FilterOK,
				execution.FilterOK,
				execution.FilterPostpone,
			},
		},
		"maxBytes zero - no size limit": {
			txs:                [][]byte{tx50bytes, tx50bytes, tx50bytes},
			maxBytes:           0,
			maxGas:             0,
			hasForceIncludedTx: true,
			checkTxFn:          okCheckTx(0),
			expStatuses: []execution.FilterStatus{
				execution.FilterOK,
				execution.FilterOK,
				execution.FilterOK,
			},
		},

		// --- Gas filtering ---
		"single tx exceeds maxGas - removed": {
			txs:                [][]byte{tx10bytes},
			maxBytes:           0,
			maxGas:             50,
			hasForceIncludedTx: true,
			checkTxFn:          okCheckTx(100),
			expStatuses:        []execution.FilterStatus{execution.FilterRemove},
		},
		"cumulative gas exceeded - postpone": {
			txs:                [][]byte{tx10bytes, tx10bytes, tx10bytes},
			maxBytes:           0,
			maxGas:             250,
			hasForceIncludedTx: true,
			checkTxFn:          checkTxWithGasPerByte(10), // 10 bytes * 10 = 100 gas each
			expStatuses: []execution.FilterStatus{
				execution.FilterOK,       // cumulative: 100
				execution.FilterOK,       // cumulative: 200
				execution.FilterPostpone, // 300 > 250
			},
		},
		"maxGas zero - no gas limit": {
			txs:                [][]byte{tx10bytes, tx10bytes},
			maxBytes:           0,
			maxGas:             0,
			hasForceIncludedTx: true,
			checkTxFn:          okCheckTx(999999),
			expStatuses: []execution.FilterStatus{
				execution.FilterOK,
				execution.FilterOK,
			},
		},

		// --- CheckTx failure ---
		"checkTx returns non-OK code - removed": {
			txs:                [][]byte{tx10bytes, tx10bytes},
			maxBytes:           0,
			maxGas:             0,
			hasForceIncludedTx: true,
			checkTxFn:          failCheckTx,
			expStatuses: []execution.FilterStatus{
				execution.FilterRemove,
				execution.FilterRemove,
			},
		},
		"checkTx returns error - removed": {
			txs:                [][]byte{tx10bytes, tx10bytes},
			maxBytes:           0,
			maxGas:             0,
			hasForceIncludedTx: true,
			checkTxFn:          errCheckTx,
			expStatuses: []execution.FilterStatus{
				execution.FilterRemove,
				execution.FilterRemove,
			},
		},

		// --- Combined bytes and gas ---
		"bytes limit hit before gas limit": {
			txs:                [][]byte{tx50bytes, tx50bytes},
			maxBytes:           60,
			maxGas:             10000,
			hasForceIncludedTx: true,
			checkTxFn:          okCheckTx(10),
			expStatuses: []execution.FilterStatus{
				execution.FilterOK,
				execution.FilterPostpone,
			},
		},
		"gas limit hit before bytes limit": {
			txs:                [][]byte{tx10bytes, tx10bytes, tx10bytes},
			maxBytes:           10000,
			maxGas:             15,
			hasForceIncludedTx: true,
			checkTxFn:          checkTxWithGasPerByte(1), // 10 gas each
			expStatuses: []execution.FilterStatus{
				execution.FilterOK,       // cumulative gas: 10
				execution.FilterPostpone, // 20 > 15
				execution.FilterPostpone, // limit already reached
			},
		},

		// --- Mixed scenario ---
		"mixed - invalid, ok, oversized, postponed": {
			txs: [][]byte{
				{},        // empty → remove
				tx10bytes, // valid, fits → OK
				tx10bytes, // valid, fits → OK
				tx10bytes, // valid, exceeds cumulative bytes → postpone
				tx10bytes, // limit reached → postpone
			},
			maxBytes:           25,
			maxGas:             0,
			hasForceIncludedTx: true,
			checkTxFn:          okCheckTx(0),
			expStatuses: []execution.FilterStatus{
				execution.FilterRemove,
				execution.FilterOK,
				execution.FilterOK,
				execution.FilterPostpone,
				execution.FilterPostpone,
			},
		},
		"all txs fit exactly": {
			txs:                [][]byte{tx10bytes, tx10bytes, tx10bytes},
			maxBytes:           30,
			maxGas:             300,
			hasForceIncludedTx: true,
			checkTxFn:          okCheckTx(100),
			expStatuses: []execution.FilterStatus{
				execution.FilterOK,
				execution.FilterOK,
				execution.FilterOK,
			},
		},
	}

	for name, spec := range specs {
		t.Run(name, func(t *testing.T) {
			mock := &MockABCIApp{}
			if !spec.expCheckTxNotCalled {
				mock.CheckTxFn = spec.checkTxFn
			}
			// CheckTxFn intentionally left nil when expCheckTxNotCalled is true;
			// if FilterTxs tries to call it, the mock will panic, catching the bug.

			adapter := NewABCIExecutor(mock, ds.NewMapDatastore(), nil, nil, log.NewTestLogger(t), nil, nil)

			statuses, err := adapter.FilterTxs(
				context.Background(),
				spec.txs,
				spec.maxBytes,
				spec.maxGas,
				spec.hasForceIncludedTx,
			)

			require.NoError(t, err)
			require.Len(t, statuses, len(spec.txs))
			assert.Equal(t, spec.expStatuses, statuses)
		})
	}
}
