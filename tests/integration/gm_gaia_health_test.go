package integration_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	"github.com/celestiaorg/tastora/framework/docker/container"
	"github.com/celestiaorg/tastora/framework/docker/cosmos"
	"github.com/celestiaorg/tastora/framework/docker/ibc"
	"github.com/celestiaorg/tastora/framework/docker/ibc/relayer"
	"github.com/celestiaorg/tastora/framework/testutil/query"
	"github.com/celestiaorg/tastora/framework/testutil/sdkacc"
	"github.com/celestiaorg/tastora/framework/testutil/wait"
	"github.com/celestiaorg/tastora/framework/types"
	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	pvm "github.com/cometbft/cometbft/privval"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module/testutil"
	"github.com/cosmos/cosmos-sdk/x/auth"
	"github.com/cosmos/cosmos-sdk/x/bank"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	ibctransfer "github.com/cosmos/ibc-go/v8/modules/apps/transfer"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	"github.com/stretchr/testify/require"
)

const (
	dockerAttesterCount    = 4
	dockerAttesterQuorum   = 3
	dockerCommitScanWindow = 500
)

type generatedAttesterIdentity struct {
	OperatorArmor          string
	OperatorAddress        sdk.AccAddress
	ConsensusAddress       string
	ConsensusPubKey        cmted25519.PubKey
	PrivValidatorKeyJSON   []byte
	PrivValidatorStateJSON []byte
}

type configuredAttester struct {
	Config AttesterConfig
	Node   *Attester
}

// TestAttesterSystem runs the Docker e2e flow with multiple attesters.
func (s *DockerIntegrationTestSuite) TestAttesterSystem() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	attesterIdentities, err := generateAttesterIdentities(dockerAttesterCount)
	require.NoError(s.T(), err)

	gmChain := s.getGmChain(ctx, attesterIdentities)

	// Start GM chain in a goroutine
	go func() {
		s.T().Log("Starting GM chain...")
		err := gmChain.Start(ctx)
		if err != nil {
			s.T().Errorf("Failed to start GM chain: %v", err)
		}
	}()

	// Wait for GM chain RPC to be ready
	err = wait.ForCondition(ctx, time.Second*30, time.Second, func() (bool, error) {
		node := gmChain.GetNodes()[0]
		rpcClient, _ := node.GetRPCClient()
		if rpcClient != nil {
			// Test if RPC client is actually working by making a simple call
			_, err := rpcClient.Status(ctx)
			if err == nil {
				return true, nil
			}
		}
		return false, nil
	})
	s.Require().NoError(err)

	attesters := s.getAttesters(ctx, gmChain, attesterIdentities)
	for _, attester := range attesters {
		s.T().Logf("Initializing attester node %s", attester.Node.Name())
		err = attester.Node.Init(ctx, attester.Config.ChainID, attester.Config.GMNodeURL)
		require.NoError(s.T(), err)

		s.T().Logf("Starting attester node %s", attester.Node.Name())
		err = attester.Node.Start(ctx, attester.Config)
		require.NoError(s.T(), err)
	}
	s.T().Logf("Started %d attester nodes", len(attesters))

	// Wait for the attesters to reach quorum on reconstructed commits.
	s.T().Log("Waiting for attestations to reach quorum...")
	valSet := validatorSetFromAttesters(attesterIdentities)
	var verifiedHeight int64
	var lastSignatureCount int
	var lastVerifyErr error
	err = wait.ForCondition(ctx, 5*time.Minute, 2*time.Second, func() (bool, error) {
		node := gmChain.GetNodes()[0]
		rpcClient, _ := node.GetRPCClient()
		if rpcClient == nil {
			return false, nil
		}

		latestBlockResp, blockErr := rpcClient.Block(ctx, nil)
		if blockErr != nil || latestBlockResp == nil || latestBlockResp.Block == nil {
			lastVerifyErr = fmt.Errorf("latest block unavailable: %v", blockErr)
			return false, nil
		}
		latestHeight := latestBlockResp.Block.Height
		if latestHeight < 2 {
			lastVerifyErr = fmt.Errorf("latest height %d is too low", latestHeight)
			return false, nil
		}

		startHeight := latestHeight - dockerCommitScanWindow
		if startHeight < 2 {
			startHeight = 2
		}
		lastVerifyErr = fmt.Errorf("no quorum commit found in height range [%d,%d]", startHeight, latestHeight)
		for height := latestHeight; height >= startHeight; height-- {
			commitResp, commitErr := rpcClient.Commit(ctx, &height)
			if commitErr != nil || commitResp == nil || commitResp.SignedHeader.Commit == nil {
				continue
			}
			commit := commitResp.SignedHeader.Commit
			if len(commit.Signatures) != dockerAttesterCount {
				continue
			}
			signatureCount := countCommitSignatures(commit)
			if signatureCount < dockerAttesterQuorum {
				lastVerifyErr = fmt.Errorf("commit at height %d has %d signatures, expected quorum", height, signatureCount)
				continue
			}
			verifyErr := valSet.VerifyCommitLight("gm", commit.BlockID, height, commit)
			if verifyErr != nil {
				lastVerifyErr = fmt.Errorf("verify commit at height %d: %w", height, verifyErr)
				continue
			}
			verifiedHeight = height
			lastSignatureCount = signatureCount
			return true, nil
		}

		return false, nil
	})
	s.Require().NoError(err, "attesters did not reconstruct a quorum commit: %v", lastVerifyErr)
	s.T().Logf("commit at height %d passes VerifyCommitLight with %d/%d signatures",
		verifiedHeight, lastSignatureCount, dockerAttesterCount)
}

