package cometcompat

import (
	"bytes"
	"context"
	stdsha256 "crypto/sha256"
	"encoding/hex"
	"fmt"

	tmcryptoed25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmtstate "github.com/cometbft/cometbft/state"
	tmtypes "github.com/cometbft/cometbft/types"
	"github.com/libp2p/go-libp2p/core/crypto"

	rollkittypes "github.com/evstack/ev-node/types"
)

// ValidatorsHasher calculates the hash of a validator set, compatible with CometBFT.
// This unified implementation works for both single validator (sequencer) and multiple validators (attester) modes.
func ValidatorsHasher(validators []crypto.PubKey, proposerAddress []byte) (rollkittypes.Hash, error) {
	var calculatedHash rollkittypes.Hash

	if len(validators) == 0 {
		// Return the hash of an empty validator set, as defined by CometBFT.
		return tmtypes.NewValidatorSet([]*tmtypes.Validator{}).Hash(), nil
	}

	tmValidators := make([]*tmtypes.Validator, len(validators))
	var proposerFound bool

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
		sequencerValidator := tmtypes.NewValidator(cometBftPubKey, votingPower)
		tmValidators[i] = sequencerValidator

		derivedAddress := sequencerValidator.Address.Bytes()
		if bytes.Equal(derivedAddress, proposerAddress) {
			proposerFound = true
		}
	}

	if !proposerFound {
		return calculatedHash, fmt.Errorf("proposer with address (%s) not found in the validator set", hex.EncodeToString(proposerAddress))
	}

	sequencerValidatorSet := tmtypes.NewValidatorSet(tmValidators)

	hashSumBytes := sequencerValidatorSet.Hash()

	calculatedHash = make(rollkittypes.Hash, stdsha256.Size)
	copy(calculatedHash, hashSumBytes)

	return calculatedHash, nil
}

// ValidatorHasherProvider returns a function that calculates the ValidatorHash
// compatible with CometBFT. This function is intended to be injected into ev-node's Manager.
// It now uses the unified ValidatorsHasher implementation for consistency.
func ValidatorHasherProvider() func(proposerAddress []byte, pubKey crypto.PubKey) (rollkittypes.Hash, error) {
	return func(proposerAddress []byte, pubKey crypto.PubKey) (rollkittypes.Hash, error) {
		// Use the unified ValidatorsHasher with a single validator
		return ValidatorsHasher([]crypto.PubKey{pubKey}, proposerAddress)
	}
}

// ValidatorHasherFromStoreProvider creates a ValidatorHasher that obtains validators from the ABCI store.
// This is useful for attester mode where validators are managed by the ABCI application.
func ValidatorHasherFromStoreProvider(store StateStore) func(proposerAddress []byte, pubKey crypto.PubKey) (rollkittypes.Hash, error) {
	return func(proposerAddress []byte, pubKey crypto.PubKey) (rollkittypes.Hash, error) {
		// Load current state from store
		state, err := store.LoadState(context.Background())
		if err != nil {
			return rollkittypes.Hash{}, fmt.Errorf("failed to load state from store: %w", err)
		}

		// If we have no validators in state, fallback to single validator mode
		if state.Validators == nil || len(state.Validators.Validators) == 0 {
			return ValidatorsHasher([]crypto.PubKey{pubKey}, proposerAddress)
		}

		// Convert CometBFT validators to libp2p crypto.PubKey
		validators := make([]crypto.PubKey, len(state.Validators.Validators))
		for i, val := range state.Validators.Validators {
			// Convert CometBFT Ed25519 PubKey to libp2p Ed25519 PubKey
			if ed25519PubKey, ok := val.PubKey.(tmcryptoed25519.PubKey); ok {
				libp2pPubKey, err := crypto.UnmarshalEd25519PublicKey(ed25519PubKey.Bytes())
				if err != nil {
					return rollkittypes.Hash{}, fmt.Errorf("failed to convert validator %d pubkey to libp2p: %w", i, err)
				}
				validators[i] = libp2pPubKey
			} else {
				return rollkittypes.Hash{}, fmt.Errorf("validator %d has unsupported pubkey type: %T", i, val.PubKey)
			}
		}

		return ValidatorsHasher(validators, proposerAddress)
	}
}

// StateStore interface defines the methods needed to load state
type StateStore interface {
	LoadState(ctx context.Context) (*cmtstate.State, error)
}
