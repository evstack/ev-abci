package keeper_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"testing"
	"time"

	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	"github.com/evstack/ev-abci/modules/network"
	"github.com/evstack/ev-abci/modules/network/keeper"
	"github.com/evstack/ev-abci/modules/network/types"
)

// staticBlockIDProvider returns the same BlockID hash for every height —
// mirrors a fixed sequencer view inside unit tests.
type staticBlockIDProvider struct{ hash []byte }

func (s staticBlockIDProvider) GetBlockID(_ context.Context, _ uint64) (*cmttypes.BlockID, error) {
	return &cmttypes.BlockID{Hash: s.hash}, nil
}

func TestAttesterCommitVerifiesAsIBCLightClient(t *testing.T) {
	chainID := "ibc-test-chain"
	const height int64 = 100

	// 1. Three attesters with fresh keys.
	privs := []cmted25519.PrivKey{
		cmted25519.GenPrivKey(),
		cmted25519.GenPrivKey(),
		cmted25519.GenPrivKey(),
	}
	attesters := make([]types.AttesterInfo, 0, len(privs))
	for _, p := range privs {
		pub := p.PubKey().(cmted25519.PubKey)
		sdkPk, err := cryptocodec.FromCmtPubKeyInterface(pub)
		require.NoError(t, err)
		ai, err := types.NewAttesterInfo(sdk.AccAddress(pub.Address()).String(), sdkPk, 0)
		require.NoError(t, err)
		attesters = append(attesters, *ai)
	}

	// 2. Set up keeper, init genesis with the 3 attesters, advance ctx to height.
	k, ctx, _ := newKeeperForGenesis(t)
	gs := types.GenesisState{
		Params:        types.DefaultParams(),
		AttesterInfos: attesters,
	}
	require.NoError(t, network.InitGenesis(ctx, k, gs))
	ctx = ctx.WithBlockHeader(cmtproto.Header{ChainID: chainID, Height: height}).
		WithChainID(chainID)

	// 3. Deterministic BlockID hash (what all attesters sign).
	blockIDHash := ibcMakeBlockHash(fmt.Sprintf("height-%d", height))
	k.SetBlockIDProvider(staticBlockIDProvider{hash: blockIDHash})

	// 4. Each attester signs and submits a real MsgAttest (signature-verified path).
	msgServer := keeper.NewMsgServerImpl(k)
	for _, p := range privs {
		pub := p.PubKey().(cmted25519.PubKey)
		consAddr := sdk.ConsAddress(pub.Address()).String()
		authority := sdk.AccAddress(pub.Address()).String()
		voteBytes := ibcSignVote(t, chainID, height, p, blockIDHash)
		_, err := msgServer.Attest(ctx, &types.MsgAttest{
			Authority:        authority,
			ConsensusAddress: consAddr,
			Height:           height,
			Vote:             voteBytes,
		})
		require.NoError(t, err, "MsgAttest rejected for consAddr=%s", consAddr)
	}

	// 5. Read state and assemble a cmttypes.Commit in ValidatorIndex order.
	commit := ibcAssembleCommit(t, k, ctx, height, blockIDHash)

	// 6. Canonical ValidatorSet (NewValidatorSet sorts by address asc, matching genesis).
	valSet := ibcBuildValidatorSet(attesters)
	blockID := cmttypes.BlockID{Hash: blockIDHash, PartSetHeader: cmttypes.PartSetHeader{}}

	// 7. 07-tendermint verification — the decisive assertion.
	require.NoError(t, valSet.VerifyCommitLight(chainID, blockID, height, commit),
		"reconstructed commit must pass 07-tendermint light-client verification")
	require.Len(t, commit.Signatures, 3, "every set member must appear in commit")
	for _, cs := range commit.Signatures {
		require.Equal(t, cmttypes.BlockIDFlagCommit, cs.BlockIDFlag)
	}
}

func TestAttesterCommit_BelowQuorum(t *testing.T) {
	chainID := "ibc-test-chain"
	const height int64 = 200

	privs := []cmted25519.PrivKey{
		cmted25519.GenPrivKey(),
		cmted25519.GenPrivKey(),
		cmted25519.GenPrivKey(),
	}
	attesters := make([]types.AttesterInfo, 0, len(privs))
	for _, p := range privs {
		pub := p.PubKey().(cmted25519.PubKey)
		sdkPk, err := cryptocodec.FromCmtPubKeyInterface(pub)
		require.NoError(t, err)
		ai, err := types.NewAttesterInfo(sdk.AccAddress(pub.Address()).String(), sdkPk, 0)
		require.NoError(t, err)
		attesters = append(attesters, *ai)
	}

	k, ctx, _ := newKeeperForGenesis(t)
	gs := types.GenesisState{
		Params:        types.DefaultParams(),
		AttesterInfos: attesters,
	}
	require.NoError(t, network.InitGenesis(ctx, k, gs))
	ctx = ctx.WithBlockHeader(cmtproto.Header{ChainID: chainID, Height: height}).
		WithChainID(chainID)

	blockIDHash := ibcMakeBlockHash(fmt.Sprintf("height-%d", height))
	k.SetBlockIDProvider(staticBlockIDProvider{hash: blockIDHash})

	// Only 1 of 3 signs — below 2/3 quorum; LastAttestedHeight must not advance.
	msgServer := keeper.NewMsgServerImpl(k)
	p := privs[0]
	pub := p.PubKey().(cmted25519.PubKey)
	_, err := msgServer.Attest(ctx, &types.MsgAttest{
		Authority:        sdk.AccAddress(pub.Address()).String(),
		ConsensusAddress: sdk.ConsAddress(pub.Address()).String(),
		Height:           height,
		Vote:             ibcSignVote(t, chainID, height, p, blockIDHash),
	})
	require.NoError(t, err)

	lastAttested, err := k.GetLastAttestedHeight(ctx)
	require.NoError(t, err)
	require.Less(t, lastAttested, height, "LastAttestedHeight should not advance below quorum")
}