func (s *DockerIntegrationTestSuite) getAttesters(
	ctx context.Context,
	gmChain *cosmos.Chain,
	identities []generatedAttesterIdentity,
) []configuredAttester {
	gmNodes := gmChain.GetNodes()
	require.NotEmpty(s.T(), gmNodes, "no GM chain nodes available")

	gmNode := gmNodes[0]
	gmNodeInfo, err := gmNode.GetNetworkInfo(ctx)
	require.NoError(s.T(), err)

	fromAddr, err := sdkacc.AddressFromWallet(gmChain.GetFaucetWallet())
	require.NoError(s.T(), err, "failed to retrieve faucet address")

	fundingMsgs := make([]sdk.Msg, 0, len(identities))
	for _, identity := range identities {
		coins := sdk.NewCoins(sdk.NewCoin(gmChain.Config.Denom, sdkmath.NewInt(5_000_000_000)))
		fundingMsgs = append(fundingMsgs, banktypes.NewMsgSend(fromAddr, identity.OperatorAddress, coins))
	}
	for start := 0; start < len(fundingMsgs); start += 2 {
		end := start + 2
		if end > len(fundingMsgs) {
			end = len(fundingMsgs)
		}
		resp, err := gmChain.BroadcastMessages(ctx, gmChain.GetFaucetWallet(), fundingMsgs[start:end]...)
		require.NoError(s.T(), err, "failed to fund attester accounts")
		require.Zero(s.T(), resp.Code, "funding tx failed for attester accounts: %s", resp.RawLog)
	}
	s.T().Logf("funded %d attester accounts", len(identities))

	configured := make([]configuredAttester, 0, len(identities))
	for i, identity := range identities {
		attesterConfig := DefaultAttesterConfig()
		attesterConfig.PrivKeyArmor = identity.OperatorArmor
		attesterConfig.GMNodeURL = fmt.Sprintf("tcp://%s:26657", gmNodeInfo.Internal.Hostname)

		attesterNode, err := NewAttester(ctx, s.dockerClient, s.T().Name(), s.networkID, i, s.logger)
		require.NoError(s.T(), err)
		require.NoError(s.T(), attesterNode.WriteFile(
			ctx,
			"config/priv_validator_key.json",
			identity.PrivValidatorKeyJSON,
		))
		require.NoError(s.T(), attesterNode.WriteFile(
			ctx,
			"data/priv_validator_state.json",
			identity.PrivValidatorStateJSON,
		))

		configured = append(configured, configuredAttester{
			Config: attesterConfig,
			Node:   attesterNode,
		})
	}

	return configured
}

