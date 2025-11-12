package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/cosmos/cosmos-sdk/client/flags"
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
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module/testutil"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	"github.com/cosmos/cosmos-sdk/x/auth"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/bank"
	"github.com/cosmos/gogoproto/proto"
	"github.com/spf13/cobra"

	evolvetypes "github.com/evstack/ev-node/types"

	networktypes "github.com/evstack/ev-abci/modules/network/types"
)

const (
	flagVerbose      = "verbose"
	flagMnemonic     = "mnemonic"
	flagPrivKeyArmor = "priv-key-armor"
)

// AttesterConfig holds all configuration parameters for the attester
type AttesterConfig struct {
	ChainID      string
	Node         string
	Home         string
	Verbose      bool
	Mnemonic     string
	PrivKeyArmor string
}

// NewAttesterCmd creates a command to run the attester client
func NewAttesterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attester",
		Short: "Run attester client for Evolve",
		Long:  `Attester client for Evolve that joins the attester set and attests to blocks`,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx := client.GetClientContextFromCmd(cmd)

			mnemonic, err := cmd.Flags().GetString(flagMnemonic)
			if err != nil {
				return err
			}

			privKeyArmor, err := cmd.Flags().GetString(flagPrivKeyArmor)
			if err != nil {
				return err
			}

			verbose, err := cmd.Flags().GetBool(flagVerbose)
			if err != nil {
				return err
			}

			config := &AttesterConfig{
				ChainID:      clientCtx.ChainID,
				Node:         clientCtx.NodeURI,
				Home:         clientCtx.HomeDir,
				Verbose:      verbose,
				Mnemonic:     mnemonic,
				PrivKeyArmor: privKeyArmor,
			}

			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			var operatorPrivKey *secp256k1.PrivKey
			if config.PrivKeyArmor != "" {
				operatorPrivKey, err = privateKeyFromArmor(config.PrivKeyArmor)
				if err != nil {
					return fmt.Errorf("failed to create private key from armored key: %w", err)
				}
			} else if config.Mnemonic != "" {
				operatorPrivKey, err = privateKeyFromMnemonic(config.Mnemonic)
				if err != nil {
					return fmt.Errorf("failed to create private key from mnemonic: %w", err)
				}
			} else {
				return fmt.Errorf("either --mnemonic or --priv-key-armor must be provided")
			}

			privKeyPath := filepath.Join(config.Home, "config", "priv_validator_key.json")
			privStatePath := filepath.Join(config.Home, "data", "priv_validator_state.json")
			consensusPrivKey := pvm.LoadFilePV(privKeyPath, privStatePath)
			valAddr := sdk.ValAddress(consensusPrivKey.Key.Address)

			if config.Verbose {
				addr := sdk.AccAddress(operatorPrivKey.PubKey().Address())
				cmd.Printf("Sender Account address: %s\n", addr.String())
				cmd.Printf("Sender Validator address: %s\n", valAddr.String())
			}

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigCh
				cmd.Println("Received signal, shutting down...")
				cancel()
			}()

			cmd.Println("Joining attester set...")
			if err := joinAttesterSet(ctx, config, valAddr, operatorPrivKey, consensusPrivKey, clientCtx); err != nil {
				return fmt.Errorf("join attester set: %w", err)
			}

			cmd.Println("Starting to watch for new blocks...")
			if err := pullBlocksAndAttest(ctx, config, valAddr, operatorPrivKey, consensusPrivKey, clientCtx); err != nil {
				return fmt.Errorf("error watching blocks: %w", err)
			}

			return nil
		},
	}

	cmd.Flags().String(flagMnemonic, "", "Mnemonic for the private key")
	cmd.Flags().String(flagPrivKeyArmor, "", "ASCII armored private key (alternative to mnemonic)")
	cmd.Flags().Bool(flagVerbose, false, "Enable verbose output")

	flags.AddTxFlagsToCmd(cmd)

	return cmd
}

