package core

import (
	"context"
	"errors"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/crypto/ed25519"
	cmtlog "github.com/cometbft/cometbft/libs/log"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
	cmtstate "github.com/cometbft/cometbft/state"
	cmttypes "github.com/cometbft/cometbft/types"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	"github.com/cosmos/gogoproto/proto"
	ds "github.com/ipfs/go-datastore"
	testifyassert "github.com/stretchr/testify/assert"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	rollkitmocks "github.com/evstack/ev-node/test/mocks"

	networktypes "github.com/evstack/ev-abci/modules/network/types"
	"github.com/evstack/ev-abci/pkg/adapter"
	execstore "github.com/evstack/ev-abci/pkg/store"
)

var (
	testSamplePubKey     = ed25519.GenPrivKey().PubKey()
	testSampleAddress    = testSamplePubKey.Address()
	testGenesisValidator = cmttypes.GenesisValidator{
		Address: testSampleAddress,
		PubKey:  testSamplePubKey,
		Power:   1,
		Name:    "genesis-validator",
	}
	testSampleConsensusParams = &cmttypes.ConsensusParams{
		Block: cmttypes.BlockParams{MaxBytes: 10, MaxGas: 100},
	}

	testProtoConsensusParams = cmtproto.ConsensusParams{
		Block:     &cmtproto.BlockParams{MaxBytes: 1024, MaxGas: 200000},
		Evidence:  &cmtproto.EvidenceParams{MaxAgeNumBlocks: 1000, MaxAgeDuration: time.Hour, MaxBytes: 512},
		Validator: &cmtproto.ValidatorParams{PubKeyTypes: []string{"ed25519"}},
		Version:   &cmtproto.VersionParams{App: 1},
	}
	testMockStateWithConsensusParams cmtstate.State
)

func init() {
	testMockStateWithConsensusParams = newValidState()
	testMockStateWithConsensusParams.ConsensusParams = cmttypes.ConsensusParamsFromProto(testProtoConsensusParams)
	testMockStateWithConsensusParams.LastHeightConsensusParamsChanged = testMockStateWithConsensusParams.InitialHeight
}

func newTestValidator() *cmttypes.Validator {
	pk := ed25519.GenPrivKey().PubKey()
	return &cmttypes.Validator{
		Address:          pk.Address(),
		PubKey:           pk,
		VotingPower:      10,
		ProposerPriority: 0,
	}
}

func newValidState() cmtstate.State {
	val := newTestValidator()
	valSet := cmttypes.NewValidatorSet([]*cmttypes.Validator{val})
	valSet.Proposer = val

	sampleHash1 := make([]byte, 32)
	for i := range sampleHash1 {
		sampleHash1[i] = byte(i)
	}
	sampleHash2 := make([]byte, 32)
	for i := range sampleHash2 {
		sampleHash2[i] = byte(i + 100)
	}

	return cmtstate.State{
		ChainID:         "test-chain-id",
		InitialHeight:   1,
		LastBlockHeight: 1,
		LastBlockID:     cmttypes.BlockID{Hash: sampleHash1, PartSetHeader: cmttypes.PartSetHeader{Total: 1, Hash: sampleHash2}},
		LastBlockTime:   time.Now().UTC(),
		Validators:      valSet,
		NextValidators:  valSet,
		LastValidators:  valSet,
		AppHash:         []byte("app_hash"),
	}
}

// setupTestValidatorsEnv helper for TestValidators
func setupTestValidatorsEnv(t *testing.T, gvs []cmttypes.GenesisValidator, consensusParams *cmttypes.ConsensusParams) *rollkitmocks.MockStore {
	t.Helper()

	mockStore := new(rollkitmocks.MockStore)
	adapterInstance := &adapter.Adapter{
		RollkitStore: mockStore,
		AppGenesis: &genutiltypes.AppGenesis{
			Consensus: &genutiltypes.ConsensusGenesis{
				Validators: gvs,
				Params:     consensusParams,
			},
		},
	}
	env = &Environment{Adapter: adapterInstance, Logger: cmtlog.NewNopLogger()}
	return mockStore
}

// setupTestConsensusParamsEnv helper for TestConsensusParams
// Returns the mockRollkitStore (if created), the adapter instance, and the abciStore.
func setupTestConsensusParamsEnv(t *testing.T, useMockRollkitStore bool, stateToSave *cmtstate.State) (*rollkitmocks.MockStore, *execstore.Store) {
	var mockRollkitStore *rollkitmocks.MockStore
	if useMockRollkitStore {
		mockRollkitStore = new(rollkitmocks.MockStore)
	}

	dsStore := ds.NewMapDatastore()
	abciStore := execstore.NewExecABCIStore(dsStore)

	if stateToSave != nil {
		err := abciStore.SaveState(context.Background(), stateToSave)
		require.NoError(t, err)
	}

	adapterInstance := &adapter.Adapter{
		Store: abciStore,
	}
	if useMockRollkitStore {
		adapterInstance.RollkitStore = mockRollkitStore
	}

	env = &Environment{Adapter: adapterInstance, Logger: cmtlog.NewNopLogger()}
	return mockRollkitStore, abciStore
}

