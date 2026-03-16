package symstaking

import (
	"cosmossdk.io/core/appmodule"
	"cosmossdk.io/core/store"
	"cosmossdk.io/depinject"
	"cosmossdk.io/depinject/appconfig"
	"github.com/cosmos/cosmos-sdk/codec"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"

	"github.com/evstack/ev-abci/modules/symstaking/keeper"
	modulev1 "github.com/evstack/ev-abci/modules/symstaking/module/v1"
	"github.com/evstack/ev-abci/modules/symstaking/relay"
	"github.com/evstack/ev-abci/modules/symstaking/types"
)

var _ appmodule.AppModule = AppModule{}

// IsOnePerModuleType implements the depinject.OnePerModuleType interface.
func (am AppModule) IsOnePerModuleType() {}

func init() {
	appconfig.Register(
		&modulev1.Module{},
		appconfig.Provide(ProvideModule),
	)
}

type ModuleInputs struct {
	depinject.In

	Config       *modulev1.Module
	Cdc          codec.Codec
	StoreService store.KVStoreService
}

type ModuleOutputs struct {
	depinject.Out

	SymStakingKeeper keeper.Keeper
	SymStakingHooks  types.SymStakingHooksWrapper
	Module           appmodule.AppModule
}

func ProvideModule(in ModuleInputs) ModuleOutputs {
	// default to governance authority if not provided
	authority := authtypes.NewModuleAddress(govtypes.ModuleName)
	if in.Config.Authority != "" {
		authority = authtypes.NewModuleAddressOrBech32Address(in.Config.Authority)
	}

	// Create relay config from module config or environment
	relayCfg := relay.Config{
		RPCAddress: in.Config.GetRelayRpcAddress(),
		KeyFile:    in.Config.GetKeyFile(),
	}
	// Override with environment variables if config not set
	if relayCfg.RPCAddress == "" && relayCfg.KeyFile == "" {
		relayCfg = relay.ConfigFromEnv()
	}

	k, err := keeper.NewKeeper(
		in.Cdc,
		in.StoreService,
		relayCfg,
		authority.String(),
	)
	if err != nil {
		panic(err)
	}

	m := NewAppModule(
		in.Cdc,
		k,
	)

	return ModuleOutputs{
		SymStakingKeeper: k,
		SymStakingHooks:  types.SymStakingHooksWrapper{SymStakingHooks: nil}, // Set by caller
		Module:           m,
	}
}
