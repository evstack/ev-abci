package server

import (
	"errors"
	"fmt"

	"github.com/cometbft/cometbft/p2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/rollkit/rollkit/pkg/config"
	"github.com/spf13/cobra"
)

var (
	errNilKey             = errors.New("node key is nil")
	errUnsupportedKeyType = errors.New("unsupported key type")
)

// GetNodeKey creates libp2p private key from Tendermints NodeKey.
func GetNodeKey(nodeKey *p2p.NodeKey) (crypto.PrivKey, error) {
	if nodeKey == nil || nodeKey.PrivKey == nil {
		return nil, errNilKey
	}
	switch nodeKey.PrivKey.Type() {
	case "ed25519":
		privKey, err := crypto.UnmarshalEd25519PrivateKey(nodeKey.PrivKey.Bytes())
		if err != nil {
			return nil, fmt.Errorf("error unmarshalling node private key: %w", err)
		}
		return privKey, nil
	default:
		return nil, errUnsupportedKeyType
	}
}

// AddFlags adds Rollkit specific configuration options to cobra Command.
func AddFlags(cmd *cobra.Command) {
	def := config.DefaultNodeConfig

	// Add CI flag for testing
	cmd.Flags().Bool("ci", false, "run node for ci testing")

	// Add base flags
	cmd.Flags().String(config.FlagDBPath, def.DBPath, "path for the node database")
	cmd.Flags().String(config.FlagChainConfigDir, def.ConfigDir, "directory containing chain configuration files")
	cmd.Flags().String(config.FlagChainID, def.ChainID, "chain ID")
	// Node configuration flags
	cmd.Flags().BoolVar(&def.Node.Aggregator, config.FlagAggregator, def.Node.Aggregator, "run node in aggregator mode")
	cmd.Flags().Bool(config.FlagLight, def.Node.Light, "run light client")
	cmd.Flags().Duration(config.FlagBlockTime, def.Node.BlockTime.Duration, "block time (for aggregator mode)")
	cmd.Flags().String(config.FlagTrustedHash, def.Node.TrustedHash, "initial trusted hash to start the header exchange service")
	cmd.Flags().Bool(config.FlagLazyAggregator, def.Node.LazyAggregator, "produce blocks only when transactions are available or after lazy block time")
	cmd.Flags().Uint64(config.FlagMaxPendingBlocks, def.Node.MaxPendingBlocks, "maximum blocks pending DA confirmation before pausing block production (0 for no limit)")
	cmd.Flags().Duration(config.FlagLazyBlockTime, def.Node.LazyBlockTime.Duration, "maximum interval between blocks in lazy aggregation mode")
	cmd.Flags().String(config.FlagSequencerAddress, def.Node.SequencerAddress, "sequencer middleware address (host:port)")
	cmd.Flags().String(config.FlagSequencerRollupID, def.Node.SequencerRollupID, "sequencer middleware rollup ID (default: mock-rollup)")
	cmd.Flags().String(config.FlagExecutorAddress, def.Node.ExecutorAddress, "executor middleware address (host:port)")

	// Data Availability configuration flags
	cmd.Flags().String(config.FlagDAAddress, def.DA.Address, "DA address (host:port)")
	cmd.Flags().String(config.FlagDAAuthToken, def.DA.AuthToken, "DA auth token")
	cmd.Flags().Duration(config.FlagDABlockTime, def.DA.BlockTime.Duration, "DA chain block time (for syncing)")
	cmd.Flags().Float64(config.FlagDAGasPrice, def.DA.GasPrice, "DA gas price for blob transactions")
	cmd.Flags().Float64(config.FlagDAGasMultiplier, def.DA.GasMultiplier, "DA gas price multiplier for retrying blob transactions")
	cmd.Flags().Uint64(config.FlagDAStartHeight, def.DA.StartHeight, "starting DA block height (for syncing)")
	cmd.Flags().String(config.FlagDANamespace, def.DA.Namespace, "DA namespace to submit blob transactions")
	cmd.Flags().String(config.FlagDASubmitOptions, def.DA.SubmitOptions, "DA submit options")
	cmd.Flags().Uint64(config.FlagDAMempoolTTL, def.DA.MempoolTTL, "number of DA blocks until transaction is dropped from the mempool")

	// P2P configuration flags
	cmd.Flags().String(config.FlagP2PListenAddress, def.P2P.ListenAddress, "P2P listen address (host:port)")
	cmd.Flags().String(config.FlagP2PSeeds, def.P2P.Seeds, "Comma separated list of seed nodes to connect to")
	cmd.Flags().String(config.FlagP2PBlockedPeers, def.P2P.BlockedPeers, "Comma separated list of nodes to ignore")
	cmd.Flags().String(config.FlagP2PAllowedPeers, def.P2P.AllowedPeers, "Comma separated list of nodes to whitelist")

	// RPC configuration flags
	cmd.Flags().String(config.FlagRPCAddress, def.RPC.Address, "RPC server address (host)")
	cmd.Flags().Uint16(config.FlagRPCPort, def.RPC.Port, "RPC server port")

	// Instrumentation configuration flags
	instrDef := config.DefaultInstrumentationConfig()
	cmd.Flags().Bool(config.FlagPrometheus, instrDef.Prometheus, "enable Prometheus metrics")
	cmd.Flags().String(config.FlagPrometheusListenAddr, instrDef.PrometheusListenAddr, "Prometheus metrics listen address")
	cmd.Flags().Int(config.FlagMaxOpenConnections, instrDef.MaxOpenConnections, "maximum number of simultaneous connections for metrics")
	cmd.Flags().Bool(config.FlagPprof, instrDef.Pprof, "enable pprof HTTP endpoint")
	cmd.Flags().String(config.FlagPprofListenAddr, instrDef.PprofListenAddr, "pprof HTTP server listening address")

	// Logging configuration flags
	cmd.Flags().String(config.FlagLogLevel, "info", "log level (debug, info, warn, error)")
	cmd.Flags().String(config.FlagLogFormat, "text", "log format (text, json)")
	cmd.Flags().Bool(config.FlagLogTrace, false, "enable stack traces in error logs")

	// Signer configuration flags
	cmd.Flags().String(config.FlagSignerType, def.Signer.SignerType, "type of signer to use (file, grpc)")
	cmd.Flags().String(config.FlagSignerPath, def.Signer.SignerPath, "path to the signer file or address")
	cmd.Flags().String(config.FlagSignerPassphrase, "", "passphrase for the signer (required for file signer and if aggregator is enabled)")
}