func TestValidators(t *testing.T) {
	assert := testifyassert.New(t)
	require := require.New(t)
	ctx := &rpctypes.Context{}

	t.Run("Success_OneValidator_LatestHeight", func(t *testing.T) {
		mockStore := setupTestValidatorsEnv(t, []cmttypes.GenesisValidator{testGenesisValidator}, testSampleConsensusParams)

		expectedHeight := uint64(100)
		mockStore.On("Height", testifymock.Anything).Return(expectedHeight, nil).Once()

		result, err := Validators(ctx, nil, nil, nil)
		require.NoError(err)
		require.NotNil(result)
		assert.Equal(int64(expectedHeight), result.BlockHeight)
		assert.Len(result.Validators, 1)
		assert.Equal(1, result.Count)
		assert.Equal(1, result.Total)
		assert.Equal(testSampleAddress, result.Validators[0].Address)
		assert.Equal(testSamplePubKey, result.Validators[0].PubKey)
		assert.Equal(int64(1), result.Validators[0].VotingPower)
		assert.Equal(int64(1), result.Validators[0].ProposerPriority)
		mockStore.AssertExpectations(t)
	})

	t.Run("Success_OneValidator_SpecificHeight", func(t *testing.T) {
		_ = setupTestValidatorsEnv(t, []cmttypes.GenesisValidator{testGenesisValidator}, testSampleConsensusParams) // mockStore not used directly here for Height mock
		specificHeight := int64(50)

		result, err := Validators(ctx, &specificHeight, nil, nil)
		require.NoError(err)
		require.NotNil(result)
		assert.Equal(specificHeight, result.BlockHeight)
		assert.Len(result.Validators, 1)
		assert.Equal(1, result.Count)
		assert.Equal(1, result.Total)
		assert.Equal(testSampleAddress, result.Validators[0].Address)
		assert.Equal(testSamplePubKey, result.Validators[0].PubKey)
		assert.Equal(int64(1), result.Validators[0].VotingPower)
		assert.Equal(int64(1), result.Validators[0].ProposerPriority)
	})

	t.Run("Error_NoValidators", func(t *testing.T) {
		mockStore := setupTestValidatorsEnv(t, []cmttypes.GenesisValidator{}, testSampleConsensusParams)
		mockStore.On("Height", testifymock.Anything).Return(uint64(0), nil).Maybe()

		result, err := Validators(ctx, nil, nil, nil)
		require.Error(err)
		assert.Nil(result)
		assert.Contains(err.Error(), "there should be exactly one validator in genesis")
		mockStore.AssertExpectations(t)
	})

	t.Run("Error_TooManyValidators", func(t *testing.T) {
		mockStore := setupTestValidatorsEnv(t, []cmttypes.GenesisValidator{testGenesisValidator, testGenesisValidator}, testSampleConsensusParams)
		mockStore.On("Height", testifymock.Anything).Return(uint64(0), nil).Maybe()

		result, err := Validators(ctx, nil, nil, nil)
		require.Error(err)
		assert.Nil(result)
		assert.Contains(err.Error(), "there should be exactly one validator in genesis")
		mockStore.AssertExpectations(t)
	})

	t.Run("Success_NilHeight", func(t *testing.T) {
		mockStore := setupTestValidatorsEnv(t, []cmttypes.GenesisValidator{testGenesisValidator}, testSampleConsensusParams)
		mockStore.On("Height", testifymock.Anything).Return(uint64(0), nil).Once()

		result, err := Validators(ctx, nil, nil, nil)
		require.NoError(err)
		require.NotNil(result)
		assert.Equal(int64(0), result.BlockHeight) // Asserting BlockHeight is 0 as per test name
		assert.Len(result.Validators, 1)           // Still expect validator details
		mockStore.AssertExpectations(t)
	})

	t.Run("Error_NilHeightAndStoreError", func(t *testing.T) {
		mockStore := setupTestValidatorsEnv(t, []cmttypes.GenesisValidator{testGenesisValidator}, testSampleConsensusParams)
		mockStore.On("Height", testifymock.Anything).Return(uint64(0), errors.New("failed to get height")).Once()

		result, err := Validators(ctx, nil, nil, nil)
		require.Error(err)
		assert.Nil(result)
		assert.Contains(err.Error(), "failed to get height")
		mockStore.AssertExpectations(t)
	})
}

