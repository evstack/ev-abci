package types

import (
	"fmt"

	"github.com/cosmos/gogoproto/proto"
)

// GenesisState defines the symstaking module's genesis state.
type GenesisState struct {
	Params Params `json:"params" yaml:"params"`
	Epoch  uint64 `json:"epoch" yaml:"epoch"`
}

// ProtoMessage implements proto.Message.
func (*GenesisState) ProtoMessage() {}

// Reset implements proto.Message.
func (m *GenesisState) Reset() { *m = GenesisState{} }

// String implements proto.Message.
func (m *GenesisState) String() string { return proto.CompactTextString(m) }

// DefaultGenesisState returns the default genesis state.
func DefaultGenesisState() *GenesisState {
	return &GenesisState{
		Params: DefaultParams(),
		Epoch:  0,
	}
}

// Validate performs basic genesis state validation.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return fmt.Errorf("invalid params: %w", err)
	}
	return nil
}

// StoreEpoch represents a stored epoch.
type StoreEpoch struct {
	Epoch uint64 `protobuf:"varint,1,opt,name=epoch,proto3" json:"epoch,omitempty"`
}

// LastValidatorSet represents the last known validator set.
type LastValidatorSet struct {
	Epoch   uint64 `protobuf:"varint,1,opt,name=epoch,proto3" json:"epoch,omitempty"`
	Updates []any  `protobuf:"bytes,2,rep,name=updates,proto3" json:"updates,omitempty"`
}

// Infraction represents the type of infraction.
type Infraction int32

const (
	Infraction_INFRACTION_UNSPECIFIED Infraction = 0
	Infraction_INFRACTION_DOUBLE_SIGN Infraction = 1
	Infraction_INFRACTION_DOWNTIME    Infraction = 2
)
