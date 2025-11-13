package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	cmtcfg "github.com/cometbft/cometbft/config"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/server"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/evstack/ev-node/core/da"
	"github.com/evstack/ev-node/da/jsonrpc"
	"github.com/evstack/ev-node/pkg/cmd"
	rollconf "github.com/evstack/ev-node/pkg/config"
	"github.com/evstack/ev-node/types"
)

const (
	flagNamespace  = "namespace"
	flagGasPrice   = "gas-price"
	flagTimeout    = "timeout"
	flagSubmitOpts = "submit-options"

	defaultTimeout = 60 * time.Second
)

// PostTxCmd returns a command to post a signed transaction to a Celestia namespace
func PostTxCmd() *cobra.Command {
	cobraCmd := &cobra.Command{
		Use:   "post-tx",
		Short: "Post a signed transaction to a Celestia namespace",
		Long: `Post a signed transaction to a Celestia namespace using the Evolve configuration.

This command submits a signed transaction to the configured Celestia DA layer.
The transaction is provided as argument, which accepts either:
  1. A path to a JSON file containing the transaction
  2. A JSON string directly

The command automatically detects whether the input is a file path or JSON string.
The JSON format must match the Cosmos SDK transaction JSON format.

Examples:
  # From JSON file
  evabcid post-tx tx.json

  # From JSON string
  evabcid post-tx '{"body":{...},"auth_info":{...},"signatures":[...]}'
`,
		Args: cobra.ExactArgs(1),
		RunE: postTxRunE,
	}

	// Add evolve config flags
	rollconf.AddFlags(cobraCmd)

	// Add command-specific flags
	cobraCmd.Flags().String(flagNamespace, "", "Celestia namespace ID (if not provided, uses config namespace)")
	cobraCmd.Flags().Float64(flagGasPrice, -1, "Gas price for DA submission (if not provided, uses config gas price)")
	cobraCmd.Flags().Duration(flagTimeout, defaultTimeout, "Timeout for DA submission")
	cobraCmd.Flags().String(flagSubmitOpts, "", "Additional submit options (if not provided, uses config submit options)")

	return cobraCmd
}