func (s *DockerIntegrationTestSuite) getGmChain(ctx context.Context, attesters []generatedAttesterIdentity) *cosmos.Chain {
	daAddress, authToken, _, err := s.getDANetworkParams(ctx)
	require.NoError(s.T(), err)

	s.T().Log("Creating GM chain connected to DA network...")
	sdk.GetConfig().SetBech32PrefixForAccount("celestia", "celestiapub")
	gmImg := container.NewImage("evabci/gm", "local", "1000:1000")
	testEncCfg := testutil.MakeTestEncodingConfig(auth.AppModuleBasic{}, bank.AppModuleBasic{}, ibctransfer.AppModuleBasic{})
	gmChain, err := cosmos.NewChainBuilder(s.T()).
		WithEncodingConfig(&testEncCfg).
		WithDockerClient(s.dockerClient).
		WithDockerNetworkID(s.networkID).
		WithName("gm").
		WithImage(gmImg).
		WithDenom("stake").
		WithBech32Prefix("celestia").
		WithChainID("gm").
		WithBinaryName("gmd").
		WithGasPrices(fmt.Sprintf("0.001%s", "stake")).
		WithAdditionalStartArgs(
			"--evnode.node.aggregator",
			"--evnode.signer.passphrase_file", fmt.Sprintf("/var/cosmos-chain/gm/%s", passphraseFile),
			"--evnode.da.address", daAddress,
			"--evnode.da.auth_token", authToken,
			"--evnode.rpc.address", "0.0.0.0:7331",
			"--evnode.da.namespace", "ev-header",
			"--evnode.da.data_namespace", "ev-data",
			"--evnode.p2p.listen_address", "/ip4/0.0.0.0/tcp/36656",
			"--rpc.laddr", "tcp://0.0.0.0:26657",
			"--evnode.attester-mode", "true",
			"--grpc.address", "0.0.0.0:9090",
			"--api.enable",
			"--minimum-gas-prices", "0.001stake",
			"--log_level", "*:info",
		).
		WithNode(cosmos.NewChainNodeConfigBuilder().
			WithPostInit(AddSingleSequencer, AddGenesisAttesters(attesters), writePasshraseFile("12345678")).
			Build()).
		Build(ctx)
	require.NoError(s.T(), err)

	return gmChain
}

func AddSingleSequencer(ctx context.Context, node *cosmos.ChainNode) error {
	genesisBz, err := node.ReadFile(ctx, "config/genesis.json")
	if err != nil {
		return fmt.Errorf("failed to read genesis.json: %w", err)
	}

	pubKey, err := getPubKey(ctx, node)
	if err != nil {
		return fmt.Errorf("failed to get pubkey: %w", err)
	}

	var genDoc map[string]interface{}
	if err := json.Unmarshal(genesisBz, &genDoc); err != nil {
		return fmt.Errorf("failed to parse genesis.json: %w", err)
	}

	consensus, ok := genDoc["consensus"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("genesis.json does not contain a valid 'consensus' object")
	}
	consensus["validators"] = []map[string]interface{}{
		{
			"name":    "Ev Node Sequencer",
			"address": pubKey.Address(),
			"pub_key": map[string]interface{}{
				"type":  "tendermint/PubKeyEd25519",
				"value": pubKey.Bytes(),
			},
			"power": "5", // NOTE: because of default validator wallet amount in tastora the power will be computed as 5.
		},
	}

	updatedGenesis, err := json.MarshalIndent(genDoc, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal genesis: %w", err)
	}
	return node.WriteFile(ctx, "config/genesis.json", updatedGenesis)
}

func generateAttesterIdentities(count int) ([]generatedAttesterIdentity, error) {
	testEncCfg := testutil.MakeTestEncodingConfig(auth.AppModuleBasic{}, bank.AppModuleBasic{}, ibctransfer.AppModuleBasic{})
	kr := keyring.NewInMemory(testEncCfg.Codec)

	identities := make([]generatedAttesterIdentity, 0, count)
	for i := range count {
		name := fmt.Sprintf("attester-%d", i)
		record, _, err := kr.NewMnemonic(name, keyring.English, sdk.FullFundraiserPath, keyring.DefaultBIP39Passphrase, hd.Secp256k1)
		if err != nil {
			return nil, fmt.Errorf("create operator key %d: %w", i, err)
		}
		operatorAddress, err := record.GetAddress()
		if err != nil {
			return nil, fmt.Errorf("get operator address %d: %w", i, err)
		}
		operatorArmor, err := kr.ExportPrivKeyArmor(name, "")
		if err != nil {
			return nil, fmt.Errorf("export operator key %d: %w", i, err)
		}

		consensusPrivKey := cmted25519.GenPrivKey()
		consensusPubKey := consensusPrivKey.PubKey().(cmted25519.PubKey)
		pv := pvm.NewFilePV(consensusPrivKey, "", "")
		privValidatorKeyJSON, err := cmtjson.MarshalIndent(pv.Key, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal priv validator key %d: %w", i, err)
		}
		privValidatorStateJSON, err := cmtjson.MarshalIndent(pvm.FilePVLastSignState{}, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal priv validator state %d: %w", i, err)
		}

		identities = append(identities, generatedAttesterIdentity{
			OperatorArmor:          operatorArmor,
			OperatorAddress:        operatorAddress,
			ConsensusAddress:       sdk.ConsAddress(consensusPubKey.Address()).String(),
			ConsensusPubKey:        consensusPubKey,
			PrivValidatorKeyJSON:   privValidatorKeyJSON,
			PrivValidatorStateJSON: privValidatorStateJSON,
		})
	}

	return identities, nil
}