func TestValidatorsAttesterModeReturnsFullAttesterSet(t *testing.T) {
	chainID := "test-chain"
	height := int64(100)
	privs := []ed25519.PrivKey{
		ed25519.GenPrivKey(),
		ed25519.GenPrivKey(),
		ed25519.GenPrivKey(),
		ed25519.GenPrivKey(),
	}

	setEntries := make([]networktypes.AttesterSetEntry, 0, len(privs))
	attesterInfoByAddr := make(map[string]*networktypes.AttesterInfo)
	for i, priv := range privs {
		pub := priv.PubKey()
		sdkPk, err := cryptocodec.FromCmtPubKeyInterface(pub)
		require.NoError(t, err)
		any, err := codectypes.NewAnyWithValue(sdkPk)
		require.NoError(t, err)
		consAddr := sdk.ConsAddress(pub.Address()).String()
		setEntries = append(setEntries, networktypes.AttesterSetEntry{
			Authority:        sdk.AccAddress(pub.Address()).String(),
			ConsensusAddress: consAddr,
			Index:            uint32(i), //nolint:gosec
			Pubkey:           any,
		})
		attesterInfoByAddr[consAddr] = &networktypes.AttesterInfo{
			Authority:        sdk.AccAddress(pub.Address()).String(),
			Pubkey:           any,
			ConsensusAddress: consAddr,
		}
	}

	setRespBz, err := proto.Marshal(&networktypes.QueryAttesterSetResponse{Entries: setEntries})
	require.NoError(t, err)

	signatures := make([]*networktypes.AttesterSignature, 0, 3)
	blockID := cmtproto.BlockID{Hash: make([]byte, 32), PartSetHeader: cmtproto.PartSetHeader{}}
	for i, priv := range privs[:3] {
		vote := cmtproto.Vote{
			Type:             cmtproto.PrecommitType,
			Height:           height,
			Round:            0,
			BlockID:          blockID,
			Timestamp:        time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC),
			ValidatorAddress: priv.PubKey().Address(),
			ValidatorIndex:   int32(i), //nolint:gosec
		}
		sig, err := priv.Sign(cmttypes.VoteSignBytes(chainID, &vote))
		require.NoError(t, err)
		vote.Signature = sig
		voteBz, err := proto.Marshal(&vote)
		require.NoError(t, err)
		signatures = append(signatures, &networktypes.AttesterSignature{
			ValidatorAddress: setEntries[i].ConsensusAddress,
			Signature:        voteBz,
		})
	}
	sigRespBz, err := proto.Marshal(&networktypes.QueryAttesterSignaturesResponse{Signatures: signatures})
	require.NoError(t, err)

	mApp := new(MockApp)
	mApp.On("Query", testifymock.Anything, testifymock.MatchedBy(func(r *abci.RequestQuery) bool {
		return r.Path == "/evabci.network.v1.Query/AttesterSet"
	})).Return(&abci.ResponseQuery{Code: 0, Value: setRespBz}, nil)
	mApp.On("Query", testifymock.Anything, testifymock.MatchedBy(func(r *abci.RequestQuery) bool {
		return r.Path == "/evabci.network.v1.Query/AttesterSignatures"
	})).Return(&abci.ResponseQuery{Code: 0, Value: sigRespBz}, nil).Maybe()
	for consAddr, info := range attesterInfoByAddr {
		infoRespBz, err := proto.Marshal(&networktypes.QueryAttesterInfoResponse{AttesterInfo: info})
		require.NoError(t, err)
		consAddr := consAddr
		mApp.On("Query", testifymock.Anything, testifymock.MatchedBy(func(r *abci.RequestQuery) bool {
			if r.Path != "/evabci.network.v1.Query/AttesterInfo" {
				return false
			}
			var req networktypes.QueryAttesterInfoRequest
			if err := proto.Unmarshal(r.Data, &req); err != nil {
				return false
			}
			return req.ValidatorAddress == consAddr
		})).Return(&abci.ResponseQuery{Code: 0, Value: infoRespBz}, nil).Maybe()
	}

	env = &Environment{
		Adapter:      &adapter.Adapter{App: mApp},
		AttesterMode: true,
		Logger:       cmtlog.NewNopLogger(),
	}

	result, err := Validators(&rpctypes.Context{}, &height, nil, nil)
	require.NoError(t, err)
	require.Len(t, result.Validators, 4)
	require.Equal(t, 4, result.Count)
	require.Equal(t, 4, result.Total)
	for i, validator := range result.Validators {
		require.Equal(t, privs[i].PubKey().Address(), validator.Address)
		require.Equal(t, int64(1), validator.VotingPower)
	}
}

