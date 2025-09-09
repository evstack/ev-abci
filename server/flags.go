package server

import (
	"github.com/spf13/cobra"

	"github.com/evstack/ev-node/pkg/config"
)

const FlagAttesterMode = config.FlagPrefixEvnode + "attester-mode"

// AddFlags adds Evolve specific configuration options to cobra Command.
func AddFlags(cmd *cobra.Command) {
	// Add ev-node flags
	config.AddFlags(cmd)

	// Add network flags
	cmd.Flags().Bool(FlagAttesterMode, false, "enable attester mode (soft confirmation by the validator network)")
}
