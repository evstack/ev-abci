package types

import (
	"fmt"

	"cosmossdk.io/math"
)

// Default parameter values
var (
	DefaultEpochLength      = uint64(1)                         // Changed from 10 to 1 to allow attestations on every block
	DefaultQuorumFraction   = math.LegacyNewDecWithPrec(667, 3) // 2/3
	DefaultMinParticipation = math.LegacyNewDecWithPrec(5, 1)   // 1/2
	DefaultPruneAfter       = uint64(15)                        // also used as number of blocks for attestations to land
	DefaultSignMode         = SignMode_SIGN_MODE_CHECKPOINT
)

// NewParams creates a new Params instance
func NewParams(
	epochLength uint64,
	quorumFraction math.LegacyDec,
	minParticipation math.LegacyDec,
	pruneAfter uint64,
	signMode SignMode,
) Params {
	return Params{
		EpochLength:      epochLength,
		QuorumFraction:   quorumFraction.String(),
		MinParticipation: minParticipation.String(),
		PruneAfter:       pruneAfter,
		SignMode:         signMode,
	}
}

// DefaultParams returns default parameters
func DefaultParams() Params {
	return NewParams(
		DefaultEpochLength,
		DefaultQuorumFraction,
		DefaultMinParticipation,
		DefaultPruneAfter,
		DefaultSignMode,
	)
}

// Validate validates the parameter set
func (p Params) Validate() error {
	if err := validateEpochLength(p.EpochLength); err != nil {
		return err
	}
	if err := validateQuorumFraction(p.QuorumFraction); err != nil {
		return err
	}
	if err := validateMinParticipation(p.MinParticipation); err != nil {
		return err
	}
	if err := validatePruneAfter(p.PruneAfter); err != nil {
		return err
	}
	if err := validateSignMode(p.SignMode); err != nil {
		return err
	}
	return nil
}

func validateEpochLength(i any) error {
	v, ok := i.(uint64)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}

	if v == 0 {
		return fmt.Errorf("epoch length must be positive: %d", v)
	}

	return nil
}

func validateQuorumFraction(i any) error {
	v, ok := i.(string)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}

	dec, err := math.LegacyNewDecFromStr(v)
	if err != nil {
		return fmt.Errorf("invalid decimal string: %w", err)
	}

	if dec.LTE(math.LegacyZeroDec()) || dec.GT(math.LegacyOneDec()) {
		return fmt.Errorf("quorum fraction must be between 0 and 1: %s", v)
	}

	return nil
}

func validateMinParticipation(i any) error {
	v, ok := i.(string)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}

	dec, err := math.LegacyNewDecFromStr(v)
	if err != nil {
		return fmt.Errorf("invalid decimal string: %w", err)
	}

	if dec.LTE(math.LegacyZeroDec()) || dec.GT(math.LegacyOneDec()) {
		return fmt.Errorf("min participation must be between 0 and 1: %s", v)
	}

	return nil
}

func validatePruneAfter(i any) error {
	v, ok := i.(uint64)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}

	if v == 0 {
		return fmt.Errorf("prune after must be positive: %d", v)
	}

	return nil
}

func validateSignMode(i any) error {
	v, ok := i.(SignMode)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}

	if v == SignMode_SIGN_MODE_UNSPECIFIED {
		return fmt.Errorf("sign mode cannot be unspecified")
	}

	if v != SignMode_SIGN_MODE_CHECKPOINT {
		return fmt.Errorf("invalid sign mode: %d", v)
	}

	return nil
}