// AddGenesisAttesters populates app_state.network.attester_infos with the fixed
// attester set used by the Docker e2e.
func AddGenesisAttesters(attesters []generatedAttesterIdentity) func(context.Context, *cosmos.ChainNode) error {
	return func(ctx context.Context, node *cosmos.ChainNode) error {
		genesisBz, err := node.ReadFile(ctx, "config/genesis.json")
		if err != nil {
			return fmt.Errorf("read genesis: %w", err)
		}
		updatedBz, err := setGenesisAttesters(genesisBz, attesters)
		if err != nil {
			return err
		}
		return node.WriteFile(ctx, "config/genesis.json", updatedBz)
	}
}

func setGenesisAttesters(genesisBz []byte, attesters []generatedAttesterIdentity) ([]byte, error) {
	var genDoc map[string]interface{}
	if err := json.Unmarshal(genesisBz, &genDoc); err != nil {
		return nil, fmt.Errorf("parse genesis: %w", err)
	}
	appState, ok := genDoc["app_state"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("genesis has no app_state object")
	}
	network, ok := appState["network"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("genesis has no app_state.network object")
	}

	attesterInfos := make([]interface{}, 0, len(attesters))
	for _, attester := range attesters {
		attesterInfos = append(attesterInfos, map[string]interface{}{
			"authority": attester.OperatorAddress.String(),
			"pubkey": map[string]interface{}{
				"@type": "/cosmos.crypto.ed25519.PubKey",
				"key":   base64.StdEncoding.EncodeToString(attester.ConsensusPubKey.Bytes()),
			},
			"joined_height":     0,
			"consensus_address": attester.ConsensusAddress,
		})
	}
	network["attester_infos"] = attesterInfos

	updatedBz, err := json.MarshalIndent(genDoc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal genesis: %w", err)
	}
	return updatedBz, nil
}

func validatorSetFromAttesters(attesters []generatedAttesterIdentity) *cmttypes.ValidatorSet {
	validators := make([]*cmttypes.Validator, 0, len(attesters))
	for _, attester := range attesters {
		validators = append(validators, cmttypes.NewValidator(attester.ConsensusPubKey, 1))
	}
	return cmttypes.NewValidatorSet(validators)
}

func countCommitSignatures(commit *cmttypes.Commit) int {
	count := 0
	for _, signature := range commit.Signatures {
		if signature.BlockIDFlag == cmttypes.BlockIDFlagCommit {
			count++
		}
	}
	return count
}

// setupIBCConnection establishes a complete IBC connection and channel
func setupIBCConnection(t *testing.T, ctx context.Context, chainA, chainB types.Chain, hermes *relayer.Hermes) (ibc.Connection, ibc.Channel) {
	err := hermes.CreateClients(ctx, chainA, chainB)
	require.NoError(t, err)

	connection, err := hermes.CreateConnections(ctx, chainA, chainB)
	require.NoError(t, err)
	require.NotEmpty(t, connection.ConnectionID, "Connection ID should not be empty")

	// give chains a moment to persist connection state and client updates
	err = wait.ForBlocks(ctx, 2, chainA, chainB)
	require.NoError(t, err)

	// Create an ICS20 channel for token transfers
	channelOpts := ibc.CreateChannelOptions{
		SourcePortName: "transfer",
		DestPortName:   "transfer",
		Order:          ibc.OrderUnordered,
		Version:        "ics20-1",
	}

	channel, err := hermes.CreateChannel(ctx, chainA, connection, channelOpts)
	require.NoError(t, err)
	require.NotNil(t, channel)
	require.NotEmpty(t, channel.ChannelID, "Channel ID should not be empty")

	t.Logf("Created IBC connection: %s <-> %s", connection.ConnectionID, connection.CounterpartyID)
	t.Logf("Created IBC channel: %s <-> %s", channel.ChannelID, channel.CounterpartyID)

	return connection, channel
}

