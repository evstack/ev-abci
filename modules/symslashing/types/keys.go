package types

const (
	// ModuleName is the name of the symslashing module.
	ModuleName = "symslashing"
)

var (
	// ParamsKey is the key for params.
	ParamsKey = []byte{0x00}
	// InfractionRecordsPrefix is the prefix for infraction records.
	InfractionRecordsPrefix = []byte{0x01}
)
