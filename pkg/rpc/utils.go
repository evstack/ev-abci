package rpc

import (
	"errors"
	"time"

	cmbytes "github.com/cometbft/cometbft/libs/bytes"
	cmproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmversion "github.com/cometbft/cometbft/proto/tendermint/version"
	cmtypes "github.com/cometbft/cometbft/types"

	"github.com/rollkit/rollkit/types"
)

// ToABCIHeaderPB converts Rollkit header to Header format defined in ABCI.
// Caller should fill all the fields that are not available in Rollkit header (like ChainID).
func ToABCIHeaderPB(header *types.Header) (cmproto.Header, error) {
	return cmproto.Header{
		Version: cmversion.Consensus{
			Block: header.Version.Block,
			App:   header.Version.App,
		},
		Height: int64(header.Height()), //nolint:gosec
		Time:   header.Time(),
		LastBlockId: cmproto.BlockID{
			Hash: header.LastHeaderHash[:],
			PartSetHeader: cmproto.PartSetHeader{
				Total: 0,
				Hash:  nil,
			},
		},
		LastCommitHash:     header.LastCommitHash[:],
		DataHash:           header.DataHash[:],
		ConsensusHash:      header.ConsensusHash[:],
		AppHash:            header.AppHash[:],
		LastResultsHash:    header.LastResultsHash[:],
		EvidenceHash:       new(cmtypes.EvidenceData).Hash(),
		ProposerAddress:    header.ProposerAddress,
		ChainID:            header.ChainID(),
		ValidatorsHash:     header.ValidatorHash,
		NextValidatorsHash: header.ValidatorHash,
	}, nil
}

// ToABCIHeader converts Rollkit header to Header format defined in ABCI.
// Caller should fill all the fields that are not available in Rollkit header (like ChainID).
func ToABCIHeader(header *types.Header) (cmtypes.Header, error) {
	return cmtypes.Header{
		Version: cmversion.Consensus{
			Block: header.Version.Block,
			App:   header.Version.App,
		},
		Height: int64(header.Height()), //nolint:gosec
		Time:   header.Time(),
		LastBlockID: cmtypes.BlockID{
			Hash: cmbytes.HexBytes(header.LastHeaderHash[:]),
			PartSetHeader: cmtypes.PartSetHeader{
				Total: 0,
				Hash:  nil,
			},
		},
		LastCommitHash:     cmbytes.HexBytes(header.LastCommitHash),
		DataHash:           cmbytes.HexBytes(header.DataHash),
		ConsensusHash:      cmbytes.HexBytes(header.ConsensusHash),
		AppHash:            cmbytes.HexBytes(header.AppHash),
		LastResultsHash:    cmbytes.HexBytes(header.LastResultsHash),
		EvidenceHash:       new(cmtypes.EvidenceData).Hash(),
		ProposerAddress:    header.ProposerAddress,
		ChainID:            header.ChainID(),
		ValidatorsHash:     cmbytes.HexBytes(header.ValidatorHash),
		NextValidatorsHash: cmbytes.HexBytes(header.ValidatorHash),
	}, nil
}

// ToABCIBlock converts Rolkit block into block format defined by ABCI.
// Returned block should pass `ValidateBasic`.
func ToABCIBlock(header *types.SignedHeader, data *types.Data) (*cmtypes.Block, error) {
	abciHeader, err := ToABCIHeader(&header.Header)
	if err != nil {
		return nil, err
	}

	// we have one validator
	if len(header.ProposerAddress) == 0 {
		return nil, errors.New("proposer address is not set")
	}

	abciCommit := getABCICommit(header.Height(), header.Hash(), header.ProposerAddress, header.Time(), header.Signature)

	// This assumes that we have only one signature
	if len(abciCommit.Signatures) == 1 {
		abciCommit.Signatures[0].ValidatorAddress = header.ProposerAddress
	}
	abciBlock := cmtypes.Block{
		Header: abciHeader,
		Evidence: cmtypes.EvidenceData{
			Evidence: nil,
		},
		LastCommit: abciCommit,
	}
	abciBlock.Txs = make([]cmtypes.Tx, len(data.Txs))
	for i := range data.Txs {
		abciBlock.Txs[i] = cmtypes.Tx(data.Txs[i])
	}
	abciBlock.DataHash = cmbytes.HexBytes(header.DataHash)

	return &abciBlock, nil
}

// ToABCIBlockMeta converts Rollkit block into BlockMeta format defined by ABCI
func ToABCIBlockMeta(header *types.SignedHeader, data *types.Data) (*cmtypes.BlockMeta, error) {
	cmblock, err := ToABCIBlock(header, data)
	if err != nil {
		return nil, err
	}
	blockID := cmtypes.BlockID{Hash: cmblock.Hash()}

	return &cmtypes.BlockMeta{
		BlockID:   blockID,
		BlockSize: cmblock.Size(),
		Header:    cmblock.Header,
		NumTxs:    len(cmblock.Txs),
	}, nil
}

// getABCICommit returns a commit format defined by ABCI.
// Other fields (especially ValidatorAddress and Timestamp of Signature) have to be filled by caller.
func getABCICommit(height uint64, hash []byte, val cmtypes.Address, time time.Time, signature []byte) *cmtypes.Commit {
	tmCommit := cmtypes.Commit{
		Height: int64(height), //nolint:gosec
		Round:  0,
		BlockID: cmtypes.BlockID{
			Hash:          cmbytes.HexBytes(hash),
			PartSetHeader: cmtypes.PartSetHeader{},
		},
		Signatures: make([]cmtypes.CommitSig, 1),
	}
	commitSig := cmtypes.CommitSig{
		BlockIDFlag:      cmtypes.BlockIDFlagCommit,
		Signature:        signature,
		ValidatorAddress: val,
		Timestamp:        time,
	}
	tmCommit.Signatures[0] = commitSig

	return &tmCommit
}