func joinAttesterSet(
	ctx context.Context,
	config *AttesterConfig,
	valAddr sdk.ValAddress,
	operatorPrivKey *secp256k1.PrivKey,
	consensusPrivKey *pvm.FilePV,
	clientCtx client.Context,
) error {
	sdkPubKey, err := cryptocodec.FromCmtPubKeyInterface(consensusPrivKey.Key.PubKey)
	if err != nil {
		return fmt.Errorf("convert public key: %w", err)
	}

	authorityAddr, err := clientCtx.InterfaceRegistry.SigningContext().AddressCodec().BytesToString(operatorPrivKey.PubKey().Address())
	if err != nil {
		return fmt.Errorf("convert authority address: %w", err)
	}

	msg, err := networktypes.NewMsgJoinAttesterSet(authorityAddr, valAddr.String(), sdkPubKey)
	if err != nil {
		return fmt.Errorf("create join attester set msg: %w", err)
	}

	txHash, err := broadcastTx(ctx, config, msg, operatorPrivKey, clientCtx)
	if err != nil {
		return fmt.Errorf("broadcast join attester set tx: %w", err)
	}

	if config.Verbose {
		fmt.Printf("📝 Transaction submitted with hash: %s\n", txHash)
	}

	time.Sleep(500 * time.Millisecond)

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
		return fmt.Errorf("transaction %s not found after %d attempts: %w", txHash, retries, err)
	}

	if config.Verbose {
		fmt.Printf("📊 Transaction Result: Code=%d, Height=%d\n", txResult.Code, txResult.Height)
	}

	if txResult.Code != 0 {
		fmt.Printf("❌ MsgJoinAttesterSet FAILED with code %d\n", txResult.Code)
		fmt.Printf("   Error details: %s\n", txResult.RawLog)

		if txResult.Code == 18 && strings.Contains(txResult.RawLog, "validator already in attester set") {
			fmt.Printf("ℹ️  Already in attester set, proceeding...\n")
			return nil
		}

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

		return fmt.Errorf("MsgJoinAttesterSet failed with code %d: %s", txResult.Code, txResult.RawLog)
	}

	fmt.Printf("✅ Successfully joined attester set\n")
	time.Sleep(500 * time.Millisecond)

	return nil
}

func pullBlocksAndAttest(
	ctx context.Context,
	config *AttesterConfig,
	valAddr sdk.ValAddress,
	senderKey *secp256k1.PrivKey,
	pv *pvm.FilePV,
	clientCtx client.Context,
) error {
	parsed, err := url.Parse(config.Node)
	if err != nil {
		return fmt.Errorf("parse node URL: %w", err)
	}

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

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

	failedBlocks := make(map[int64]int)
	maxRetries := 3

	for height := int64(1); height <= currentHeight; height++ {
		select {
		case <-ctx.Done():
			return nil
		default:
			if height%10 == 0 || height == 1 || height == currentHeight {
				fmt.Printf("📦 Attesting blocks... %d/%d\n", height, currentHeight)
			}

			err = submitAttestation(ctx, config, height, valAddr, senderKey, pv, clientCtx)
			if err != nil {
				fmt.Printf("⚠️  Error attesting block %d: %v\n", height, err)
				failedBlocks[height] = 1
				continue
			}

			time.Sleep(time.Millisecond * 100)
		}
	}

	if len(failedBlocks) > 0 {
		fmt.Printf("\n🔄 Retrying %d failed blocks...\n", len(failedBlocks))
		for retryRound := 1; retryRound <= maxRetries && len(failedBlocks) > 0; retryRound++ {
			fmt.Printf("  Round %d/%d - %d blocks remaining\n", retryRound, maxRetries, len(failedBlocks))

			blocksToRetry := make([]int64, 0, len(failedBlocks))
			for height := range failedBlocks {
				blocksToRetry = append(blocksToRetry, height)
			}

			for i := 0; i < len(blocksToRetry); i++ {
				for j := i + 1; j < len(blocksToRetry); j++ {
					if blocksToRetry[i] > blocksToRetry[j] {
						blocksToRetry[i], blocksToRetry[j] = blocksToRetry[j], blocksToRetry[i]
					}
				}
			}

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

					time.Sleep(time.Millisecond * 300)
				}
			}

			if len(failedBlocks) > 0 {
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

	lastAttested := currentHeight

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
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

			if height > lastAttested {
				for missedHeight := lastAttested + 1; missedHeight <= height; missedHeight++ {
					fmt.Printf("📦 New block %d - attesting...\n", missedHeight)

					err = submitAttestation(ctx, config, missedHeight, valAddr, senderKey, pv, clientCtx)
					if err != nil {
						fmt.Printf("⚠️  Error submitting attestation for block %d: %v\n", missedHeight, err)
						continue
					}
					fmt.Printf("✅ Attested block %d\n", missedHeight)
				}

				lastAttested = height
			}

			time.Sleep(50 * time.Millisecond)
		}
	}
}

var accSeq uint64 = 0

