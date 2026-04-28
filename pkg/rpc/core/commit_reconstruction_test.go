package core_test

import (
	"bytes"
	"context"
	"sort"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmtlog "github.com/cometbft/cometbft/libs/log"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	ds "github.com/ipfs/go-datastore"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	networktypes "github.com/evstack/ev-abci/modules/network/types"
	"github.com/evstack/ev-abci/pkg/adapter"
	"github.com/evstack/ev-abci/pkg/rpc/core"
	execstore "github.com/evstack/ev-abci/pkg/store"
)

// mockABCI is a minimal mock for the servertypes.ABCI interface.
type mockABCI struct {
	mock.Mock
}

func (m *mockABCI) Info(req *abci.RequestInfo) (*abci.ResponseInfo, error) {
	args := m.Called(req)
	r, _ := args.Get(0).(*abci.ResponseInfo)
	return r, args.Error(1)
}

func (m *mockABCI) Query(ctx context.Context, req *abci.RequestQuery) (*abci.ResponseQuery, error) {
	args := m.Called(ctx, req)
	r, _ := args.Get(0).(*abci.ResponseQuery)
	return r, args.Error(1)
}

func (m *mockABCI) CheckTx(req *abci.RequestCheckTx) (*abci.ResponseCheckTx, error) {
	args := m.Called(req)
	r, _ := args.Get(0).(*abci.ResponseCheckTx)
	return r, args.Error(1)
}

func (m *mockABCI) InitChain(req *abci.RequestInitChain) (*abci.ResponseInitChain, error) {
	args := m.Called(req)
	r, _ := args.Get(0).(*abci.ResponseInitChain)
	return r, args.Error(1)
}

func (m *mockABCI) PrepareProposal(req *abci.RequestPrepareProposal) (*abci.ResponsePrepareProposal, error) {
	args := m.Called(req)
	r, _ := args.Get(0).(*abci.ResponsePrepareProposal)
	return r, args.Error(1)
}

func (m *mockABCI) ProcessProposal(req *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
	args := m.Called(req)
	r, _ := args.Get(0).(*abci.ResponseProcessProposal)
	return r, args.Error(1)
}

func (m *mockABCI) FinalizeBlock(req *abci.RequestFinalizeBlock) (*abci.ResponseFinalizeBlock, error) {
	args := m.Called(req)
	r, _ := args.Get(0).(*abci.ResponseFinalizeBlock)
	return r, args.Error(1)
}

func (m *mockABCI) ExtendVote(ctx context.Context, req *abci.RequestExtendVote) (*abci.ResponseExtendVote, error) {
	args := m.Called(ctx, req)
	r, _ := args.Get(0).(*abci.ResponseExtendVote)
	return r, args.Error(1)
}

func (m *mockABCI) VerifyVoteExtension(req *abci.RequestVerifyVoteExtension) (*abci.ResponseVerifyVoteExtension, error) {
	args := m.Called(req)
	r, _ := args.Get(0).(*abci.ResponseVerifyVoteExtension)
	return r, args.Error(1)
}

func (m *mockABCI) Commit() (*abci.ResponseCommit, error) {
	args := m.Called()
	r, _ := args.Get(0).(*abci.ResponseCommit)
	return r, args.Error(1)
}

func (m *mockABCI) ListSnapshots(req *abci.RequestListSnapshots) (*abci.ResponseListSnapshots, error) {
	args := m.Called(req)
	r, _ := args.Get(0).(*abci.ResponseListSnapshots)
	return r, args.Error(1)
}

func (m *mockABCI) OfferSnapshot(req *abci.RequestOfferSnapshot) (*abci.ResponseOfferSnapshot, error) {
	args := m.Called(req)
	r, _ := args.Get(0).(*abci.ResponseOfferSnapshot)
	return r, args.Error(1)
}

func (m *mockABCI) LoadSnapshotChunk(req *abci.RequestLoadSnapshotChunk) (*abci.ResponseLoadSnapshotChunk, error) {
	args := m.Called(req)
	r, _ := args.Get(0).(*abci.ResponseLoadSnapshotChunk)
	return r, args.Error(1)
}

