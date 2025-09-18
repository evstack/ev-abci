package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cosmossdk.io/math"
	pvm "github.com/cometbft/cometbft/privval"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/cosmos/cosmos-sdk/std"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/spf13/cobra"

	networktypes "github.com/evstack/ev-abci/modules/network/types"
	evolvetypes "github.com/evstack/ev-node/types"
)

const (
	flagChainID               = "chain-id"
	flagNode                  = "node"
	flagAPIAddr               = "api-addr"
	flagHome                  = "home"
	flagVerbose               = "verbose"
	flagMnemonic              = "mnemonic"
	flagBech32AccountPrefix   = "bech32-account-prefix"
	flagBech32AccountPubkey   = "bech32-account-pubkey"
	flagBech32ValidatorPrefix = "bech32-validator-prefix"
	flagBech32ValidatorPubkey = "bech32-validator-pubkey"
)

// Config holds all configuration parameters for the attester
type Config struct {
	ChainID               string
	Node                  string
	APIAddr               string
	Home                  string
	Verbose               bool
	Mnemonic              string
	Bech32AccountPrefix   string
	Bech32AccountPubkey   string
	Bech32ValidatorPrefix string
	Bech32ValidatorPubkey string
}

func main() {
	rootCmd := &cobra.Command{
		Use:                        "attester",
		Short:                      "Attester client for Evolve",
		Long:                       `Attester client for Evolve that joins the attester set and attests to blocks`,
		DisableFlagParsing:         false,
		SuggestionsMinimumDistance: 2,
		RunE:                       runAttester,
	}

	// Add flags
	rootCmd.Flags().String(flagChainID, "", "Chain ID of the blockchain")
	rootCmd.Flags().String(flagNode, "tcp://localhost:26657", "RPC node address")
	rootCmd.Flags().String(flagAPIAddr, "http://localhost:1317", "API node address")
	rootCmd.Flags().String(flagHome, "", "Directory for config and data")
	rootCmd.Flags().Bool(flagVerbose, false, "Enable verbose output")
	rootCmd.Flags().String(flagMnemonic, "", "Mnemonic for the private key")
	rootCmd.Flags().String(flagBech32AccountPrefix, "gm", "Bech32 prefix for account addresses")
	rootCmd.Flags().String(flagBech32AccountPubkey, "gmpub", "Bech32 prefix for account public keys")
	rootCmd.Flags().String(flagBech32ValidatorPrefix, "gmvaloper", "Bech32 prefix for validator addresses")
	rootCmd.Flags().String(flagBech32ValidatorPubkey, "gmvaloperpub", "Bech32 prefix for validator public keys")

	_ = rootCmd.MarkFlagRequired(flagChainID)
	_ = rootCmd.MarkFlagRequired(flagMnemonic)

	// Execute
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

// ParseFlags parses command line flags and returns a Config struct
func ParseFlags(cmd *cobra.Command) (*Config, error) {
	chainID, err := cmd.Flags().GetString(flagChainID)
	if err != nil {
		return nil, err
	}

	node, err := cmd.Flags().GetString(flagNode)
	if err != nil {
		return nil, err
	}

	apiAddr, err := cmd.Flags().GetString(flagAPIAddr)
	if err != nil {
		return nil, err
	}

	home, err := cmd.Flags().GetString(flagHome)
	if err != nil {
		return nil, err
	}

	verbose, err := cmd.Flags().GetBool(flagVerbose)
	if err != nil {
		return nil, err
	}

	mnemonic, err := cmd.Flags().GetString(flagMnemonic)
	if err != nil {
		return nil, err
	}

	bech32AccountPrefix, err := cmd.Flags().GetString(flagBech32AccountPrefix)
	if err != nil {
		return nil, err
	}

	bech32AccountPubkey, err := cmd.Flags().GetString(flagBech32AccountPubkey)
	if err != nil {
		return nil, err
	}

	bech32ValidatorPrefix, err := cmd.Flags().GetString(flagBech32ValidatorPrefix)
	if err != nil {
		return nil, err
	}

	bech32ValidatorPubkey, err := cmd.Flags().GetString(flagBech32ValidatorPubkey)
	if err != nil {
		return nil, err
	}

	return &Config{
		ChainID:               chainID,
		Node:                  node,
		APIAddr:               apiAddr,
		Home:                  home,
		Verbose:               verbose,
		Mnemonic:              mnemonic,
		Bech32AccountPrefix:   bech32AccountPrefix,
		Bech32AccountPubkey:   bech32AccountPubkey,
		Bech32ValidatorPrefix: bech32ValidatorPrefix,
		Bech32ValidatorPubkey: bech32ValidatorPubkey,
	}, nil
}

func runAttester(cmd *cobra.Command, _ []string) error {
	config, err := ParseFlags(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	sdkConfig := sdk.GetConfig()
	sdkConfig.SetBech32PrefixForAccount(config.Bech32AccountPrefix, config.Bech32AccountPubkey)
	sdkConfig.SetBech32PrefixForValidator(config.Bech32ValidatorPrefix, config.Bech32ValidatorPubkey)
	sdkConfig.Seal()

	if config.Verbose {
		fmt.Printf("Bech32 Configuration:\n")
		fmt.Printf("  Account prefix: %s\n", config.Bech32AccountPrefix)
		fmt.Printf("  Account pubkey: %s\n", config.Bech32AccountPubkey)
		fmt.Printf("  Validator prefix: %s\n", config.Bech32ValidatorPrefix)
		fmt.Printf("  Validator pubkey: %s\n", config.Bech32ValidatorPubkey)
	}

	operatorPrivKey, err := privateKeyFromMnemonic(config.Mnemonic)
	if err != nil {
		return fmt.Errorf("failed to create private key from mnemonic: %w", err)
	}

	privKeyPath := filepath.Join(config.Home, "config", "priv_validator_key.json")
	privStatePath := filepath.Join(config.Home, "data", "priv_validator_state.json")
	consensusPrivKey := pvm.LoadFilePV(privKeyPath, privStatePath)
	valAddr := sdk.ValAddress(consensusPrivKey.Key.Address)

	if config.Verbose {
		addr := sdk.AccAddress(operatorPrivKey.PubKey().Address())
		fmt.Printf("Sender Account address: %s\n", addr.String())
		fmt.Printf("Sender Validator address: %s\n", valAddr.String())
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("Received signal, shutting down...")
		cancel()
	}()

	// Create shared client context with codecs
	clientCtx, err := createClientContext(config)
	if err != nil {
		return fmt.Errorf("creating client context: %w", err)
	}

	fmt.Println("Joining attester set...")
	if err := joinAttesterSet(ctx, config, valAddr, operatorPrivKey, consensusPrivKey, clientCtx); err != nil {
		return fmt.Errorf("join attester set: %w", err)
	}

	fmt.Println("Starting to watch for new blocks...")
	if err := pullBlocksAndAttest(ctx, config, valAddr, operatorPrivKey, consensusPrivKey, clientCtx); err != nil {
		return fmt.Errorf("error watching blocks: %w", err)
	}

	return nil
}

// joinAttesterSet creates and submits a MsgJoinAttesterSet transaction
func joinAttesterSet(
	ctx context.Context,
	config *Config,
	valAddr sdk.ValAddress,
	operatorPrivKey *secp256k1.PrivKey,
	consensusPrivKey *pvm.FilePV,
	clientCtx client.Context,
) error {
	// Convert CometBFT PubKey to Cosmos SDK PubKey
	sdkPubKey, err := cryptocodec.FromCmtPubKeyInterface(consensusPrivKey.Key.PubKey)
	if err != nil {
		fmt.Printf("❌ Failed to convert public key: %v\n", err)
		return fmt.Errorf("convert public key: %w", err)
	}

	authorityAddr := sdk.AccAddress(operatorPrivKey.PubKey().Address()).String()
	msg, err := networktypes.NewMsgJoinAttesterSet(authorityAddr, valAddr.String(), sdkPubKey)
	if err != nil {
		fmt.Printf("❌ Failed to create MsgJoinAttesterSet: %v\n", err)
		return fmt.Errorf("create join attester set msg: %w", err)
	}

	txHash, err := broadcastTx(ctx, config, msg, operatorPrivKey, clientCtx)
	if err != nil {
		fmt.Printf("❌ Failed to broadcast MsgJoinAttesterSet: %v\n", err)
		return fmt.Errorf("broadcast join attester set tx: %w", err)
	}

	if config.Verbose {
		fmt.Printf("📝 Transaction submitted with hash: %s\n", txHash)
	}

	// Wait a bit more to ensure transaction is fully processed
	time.Sleep(500 * time.Millisecond)

	// Query the transaction multiple times to ensure it's been processed
	var txResult *sdk.TxResponse
	var retries = 10
	for i := 0; i < retries; i++ {
		txResult, err = authtx.QueryTx(clientCtx, txHash)
		if err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if err != nil {
		fmt.Printf("❌ ERROR: Could not find transaction %s after %d attempts\n", txHash, retries)
		return fmt.Errorf("transaction %s not found after %d attempts: %w", txHash, retries, err)
	}

	// Check the transaction result
	if config.Verbose {
		fmt.Printf("📊 Transaction Result: Code=%d, Height=%d\n", txResult.Code, txResult.Height)
	}

	if txResult.Code != 0 {
		fmt.Printf("❌ MsgJoinAttesterSet FAILED with code %d\n", txResult.Code)
		fmt.Printf("   Error details: %s\n", txResult.RawLog)

		// Check if the error is because we're already in the attester set
		if txResult.Code == 18 && strings.Contains(txResult.RawLog, "validator already in attester set") {
			fmt.Printf("ℹ️  Already in attester set, proceeding...\n")
			return nil
		}

		// Parse other error codes
		switch txResult.Code {
		case 4:
			fmt.Println("   Error: Unauthorized - The address may not be a valid validator")
		case 5:
			fmt.Println("   Error: Insufficient funds")
		case 11:
			fmt.Println("   Error: Out of gas")
		case 18:
			fmt.Println("   Error: Invalid request")
		default:
			fmt.Printf("   Error code %d\n", txResult.Code)
		}

		// For other errors, we should fail
		return fmt.Errorf("MsgJoinAttesterSet failed with code %d: %s", txResult.Code, txResult.RawLog)
	}

	fmt.Printf("✅ Successfully joined attester set\n")

	// Give the chain a moment to update state
	time.Sleep(500 * time.Millisecond)

	return nil
}

// pullBlocksAndAttest polls for new blocks via HTTP and attests at the end of each epoch
func pullBlocksAndAttest(
	ctx context.Context,
	config *Config,
	valAddr sdk.ValAddress,
	senderKey *secp256k1.PrivKey,
	pv *pvm.FilePV,
	clientCtx client.Context,
) error {
	// Parse node URL
	parsed, err := url.Parse(config.Node)
	if err != nil {
		return fmt.Errorf("parse node URL: %w", err)
	}

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	// First, get the current block height
	resp, err := httpClient.Get(fmt.Sprintf("http://%s/status", parsed.Host))
	if err != nil {
		return fmt.Errorf("error querying status: %v", err)
	}

	var statusResponse struct {
		Result struct {
			SyncInfo struct {
				LatestBlockHeight string `json:"latest_block_height"`
			} `json:"sync_info"`
		} `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&statusResponse); err != nil {
		_ = resp.Body.Close()
		return fmt.Errorf("error parsing status response: %v", err)
	}
	_ = resp.Body.Close()

	currentHeight, err := strconv.ParseInt(statusResponse.Result.SyncInfo.LatestBlockHeight, 10, 64)
	if err != nil {
		return fmt.Errorf("error parsing current height: %v", err)
	}

	fmt.Printf("📊 Current blockchain height: %d\n", currentHeight)
	fmt.Printf("📝 Attesting blocks 1-%d...\n", currentHeight)

	// Track failed blocks for retry
	failedBlocks := make(map[int64]int) // block height -> retry count
	maxRetries := 3

	// Attest all historical blocks from 1 to current height
	for height := int64(1); height <= currentHeight; height++ {
		select {
		case <-ctx.Done():
			return nil
		default:
			if height%10 == 0 || height == 1 || height == currentHeight {
				fmt.Printf("📦 Attesting blocks... %d/%d\n", height, currentHeight)
			}

			// Submit attestation for this historical block
			err = submitAttestation(ctx, config, height, valAddr, senderKey, pv, clientCtx)
			if err != nil {
				fmt.Printf("⚠️  Error attesting block %d: %v\n", height, err)
				failedBlocks[height] = 1
				// Continue with next block instead of failing
				continue
			}
			// Successfully attested

			// Small delay to avoid overwhelming the node
			time.Sleep(time.Millisecond * 100)
		}
	}

	// Retry failed blocks
	if len(failedBlocks) > 0 {
		fmt.Printf("\n🔄 Retrying %d failed blocks...\n", len(failedBlocks))
		for retryRound := 1; retryRound <= maxRetries && len(failedBlocks) > 0; retryRound++ {
			fmt.Printf("  Round %d/%d - %d blocks remaining\n", retryRound, maxRetries, len(failedBlocks))

			// Create a list of blocks to retry in this round
			blocksToRetry := make([]int64, 0, len(failedBlocks))
			for height := range failedBlocks {
				blocksToRetry = append(blocksToRetry, height)
			}

			// Sort blocks to retry them in order
			for i := 0; i < len(blocksToRetry); i++ {
				for j := i + 1; j < len(blocksToRetry); j++ {
					if blocksToRetry[i] > blocksToRetry[j] {
						blocksToRetry[i], blocksToRetry[j] = blocksToRetry[j], blocksToRetry[i]
					}
				}
			}

			// Retry each failed block
			for _, height := range blocksToRetry {
				select {
				case <-ctx.Done():
					return nil
				default:
					fmt.Printf("  🔄 Retrying block %d (attempt %d)...\n", height, failedBlocks[height]+1)
					err = submitAttestation(ctx, config, height, valAddr, senderKey, pv, clientCtx)
					if err != nil {
						failedBlocks[height]++
						if failedBlocks[height] >= maxRetries {
							fmt.Printf("  ❌ Block %d failed after %d attempts\n", height, maxRetries)
							delete(failedBlocks, height)
						}
					} else {
						fmt.Printf("  ✅ Block %d attested successfully\n", height)
						delete(failedBlocks, height)
					}

					// Small delay between retries
					time.Sleep(time.Millisecond * 300)
				}
			}

			if len(failedBlocks) > 0 {
				// Wait between retry rounds
				time.Sleep(time.Second * 2)
			}
		}

		if len(failedBlocks) > 0 {
			fmt.Printf("\n❌ Failed to attest %d blocks after all retries\n", len(failedBlocks))
			for height := range failedBlocks {
				fmt.Printf("  - Block %d\n", height)
			}
		}
	}

	fmt.Printf("✅ Finished historical blocks. Watching for new blocks...\n")

	var lastAttested int64 = currentHeight

	// Poll for new blocks
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			// Query latest block
			resp, err := httpClient.Get(fmt.Sprintf("http://%s/block", parsed.Host))
			if err != nil {
				fmt.Printf("Error querying block: %v\n", err)
				time.Sleep(time.Second / 10)
				continue
			}

			var blockResponse struct {
				Result struct {
					Block struct {
						Header struct {
							Height  string `json:"height"`
							AppHash string `json:"app_hash"`
						} `json:"header"`
					} `json:"block"`
				} `json:"result"`
			}

			var buf bytes.Buffer
			if err := json.NewDecoder(io.TeeReader(resp.Body, &buf)).Decode(&blockResponse); err != nil {
				fmt.Printf("Error parsing response: %v: %s\n", err, buf.String())
				_ = resp.Body.Close()
				time.Sleep(time.Second / 10)
				continue
			}
			_ = resp.Body.Close()

			// Extract block height
			heightStr := blockResponse.Result.Block.Header.Height
			if heightStr == "" {
				if config.Verbose {
					fmt.Println("Height field is empty in response, retrying...")
				}
				time.Sleep(time.Second / 10)
				continue
			}
			height, err := strconv.ParseInt(heightStr, 10, 64)
			if err != nil {
				fmt.Printf("Error parsing height: %v\n", err)
				time.Sleep(time.Second / 10)
				continue
			}

			// Attest to any blocks we might have missed
			if height > lastAttested {
				// Catch up on any blocks we missed
				for missedHeight := lastAttested + 1; missedHeight <= height; missedHeight++ {
					fmt.Printf("📦 New block %d - attesting...\n", missedHeight)

					err = submitAttestation(ctx, config, missedHeight, valAddr, senderKey, pv, clientCtx)
					if err != nil {
						fmt.Printf("⚠️  Error submitting attestation for block %d: %v\n", missedHeight, err)
						// Continue to next block instead of failing completely
						continue
					}
					fmt.Printf("✅ Attested block %d\n", missedHeight)
				}

				lastAttested = height
			}

			// Wait before next poll
			time.Sleep(50 * time.Millisecond)
		}
	}
}

var accSeq uint64 = 0

// broadcastTx executes a command to broadcast a transaction using the Cosmos SDK
func broadcastTx(ctx context.Context, config *Config, msg proto.Message, privKey *secp256k1.PrivKey, clientCtx client.Context) (string, error) {
	// Create proper transaction
	txBuilder := clientCtx.TxConfig.NewTxBuilder()
	err := txBuilder.SetMsgs(msg)
	if err != nil {
		return "", fmt.Errorf("setting messages: %w", err)
	}

	txBuilder.SetGasLimit(200000)
	txBuilder.SetFeeAmount(sdk.NewCoins(sdk.NewCoin("stake", math.NewInt(200))))
	txBuilder.SetMemo("")
	// Get account info from node
	addr := sdk.AccAddress(privKey.PubKey().Address())
	accountRetriever := authtypes.AccountRetriever{}
	account, err := accountRetriever.GetAccount(clientCtx, addr)
	if err != nil {
		return "", fmt.Errorf("getting account: %w", err)
	}
	fmt.Printf("+++ chainid: %s, GetAccountNumber: %d\n", config.ChainID, account.GetAccountNumber())

	// Always use the current sequence from the account, don't rely on cached value
	// This prevents sequence mismatch errors when multiple transactions are sent
	currentSeq := account.GetSequence()
	if accSeq == 0 {
		// First time - use account sequence
		accSeq = currentSeq
		fmt.Printf("+++ Initializing sequence to: %d\n", accSeq)
	} else if currentSeq > accSeq {
		// Account sequence has advanced beyond our cached value - sync up
		fmt.Printf("+++ Sequence drift detected: cached=%d, actual=%d, syncing to %d\n", accSeq, currentSeq, currentSeq)
		accSeq = currentSeq
	}
	// If currentSeq < accSeq, our cached value is ahead (pending tx), keep using it

	signerData := authsigning.SignerData{
		Address:       addr.String(),
		ChainID:       config.ChainID,
		AccountNumber: account.GetAccountNumber(),
		Sequence:      accSeq,
		PubKey:        privKey.PubKey(),
	}

	// For SIGN_MODE_DIRECT, we need to set a nil signature first
	// to generate the correct sign bytes
	sigData := signing.SingleSignatureData{
		SignMode:  signing.SignMode_SIGN_MODE_DIRECT,
		Signature: nil,
	}
	sig := signing.SignatureV2{
		PubKey:   privKey.PubKey(),
		Data:     &sigData,
		Sequence: accSeq,
	}

	err = txBuilder.SetSignatures(sig)
	if err != nil {
		return "", fmt.Errorf("setting nil signatures: %w", err)
	}

	// Now get the bytes to sign and create the real signature
	signBytes, err := authsigning.GetSignBytesAdapter(
		ctx,
		clientCtx.TxConfig.SignModeHandler(),
		signing.SignMode_SIGN_MODE_DIRECT,
		signerData,
		txBuilder.GetTx(),
	)
	if err != nil {
		return "", fmt.Errorf("getting sign bytes: %w", err)
	}

	// Sign those bytes
	signature, err := privKey.Sign(signBytes)
	if err != nil {
		return "", fmt.Errorf("signing bytes: %w", err)
	}

	// Construct the SignatureV2 struct with the actual signature
	sigData = signing.SingleSignatureData{
		SignMode:  signing.SignMode_SIGN_MODE_DIRECT,
		Signature: signature,
	}
	sig = signing.SignatureV2{
		PubKey:   privKey.PubKey(),
		Data:     &sigData,
		Sequence: accSeq,
	}

	err = txBuilder.SetSignatures(sig)
	if err != nil {
		return "", fmt.Errorf("setting signatures: %w", err)
	}

	txBytes, err := clientCtx.TxConfig.TxEncoder()(txBuilder.GetTx())
	if err != nil {
		return "", fmt.Errorf("encoding transaction: %w", err)
	}

	// Use sync mode (commit mode is not supported in this version)
	clientCtx = clientCtx.WithBroadcastMode("sync")

	// Broadcast the transaction
	resp, err := clientCtx.BroadcastTx(txBytes)
	if err != nil {
		return "", fmt.Errorf("broadcasting transaction: %w", err)
	}

	// In sync mode, resp.Code only reflects CheckTx result (mempool validation)
	if resp.Code != 0 {
		// Transaction failed in mempool validation
		fmt.Printf("❌ Transaction FAILED in mempool validation with code %d: %s\n", resp.Code, resp.RawLog)
		return "", fmt.Errorf("transaction failed in mempool with code %d: %s", resp.Code, resp.RawLog)
	}

	// Transaction passed mempool validation, now wait and check final result
	fmt.Printf("📝 Transaction submitted with hash: %s\n", resp.TxHash)
	fmt.Printf("   Waiting for confirmation...\n")

	// Wait a bit for the transaction to be included in a block
	time.Sleep(500 * time.Millisecond)

	// Query the transaction to get the final execution result
	var txResult *sdk.TxResponse
	var retries = 5
	for i := 0; i < retries; i++ {
		txResult, err = authtx.QueryTx(clientCtx, resp.TxHash)
		if err == nil {
			break
		}
		if i < retries-1 {
			time.Sleep(500 * time.Millisecond)
		}
	}

	if err != nil {
		fmt.Printf("⚠️  Warning: Could not verify transaction result after %d attempts: %v\n", retries, err)
		fmt.Printf("   Transaction may still be pending. Hash: %s\n", resp.TxHash)
	} else {
		// Check the final execution result
		if txResult.Code != 0 {
			// Transaction failed during execution
			fmt.Printf("❌ Transaction FAILED during execution with code %d\n", txResult.Code)
			fmt.Printf("   Transaction hash: %s\n", txResult.TxHash)
			fmt.Printf("   Error details: %s\n", txResult.RawLog)
			fmt.Printf("   Height: %d\n", txResult.Height)

			// Special handling for "already in attester set" error for MsgJoinAttesterSet
			if txResult.Code == 18 && strings.Contains(txResult.RawLog, "validator already in attester set") {
				fmt.Printf("ℹ️  Transaction indicates validator is already in attester set - this is OK for join operations\n")
				// Don't return error for this case - let the calling function handle it
				// We'll return success but the caller should check the result
			} else {
				// Parse other error codes
				switch txResult.Code {
				case 4:
					fmt.Println("   Error type: Unauthorized (likely signature verification failed)")
				case 5:
					fmt.Println("   Error type: Insufficient funds")
				case 11:
					fmt.Println("   Error type: Out of gas")
				case 18:
					fmt.Println("   Error type: Invalid request (e.g., height is not a checkpoint)")
					fmt.Println("   💡 Tip: Attestations are only accepted at checkpoint heights (multiples of epoch_length)")
				case 19:
					fmt.Println("   Error type: Transaction already in mempool")
				case 22:
					fmt.Println("   Error type: Invalid argument")
				default:
					fmt.Printf("   Error code %d - check logs for details\n", txResult.Code)
				}

				return "", fmt.Errorf("transaction failed during execution with code %d: %s", txResult.Code, txResult.RawLog)
			}
		}

		// Transaction was successful
		fmt.Printf("✅ Transaction SUCCEEDED at height %d\n", txResult.Height)
		fmt.Printf("   Hash: %s\n", txResult.TxHash)
		if config.Verbose {
			fmt.Printf("   Gas used: %d/%d\n", txResult.GasUsed, txResult.GasWanted)
		}
	}

	accSeq++
	return resp.TxHash, nil
}

// createClientContext creates a client.Context with codecs and the necessary fields for broadcasting transactions
func createClientContext(config *Config) (client.Context, error) {
	rpcClient, err := client.NewClientFromNode(config.Node)
	if err != nil {
		return client.Context{}, fmt.Errorf("creating RPC client: %w", err)
	}

	interfaceRegistry := codectypes.NewInterfaceRegistry()
	authtypes.RegisterInterfaces(interfaceRegistry)
	std.RegisterInterfaces(interfaceRegistry)
	networktypes.RegisterInterfaces(interfaceRegistry)
	protoCodec := codec.NewProtoCodec(interfaceRegistry)
	txConfig := authtx.NewTxConfig(protoCodec, authtx.DefaultSignModes)

	clientCtx := client.Context{
		Client:            rpcClient,
		ChainID:           config.ChainID,
		TxConfig:          txConfig,
		BroadcastMode:     "sync",
		Output:            os.Stdout,
		AccountRetriever:  authtypes.AccountRetriever{},
		InterfaceRegistry: interfaceRegistry,
		Codec:             protoCodec,
	}

	return clientCtx, nil
}

// privateKeyFromMnemonic derives a private key from a mnemonic using the standard
// BIP44 HD path (m/44'/118'/0'/0/0)
func privateKeyFromMnemonic(mnemonic string) (*secp256k1.PrivKey, error) {
	derivedPriv, err := hd.Secp256k1.Derive()(
		mnemonic,
		"",
		hd.CreateHDPath(118, 0, 0).String(), // Cosmos HD path
	)
	if err != nil {
		return nil, fmt.Errorf("failed to derive private key: %w", err)
	}
	return &secp256k1.PrivKey{Key: derivedPriv}, nil
}

// submitAttestation creates and submits an attestation for a block using PayloadProvider
func submitAttestation(
	ctx context.Context,
	config *Config,
	height int64,
	valAddr sdk.ValAddress,
	senderKey *secp256k1.PrivKey,
	pv *pvm.FilePV,
	clientCtx client.Context,
) error {
	header, err := getEvolveHeader(config.Node, height)
	if err != nil {
		return fmt.Errorf("getting Evolve header: %w", err)
	}

	// Get BlockID from store like the sequencer does via SignaturePayloadProvider
	// We need to reconstruct the original BlockID that the proposer used
	blockID, err := getOriginalBlockID(ctx, config.Node, height)
	if err != nil {
		return fmt.Errorf("getting original block ID: %w", err)
	}

	// Create vote using the same logic as SignaturePayloadProvider
	// Use the original header values that the proposer used, not the post-processed ones
	vote := cmtproto.Vote{
		Type:             cmtproto.PrecommitType,
		Height:           height,
		BlockID:          blockID,
		Round:            0,
		Timestamp:        header.Time(),
		ValidatorAddress: pv.Key.PrivKey.PubKey().Address(), // Use attester's consensus address
		ValidatorIndex:   0,
	}

	// Create vote structure for signing

	// Use the same method as cometcompat to get sign bytes
	signBytes := cmttypes.VoteSignBytes(config.ChainID, &vote)

	// Sign the payload using the private validator
	signature, err := pv.Key.PrivKey.Sign(signBytes)
	if err != nil {
		return fmt.Errorf("signing payload: %w", err)
	}

	// Sign the vote for attestation

	// Get the actual validator address from the private validator key file
	// instead of using pv.GetAddress() which calculates it differently
	validatorAddr := pv.Key.Address

	fmt.Printf("🔍 DEBUG ValidatorAddr used in vote: %X\n", validatorAddr)
	fmt.Printf("🔍 DEBUG pv.GetAddress(): %X\n", pv.GetAddress())
	fmt.Printf("🔍 DEBUG pubKey.Address(): %X\n", pv.Key.PubKey.Address())

	// Create the vote structure for the attestation
	attesterVote := &cmtproto.Vote{
		Type:             cmtproto.PrecommitType,
		ValidatorAddress: validatorAddr,
		Height:           height,
		Round:            0,
		BlockID:          cmtproto.BlockID{Hash: header.Hash(), PartSetHeader: cmtproto.PartSetHeader{}},
		Timestamp:        header.Time(),
		Signature:        signature,
	}

	voteBytes, err := proto.Marshal(attesterVote)
	if err != nil {
		return fmt.Errorf("marshal vote: %w", err)
	}

	// Create MsgAttest with separate authority and consensus address
	// authority: account that pays the transaction (from mnemonic)
	// consensusAddress: consensus address that will be stored for attestation
	authorityAddr := sdk.AccAddress(senderKey.PubKey().Address()).String()
	msg := networktypes.NewMsgAttest(
		authorityAddr,
		valAddr.String(),
		height,
		voteBytes,
	)

	// Create attestation message

	txHash, err := broadcastTx(ctx, config, msg, senderKey, clientCtx)
	if err != nil {
		return fmt.Errorf("broadcast attest tx: %w", err)
	}

	if config.Verbose {
		fmt.Printf("Attestation submitted for block %d with hash: %s\n", height, txHash)
	}
	return nil
}

// getEvolveHeader gets the Evolve header for a given height
func getEvolveHeader(node string, height int64) (*evolvetypes.Header, error) {
	// Parse node URL
	parsed, err := url.Parse(node)
	if err != nil {
		return nil, fmt.Errorf("parse node URL: %w", err)
	}

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	// Query the block at the specific height
	resp, err := httpClient.Get(fmt.Sprintf("http://%s/block?height=%d", parsed.Host, height))
	if err != nil {
		return nil, fmt.Errorf("querying block: %w", err)
	}
	defer resp.Body.Close()

	var blockResponse struct {
		Result struct {
			Block struct {
				Header struct {
					Version struct {
						Block string `json:"block"`
						App   string `json:"app"`
					} `json:"version"`
					Height      string `json:"height"`
					Time        string `json:"time"`
					LastBlockID struct {
						Hash string `json:"hash"`
					} `json:"last_block_id"`
					LastCommitHash     string `json:"last_commit_hash"`
					DataHash           string `json:"data_hash"`
					ValidatorsHash     string `json:"validators_hash"`
					NextValidatorsHash string `json:"next_validators_hash"`
					ConsensusHash      string `json:"consensus_hash"`
					AppHash            string `json:"app_hash"`
					LastResultsHash    string `json:"last_results_hash"`
					EvidenceHash       string `json:"evidence_hash"`
					ProposerAddress    string `json:"proposer_address"`
					ChainID            string `json:"chain_id"`
				} `json:"header"`
			} `json:"block"`
		} `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&blockResponse); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	header := blockResponse.Result.Block.Header

	// Parse height
	heightUint, err := strconv.ParseUint(header.Height, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parsing height: %w", err)
	}

	// Parse time
	timeStamp, err := time.Parse(time.RFC3339Nano, header.Time)
	if err != nil {
		return nil, fmt.Errorf("parsing time: %w", err)
	}

	// Parse hex fields
	lastHeaderHash, _ := hex.DecodeString(header.LastBlockID.Hash)
	lastCommitHash, _ := hex.DecodeString(header.LastCommitHash)
	dataHash, _ := hex.DecodeString(header.DataHash)
	validatorsHash, _ := hex.DecodeString(header.ValidatorsHash)
	consensusHash, _ := hex.DecodeString(header.ConsensusHash)
	appHash, _ := hex.DecodeString(header.AppHash)
	lastResultsHash, _ := hex.DecodeString(header.LastResultsHash)
	proposerAddress, _ := hex.DecodeString(header.ProposerAddress)

	// Parse version
	appVersion, _ := strconv.ParseUint(header.Version.App, 10, 64)

	// Create Evolve header
	evHeader := &evolvetypes.Header{
		BaseHeader: evolvetypes.BaseHeader{
			Height:  heightUint,
			Time:    uint64(timeStamp.UnixNano()),
			ChainID: header.ChainID,
		},
		Version: evolvetypes.Version{
			Block: 1, // Default block version
			App:   appVersion,
		},
		LastHeaderHash:  lastHeaderHash,
		LastCommitHash:  lastCommitHash,
		DataHash:        dataHash,
		ConsensusHash:   consensusHash,
		AppHash:         appHash,
		LastResultsHash: lastResultsHash,
		ProposerAddress: proposerAddress,
		ValidatorHash:   validatorsHash,
	}

	return evHeader, nil
}

// getOriginalBlockID gets the BlockID from the RPC endpoint
// The /block endpoint returns the same BlockID that the sequencer uses via SignaturePayloadProvider
func getOriginalBlockID(ctx context.Context, node string, height int64) (cmtproto.BlockID, error) {
	// For height 1 and below, use empty BlockID like the sequencer does
	// The sequencer's SignaturePayloadProvider ignores errors for height <= 1
	if height <= 1 {
		return cmtproto.BlockID{}, nil
	}

	// Parse node URL
	parsed, err := url.Parse(node)
	if err != nil {
		return cmtproto.BlockID{}, fmt.Errorf("parse node URL: %w", err)
	}

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	// Query the block endpoint which includes the BlockID
	resp, err := httpClient.Get(fmt.Sprintf("http://%s/block?height=%d", parsed.Host, height))
	if err != nil {
		return cmtproto.BlockID{}, fmt.Errorf("querying block: %w", err)
	}
	defer resp.Body.Close()

	// Parse the full block response including BlockID
	var blockResponse struct {
		Result struct {
			BlockID struct {
				Hash  string `json:"hash"`
				Parts struct {
					Hash  string `json:"hash"`
					Total uint32 `json:"total"`
				} `json:"parts"`
			} `json:"block_id"`
			Block struct {
				Header struct {
					Height  string `json:"height"`
					Time    string `json:"time"`
					ChainID string `json:"chain_id"`
				} `json:"header"`
			} `json:"block"`
		} `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&blockResponse); err != nil {
		return cmtproto.BlockID{}, fmt.Errorf("decoding response: %w", err)
	}

	// Parse BlockID from RPC response

	// Parse block ID hash
	blockIDHash, err := hex.DecodeString(blockResponse.Result.BlockID.Hash)
	if err != nil {
		return cmtproto.BlockID{}, fmt.Errorf("decoding block ID hash: %w", err)
	}

	partSetHeaderHash, err := hex.DecodeString(blockResponse.Result.BlockID.Parts.Hash)
	if err != nil {
		return cmtproto.BlockID{}, fmt.Errorf("decoding part set header hash: %w", err)
	}

	// Return the BlockID with full PartSetHeader
	return cmtproto.BlockID{
		Hash: blockIDHash,
		PartSetHeader: cmtproto.PartSetHeader{
			Total: blockResponse.Result.BlockID.Parts.Total,
			Hash:  partSetHeaderHash,
		},
	}, nil
}
