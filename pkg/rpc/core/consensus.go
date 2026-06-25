package core

import (
	"fmt"

	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/crypto/codec"
)

// Validators gets the validator set at the given block height.
//
// If no height is provided, it will fetch the latest validator set. Note the
// validators are sorted by their voting power - this is the canonical order
// for the validators in the set as used in computing their Merkle root.
//
// More: https://docs.cometbft.com/v0.37/rpc/#/Info/validators
func Validators(ctx *rpctypes.Context, heightPtr *int64, _, _ *int) (*coretypes.ResultValidators, error) {
	height, err := normalizeHeight(ctx.Context(), heightPtr)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize height: %w", err)
	}

	// In attester mode, return the full fixed attester set. /commit uses the
	// same set and marks missing signatures as absent, so the two endpoints must
	// stay aligned for light-client verification.
	if env.AttesterMode {
		env.Logger.Info("Validators endpoint in attester mode - returning full attester set", "height", height)
		entries, err := getAttesterSet(ctx.Context())
		if err != nil {
			return nil, fmt.Errorf("get attester set: %w", err)
		}
		validators, err := buildValidatorSetFromAttesterSet(entries)
		if err != nil {
			return nil, fmt.Errorf("build validator set from attesters: %w", err)
		}

		return &coretypes.ResultValidators{
			BlockHeight: int64(height), //nolint:gosec
			Validators:  validators,
			Count:       len(validators),
			Total:       len(validators),
		}, nil
	}

	// Non-attester mode: return genesis validator (original behavior)
	env.Logger.Info("Validators endpoint in sequencer mode - returning genesis validator")
	return getGenesisValidatorSet(height)
}

// getGenesisValidatorSet returns the genesis validator set
func getGenesisValidatorSet(height uint64) (*coretypes.ResultValidators, error) {
	genesisValidators := env.Adapter.AppGenesis.Consensus.Validators
	if len(genesisValidators) != 1 {
		return nil, fmt.Errorf("there should be exactly one validator in genesis")
	}

	// Since it's a centralized sequencer
	// changed behavior to get this from genesis
	genesisValidator := genesisValidators[0]
	validator := cmttypes.Validator{
		Address:          genesisValidator.Address,
		PubKey:           genesisValidator.PubKey,
		VotingPower:      genesisValidator.Power,
		ProposerPriority: int64(1),
	}

	return &coretypes.ResultValidators{
		BlockHeight: int64(height), //nolint:gosec
		Validators: []*cmttypes.Validator{
			&validator,
		},
		Count: 1,
		Total: 1,
	}, nil
}

func buildValidatorSetFromAttesterSet(entries []attesterSetEntry) ([]*cmttypes.Validator, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("no attesters found")
	}

	validators := make([]*cmttypes.Validator, 0, len(entries))
	for _, e := range entries {
		cmtPubKey, err := codec.ToCmtPubKeyInterface(e.Pubkey)
		if err != nil {
			return nil, fmt.Errorf("convert pubkey for %s: %w", e.ConsensusAddress, err)
		}
		validators = append(validators, &cmttypes.Validator{
			Address:          e.ValidatorAddress,
			PubKey:           cmtPubKey,
			VotingPower:      1,
			ProposerPriority: 0,
		})
	}
	return validators, nil
}

// DumpConsensusState dumps consensus state.
// UNSTABLE
// More: https://docs.cometbft.com/v0.37/rpc/#/Info/dump_consensus_state
func DumpConsensusState(_ *rpctypes.Context) (*coretypes.ResultDumpConsensusState, error) {
	// Evolve doesn't have CometBFT consensus state.
	return nil, ErrConsensusStateNotAvailable
}

// ConsensusState returns a concise summary of the consensus state.
// UNSTABLE
// More: https://docs.cometbft.com/v0.37/rpc/#/Info/consensus_state
func ConsensusState(_ *rpctypes.Context) (*coretypes.ResultConsensusState, error) {
	// Evolve doesn't have CometBFT consensus state.
	return nil, ErrConsensusStateNotAvailable
}

// ConsensusParams gets the consensus parameters at the given block height.
// If no height is provided, it will fetch the latest consensus params.
// More: https://docs.cometbft.com/v0.37/rpc/#/Info/consensus_params
func ConsensusParams(ctx *rpctypes.Context, heightPtr *int64) (*coretypes.ResultConsensusParams, error) {
	height, err := normalizeHeight(ctx.Context(), heightPtr)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize height: %w", err)
	}

	state, err := env.Adapter.Store.LoadState(ctx.Context())
	if err != nil {
		return nil, err
	}

	params := state.ConsensusParams
	return &coretypes.ResultConsensusParams{
		BlockHeight: int64(height), //nolint:gosec
		ConsensusParams: cmttypes.ConsensusParams{
			Block: cmttypes.BlockParams{
				MaxBytes: params.Block.MaxBytes,
				MaxGas:   params.Block.MaxGas,
			},
			Evidence: cmttypes.EvidenceParams{
				MaxAgeNumBlocks: params.Evidence.MaxAgeNumBlocks,
				MaxAgeDuration:  params.Evidence.MaxAgeDuration,
				MaxBytes:        params.Evidence.MaxBytes,
			},
			Validator: cmttypes.ValidatorParams{
				PubKeyTypes: params.Validator.PubKeyTypes,
			},
			Version: cmttypes.VersionParams{
				App: params.Version.App,
			},
		},
	}, nil
}