func (m *mockABCI) ApplySnapshotChunk(req *abci.RequestApplySnapshotChunk) (*abci.ResponseApplySnapshotChunk, error) {
	args := m.Called(req)
	r, _ := args.Get(0).(*abci.ResponseApplySnapshotChunk)
	return r, args.Error(1)
}

// buildEnv sets up a test Environment and returns it along with the canonical
// ValidatorSet and BlockID used for verification.
func buildEnv(t *testing.T, height uint64, keys []cmted25519.PrivKey, signers []int, chainID string) (*core.Environment, *cmttypes.ValidatorSet, cmttypes.BlockID) {
	t.Helper()

	blockIDHash := bytes.Repeat([]byte{0xab}, 32)

	// Build canonical ValidatorSet (NewValidatorSet sorts by address internally).
	vals := make([]*cmttypes.Validator, len(keys))
	for i, k := range keys {
		vals[i] = cmttypes.NewValidator(k.PubKey(), 1)
	}
	valSet := cmttypes.NewValidatorSet(vals)

	// Map raw 20-byte address → private key for signing.
	privByAddr := map[string]cmted25519.PrivKey{}
	for _, priv := range keys {
		privByAddr[string(priv.PubKey().Address())] = priv
	}

	// Build ordered list following valSet order (already sorted by address).
	consAddrs := make([]string, len(valSet.Validators))
	pubkeyAnys := make([]*codectypes.Any, len(valSet.Validators))
	for i, v := range valSet.Validators {
		sdkPk, err := cryptocodec.FromCmtPubKeyInterface(v.PubKey)
		require.NoError(t, err)
		consAddrs[i] = sdk.ConsAddress(v.Address).String()
		any, err := codectypes.NewAnyWithValue(sdkPk)
		require.NoError(t, err)
		pubkeyAnys[i] = any
	}

	// Sign for each selected signer index (indices into valSet.Validators).
	signatures := map[string][]byte{}
	for _, i := range signers {
		priv := privByAddr[string(valSet.Validators[i].Address)]
		v := cmtproto.Vote{
			Type:             cmtproto.PrecommitType,
			Height:           int64(height), //nolint:gosec
			Round:            0,
			BlockID:          cmtproto.BlockID{Hash: blockIDHash, PartSetHeader: cmtproto.PartSetHeader{}},
			Timestamp:        time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC),
			ValidatorAddress: valSet.Validators[i].Address,
			ValidatorIndex:   int32(i), //nolint:gosec
		}
		sb := cmttypes.VoteSignBytes(chainID, &v)
		sig, err := priv.Sign(sb)
		require.NoError(t, err)
		v.Signature = sig
		bz, err := proto.Marshal(&v)
		require.NoError(t, err)
		signatures[consAddrs[i]] = bz
	}

	// Prepare AttesterSet query response (index == position in sorted valSet).
	setEntries := make([]networktypes.AttesterSetEntry, 0, len(valSet.Validators))
	for i := range valSet.Validators {
		setEntries = append(setEntries, networktypes.AttesterSetEntry{
			Authority:        sdk.AccAddress(valSet.Validators[i].Address).String(),
			ConsensusAddress: consAddrs[i],
			Index:            uint32(i), //nolint:gosec
			Pubkey:           pubkeyAnys[i],
		})
	}
	setRespBz, err := proto.Marshal(&networktypes.QueryAttesterSetResponse{Entries: setEntries})
	require.NoError(t, err)

	// Prepare AttesterSignatures query response.
	sigList := make([]*networktypes.AttesterSignature, 0, len(signatures))
	for consAddr, sig := range signatures {
		sigList = append(sigList, &networktypes.AttesterSignature{
			ValidatorAddress: consAddr,
			Signature:        sig,
		})
	}
	sigRespBz, err := proto.Marshal(&networktypes.QueryAttesterSignaturesResponse{Signatures: sigList})
	require.NoError(t, err)

	mApp := new(mockABCI)
	mApp.On("Query", mock.Anything, mock.MatchedBy(func(r *abci.RequestQuery) bool {
		return r.Path == "/evabci.network.v1.Query/AttesterSet"
	})).Return(&abci.ResponseQuery{Code: 0, Value: setRespBz}, nil)
	mApp.On("Query", mock.Anything, mock.MatchedBy(func(r *abci.RequestQuery) bool {
		return r.Path == "/evabci.network.v1.Query/AttesterSignatures"
	})).Return(&abci.ResponseQuery{Code: 0, Value: sigRespBz}, nil)

	// Use real store with in-memory backend, save the block ID.
	dsStore := ds.NewMapDatastore()
	abciExecStore := execstore.NewExecABCIStore(dsStore)
	blockID := cmttypes.BlockID{Hash: blockIDHash, PartSetHeader: cmttypes.PartSetHeader{}}
	err = abciExecStore.SaveBlockID(context.Background(), height, &blockID)
	require.NoError(t, err)

	env := &core.Environment{
		Adapter: &adapter.Adapter{
			App:   mApp,
			Store: abciExecStore,
		},
		AttesterMode: true,
		Logger:       cmtlog.NewNopLogger(),
	}

	return env, valSet, blockID
}