func broadcastTx(
	ctx context.Context,
	config *AttesterConfig,
	msg proto.Message,
	privKey *secp256k1.PrivKey,
	clientCtx client.Context,
) (string, error) {
	txBuilder := clientCtx.TxConfig.NewTxBuilder()
	err := txBuilder.SetMsgs(msg)
	if err != nil {
		return "", fmt.Errorf("setting messages: %w", err)
	}

	txBuilder.SetGasLimit(200000)
	txBuilder.SetFeeAmount(sdk.NewCoins(sdk.NewCoin(sdk.DefaultBondDenom, math.NewInt(200))))
	txBuilder.SetMemo("")

	addr := sdk.AccAddress(privKey.PubKey().Address())
	accountRetriever := authtypes.AccountRetriever{}
	account, err := accountRetriever.GetAccount(clientCtx, addr)
	if err != nil {
		return "", fmt.Errorf("getting account: %w", err)
	}
	fmt.Printf("+++ chainid: %s, GetAccountNumber: %d\n", config.ChainID, account.GetAccountNumber())

	currentSeq := account.GetSequence()
	if accSeq == 0 {
		accSeq = currentSeq
		fmt.Printf("+++ Initializing sequence to: %d\n", accSeq)
	} else if currentSeq > accSeq {
		fmt.Printf("+++ Sequence drift detected: cached=%d, actual=%d, syncing to %d\n", accSeq, currentSeq, currentSeq)
		accSeq = currentSeq
	}

	signerData := authsigning.SignerData{
		Address:       addr.String(),
		ChainID:       config.ChainID,
		AccountNumber: account.GetAccountNumber(),
		Sequence:      accSeq,
		PubKey:        privKey.PubKey(),
	}

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

	signature, err := privKey.Sign(signBytes)
	if err != nil {
		return "", fmt.Errorf("signing bytes: %w", err)
	}

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

	clientCtx = clientCtx.WithBroadcastMode("sync")

	resp, err := clientCtx.BroadcastTx(txBytes)
	if err != nil {
		return "", fmt.Errorf("broadcasting transaction: %w", err)
	}

	if resp.Code != 0 {
		fmt.Printf("❌ Transaction FAILED in mempool validation with code %d: %s\n", resp.Code, resp.RawLog)
		return "", fmt.Errorf("transaction failed in mempool with code %d: %s", resp.Code, resp.RawLog)
	}

	fmt.Printf("📝 Transaction submitted with hash: %s\n", resp.TxHash)
	fmt.Printf("   Waiting for confirmation...\n")

	time.Sleep(500 * time.Millisecond)

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
		if txResult.Code != 0 {
			fmt.Printf("❌ Transaction FAILED during execution with code %d\n", txResult.Code)
			fmt.Printf("   Transaction hash: %s\n", txResult.TxHash)
			fmt.Printf("   Error details: %s\n", txResult.RawLog)
			fmt.Printf("   Height: %d\n", txResult.Height)

			if txResult.Code == 18 && strings.Contains(txResult.RawLog, "validator already in attester set") {
				fmt.Printf("ℹ️  Transaction indicates validator is already in attester set - this is OK for join operations\n")
			} else {
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

		fmt.Printf("✅ Transaction SUCCEEDED at height %d\n", txResult.Height)
		fmt.Printf("   Hash: %s\n", txResult.TxHash)
		if config.Verbose {
			fmt.Printf("   Gas used: %d/%d\n", txResult.GasUsed, txResult.GasWanted)
		}
	}

	accSeq++
	return resp.TxHash, nil
}

func privateKeyFromMnemonic(mnemonic string) (*secp256k1.PrivKey, error) {
	derivedPriv, err := hd.Secp256k1.Derive()(
		mnemonic,
		"",
		hd.CreateHDPath(118, 0, 0).String(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to derive private key: %w", err)
	}
	return &secp256k1.PrivKey{Key: derivedPriv}, nil
}

func privateKeyFromArmor(armoredKey string) (*secp256k1.PrivKey, error) {
	testEncCfg := testutil.MakeTestEncodingConfig(auth.AppModuleBasic{}, bank.AppModuleBasic{})
	kr := keyring.NewInMemory(testEncCfg.Codec)

	err := kr.ImportPrivKey("temp", armoredKey, "")
	if err != nil {
		return nil, fmt.Errorf("failed to import armored private key: %w", err)
	}

	keyRecord, err := kr.Key("temp")
	if err != nil {
		return nil, fmt.Errorf("failed to get imported key: %w", err)
	}

	localRecord := keyRecord.GetLocal()
	if localRecord == nil {
		return nil, fmt.Errorf("failed to get local record: record is nil")
	}

	if localRecord.PrivKey == nil {
		return nil, fmt.Errorf("failed to get private key: key is nil")
	}

	privKey, ok := localRecord.PrivKey.GetCachedValue().(*secp256k1.PrivKey)
	if !ok {
		return nil, fmt.Errorf("failed to cast private key to secp256k1.PrivKey")
	}

	return privKey, nil
}

func submitAttestation(
	ctx context.Context,
	config *AttesterConfig,
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

	blockID, err := getOriginalBlockID(ctx, config.Node, height)
	if err != nil {
		return fmt.Errorf("getting original block ID: %w", err)
	}

	vote := cmtproto.Vote{
		Type:             cmtproto.PrecommitType,
		Height:           height,
		BlockID:          blockID,
		Round:            0,
		Timestamp:        header.Time(),
		ValidatorAddress: pv.Key.PrivKey.PubKey().Address(),
		ValidatorIndex:   0,
	}

	signBytes := cmttypes.VoteSignBytes(config.ChainID, &vote)

	signature, err := pv.Key.PrivKey.Sign(signBytes)
	if err != nil {
		return fmt.Errorf("signing payload: %w", err)
	}

	validatorAddr := pv.Key.Address

	fmt.Printf("🔍 DEBUG ValidatorAddr used in vote: %X\n", validatorAddr)
	fmt.Printf("🔍 DEBUG pv.GetAddress(): %X\n", pv.GetAddress())
	fmt.Printf("🔍 DEBUG pubKey.Address(): %X\n", pv.Key.PubKey.Address())

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

	authorityAddr := sdk.AccAddress(senderKey.PubKey().Address()).String()
	msg := networktypes.NewMsgAttest(
		authorityAddr,
		valAddr.String(),
		height,
		voteBytes,
	)

	txHash, err := broadcastTx(ctx, config, msg, senderKey, clientCtx)
	if err != nil {
		return fmt.Errorf("broadcast attest tx: %w", err)
	}

	if config.Verbose {
		fmt.Printf("Attestation submitted for block %d with hash: %s\n", height, txHash)
	}
	return nil
}

func getEvolveHeader(node string, height int64) (*evolvetypes.Header, error) {
	parsed, err := url.Parse(node)
	if err != nil {
		return nil, fmt.Errorf("parse node URL: %w", err)
	}

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := httpClient.Get(fmt.Sprintf("http://%s/block?height=%d", parsed.Host, height))
	if err != nil {
		return nil, fmt.Errorf("querying block: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

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

	heightUint, err := strconv.ParseUint(header.Height, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parsing height: %w", err)
	}

	timeStamp, err := time.Parse(time.RFC3339Nano, header.Time)
	if err != nil {
		return nil, fmt.Errorf("parsing time: %w", err)
	}

	lastHeaderHash, _ := hex.DecodeString(header.LastBlockID.Hash)
	dataHash, _ := hex.DecodeString(header.DataHash)
	validatorsHash, _ := hex.DecodeString(header.ValidatorsHash)
	appHash, _ := hex.DecodeString(header.AppHash)
	proposerAddress, _ := hex.DecodeString(header.ProposerAddress)

	appVersion, _ := strconv.ParseUint(header.Version.App, 10, 64)

	evHeader := &evolvetypes.Header{
		BaseHeader: evolvetypes.BaseHeader{
			Height:  heightUint,
			Time:    uint64(timeStamp.UnixNano()),
			ChainID: header.ChainID,
		},
		Version: evolvetypes.Version{
			Block: 1,
			App:   appVersion,
		},
		LastHeaderHash:  lastHeaderHash,
		DataHash:        dataHash,
		AppHash:         appHash,
		ProposerAddress: proposerAddress,
		ValidatorHash:   validatorsHash,
	}

	return evHeader, nil
}

func getOriginalBlockID(ctx context.Context, node string, height int64) (cmtproto.BlockID, error) {
	if height <= 1 {
		return cmtproto.BlockID{}, nil
	}

	parsed, err := url.Parse(node)
	if err != nil {
		return cmtproto.BlockID{}, fmt.Errorf("parse node URL: %w", err)
	}

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := httpClient.Get(fmt.Sprintf("http://%s/block?height=%d", parsed.Host, height))
	if err != nil {
		return cmtproto.BlockID{}, fmt.Errorf("querying block: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

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

	blockIDHash, err := hex.DecodeString(blockResponse.Result.BlockID.Hash)
	if err != nil {
		return cmtproto.BlockID{}, fmt.Errorf("decoding block ID hash: %w", err)
	}

	partSetHeaderHash, err := hex.DecodeString(blockResponse.Result.BlockID.Parts.Hash)
	if err != nil {
		return cmtproto.BlockID{}, fmt.Errorf("decoding part set header hash: %w", err)
	}

	return cmtproto.BlockID{
		Hash: blockIDHash,
		PartSetHeader: cmtproto.PartSetHeader{
			Total: blockResponse.Result.BlockID.Parts.Total,
			Hash:  partSetHeaderHash,
		},
	}, nil
}
