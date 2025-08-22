package cometcompat

import (
	"bytes"
	stdsha256 "crypto/sha256"
	"encoding/hex"
	"fmt"

	tmcryptoed25519 "github.com/cometbft/cometbft/crypto/ed25519"
	tmtypes "github.com/cometbft/cometbft/types"
	"github.com/libp2p/go-libp2p/core/crypto"

	rollkittypes "github.com/evstack/ev-node/types"
)

// validatorHasher returns a function that calculates the ValidatorHash
// compatible with CometBFT. This function is intended to be injected into Rollkit's Manager.
func validatorHasher(proposerAddress []byte, pubKey crypto.PubKey) (rollkittypes.Hash, error) {
	var calculatedHash rollkittypes.Hash

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

	derivedAddress := sequencerValidator.Address.Bytes()
	if !bytes.Equal(derivedAddress, proposerAddress) {
		return calculatedHash, fmt.Errorf("CRITICAL MISMATCH - derived validator address (%s) does not match expected proposer address (%s). PubKey used for derivation: %s",
			hex.EncodeToString(derivedAddress),
			hex.EncodeToString(proposerAddress),
			hex.EncodeToString(cometBftPubKey.Bytes()))
	}

	sequencerValidatorSet := tmtypes.NewValidatorSet([]*tmtypes.Validator{sequencerValidator})

	hashSumBytes := sequencerValidatorSet.Hash()

	calculatedHash = make(rollkittypes.Hash, stdsha256.Size)
	copy(calculatedHash, hashSumBytes)

	return calculatedHash, nil
}

// ValidatorsHasher calculates the hash of a validator set, compatible with CometBFT.
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
