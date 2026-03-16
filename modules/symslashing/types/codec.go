package types

import (
	"github.com/cosmos/cosmos-sdk/codec"
)

func RegisterCodec(cdc *codec.LegacyAmino) {
	// Register amino codec
}

var (
	amino = codec.NewLegacyAmino()

	// AminoCdc references the x/symslashing module amino codec.
	AminoCdc = codec.NewAminoCodec(amino)
)

func init() {
	RegisterCodec(amino)
}
