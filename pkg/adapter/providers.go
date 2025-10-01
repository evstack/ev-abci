package adapter

import (
	"bytes"
	"context"
	stdsha256 "crypto/sha256"
	"encoding/hex"
	"fmt"

	tmcryptoed25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/libp2p/go-libp2p/core/crypto"

	evtypes "github.com/evstack/ev-node/types"
)

func AggregatorNodeSignatureBytesProvider(adapter *Adapter) evtypes.AggregatorNodeSignatureBytesProvider {
	return func(header *evtypes.Header) ([]byte, error) {
		blockID, err := adapter.Store.GetBlockID(context.Background(), header.Height())
		if err != nil {
			return nil, err
		}

		fmt.Printf("-----------agg node (height %d)------------\n", header.Height())
		if blockID != nil {
			fmt.Printf("AGG BlockID: Hash=%x, PartSetHeader=%+v\n", blockID.Hash, blockID.PartSetHeader)
		} else {
			fmt.Println("AGG BlockID: nil")
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

		fmt.Printf("-----------sync node (height %d)------------\n", header.Height())
		if blockID != nil {
			fmt.Printf("SYNC BlockID: Hash=%x, PartSetHeader=%+v\n", blockID.Hash, blockID.PartSetHeader)
		} else {
			fmt.Println("SYNC BlockID: nil")
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

	fmt.Println(vote)
	fmt.Println("-----------------------")

	return consensusVoteBytes
}

// ValidatorHasher returns a function that calculates the ValidatorHash
// compatible with CometBFT. This function is intended to be injected into ev-node's Manager.
func ValidatorHasherProvider() func(proposerAddress []byte, pubKey crypto.PubKey) (evtypes.Hash, error) {
	return func(proposerAddress []byte, pubKey crypto.PubKey) (evtypes.Hash, error) {
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
		if !bytes.Equal(derivedAddress, proposerAddress) {
			return calculatedHash, fmt.Errorf("CRITICAL MISMATCH - derived validator address (%s) does not match expected proposer address (%s). PubKey used for derivation: %s",
				hex.EncodeToString(derivedAddress),
				hex.EncodeToString(proposerAddress),
				hex.EncodeToString(cometBftPubKey.Bytes()))
		}

		sequencerValidatorSet := cmttypes.NewValidatorSet([]*cmttypes.Validator{sequencerValidator})

		hashSumBytes := sequencerValidatorSet.Hash()

		calculatedHash = make(evtypes.Hash, stdsha256.Size)
		copy(calculatedHash, hashSumBytes)

		return calculatedHash, nil
	}
}
