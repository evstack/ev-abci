package keeper

import (
	"bytes"
	"testing"
	"time"

	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"
)

// signTestVote builds a cmtproto.Vote for the given height and key and returns
// the protobuf-marshaled bytes with the signature attached.
func signTestVote(t *testing.T, chainID string, height int64, priv cmted25519.PrivKey, blockIDHash []byte) []byte {
	t.Helper()
	pub := priv.PubKey().(cmted25519.PubKey)
	v := cmtproto.Vote{
		Type:             cmtproto.PrecommitType,
		Height:           height,
		Round:            0,
		BlockID:          cmtproto.BlockID{Hash: blockIDHash, PartSetHeader: cmtproto.PartSetHeader{}},
		Timestamp:        testTimeUTC(),
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

// testTimeUTC returns a fixed deterministic time for vote timestamps.
func testTimeUTC() time.Time {
	return time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
}

func TestSignTestVoteCompiles(t *testing.T) {
	priv := cmted25519.GenPrivKey()
	bz := signTestVote(t, "chain", 10, priv, bytes.Repeat([]byte{0xab}, 32))
	require.NotEmpty(t, bz)
}
