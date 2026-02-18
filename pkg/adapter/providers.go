package adapter

import (
	"bytes"
	"context"
	stdsha256 "crypto/sha256"
	"encoding/hex"
	"fmt"

	tmcryptoed25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmtstate "github.com/cometbft/cometbft/state"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/libp2p/go-libp2p/core/crypto"

	evtypes "github.com/evstack/ev-node/types"
)

func SequencerNodeSignatureBytesProvider(adapter *Adapter) evtypes.AggregatorNodeSignatureBytesProvider {
	return func(header *evtypes.Header) ([]byte, error) {
		// Special-case first block: use empty BlockID to avoid race/mismatch
		// between sequencer and sync node at height 1.
		if header.Height() == 1 {
			return createVote(header, &cmttypes.BlockID{}), nil
		}

		blockID, err := adapter.Store.GetBlockID(context.Background(), header.Height())
		if err != nil {
			return nil, fmt.Errorf("get block ID: %w", err)
		}

		return createVote(header, blockID), nil
	}
}

func SyncNodeSignatureBytesProvider(adapter *Adapter) evtypes.SyncNodeSignatureBytesProvider {
	return func(ctx context.Context, header *evtypes.Header, data *evtypes.Data) ([]byte, error) {
		blockHeight := header.Height()
		cmtTxs := make(cmttypes.Txs, len(data.Txs))
		for i := range data.Txs {
			cmtTxs[i] = cmttypes.Tx(data.Txs[i])
		}

		// Special-case first block to match sequencer behavior
		if blockHeight == 1 {
			return createVote(header, &cmttypes.BlockID{}), nil
		}

		lastCommit, err := adapter.GetLastCommit(ctx, blockHeight)
		if err != nil {
			return nil, fmt.Errorf("get last commit: %w", err)
		}

		abciHeader, err := ToABCIHeader(*header, lastCommit)
		if err != nil {
			return nil, fmt.Errorf("compute header hash: %w", err)
		}

		currentState, err := adapter.Store.LoadState(ctx)
		if err != nil {
			return nil, fmt.Errorf("load state: %w", err)
		}

		_, blockID, err := MakeABCIBlock(blockHeight, cmtTxs, currentState, abciHeader, lastCommit)
		if err != nil {
			return nil, fmt.Errorf("make ABCI block: %w", err)
		}

		return createVote(header, blockID), nil
	}
}

// createVote builds the vote for the given header and block ID to be signed.
func createVote(header *evtypes.Header, blockID *cmttypes.BlockID) []byte {
	vote := cmtproto.Vote{
		Type:             cmtproto.PrecommitType,
		Height:           int64(header.Height()), //nolint:gosec
		BlockID:          blockID.ToProto(),
		Round:            0,
		Timestamp:        header.Time(),
		ValidatorAddress: header.ProposerAddress,
		ValidatorIndex:   0,
	}

	chainID := header.ChainID()
	consensusVoteBytes := cmttypes.VoteSignBytes(chainID, &vote)

	return consensusVoteBytes
}

// ValidatorHasherProvider returns a function that calculates the ValidatorHash
// compatible with CometBFT. This function is intended to be injected into ev-node's Manager.
func ValidatorHasherProvider() func(sequencerAddress []byte, pubKey crypto.PubKey) (evtypes.Hash, error) {
	return func(sequencerAddress []byte, pubKey crypto.PubKey) (evtypes.Hash, error) {
		var calculatedHash evtypes.Hash

		var cometBftPubKey tmcryptoed25519.PubKey
		if pubKey.Type() == crypto.Ed25519 {
			rawKey, err := pubKey.Raw()
			if err != nil {
				return calculatedHash, fmt.Errorf("failed to get raw bytes from libp2p public key: %w", err)
			}
			if len(rawKey) != tmcryptoed25519.PubKeySize {
				return calculatedHash, fmt.Errorf("libp2p public key size (%d) does not match CometBFT Ed25519 PubKeySize (%d)", len(rawKey), tmcryptoed25519.PubKeySize)
			}
			cometBftPubKey = rawKey
		} else {
			return calculatedHash, fmt.Errorf("unsupported public key type '%s', expected Ed25519 for CometBFT compatibility", pubKey.Type())
		}

		votingPower := int64(1)
		sequencerValidator := cmttypes.NewValidator(cometBftPubKey, votingPower)

		derivedAddress := sequencerValidator.Address.Bytes()
		if !bytes.Equal(derivedAddress, sequencerAddress) {
			return calculatedHash, fmt.Errorf("CRITICAL MISMATCH - derived validator address (%s) does not match expected sequencer address (%s). PubKey used for derivation: %s",
				hex.EncodeToString(derivedAddress),
				hex.EncodeToString(sequencerAddress),
				hex.EncodeToString(cometBftPubKey.Bytes()))
		}

		sequencerValidatorSet := cmttypes.NewValidatorSet([]*cmttypes.Validator{sequencerValidator})

		hashSumBytes := sequencerValidatorSet.Hash()

		calculatedHash = make(evtypes.Hash, stdsha256.Size)
		copy(calculatedHash, hashSumBytes)

		return calculatedHash, nil
	}
}

