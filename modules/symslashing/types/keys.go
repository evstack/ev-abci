package types

import (
	"github.com/cosmos/cosmos-sdk/codec"
)

const (
	// ModuleName is the name of the symslashing module.
	ModuleName = "symslashing"

	// StoreKey is the store key string for symslashing.
	StoreKey = ModuleName

	// RouterKey is the message route for symslashing.
	RouterKey = ModuleName

	// QuerierRoute is the querier route for symslashing.
	QuerierRoute = ModuleName
)

var (
	// ParamsKey is the key for params.
	ParamsKey = []byte{0x00}
	// InfractionRecordsPrefix is the prefix for infraction records.
	InfractionRecordsPrefix = []byte{0x01}
)

// ModuleCdc references the global x/symslashing module codec.
var ModuleCdc codec.Codec
