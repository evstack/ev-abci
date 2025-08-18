package migrationmngr

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	"github.com/evstack/ev-abci/modules/migrationmngr/types"
)

// AutoCLIOptions implements the autocli.HasAutoCLIConfig interface.
func (am AppModule) AutoCLIOptions() *autocliv1.ModuleOptions {
	return &autocliv1.ModuleOptions{
		Query: &autocliv1.ServiceCommandDescriptor{
			Service: types.Query_serviceDesc.ServiceName,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{
					RpcMethod: "Attesters",
					Use:       "attesters",
					Short:     "Shows the current attesters",
				},
				{
					RpcMethod: "Sequencer",
					Use:       "sequencer",
					Short:     "Shows the current sequencer",
				},
				{
					RpcMethod: "IsMigrating",
					Use:       "is_migrating",
					Short:     "Shows whether the chain is migrating to evolve",
				},
			},
		},
		Tx: &autocliv1.ServiceCommandDescriptor{
			Service: types.Msg_serviceDesc.ServiceName,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{
					RpcMethod:   "MigrateToEvolve",
					GovProposal: true,
				},
			},
		},
	}
}
