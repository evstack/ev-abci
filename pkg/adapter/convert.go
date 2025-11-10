package adapter

import (
	"errors"
	"fmt"

	cmbytes "github.com/cometbft/cometbft/libs/bytes"
	cmprotoversion "github.com/cometbft/cometbft/proto/tendermint/version"
	cmtstate "github.com/cometbft/cometbft/state"
	cmttypes "github.com/cometbft/cometbft/types"
	cmtversion "github.com/cometbft/cometbft/version"

	evtypes "github.com/evstack/ev-node/types"
)

// ToABCIHeader converts rollkit header format defined by ABCI.
func ToABCIHeader(header evtypes.Header, lastCommit *cmttypes.Commit) (cmttypes.Header, error) {
	if len(header.ProposerAddress) == 0 {
		return cmttypes.Header{}, errors.New("proposer address is not set")
	}

	return cmttypes.Header{
		Version: cmprotoversion.Consensus{
			Block: cmtversion.BlockProtocol,
			App:   header.Version.App,
		},
		Height:             int64(header.Height()), //nolint:gosec
		Time:               header.Time(),
		LastBlockID:        lastCommit.BlockID,
		LastCommitHash:     lastCommit.Hash(),
		DataHash:           cmbytes.HexBytes(header.DataHash),
		ConsensusHash:      cmbytes.HexBytes(make(evtypes.Hash, 32)),
		AppHash:            cmbytes.HexBytes(header.AppHash),
		LastResultsHash:    nil, // not set
		EvidenceHash:       new(cmttypes.EvidenceData).Hash(),
		ProposerAddress:    header.ProposerAddress,
		ChainID:            header.ChainID(),
		ValidatorsHash:     cmbytes.HexBytes(header.ValidatorHash),
		NextValidatorsHash: cmbytes.HexBytes(header.ValidatorHash),
	}, nil
}

// ToABCIBlock converts rollit block into block format defined by ABCI.
func ToABCIBlock(header cmttypes.Header, lastCommit *cmttypes.Commit, data *evtypes.Data) (*cmttypes.Block, error) {
	abciBlock := cmttypes.Block{
		Header: header,
		Evidence: cmttypes.EvidenceData{
			Evidence: nil,
		},
		LastCommit: lastCommit,
	}

	abciBlock.Txs = make([]cmttypes.Tx, len(data.Txs))
	for i := range data.Txs {
		abciBlock.Txs[i] = cmttypes.Tx(data.Txs[i])
	}

	return &abciBlock, nil
}

// ToABCIBlockMeta converts an ABCI block into a BlockMeta format.
func ToABCIBlockMeta(abciBlock *cmttypes.Block) (*cmttypes.BlockMeta, error) {
	blockParts, err := abciBlock.MakePartSet(cmttypes.BlockPartSizeBytes)
	if err != nil {
		return nil, fmt.Errorf("make part set: %w", err)
	}

	return &cmttypes.BlockMeta{
		BlockID: cmttypes.BlockID{
			Hash:          abciBlock.Header.Hash(),
			PartSetHeader: blockParts.Header(),
		},
		BlockSize: abciBlock.Size(),
		Header:    abciBlock.Header,
		NumTxs:    len(abciBlock.Txs),
	}, nil
}

// MakeABCIBlock makes an ABCI block and its block ID.
func MakeABCIBlock(
	blockHeight uint64,
	cmtTxs cmttypes.Txs,
	currentState *cmtstate.State,
	abciHeader cmttypes.Header,
	lastCommit *cmttypes.Commit,
) (*cmttypes.Block, *cmttypes.BlockID, error) {
	abciBlock := currentState.MakeBlock(
		int64(blockHeight),
		cmtTxs,
		lastCommit,
		nil,
		currentState.Validators.Proposer.Address,
	)

	blockParts, err := abciBlock.MakePartSet(cmttypes.BlockPartSizeBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("make part set: %w", err)
	}

	// use abci header hash to match the light client validation check
	// where sh.Header.Hash() (comet header) must equal sh.Commit.BlockID.Hash
	currentBlockID := &cmttypes.BlockID{
		Hash:          abciHeader.Hash(),
		PartSetHeader: blockParts.Header(),
	}

	return abciBlock, currentBlockID, nil
}
