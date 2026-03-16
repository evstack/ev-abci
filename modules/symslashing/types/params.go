package types

import (
	"context"

	"github.com/evstack/ev-abci/modules/symstaking/types"
)

// SymStakingKeeper defines the expected interface for the symstaking module.
type SymStakingKeeper interface {
	SlashWithInfractionReason(
		ctx context.Context,
		validatorPubKey []byte,
		infractionType types.Infraction,
		height int64,
	) (string, error)
}

// Params defines the parameters for the symslashing module.
type Params struct {
	// SignedBlocksWindow is the number of blocks to consider for downtime slashing.
	SignedBlocksWindow int64 `json:"signed_blocks_window"`
	// MinSignedPerWindow is the minimum percentage of blocks that must be signed.
	MinSignedPerWindow string `json:"min_signed_per_window"`
	// DowntimeJailDuration is the duration a validator is jailed for downtime.
	DowntimeJailDuration int64 `json:"downtime_jail_duration"`
	// SlashFractionDoubleSign is the fraction of stake slashed for double sign.
	SlashFractionDoubleSign string `json:"slash_fraction_double_sign"`
	// SlashFractionDowntime is the fraction of stake slashed for downtime.
	SlashFractionDowntime string `json:"slash_fraction_downtime"`
}

// NewParams creates a new Params instance.
func NewParams(
	signedBlocksWindow int64,
	minSignedPerWindow string,
	downtimeJailDuration int64,
	slashFractionDoubleSign string,
	slashFractionDowntime string,
) Params {
	return Params{
		SignedBlocksWindow:      signedBlocksWindow,
		MinSignedPerWindow:      minSignedPerWindow,
		DowntimeJailDuration:    downtimeJailDuration,
		SlashFractionDoubleSign: slashFractionDoubleSign,
		SlashFractionDowntime:   slashFractionDowntime,
	}
}

// DefaultParams returns a default set of parameters.
func DefaultParams() Params {
	return NewParams(
		100,    // 100 blocks window
		"0.5",  // 50% minimum signed
		60,     // 60 seconds jail duration
		"0.05", // 5% slash for double sign
		"0.01", // 1% slash for downtime
	)
}

// Validate validates the set of params.
func (p Params) Validate() error {
	return nil
}

// InfractionRecord stores information about an infraction.
type InfractionRecord struct {
	ValidatorPubKey []byte          `json:"validator_pub_key"`
	InfractionType  types.Infraction `json:"infraction_type"`
	Height          int64           `json:"height"`
	RequestID       string          `json:"request_id"`
}
