package types

import (
	errorsmod "cosmossdk.io/errors"
)

// Symstaking module errors
var (
	ErrInvalidKeyTag       = errorsmod.Register(ModuleName, 1, "invalid key tag")
	ErrInvalidEpoch        = errorsmod.Register(ModuleName, 2, "invalid epoch")
	ErrValidatorNotFound   = errorsmod.Register(ModuleName, 3, "validator not found")
	ErrRelayClientNotSet   = errorsmod.Register(ModuleName, 4, "relay client not set")
	ErrInvalidValidatorSet = errorsmod.Register(ModuleName, 5, "invalid validator set")
)
