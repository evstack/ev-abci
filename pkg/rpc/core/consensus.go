package core

import (
	"context"
	"fmt"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/crypto/codec"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/cosmos/gogoproto/proto"
	
	networktypes "github.com/evstack/ev-abci/modules/network/types"
)

// Validators gets the validator set at the given block height.
//
// If no height is provided, it will fetch the latest validator set. Note the
// validators are sorted by their voting power - this is the canonical order
// for the validators in the set as used in computing their Merkle root.
//
// More: https://docs.cometbft.com/v0.37/rpc/#/Info/validators
func Validators(ctx *rpctypes.Context, heightPtr *int64, pagePtr, perPagePtr *int) (*coretypes.ResultValidators, error) {
	height, err := normalizeHeight(ctx.Context(), heightPtr)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize height: %w", err)
	}

	// In attester mode, return active attesters instead of genesis validator
	if env.NetworkSoftConfirmation {
		env.Logger.Info("Validators endpoint in attester mode - returning active attesters", "height", height)
		
		// Get attester signatures for this height to determine active attesters
		signatures, err := getAttesterSignatures(ctx.Context(), int64(height))
		if err != nil {
			env.Logger.Error("failed to get attester signatures", "height", height, "error", err)
			// Fallback to genesis validator if no attester signatures available
			return getGenesisValidatorSet(height)
		}

		// If no attester signatures for this height, fallback to genesis validator
		if len(signatures) == 0 {
			env.Logger.Info("no attester signatures found for height, using genesis validator", "height", height)
			return getGenesisValidatorSet(height)
		}

		// Convert attester signatures to validator set
		validators, err := buildValidatorSetFromAttesters(signatures, height)
		if err != nil {
			env.Logger.Error("failed to build validator set from attesters", "error", err)
			return getGenesisValidatorSet(height)
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

// DumpConsensusState dumps consensus state.
// UNSTABLE
// More: https://docs.cometbft.com/v0.37/rpc/#/Info/dump_consensus_state
func DumpConsensusState(ctx *rpctypes.Context) (*coretypes.ResultDumpConsensusState, error) {
	// Evolve doesn't have CometBFT consensus state.
	return nil, ErrConsensusStateNotAvailable
}

// ConsensusState returns a concise summary of the consensus state.
// UNSTABLE
// More: https://docs.cometbft.com/v0.37/rpc/#/Info/consensus_state
func ConsensusState(ctx *rpctypes.Context) (*coretypes.ResultConsensusState, error) {
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

// buildValidatorSetFromAttesters builds a CometBFT validator set from attester signatures
// using stored attester information including public keys
func buildValidatorSetFromAttesters(signatures map[string][]byte, height uint64) ([]*cmttypes.Validator, error) {
	ctx := context.Background()
	validators := make([]*cmttypes.Validator, 0, len(signatures))

	for validatorAddr, signature := range signatures {
		// Parse the signature bytes (they should be marshaled cmtproto.Vote)
		var vote cmtproto.Vote
		if err := proto.Unmarshal(signature, &vote); err != nil {
			env.Logger.Error("failed to unmarshal attester vote",
				"validator", validatorAddr, "error", err)
			continue
		}

		// Use the validator address from the vote for the consensus address
		consensusAddr := cmttypes.Address(vote.ValidatorAddress)
		if len(vote.ValidatorAddress) != 20 {
			// Fallback: try to derive from the bech32 address
			valAddrBytes, err := sdk.ValAddressFromBech32(validatorAddr)
			if err != nil {
				env.Logger.Error("failed to decode validator address",
					"validator", validatorAddr, "error", err)
				continue
			}
			// Use first 20 bytes as consensus address
			consensusAddr = cmttypes.Address(valAddrBytes[:20])
		}

		// Query the network module for attester information via ABCI
		attesterInfo, err := getAttesterInfoByAddress(ctx, validatorAddr)
		if err != nil {
			env.Logger.Error("failed to get attester info",
				"validator", validatorAddr, "error", err)
			continue
		}

		// Unpack the Any type to get the actual public key using network module codec
		var actualPubKey cryptotypes.PubKey
		if err := networktypes.ModuleCdc.InterfaceRegistry().UnpackAny(attesterInfo.Pubkey, &actualPubKey); err != nil {
			env.Logger.Error("failed to unpack public key from Any type",
				"validator", validatorAddr, "error", err)
			continue
		}

		// Convert Cosmos SDK PubKey to CometBFT PubKey using standard codec
		cmtPubKey, err := codec.ToCmtPubKeyInterface(actualPubKey)
		if err != nil {
			env.Logger.Error("failed to convert public key to CometBFT format",
				"validator", validatorAddr, "error", err)
			continue
		}

		env.Logger.Info("creating validator entry for attester", 
			"validator", validatorAddr, "address", consensusAddr.String())

		validators = append(validators, &cmttypes.Validator{
			Address:          consensusAddr,
			PubKey:           cmtPubKey,
			VotingPower:      1, // Set uniform voting power for attesters
			ProposerPriority: 0, // Set to 0 for all attesters
		})
	}

	if len(validators) == 0 {
		return nil, fmt.Errorf("no valid attester validators found")
	}

	env.Logger.Info("Built validator set from attesters", 
		"count", len(validators), "height", height)

	return validators, nil
}

// getValidatorByOperatorAddress queries the staking module for validator info
func getValidatorByOperatorAddress(ctx context.Context, valAddr sdk.ValAddress) (stakingtypes.Validator, error) {
	// Query the staking module via ABCI
	queryReq := &stakingtypes.QueryValidatorRequest{
		ValidatorAddr: valAddr.String(),
	}

	queryReqBytes, err := proto.Marshal(queryReq)
	if err != nil {
		return stakingtypes.Validator{}, fmt.Errorf("marshal validator query request: %w", err)
	}

	result, err := env.Adapter.App.Query(ctx, &abci.RequestQuery{
		Path: "/cosmos.staking.v1beta1.Query/Validator",
		Data: queryReqBytes,
	})
	if err != nil {
		return stakingtypes.Validator{}, fmt.Errorf("query validator: %w", err)
	}

	if result.Code != 0 {
		return stakingtypes.Validator{}, fmt.Errorf("validator not found: code %d, log: %s", result.Code, result.Log)
	}

	var queryResp stakingtypes.QueryValidatorResponse
	if err := proto.Unmarshal(result.Value, &queryResp); err != nil {
		return stakingtypes.Validator{}, fmt.Errorf("unmarshal validator response: %w", err)
	}

	return queryResp.Validator, nil
}

// getAttesterInfoByAddress queries the network module for attester information via ABCI
func getAttesterInfoByAddress(ctx context.Context, validatorAddr string) (*networktypes.AttesterInfo, error) {
	// Create properly marshaled query request
	queryReq := &networktypes.QueryAttesterInfoRequest{
		ValidatorAddress: validatorAddr,
	}
	
	queryReqBytes, err := proto.Marshal(queryReq)
	if err != nil {
		return nil, fmt.Errorf("marshal query request: %w", err)
	}
	
	result, err := env.Adapter.App.Query(ctx, &abci.RequestQuery{
		Path: "/evabci.network.v1.Query/AttesterInfo",
		Data: queryReqBytes, // Properly marshaled protobuf request
	})
	if err != nil {
		return nil, fmt.Errorf("query attester info: %w", err)
	}

	if result.Code != 0 {
		return nil, fmt.Errorf("attester info not found: code %d, log: %s", result.Code, result.Log)
	}

	var queryResp networktypes.QueryAttesterInfoResponse
	if err := proto.Unmarshal(result.Value, &queryResp); err != nil {
		return nil, fmt.Errorf("unmarshal attester info response: %w", err)
	}

	return queryResp.AttesterInfo, nil
}

