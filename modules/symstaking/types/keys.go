package types

const (
	// ModuleName is the name of the symstaking module.
	ModuleName = "symstaking"
)

var (
	// ParamsKey is the key for params.
	ParamsKey = []byte{0x00}
	// CurrentEpochKey is the key for the current epoch.
	CurrentEpochKey = []byte{0x01}
	// LastValidatorSetPrefix is the prefix for the last validator set.
	LastValidatorSetPrefix = []byte{0x02}
)