func TestDumpConsensusState(t *testing.T) {
	assert := testifyassert.New(t)
	require := require.New(t)
	ctx := &rpctypes.Context{}

	result, err := DumpConsensusState(ctx)

	require.Error(err)
	assert.Nil(result)
	assert.Equal(ErrConsensusStateNotAvailable, err)
}

func TestConsensusState(t *testing.T) {
	assert := testifyassert.New(t)
	require := require.New(t)
	ctx := &rpctypes.Context{}

	result, err := ConsensusState(ctx)

	require.Error(err)
	assert.Nil(result)
	assert.Equal(ErrConsensusStateNotAvailable, err)
}

func TestConsensusParams(t *testing.T) {
	assert := testifyassert.New(t)
	require := require.New(t)
	ctx := &rpctypes.Context{}

	// sampleProtoParams and mockStateWithConsensusParams moved to package level vars (testProtoConsensusParams, testMockStateWithConsensusParams)

	t.Run("Success_LatestHeight", func(t *testing.T) {
		mockRollkitStore, _ := setupTestConsensusParamsEnv(t, true, &testMockStateWithConsensusParams)

		expectedHeight := uint64(120)
		mockRollkitStore.On("Height", testifymock.Anything).Return(expectedHeight, nil).Once()

		result, err := ConsensusParams(ctx, nil)
		require.NoError(err)
		require.NotNil(result)

		assert.Equal(int64(expectedHeight), result.BlockHeight)
		assert.Equal(testProtoConsensusParams.Block.MaxBytes, result.ConsensusParams.Block.MaxBytes)
		assert.Equal(testProtoConsensusParams.Block.MaxGas, result.ConsensusParams.Block.MaxGas)

		mockRollkitStore.AssertExpectations(t)
	})

	t.Run("Success_SpecificHeight", func(t *testing.T) {
		_, _ = setupTestConsensusParamsEnv(t, false, &testMockStateWithConsensusParams) // mockRollkitStore not needed
		specificHeight := int64(60)
		// err := abciStore.SaveState(context.Background(), &testMockStateWithConsensusParams) // Moved to helper
		// require.NoError(err) // Moved to helper

		result, err := ConsensusParams(ctx, &specificHeight)
		require.NoError(err)
		require.NotNil(result)
		assert.Equal(specificHeight, result.BlockHeight)
		assert.Equal(testProtoConsensusParams.Block.MaxBytes, result.ConsensusParams.Block.MaxBytes)
	})

	t.Run("Error_LoadStateFails", func(t *testing.T) {
		mockRollkitStore, _ := setupTestConsensusParamsEnv(t, true, nil) // Don't save state to force load failure
		mockRollkitStore.On("Height", testifymock.Anything).Return(uint64(100), nil).Maybe()

		result, err := ConsensusParams(ctx, nil)

		require.Error(err)
		assert.Nil(result)
		// The original test checked: assert.ErrorContains(err, "failed to get state metadata")
		// and require.True(errors.Is(err, ds.ErrNotFound), "error should wrap ds.ErrNotFound")
		// The error message check is kept from original if LoadState returns a clear error.
		// If LoadState wraps ds.ErrNotFound, then errors.Is(err, ds.ErrNotFound) would be more precise.
		assert.ErrorContains(err, "failed to get state metadata")
		require.True(errors.Is(err, ds.ErrNotFound), "error should wrap ds.ErrNotFound")

		mockRollkitStore.AssertExpectations(t)
	})

	t.Run("Error_NilHeightAndStoreError", func(t *testing.T) {
		mockRollkitStore, _ := setupTestConsensusParamsEnv(t, true, &testMockStateWithConsensusParams)
		mockRollkitStore.On("Height", testifymock.Anything).Return(uint64(0), errors.New("failed to get height")).Once()

		_, err := ConsensusParams(ctx, nil)
		require.Error(err)
		mockRollkitStore.AssertExpectations(t)
	})

	t.Run("Success_NilHeight", func(t *testing.T) {
		mockRollkitStore, _ := setupTestConsensusParamsEnv(t, true, &testMockStateWithConsensusParams)
		mockRollkitStore.On("Height", testifymock.Anything).Return(uint64(0), nil).Once()

		result, err := ConsensusParams(ctx, nil)
		require.NoError(err)
		require.NotNil(result)
		assert.Equal(int64(0), result.BlockHeight)
		assert.Equal(testProtoConsensusParams.Block.MaxBytes, result.ConsensusParams.Block.MaxBytes)
		mockRollkitStore.AssertExpectations(t)
	})
}
