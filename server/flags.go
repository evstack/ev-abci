package server

import (
	"github.com/spf13/cobra"

	"github.com/evstack/ev-node/pkg/config"
)

// Deprecated: FlagAttesterMode is no longer needed. Attester mode is now
// auto-detected by probing the network module at startup.
const FlagAttesterMode = config.FlagPrefixEvnode + "attester-mode"

// AddFlags adds Evolve specific configuration options to cobra Command.
func AddFlags(cmd *cobra.Command) {
	// Add ev-node flags
	config.AddFlags(cmd)

	// Deprecated: attester mode is auto-detected via the network module.
	// The flag is kept for backward compatibility but has no effect.
	cmd.Flags().Bool(FlagAttesterMode, false, "deprecated: attester mode is now auto-detected")
	_ = cmd.Flags().MarkDeprecated(FlagAttesterMode, "attester mode is now auto-detected via the network module")
}