// ValidatorsHasher calculates the hash of a validator set, compatible with CometBFT.
// This unified implementation works for both single validator (sequencer) and multiple validators (attester) modes.
func ValidatorsHasher(validators []crypto.PubKey, sequencerAddress []byte) (evtypes.Hash, error) {
	var calculatedHash evtypes.Hash

	if len(validators) == 0 {
		// Return the hash of an empty validator set, as defined by CometBFT.
		return cmttypes.NewValidatorSet([]*cmttypes.Validator{}).Hash(), nil
	}

	tmValidators := make([]*cmttypes.Validator, len(validators))
	var sequencerFound bool

	for i, pubKey := range validators {
		var cometBftPubKey tmcryptoed25519.PubKey
		if pubKey.Type() == crypto.Ed25519 {
			rawKey, err := pubKey.Raw()
			if err != nil {
				return calculatedHash, fmt.Errorf("failed to get raw bytes from libp2p public key: %w", err)
			}
			if len(rawKey) != tmcryptoed25519.PubKeySize {
				return calculatedHash, fmt.Errorf("libp2p public key size (%d) does not match CometBFT Ed25519 PubKeySize (%d)", len(rawKey), tmcryptoed25519.PubKeySize)
			}
			cometBftPubKey = rawKey
		} else {
			return calculatedHash, fmt.Errorf("unsupported public key type '%s', expected Ed25519 for CometBFT compatibility", pubKey.Type())
		}

		votingPower := int64(1)
		sequencerValidator := cmttypes.NewValidator(cometBftPubKey, votingPower)
		tmValidators[i] = sequencerValidator

		derivedAddress := sequencerValidator.Address.Bytes()
		if bytes.Equal(derivedAddress, sequencerAddress) {
			sequencerFound = true
		}
	}

	if !sequencerFound {
		return calculatedHash, fmt.Errorf("sequencer with address (%s) not found in the validator set", hex.EncodeToString(sequencerAddress))
	}

	sequencerValidatorSet := cmttypes.NewValidatorSet(tmValidators)

	hashSumBytes := sequencerValidatorSet.Hash()

	calculatedHash = make(evtypes.Hash, stdsha256.Size)
	copy(calculatedHash, hashSumBytes)

	return calculatedHash, nil
}

// ValidatorHasherFromStoreProvider creates a ValidatorHasher that obtains validators from the ABCI store.
// This is useful for attester mode where validators are managed by the ABCI application.
func ValidatorHasherFromStoreProvider(store StateStore) func(sequencerAddress []byte, pubKey crypto.PubKey) (evtypes.Hash, error) {
	return func(sequencerAddress []byte, pubKey crypto.PubKey) (evtypes.Hash, error) {
		// Load current state from store
		state, err := store.LoadState(context.Background())
		if err != nil {
			return evtypes.Hash{}, fmt.Errorf("failed to load state from store: %w", err)
		}

		// If we have no validators in state, fallback to single validator mode
		if state.Validators == nil || len(state.Validators.Validators) == 0 {
			return ValidatorsHasher([]crypto.PubKey{pubKey}, sequencerAddress)
		}

		// Convert CometBFT validators to libp2p crypto.PubKey
		validators := make([]crypto.PubKey, len(state.Validators.Validators))
		for i, val := range state.Validators.Validators {
			// Convert CometBFT Ed25519 PubKey to libp2p Ed25519 PubKey
			if ed25519PubKey, ok := val.PubKey.(tmcryptoed25519.PubKey); ok {
				libp2pPubKey, err := crypto.UnmarshalEd25519PublicKey(ed25519PubKey.Bytes())
				if err != nil {
					return evtypes.Hash{}, fmt.Errorf("failed to convert validator %d pubkey to libp2p: %w", i, err)
				}
				validators[i] = libp2pPubKey
			} else {
				return evtypes.Hash{}, fmt.Errorf("validator %d has unsupported pubkey type: %T", i, val.PubKey)
			}
		}

		return ValidatorsHasher(validators, sequencerAddress)
	}
}

// StateStore interface defines the methods needed to load state
type StateStore interface {
	LoadState(ctx context.Context) (*cmtstate.State, error)
}
