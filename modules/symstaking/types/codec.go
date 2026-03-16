package types

import (
	"github.com/cosmos/cosmos-sdk/codec"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
)

// RegisterCodec registers the necessary symstaking interfaces and concrete types
// on the provided LegacyAmino codec.
func RegisterCodec(cdc *codec.LegacyAmino) {
	// No legacy amino types to register
}

// RegisterLegacyAminoCodec is an alias for RegisterCodec for compatibility.
func RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	RegisterCodec(cdc)
}

// RegisterInterfaces registers the module's interfaces with the interface registry.
func RegisterInterfaces(registry cdctypes.InterfaceRegistry) {
	// No interfaces to register yet
}