func TestGetCommitForHeight_QuorumMet_SortedWithAbsent(t *testing.T) {
	chainID := "test-chain"
	keys := []cmted25519.PrivKey{
		cmted25519.GenPrivKey(), cmted25519.GenPrivKey(),
		cmted25519.GenPrivKey(), cmted25519.GenPrivKey(),
	}
	signers := []int{0, 1, 2} // 3 of 4 — quorum
	env, valSet, blockID := buildEnv(t, 100, keys, signers, chainID)

	commit, err := core.GetCommitForHeightForTest(context.Background(), env, 100)
	require.NoError(t, err)
	require.Equal(t, int64(100), commit.Height)
	require.Equal(t, int32(0), commit.Round)
	require.Equal(t, blockID, commit.BlockID)
	require.Len(t, commit.Signatures, 4)

	// Committed validator addresses must be in ascending order.
	var addrs [][]byte
	for _, cs := range commit.Signatures {
		if cs.BlockIDFlag == cmttypes.BlockIDFlagCommit {
			addrs = append(addrs, cs.ValidatorAddress)
		}
	}
	require.True(t, sort.SliceIsSorted(addrs, func(i, j int) bool {
		return bytes.Compare(addrs[i], addrs[j]) < 0
	}))

	var commitCnt, absentCnt int
	for _, cs := range commit.Signatures {
		switch cs.BlockIDFlag {
		case cmttypes.BlockIDFlagCommit:
			commitCnt++
		case cmttypes.BlockIDFlagAbsent:
			absentCnt++
		}
	}
	require.Equal(t, 3, commitCnt)
	require.Equal(t, 1, absentCnt)

	// 07-tendermint light client must accept this commit.
	require.NoError(t, valSet.VerifyCommitLight(chainID, blockID, 100, commit))
}

func TestGetCommitForHeight_NoQuorum_Error(t *testing.T) {
	chainID := "test-chain"
	keys := []cmted25519.PrivKey{
		cmted25519.GenPrivKey(), cmted25519.GenPrivKey(),
		cmted25519.GenPrivKey(), cmted25519.GenPrivKey(),
	}
	signers := []int{0, 1} // 2 of 4 — not > 2/3
	env, _, _ := buildEnv(t, 200, keys, signers, chainID)

	_, err := core.GetCommitForHeightForTest(context.Background(), env, 200)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not yet attested")
}

func TestGetCommitForHeight_AllSigned(t *testing.T) {
	chainID := "test-chain"
	keys := []cmted25519.PrivKey{
		cmted25519.GenPrivKey(), cmted25519.GenPrivKey(),
		cmted25519.GenPrivKey(), cmted25519.GenPrivKey(),
	}
	env, valSet, blockID := buildEnv(t, 300, keys, []int{0, 1, 2, 3}, chainID)

	commit, err := core.GetCommitForHeightForTest(context.Background(), env, 300)
	require.NoError(t, err)
	require.Len(t, commit.Signatures, 4)
	for _, cs := range commit.Signatures {
		require.Equal(t, cmttypes.BlockIDFlagCommit, cs.BlockIDFlag)
	}
	require.NoError(t, valSet.VerifyCommitLight(chainID, blockID, 300, commit))
}
