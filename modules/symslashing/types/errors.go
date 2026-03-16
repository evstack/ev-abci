package types

import (
	errorsmod "cosmossdk.io/errors"
)

// x/symslashing module sentinel errors
var (
	ErrEmptyValidatorPubKey = errorsmod.Register(ModuleName, 1, "empty validator public key")
	ErrInvalidInfraction    = errorsmod.Register(ModuleName, 2, "invalid infraction type")
	ErrSignFailed           = errorsmod.Register(ModuleName, 3, "failed to sign slash message")
)