// testIBCTransfers performs bidirectional IBC transfers and validates they succeed
func (s *DockerIntegrationTestSuite) testIBCTransfers(ctx context.Context, celestiaChain, gmChain *cosmos.Chain, channel ibc.Channel, hermes *relayer.Hermes) {
	transferAmount := sdkmath.NewInt(1_000_000)

	celestiaWallet := celestiaChain.GetFaucetWallet()
	gmWallet := gmChain.GetFaucetWallet()

	celestiaAddr, err := sdkacc.AddressFromWallet(celestiaWallet)
	require.NoError(s.T(), err)

	gmAddr, err := sdkacc.AddressFromWallet(gmWallet)
	require.NoError(s.T(), err)

	s.T().Logf("Celestia wallet address: %s", celestiaAddr.String())
	s.T().Logf("GM wallet address: %s", gmAddr.String())

	initialCelestiaNativeBalance := s.getBalance(ctx, celestiaChain, celestiaAddr, "utia")
	s.T().Logf("Initial Celestia native balance: %s utia", initialCelestiaNativeBalance.String())

	// Calculate IBC denom for GM chain receiving Celestia tokens
	celestiaToGMIBCDenom := s.calculateIBCDenom(channel.CounterpartyPort, channel.CounterpartyID, "utia")

	s.T().Log("Starting Hermes relayer...")
	err = hermes.Start(ctx)
	require.NoError(s.T(), err)

	// Allow Hermes to sync initial heights before sending packets
	err = wait.ForBlocks(ctx, 2, celestiaChain, gmChain)
	require.NoError(s.T(), err)

	// Test 1: Transfer from Celestia to GM chain
	s.T().Log("=== Testing transfer from Celestia to GM chain ===")

	// Get initial balance
	initialGMBalance := s.getBalance(ctx, gmChain, gmAddr, celestiaToGMIBCDenom)
	s.T().Logf("Initial GM IBC balance: %s %s", initialGMBalance.String(), celestiaToGMIBCDenom)

	// Perform transfer
	transferMsg := transfertypes.NewMsgTransfer(
		channel.PortID,
		channel.ChannelID,
		sdk.NewCoin("utia", transferAmount),
		celestiaWallet.GetFormattedAddress(),
		gmAddr.String(),
		clienttypes.ZeroHeight(),
		uint64(time.Now().Add(time.Hour).UnixNano()),
		"",
	)

	// Use a longer per-tx timeout to avoid 60s default aborts on busy or lagging nodes
	ctxTx, cancelTx := context.WithTimeout(ctx, 2*time.Minute)
	defer cancelTx()
	resp, err := celestiaChain.BroadcastMessages(ctxTx, celestiaWallet, transferMsg)

	require.NoError(s.T(), err)
	require.Equal(s.T(), uint32(0), resp.Code, "IBC transfer failed: %s", resp.RawLog)

	s.T().Logf("IBC transfer broadcast successful. TX hash: %s", resp.TxHash)

	// Wait until GM balance reflects the transfer (poll with timeout)
	s.T().Log("Waiting for GM balance to update...")
	require.NoError(s.T(), s.waitForBalanceIncrease(ctx, gmChain, gmAddr, celestiaToGMIBCDenom, initialGMBalance, transferAmount, 2*time.Minute))

	// Check final balance
	finalGMBalance := s.getBalance(ctx, gmChain, gmAddr, celestiaToGMIBCDenom)
	s.T().Logf("Final GM IBC balance: %s %s", finalGMBalance.String(), celestiaToGMIBCDenom)

	// Verify transfer succeeded
	expectedBalance := initialGMBalance.Add(transferAmount)
	require.True(s.T(), finalGMBalance.Equal(expectedBalance),
		"GM balance mismatch: expected %s, got %s", expectedBalance.String(), finalGMBalance.String())

	postInboundCelestiaNativeBalance := s.getBalance(ctx, celestiaChain, celestiaAddr, "utia")
	s.T().Logf("Celestia native balance after outbound transfer: %s utia", postInboundCelestiaNativeBalance.String())

	// Test 2: Return Celestia-originated tokens back to Celestia
	s.T().Log("=== Returning Celestia-originated tokens to Celestia ===")

	returnTransferMsg := transfertypes.NewMsgTransfer(
		channel.CounterpartyPort,
		channel.CounterpartyID,
		sdk.NewCoin(celestiaToGMIBCDenom, transferAmount),
		gmWallet.GetFormattedAddress(),
		celestiaAddr.String(),
		clienttypes.ZeroHeight(),
		uint64(time.Now().Add(time.Hour).UnixNano()),
		"",
	)

	ctxTxReturn, cancelTxReturn := context.WithTimeout(ctx, 2*time.Minute)
	defer cancelTxReturn()
	resp, err = gmChain.BroadcastMessages(ctxTxReturn, gmWallet, returnTransferMsg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), uint32(0), resp.Code, "Return IBC transfer failed: %s", resp.RawLog)

	s.T().Logf("Return IBC transfer broadcast successful. TX hash: %s", resp.TxHash)

	s.T().Log("Waiting for Celestia native balance to restore...")
	require.NoError(s.T(), s.waitForBalanceIncrease(ctx, celestiaChain, celestiaAddr, "utia", postInboundCelestiaNativeBalance, transferAmount, 2*time.Minute))

	restoredGMBalance := s.getBalance(ctx, gmChain, gmAddr, celestiaToGMIBCDenom)
	s.T().Logf("GM IBC balance after returning tokens: %s %s", restoredGMBalance.String(), celestiaToGMIBCDenom)
	require.True(s.T(), restoredGMBalance.Equal(initialGMBalance),
		"GM balance mismatch after returning tokens: expected %s, got %s", initialGMBalance.String(), restoredGMBalance.String())

	finalCelestiaNativeBalance := s.getBalance(ctx, celestiaChain, celestiaAddr, "utia")
	s.T().Logf("Final Celestia native balance: %s utia", finalCelestiaNativeBalance.String())
	expectedReturnBalance := postInboundCelestiaNativeBalance.Add(transferAmount)
	require.True(s.T(), finalCelestiaNativeBalance.Equal(expectedReturnBalance),
		"Celestia native balance mismatch after return: expected %s, got %s",
		expectedReturnBalance.String(), finalCelestiaNativeBalance.String())

	s.T().Log("=== IBC Transfer Tests Completed Successfully ===")
}

