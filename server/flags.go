package server

import (
	"github.com/spf13/cobra"

	"github.com/evstack/ev-node/pkg/config"
)

const FlagNetworkSoftConfirmation = config.FlagPrefixEvnode + "network.soft-confirmation"

// AddFlags adds Rollkit specific configuration options to cobra Command.
func AddFlags(cmd *cobra.Command) {
	// Add ev-node flags
	config.AddFlags(cmd)

	// Add network flags
	cmd.Flags().Bool(FlagNetworkSoftConfirmation, false, "enable soft confirmation by the validator network")
}
