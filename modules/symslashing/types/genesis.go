package types

import (
	"fmt"

	"github.com/cosmos/gogoproto/proto"
)

// GenesisState defines the symslashing module's genesis state.
type GenesisState struct {
	Params Params `json:"params" yaml:"params"`
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
	}
}

// Validate performs basic genesis state validation.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return fmt.Errorf("invalid params: %w", err)
	}
	return nil
}