// waitForBalanceIncrease polls the balance until it increases by expectedIncrease or timeout expires.
func (s *DockerIntegrationTestSuite) waitForBalanceIncrease(ctx context.Context, chain *cosmos.Chain, address sdk.AccAddress, denom string, initial sdkmath.Int, expectedIncrease sdkmath.Int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	target := initial.Add(expectedIncrease)
	for {
		current := s.getBalance(ctx, chain, address, denom)
		if current.GTE(target) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("balance did not reach target within %s: got %s, want %s (%s)", timeout, current.String(), target.String(), denom)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
}

// getBalance queries the balance of an address for a specific denom
func (s *DockerIntegrationTestSuite) getBalance(ctx context.Context, chain *cosmos.Chain, address sdk.AccAddress, denom string) sdkmath.Int {
	node := chain.GetNode()
	amount, err := query.Balance(ctx, node.GrpcConn, address.String(), denom)
	if err != nil {
		s.T().Logf("Failed to query balance for %s denom %s: %v", address.String(), denom, err)
		return sdkmath.ZeroInt()
	}
	return amount
}

// calculateIBCDenom calculates the IBC denomination for a token transferred over IBC
func (s *DockerIntegrationTestSuite) calculateIBCDenom(portID, channelID, baseDenom string) string {
	prefixedDenom := transfertypes.GetPrefixedDenom(
		portID,
		channelID,
		baseDenom,
	)
	return transfertypes.ParseDenomTrace(prefixedDenom).IBCDenom()
}
