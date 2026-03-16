package types

import (
	"context"

	"cosmossdk.io/core/address"

	abci "github.com/cometbft/cometbft/abci/types"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// AuthKeeper defines the expected interface for the Auth module.
type AuthKeeper interface {
	AddressCodec() address.Codec
	GetAccount(context.Context, sdk.AccAddress) sdk.AccountI
}

// BankKeeper defines the expected interface for the Bank module.
type BankKeeper interface {
	SpendableCoins(context.Context, sdk.AccAddress) sdk.Coins
}

// RelayClient defines the interface for the Symbiotic relay client.
type RelayClient interface {
	GetCurrentEpoch(ctx context.Context) (uint64, error)
	GetValidatorSet(ctx context.Context, epoch uint64) ([]abci.ValidatorUpdate, error)
	SignMessage(ctx context.Context, keyTag uint32, message []byte) (string, error)
	Close() error
}

// SymStakingHooks event hooks for staking validator object.
type SymStakingHooks interface {
	AfterValidatorCreated(ctx context.Context, consPubKey cryptotypes.PubKey) error
	AfterValidatorModified(ctx context.Context, consPubKey cryptotypes.PubKey) error
	AfterValidatorRemoved(ctx context.Context, consPubKey cryptotypes.PubKey) error
}

// MultiSymStakingHooks combines multiple staking hooks.
type MultiSymStakingHooks []SymStakingHooks

func (h MultiSymStakingHooks) AfterValidatorCreated(ctx context.Context, consPubKey cryptotypes.PubKey) error {
	for _, hook := range h {
		if err := hook.AfterValidatorCreated(ctx, consPubKey); err != nil {
			return err
		}
	}
	return nil
}

func (h MultiSymStakingHooks) AfterValidatorModified(ctx context.Context, consPubKey cryptotypes.PubKey) error {
	for _, hook := range h {
		if err := hook.AfterValidatorModified(ctx, consPubKey); err != nil {
			return err
		}
	}
	return nil
}

func (h MultiSymStakingHooks) AfterValidatorRemoved(ctx context.Context, consPubKey cryptotypes.PubKey) error {
	for _, hook := range h {
		if err := hook.AfterValidatorRemoved(ctx, consPubKey); err != nil {
			return err
		}
	}
	return nil
}

// SymStakingHooksWrapper is a wrapper for modules to inject StakingHooks using depinject.
type SymStakingHooksWrapper struct{ SymStakingHooks }

// IsOnePerModuleType implements the depinject.OnePerModuleType interface.
func (SymStakingHooksWrapper) IsOnePerModuleType() {}