// postTxRunE executes the post-tx command
func postTxRunE(cobraCmd *cobra.Command, args []string) error {
	clientCtx := client.GetClientContextFromCmd(cobraCmd)
	serverCtx := server.GetServerContextFromCmd(cobraCmd)

	timeout, err := cobraCmd.Flags().GetDuration(flagTimeout)
	if err != nil {
		return fmt.Errorf("failed to get timeout flag: %w", err)
	}

	ctx, cancel := context.WithTimeout(cobraCmd.Context(), timeout)
	defer cancel()

	txInput := args[0]

	if txInput == "" {
		return fmt.Errorf("transaction cannot be empty")
	}

	var txData []byte
	if _, err := os.Stat(txInput); err == nil {
		// Input is a file path
		txData, err = decodeTxFromFile(clientCtx, txInput)
		if err != nil {
			return fmt.Errorf("failed to decode transaction from file: %w", err)
		}
	} else {
		// Input is a JSON string
		txData, err = decodeTxFromJSON(clientCtx, txInput)
		if err != nil {
			return fmt.Errorf("failed to decode transaction from JSON: %w", err)
		}
	}

	if len(txData) == 0 {
		return fmt.Errorf("transaction data cannot be empty")
	}

	// Load evolve configuration
	cfg, err := rollconf.Load(cobraCmd)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// Get namespace (use flag if provided, otherwise use config)
	namespace, err := cobraCmd.Flags().GetString(flagNamespace)
	if err != nil {
		return fmt.Errorf("failed to get namespace flag: %w", err)
	}

	if namespace == "" {
		namespace = cfg.DA.GetForcedInclusionNamespace()
	}
	namespaceBz := da.NamespaceFromString(namespace).Bytes()

	// Get gas price (use flag if provided, otherwise use config)
	gasPrice, err := cobraCmd.Flags().GetFloat64(flagGasPrice)
	if err != nil {
		return fmt.Errorf("failed to get gas-price flag: %w", err)
	}

	// Get submit options (use flag if provided, otherwise use config)
	submitOpts, err := cobraCmd.Flags().GetString(flagSubmitOpts)
	if err != nil {
		return fmt.Errorf("failed to get submit-options flag: %w", err)
	}

	if submitOpts == "" {
		submitOpts = cfg.DA.SubmitOptions
	}

	// Setup logger
	logger := serverCtx.Logger
	zlLogger, ok := logger.Impl().(*zerolog.Logger)
	if !ok {
		znop := zerolog.Nop()
		zlLogger = &znop
	}

	logger.Info("posting transaction to Celestia", "namespace", namespace, "gas_price", gasPrice, "tx_size", len(txData))

	// Create DA client
	submitCtx, submitCancel := context.WithTimeout(ctx, timeout)
	defer submitCancel()

	daClient, err := jsonrpc.NewClient(
		submitCtx,
		*zlLogger,
		cfg.DA.Address,
		cfg.DA.AuthToken,
		cmd.DefaultMaxBlobSize,
	)
	if err != nil {
		return fmt.Errorf("failed to create DA client: %w", err)
	}

	// Submit transaction to DA layer
	logger.Info("submitting transaction to DA layer...")

	blobs := [][]byte{txData}
	options := []byte(submitOpts)

	result := types.SubmitWithHelpers(submitCtx, &daClient.DA, *zlLogger, blobs, gasPrice, namespaceBz, options)

	// Check result
	switch result.Code {
	case da.StatusSuccess:
		logger.Info("transaction successfully submitted to DA layer")
		cobraCmd.Printf("\n✓ Transaction posted successfully\n\n")
		cobraCmd.Printf("Namespace:  %s\n", namespace)
		cobraCmd.Printf("DA Height:  %d\n", result.Height)
		cobraCmd.Printf("Data Size:  %d bytes\n", len(txData))

		// Calculate when transaction will be included based on DA epochs
		daStartHeight, err := getDaStartHeight(serverCtx.Config)
		if err != nil {
			cobraCmd.Printf("Failed to get DA start height: %v\n", err)
			daStartHeight = 0
		}
		epochSize, err := getDaEpoch(serverCtx.Config)
		if err != nil {
			cobraCmd.Printf("Failed to get DA epoch size: %v\n", err)
			epochSize = 0
		}

		_, epochEnd := types.CalculateEpochBoundaries(result.Height, daStartHeight, epochSize)
		cobraCmd.Printf(
			"DA Blocks until inclusion: %d (at DA height %d)\n",
			epochEnd-(result.Height+1),
			epochEnd+1,
		)

		cobraCmd.Printf("\n")
		return nil

	case da.StatusTooBig:
		return fmt.Errorf("transaction too large for DA layer: %s", result.Message)

	case da.StatusNotIncludedInBlock:
		return fmt.Errorf("transaction not included in DA block: %s", result.Message)

	case da.StatusAlreadyInMempool:
		cobraCmd.Printf("⚠ Transaction already in mempool\n")
		if result.Height > 0 {
			cobraCmd.Printf("  DA Height: %d\n", result.Height)
		}
		return nil

	case da.StatusContextCanceled:
		return fmt.Errorf("submission canceled: %s", result.Message)

	default:
		return fmt.Errorf("DA submission failed (code: %d): %s", result.Code, result.Message)
	}
}

// decodeTxFromFile reads a JSON transaction from a file and decodes it to bytes
func decodeTxFromFile(clientCtx client.Context, filePath string) ([]byte, error) {
	jsonData, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	return decodeTxFromJSON(clientCtx, string(jsonData))
}

// decodeTxFromJSON decodes a JSON transaction string to bytes
func decodeTxFromJSON(clientCtx client.Context, jsonStr string) ([]byte, error) {
	// First try to decode as a Cosmos SDK transaction JSON
	var txJSON map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &txJSON); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}

	// Use the SDK's JSON decoder
	txJSONDecoder := clientCtx.TxConfig.TxJSONDecoder()
	tx, err := txJSONDecoder([]byte(jsonStr))
	if err != nil {
		return nil, fmt.Errorf("decoding transaction JSON: %w", err)
	}

	// Encode the transaction to bytes
	txEncoder := clientCtx.TxConfig.TxEncoder()
	txBytes, err := txEncoder(tx)
	if err != nil {
		return nil, fmt.Errorf("encoding transaction: %w", err)
	}

	return txBytes, nil
}

// getDaEpoch parses the da_start_height from the genesis file.
func getDaEpoch(cfg *cmtcfg.Config) (uint64, error) {
	const daEpochFieldName = "da_epoch_forced_inclusion"

	genFile, err := os.Open(filepath.Clean(cfg.GenesisFile()))
	if err != nil {
		return 0, fmt.Errorf("failed to open genesis file %s: %w", cfg.GenesisFile(), err)
	}

	daStartHeight, err := parseFieldFromGenesis(bufio.NewReader(genFile), daEpochFieldName)
	if err != nil {
		return 0, err
	}

	if err := genFile.Close(); err != nil {
		return 0, fmt.Errorf("failed to close genesis file %s: %v", genFile.Name(), err)
	}

	return daStartHeight, nil
}
