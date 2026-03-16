package types

import (
	"fmt"

	errorsmod "cosmossdk.io/errors"
)

// Params defines the parameters for the symstaking module.
type Params struct {
	// ValidatorKeyTag is the key tag for validator keys (e.g., 43 for Ed25519 type 2 with id 11).
	ValidatorKeyTag uint32 `protobuf:"varint,1,opt,name=validator_key_tag,json=validatorKeyTag,proto3" json:"validator_key_tag,omitempty"`
	// SigningKeyTag is the key tag for signing operations.
	SigningKeyTag uint32 `protobuf:"varint,2,opt,name=signing_key_tag,json=signingKeyTag,proto3" json:"signing_key_tag,omitempty"`
	// EpochCheckInterval is the number of blocks between epoch checks.
	EpochCheckInterval int64 `protobuf:"varint,3,opt,name=epoch_check_interval,json=epochCheckInterval,proto3" json:"epoch_check_interval,omitempty"`
}

// NewParams creates a new Params instance.
func NewParams(validatorKeyTag, signingKeyTag uint32, epochCheckInterval int64) Params {
	return Params{
		ValidatorKeyTag:    validatorKeyTag,
		SigningKeyTag:      signingKeyTag,
		EpochCheckInterval: epochCheckInterval,
	}
}

// DefaultParams returns a default set of parameters.
func DefaultParams() Params {
	return NewParams(
		43,  // Key tag 43: type 2 (Ed25519) with id 11
		15,  // Default symbiotic signing key
		10,  // Check every 10 blocks
	)
}

// Validate validates the set of params.
func (p Params) Validate() error {
	if p.ValidatorKeyTag>>4 != 2 {
		return errorsmod.Wrapf(ErrInvalidKeyTag, "expected key tag to be of type 2 (indicating a ed25519 key), got %d", p.ValidatorKeyTag>>4)
	}
	if p.EpochCheckInterval <= 0 {
		return fmt.Errorf("epoch check interval must be positive")
	}
	return nil
}