// -- helpers --

func ibcMakeBlockHash(seed string) []byte {
	h := sha256.Sum256([]byte(seed))
	return h[:]
}

// ibcSignVote builds and signs a precommit vote, returning the marshaled proto bytes.
func ibcSignVote(t *testing.T, chainID string, height int64, priv cmted25519.PrivKey, blockIDHash []byte) []byte {
	t.Helper()
	pub := priv.PubKey().(cmted25519.PubKey)
	v := cmtproto.Vote{
		Type:             cmtproto.PrecommitType,
		Height:           height,
		Round:            0,
		BlockID:          cmtproto.BlockID{Hash: blockIDHash, PartSetHeader: cmtproto.PartSetHeader{}},
		Timestamp:        time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC),
		ValidatorAddress: pub.Address(),
		ValidatorIndex:   0,
	}
	sb := cmttypes.VoteSignBytes(chainID, &v)
	sig, err := priv.Sign(sb)
	require.NoError(t, err)
	v.Signature = sig
	out, err := proto.Marshal(&v)
	require.NoError(t, err)
	return out
}

func ibcBuildValidatorSet(attesters []types.AttesterInfo) *cmttypes.ValidatorSet {
	vals := make([]*cmttypes.Validator, 0, len(attesters))
	for _, a := range attesters {
		pk, err := a.GetPubKey()
		if err != nil {
			panic(err)
		}
		cmtPk, err := cryptocodec.ToCmtPubKeyInterface(pk)
		if err != nil {
			panic(err)
		}
		vals = append(vals, cmttypes.NewValidator(cmtPk, 1))
	}
	// NewValidatorSet sorts internally by address ascending.
	return cmttypes.NewValidatorSet(vals)
}

// ibcAssembleCommit reads ValidatorIndex + Signatures from keeper state and
// assembles a Commit ordered by ValidatorIndex (mirrors the /commit RPC path).
func ibcAssembleCommit(t *testing.T, k keeper.Keeper, ctx sdk.Context, height int64, blockIDHash []byte) *cmttypes.Commit {
	t.Helper()

	type entry struct {
		consAddr string
		addr     []byte
		index    uint16
	}

	var entries []entry
	require.NoError(t, k.ValidatorIndex.Walk(ctx, nil, func(addr string, idx uint16) (bool, error) {
		info, err := k.GetAttesterInfo(ctx, addr)
		if err != nil {
			return false, err
		}
		pk, err := info.GetPubKey()
		if err != nil {
			return false, err
		}
		entries = append(entries, entry{
			consAddr: addr,
			addr:     pk.Address(),
			index:    idx,
		})
		return false, nil
	}))
	sort.Slice(entries, func(i, j int) bool { return entries[i].index < entries[j].index })

	sigs := make([]cmttypes.CommitSig, 0, len(entries))
	for _, e := range entries {
		has, err := k.HasSignature(ctx, height, e.consAddr)
		require.NoError(t, err)
		if !has {
			sigs = append(sigs, cmttypes.CommitSig{BlockIDFlag: cmttypes.BlockIDFlagAbsent})
			continue
		}
		voteBytes, err := k.GetSignature(ctx, height, e.consAddr)
		require.NoError(t, err)
		var vote cmtproto.Vote
		require.NoError(t, proto.Unmarshal(voteBytes, &vote))
		sigs = append(sigs, cmttypes.CommitSig{
			BlockIDFlag:      cmttypes.BlockIDFlagCommit,
			ValidatorAddress: e.addr,
			Timestamp:        vote.Timestamp,
			Signature:        vote.Signature,
		})
	}

	// Sanity: validator addresses in commit order must be ascending.
	prev := []byte(nil)
	for _, s := range sigs {
		if s.BlockIDFlag == cmttypes.BlockIDFlagCommit && prev != nil {
			require.True(t, bytes.Compare(prev, s.ValidatorAddress) < 0,
				"validator addresses not ascending in commit")
		}
		if s.BlockIDFlag == cmttypes.BlockIDFlagCommit {
			prev = s.ValidatorAddress
		}
	}

	return &cmttypes.Commit{
		Height:     height,
		Round:      0,
		BlockID:    cmttypes.BlockID{Hash: blockIDHash, PartSetHeader: cmttypes.PartSetHeader{}},
		Signatures: sigs,
	}
}
