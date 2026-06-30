package adapter_test

import (
	"bytes"
	"context"
	"sort"
	"testing"

	tmcryptoed25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmtstate "github.com/cometbft/cometbft/state"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/stretchr/testify/require"

	"github.com/evstack/ev-abci/pkg/adapter"
)

type mockStateStore struct {
	state *cmtstate.State
}

func (m *mockStateStore) LoadState(_ context.Context) (*cmtstate.State, error) {
	return m.state, nil
}

func TestValidatorHasherOrderingMatchesAddressSort(t *testing.T) {
	// 3 random ed25519 validators
	keys := []tmcryptoed25519.PubKey{
		tmcryptoed25519.GenPrivKey().PubKey().(tmcryptoed25519.PubKey),
		tmcryptoed25519.GenPrivKey().PubKey().(tmcryptoed25519.PubKey),
		tmcryptoed25519.GenPrivKey().PubKey().(tmcryptoed25519.PubKey),
	}

	// Canonical: sort by Address() bytes ascending, convert to libp2p, hash.
	canonicalOrder := make([]tmcryptoed25519.PubKey, len(keys))
	copy(canonicalOrder, keys)
	sort.Slice(canonicalOrder, func(i, j int) bool {
		return bytes.Compare(canonicalOrder[i].Address(), canonicalOrder[j].Address()) < 0
	})
	libp2pCanonical := make([]crypto.PubKey, len(canonicalOrder))
	for i, k := range canonicalOrder {
		p, err := crypto.UnmarshalEd25519PublicKey(k.Bytes())
		require.NoError(t, err)
		libp2pCanonical[i] = p
	}
	sequencerAddr := canonicalOrder[0].Address().Bytes()
	canonicalHash, err := adapter.ValidatorsHasher(libp2pCanonical, sequencerAddr)
	require.NoError(t, err)

	// Build a cmttypes.ValidatorSet via NewValidatorSet (will sort internally).
	vals := make([]*cmttypes.Validator, len(keys))
	for i, k := range keys {
		vals[i] = cmttypes.NewValidator(k, 1)
	}
	vs := cmttypes.NewValidatorSet(vals)

	// Wrap in a mock state and call the provider.
	st := &cmtstate.State{Validators: vs}
	store := &mockStateStore{state: st}
	hasher := adapter.ValidatorHasherFromStoreProvider(store)
	gotHash, err := hasher(sequencerAddr, nil)
	require.NoError(t, err)

	require.Equal(t, []byte(canonicalHash), []byte(gotHash),
		"provider hash must match address-sorted canonical hash")
}
