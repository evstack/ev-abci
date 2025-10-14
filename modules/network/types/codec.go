package types

import (
	"github.com/cosmos/cosmos-sdk/codec"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/msgservice"
)

// RegisterCodec registers the necessary types and interfaces with the codec
func RegisterCodec(cdc *codec.LegacyAmino) {
	cdc.RegisterConcrete(&MsgAttest{}, "network/Attest", nil)
	cdc.RegisterConcrete(&MsgJoinAttesterSet{}, "network/JoinAttesterSet", nil)
	cdc.RegisterConcrete(&MsgLeaveAttesterSet{}, "network/LeaveAttesterSet", nil)
	cdc.RegisterConcrete(&MsgUpdateParams{}, "network/UpdateParams", nil)
}

// RegisterInterfaces registers the module interface types
func RegisterInterfaces(registry cdctypes.InterfaceRegistry) {
	registry.RegisterImplementations((*sdk.Msg)(nil),
		&MsgAttest{},
		&MsgJoinAttesterSet{},
		&MsgLeaveAttesterSet{},
		&MsgUpdateParams{},
	)

	// Register cryptographic public key types
	registry.RegisterInterface(
		"cosmos.crypto.PubKey",
		(*cryptotypes.PubKey)(nil),
		&ed25519.PubKey{},
		&secp256k1.PubKey{},
	)

	msgservice.RegisterMsgServiceDesc(registry, &_Msg_serviceDesc)
}

var (
	Amino = codec.NewLegacyAmino()
	// Create a separate interface registry for this module to avoid conflicts
	moduleInterfaceRegistry = cdctypes.NewInterfaceRegistry()
	ModuleCdc               = codec.NewProtoCodec(moduleInterfaceRegistry)
)

func init() {
	RegisterCodec(Amino)
	// Only register crypto types in the module codec, not the messages
	// Messages will be registered when the module is registered with the app
	moduleInterfaceRegistry.RegisterInterface(
		"cosmos.crypto.PubKey",
		(*cryptotypes.PubKey)(nil),
		&ed25519.PubKey{},
		&secp256k1.PubKey{},
	)
	Amino.Seal()
}
