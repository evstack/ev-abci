package server

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/evstack/ev-node/core/da"
	"github.com/evstack/ev-node/da/jsonrpc"
	"github.com/evstack/ev-node/pkg/cmd"
	rollconf "github.com/evstack/ev-node/pkg/config"
	"github.com/evstack/ev-node/types"
)

const (
	flagTxData     = "tx"
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
		Short: "Post a signed transaction to Celestia namespace",
		Long: `Post a signed transaction to a Celestia namespace using the Evolve configuration.

This command submits a raw signed transaction to the configured Celestia DA layer.
The transaction data should be provided as a hex-encoded string.

Example:
  evabcid post-tx --tx 0x1234567890abcdef --namespace 0000000000000000000000000000000000000000000001
`,
		Args: cobra.NoArgs,
		RunE: postTxRunE,
	}

	// Add evolve config flags
	rollconf.AddFlags(cobraCmd)

	// Add command-specific flags
	cobraCmd.Flags().String(flagTxData, "", "Hex-encoded signed transaction data (required)")
	cobraCmd.Flags().String(flagNamespace, "", "Celestia namespace ID (if not provided, uses config namespace)")
	cobraCmd.Flags().Float64(flagGasPrice, -1, "Gas price for DA submission (if not provided, uses config gas price)")
	cobraCmd.Flags().Duration(flagTimeout, defaultTimeout, "Timeout for DA submission")
	cobraCmd.Flags().String(flagSubmitOpts, "", "Additional submit options (if not provided, uses config submit options)")

	_ = cobraCmd.MarkFlagRequired(flagTxData)

	return cobraCmd
}

// postTxRunE executes the post-tx command.
// It accepts hex-encoded transaction bytes via CLI (for convenience),
// decodes them to raw bytes, and submits those raw bytes to Celestia DA.
// The raw bytes are the same format that would be passed to ExecuteTxs in the ABCI adapter.
func postTxRunE(cobraCmd *cobra.Command, _ []string) error {
	// Get timeout from flags
	timeout, err := cobraCmd.Flags().GetDuration(flagTimeout)
	if err != nil {
		return fmt.Errorf("failed to get timeout flag: %w", err)
	}

	ctx, cancel := context.WithTimeout(cobraCmd.Context(), timeout)
	defer cancel()

	// Get transaction data (hex-encoded for CLI convenience)
	txHex, err := cobraCmd.Flags().GetString(flagTxData)
	if err != nil {
		return fmt.Errorf("failed to get tx flag: %w", err)
	}

	if txHex == "" {
		return fmt.Errorf("transaction data cannot be empty")
	}

	// Remove 0x prefix if present
	if len(txHex) >= 2 && txHex[:2] == "0x" {
		txHex = txHex[2:]
	}

	// Decode hex to raw transaction bytes.
	// These are the raw bytes that will be submitted to Celestia and would be
	// passed to ExecuteTxs in the ABCI adapter.
	txData, err := hex.DecodeString(txHex)
	if err != nil {
		return fmt.Errorf("failed to decode hex transaction: %w", err)
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
		namespace = cfg.DA.GetNamespace()
	}

	namespaceBz := da.NamespaceFromString(namespace).Bytes()

	// Get gas price (use flag if provided, otherwise use config)
	gasPrice, err := cobraCmd.Flags().GetFloat64(flagGasPrice)
	if err != nil {
		return fmt.Errorf("failed to get gas-price flag: %w", err)
	}

	if gasPrice == -1 {
		gasPrice = cfg.DA.GasPrice
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
	logger := zerolog.New(os.Stderr).With().
		Timestamp().
		Str("component", "post-tx").
		Logger()

	logger.Info().
		Str("namespace", namespace).
		Float64("gas_price", gasPrice).
		Int("tx_size", len(txData)).
		Msg("posting transaction to Celestia")

	// Create DA client
	submitCtx, submitCancel := context.WithTimeout(ctx, timeout)
	defer submitCancel()

	daClient, err := jsonrpc.NewClient(
		submitCtx,
		logger,
		cfg.DA.Address,
		cfg.DA.AuthToken,
		gasPrice,
		cfg.DA.GasMultiplier,
		cmd.DefaultMaxBlobSize,
	)
	if err != nil {
		return fmt.Errorf("failed to create DA client: %w", err)
	}

	// Submit transaction to DA layer.
	// The raw bytes (txData) are submitted as-is to Celestia.
	logger.Info().Msg("submitting transaction to DA layer...")

	blobs := [][]byte{txData}
	options := []byte(submitOpts)

	result := types.SubmitWithHelpers(submitCtx, &daClient.DA, logger, blobs, gasPrice, namespaceBz, options)

	// Check result
	switch result.Code {
	case da.StatusSuccess:
		logger.Info().Msg("transaction successfully submitted to DA layer")
		fmt.Fprintf(cobraCmd.OutOrStdout(), "\n✓ Transaction posted successfully\n\n")
		fmt.Fprintf(cobraCmd.OutOrStdout(), "Namespace:  %s\n", namespace)
		fmt.Fprintf(cobraCmd.OutOrStdout(), "DA Height:  %d\n", result.Height)
		fmt.Fprintf(cobraCmd.OutOrStdout(), "Gas Price:  %.2f\n", gasPrice)
		fmt.Fprintf(cobraCmd.OutOrStdout(), "Data Size:  %d bytes\n", len(txData))
		fmt.Fprintf(cobraCmd.OutOrStdout(), "\n")
		return nil

	case da.StatusTooBig:
		return fmt.Errorf("transaction too large for DA layer: %s", result.Message)

	case da.StatusNotIncludedInBlock:
		return fmt.Errorf("transaction not included in DA block: %s", result.Message)

	case da.StatusAlreadyInMempool:
		fmt.Fprintf(cobraCmd.OutOrStdout(), "⚠ Transaction already in mempool\n")
		if result.Height > 0 {
			fmt.Fprintf(cobraCmd.OutOrStdout(), "  DA Height: %d\n", result.Height)
		}
		return nil

	case da.StatusContextCanceled:
		return fmt.Errorf("submission canceled: %s", result.Message)

	default:
		return fmt.Errorf("DA submission failed (code: %d): %s", result.Code, result.Message)
	}
}
