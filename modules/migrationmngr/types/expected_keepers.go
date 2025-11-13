package types

import (
	context "context"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// StakingKeeper defines the expected staking keeper.
type StakingKeeper interface {
	GetValidatorByConsAddr(ctx context.Context, consAddr sdk.ConsAddress) (stakingtypes.Validator, error)
	GetLastValidators(ctx context.Context) (validators []stakingtypes.Validator, err error)
	IterateBondedValidatorsByPower(context.Context, func(index int64, validator stakingtypes.ValidatorI) (stop bool)) error
	GetValidatorDelegations(ctx context.Context, valAddr sdk.ValAddress) ([]stakingtypes.Delegation, error)
	Undelegate(ctx context.Context, delAddr sdk.AccAddress, valAddr sdk.ValAddress, sharesAmount math.LegacyDec) (time.Time, math.Int, error)
}
