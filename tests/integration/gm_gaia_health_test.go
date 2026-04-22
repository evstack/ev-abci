package integration_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
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
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module/testutil"
	"github.com/cosmos/cosmos-sdk/x/auth"
	"github.com/cosmos/cosmos-sdk/x/bank"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/ibc-go/v8/modules/apps/transfer"
	ibctransfer "github.com/cosmos/ibc-go/v8/modules/apps/transfer"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	"github.com/stretchr/testify/require"
)

// TestAttesterSystem is an empty test case using the DockerIntegrationTestSuite
func (s *DockerIntegrationTestSuite) TestAttesterSystem() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gmChain := s.getGmChain(ctx)

	// Start GM chain in a goroutine
	go func() {
		s.T().Log("Starting GM chain...")
		err := gmChain.Start(ctx)
		if err != nil {
			s.T().Errorf("Failed to start GM chain: %v", err)
		}
	}()

	// Wait for GM chain RPC to be ready
	err := wait.ForCondition(ctx, time.Second*30, time.Second, func() (bool, error) {
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

	kr, err := gmChain.GetNodes()[0].GetKeyring()
	require.NoError(s.T(), err)

	keys, err := kr.List()
	require.NoError(s.T(), err)
	s.T().Logf("Available keys in keyring: %d", len(keys))

	// Log all available keys and find validator key
	var validatorKey *keyring.Record
	for i, key := range keys {
		keyAddr, _ := key.GetAddress()
		s.T().Logf("Key %d: Name=%s, Address=%s", i, key.Name, keyAddr.String())

		if key.Name == "validator" {
			validatorKey = key
		}
	}

	s.Require().NotNil(validatorKey, "validator key not found in keyring")

	validatorArmoredKey, err := kr.ExportPrivKeyArmor("validator", "")
	s.Require().NoError(err, "failed to export validator private key")

	attesterConfig, attesterNode := s.getAttester(ctx, gmChain, validatorArmoredKey)

	s.T().Logf("Initializing attester node %s", attesterNode.Name())
	err = attesterNode.Init(ctx, attesterConfig.ChainID, attesterConfig.GMNodeURL)
	require.NoError(s.T(), err)

	s.T().Logf("Starting attester node %s", attesterNode.Name())
	err = attesterNode.Start(ctx, attesterConfig)
	require.NoError(s.T(), err)
	s.T().Log("Attester node started successfully")

	// Wait for the attester to attest some blocks and LastAttestedHeight to advance.
	s.T().Log("Waiting for attestations to reach quorum...")
	var targetHeight int64 = 10
	err = wait.ForCondition(ctx, 2*time.Minute, 2*time.Second, func() (bool, error) {
		node := gmChain.GetNodes()[0]
		rpcClient, _ := node.GetRPCClient()
		if rpcClient == nil {
			return false, nil
		}
		status, statusErr := rpcClient.Status(ctx)
		if statusErr != nil {
			return false, nil
		}
		return status.SyncInfo.LatestBlockHeight >= targetHeight, nil
	})
	s.Require().NoError(err, "chain did not reach target height %d", targetHeight)

	// Fetch /commit for the target height and assert VerifyCommitLight passes.
	{
		node := gmChain.GetNodes()[0]
		rpcClient, err := node.GetRPCClient()
		s.Require().NoError(err)
		commitResp, err := rpcClient.Commit(ctx, &targetHeight)
		s.Require().NoError(err, "fetch commit at height %d", targetHeight)

		privValJSONBz, err := node.ReadFile(ctx, "config/priv_validator_key.json")
		s.Require().NoError(err)
		var pv struct {
			PubKey struct {
				Type  string `json:"type"`
				Value string `json:"value"`
			} `json:"pub_key"`
		}
		s.Require().NoError(json.Unmarshal(privValJSONBz, &pv))
		pkBytes, err := base64.StdEncoding.DecodeString(pv.PubKey.Value)
		s.Require().NoError(err)
		cmtPub := cmted25519.PubKey(pkBytes)
		valSet := cmttypes.NewValidatorSet([]*cmttypes.Validator{cmttypes.NewValidator(cmtPub, 1)})

		commit := commitResp.SignedHeader.Commit
		s.Require().NoError(
			valSet.VerifyCommitLight("gm", commit.BlockID, targetHeight, commit),
			"reconstructed commit must pass 07-tendermint light-client verification",
		)
		s.T().Logf("commit at height %d passes VerifyCommitLight with %d signatures", targetHeight, len(commit.Signatures))
	}

	hermes, err := relayer.NewHermes(ctx, s.dockerClient, s.T().Name(), s.networkID, 0, s.logger)
	require.NoError(s.T(), err, "failed to create hermes relayer")

	err = hermes.Init(ctx, []types.Chain{s.celestiaChain, gmChain}, func(cfg *relayer.HermesConfig) {
		for i := range cfg.Chains {
			// switch hermes to pull mode to avoid WebSocket connection issues
			cfg.Chains[i].EventSource = map[string]interface{}{
				"mode":     "pull",
				"interval": "200ms",
			}
			cfg.Chains[i].ClockDrift = "60s"
		}
	})
	require.NoError(s.T(), err, "failed to initialize relayer")

	connection, channel := setupIBCConnection(s.T(), ctx, s.celestiaChain, gmChain, hermes)
	s.T().Logf("Established IBC connection %s and channel %s between Celestia and GM chain", connection.ConnectionID, channel.ChannelID)

	s.testIBCTransfers(ctx, s.celestiaChain, gmChain, channel, hermes)
}

func (s *DockerIntegrationTestSuite) getAttester(ctx context.Context, gmChain *cosmos.Chain, validatorArmoredKey string) (AttesterConfig, *Attester) {
	// Create attester configuration
	attesterConfig := DefaultAttesterConfig()

	// Set armored key (required)
	require.NotEmpty(s.T(), validatorArmoredKey, "validator armored key is required")
	attesterConfig.PrivKeyArmor = validatorArmoredKey

	// Get the internal network addresses for the GM chain
	gmNodes := gmChain.GetNodes()
	require.NotEmpty(s.T(), gmNodes, "no GM chain nodes available")

	gmNode := gmNodes[0]
	gmNodeInfo, err := gmNode.GetNetworkInfo(ctx)
	require.NoError(s.T(), err)

	privValidatorKeyJSON, err := gmNode.ReadFile(ctx, "config/priv_validator_key.json")
	require.NoError(s.T(), err, "unable to read priv_validator_key.json from GM node")

	privValidatorStateJSON, err := gmNode.ReadFile(ctx, "data/priv_validator_state.json")
	require.NoError(s.T(), err, "unable to read priv_validator_state.json from GM node")

	// Derive attester account address from armored key
	attesterAccAddr, err := deriveAttesterAccountFromArmor(attesterConfig.PrivKeyArmor)
	require.NoError(s.T(), err, "failed to derive attester account address from armored key")

	fromAddr, err := sdkacc.AddressFromWallet(gmChain.GetFaucetWallet())
	require.NoError(s.T(), err, "failed to retrieve faucet address")
	coins := sdk.NewCoins(sdk.NewCoin(gmChain.Config.Denom, sdkmath.NewInt(5_000_000_000)))
	fundingMsg := banktypes.NewMsgSend(fromAddr, attesterAccAddr, coins)
	resp, err := gmChain.BroadcastMessages(ctx, gmChain.GetFaucetWallet(), fundingMsg)
	require.NoError(s.T(), err, "failed to fund attester account")
	require.Zero(s.T(), resp.Code, "funding tx failed: %s", resp.RawLog)
	s.T().Logf("funded attester account %s with %s", attesterAccAddr.String(), coins)

	// Use internal addresses for communication within docker network
	attesterConfig.GMNodeURL = fmt.Sprintf("tcp://%s:26657", gmNodeInfo.Internal.Hostname)

	// Create and start the attester
	attesterNode, err := NewAttester(ctx, s.dockerClient, s.T().Name(), s.networkID, 0, s.logger)
	require.NoError(s.T(), err)
	require.NoError(s.T(), attesterNode.WriteFile(
		ctx,
		"config/priv_validator_key.json",
		privValidatorKeyJSON,
	))
	require.NoError(s.T(), attesterNode.WriteFile(
		ctx,
		"data/priv_validator_state.json",
		privValidatorStateJSON,
	))

	// Verify validator key can be imported (demonstration)
	s.T().Log("Setting up attester keyring with validator key...")

	// Create an in-memory keyring for the attester
	// Include transfer module so MsgTransfer is registered in the interface registry
	testEncCfg := testutil.MakeTestEncodingConfig(auth.AppModuleBasic{}, bank.AppModuleBasic{}, ibctransfer.AppModuleBasic{})
	attesterKeyring := keyring.NewInMemory(testEncCfg.Codec)

	// Import the validator key into the attester keyring
	err = attesterKeyring.ImportPrivKey("validator", validatorArmoredKey, "")
	require.NoError(s.T(), err, "failed to import validator key into attester keyring")

	s.T().Log("Validator key imported successfully into attester keyring")

	// List keys in attester keyring to verify
	attesterKeys, err := attesterKeyring.List()
	require.NoError(s.T(), err)
	s.T().Logf("Attester keyring now has %d keys", len(attesterKeys))

	for i, key := range attesterKeys {
		keyAddr, _ := key.GetAddress()
		s.T().Logf("Attester Key %d: Name=%s, Address=%s", i, key.Name, keyAddr.String())
	}

	return attesterConfig, attesterNode
}

func deriveAttesterAccountFromArmor(armoredKey string) (sdk.AccAddress, error) {
	// Create a temporary in-memory keyring for importing
	testEncCfg := testutil.MakeTestEncodingConfig(auth.AppModuleBasic{}, bank.AppModuleBasic{})
	kr := keyring.NewInMemory(testEncCfg.Codec)

	// Import the armored key into the temporary keyring
	err := kr.ImportPrivKey("temp", armoredKey, "")
	if err != nil {
		return nil, fmt.Errorf("failed to import armored private key: %w", err)
	}

	// Get the key record
	keyRecord, err := kr.Key("temp")
	if err != nil {
		return nil, fmt.Errorf("failed to get imported key: %w", err)
	}

	// Get the address from the key record
	keyAddr, err := keyRecord.GetAddress()
	if err != nil {
		return nil, fmt.Errorf("failed to get address from key: %w", err)
	}

	return keyAddr, nil
}

func (s *DockerIntegrationTestSuite) getGmChain(ctx context.Context) *cosmos.Chain {
	daAddress, authToken, _, err := s.getDANetworkParams(ctx)
	require.NoError(s.T(), err)

	s.T().Log("Creating GM chain connected to DA network...")
	sdk.GetConfig().SetBech32PrefixForAccount("celestia", "celestiapub")
	gmImg := container.NewImage("evabci/gm", "local", "1000:1000")
	testEncCfg := testutil.MakeTestEncodingConfig(auth.AppModuleBasic{}, bank.AppModuleBasic{}, transfer.AppModuleBasic{})
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
			WithPostInit(AddSingleSequencer, AddGenesisAttester, writePasshraseFile("12345678")).
			Build()).
		Build(ctx)
	require.NoError(s.T(), err)

	return gmChain
}

// AddSingleSequencer modifies the genesis file to add a single sequencer with specified power and public key.
// Reads the genesis file from the node, updates the validators with the sequencer info, and writes the updated file back.
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

// AddGenesisAttester populates app_state.network.attester_infos with a single
// attester entry derived from the node's priv_validator_key.json and the
// operator address of the "validator" keyring entry.
func AddGenesisAttester(ctx context.Context, node *cosmos.ChainNode) error {
	genesisBz, err := node.ReadFile(ctx, "config/genesis.json")
	if err != nil {
		return fmt.Errorf("read genesis: %w", err)
	}

	pubKey, err := getPubKey(ctx, node)
	if err != nil {
		return fmt.Errorf("get consensus pubkey: %w", err)
	}

	// Consensus address (cosmosvalcons1... derived from ed25519 Address())
	consensusAddress := sdk.ConsAddress(pubKey.Address()).String()

	// Operator address: run `gmd keys show validator -a` inside the node container.
	stdout, stderr, err := node.Exec(ctx, []string{
		node.BinaryName,
		"keys", "show", "validator", "-a",
		"--keyring-backend", "test",
		"--home", node.HomeDir(),
	}, nil)
	if err != nil {
		return fmt.Errorf("query validator operator address (stderr=%q): %w", string(stderr), err)
	}
	authority := strings.TrimSpace(string(stdout))
	if authority == "" {
		return fmt.Errorf("empty operator address for validator keyring entry")
	}

	attesterInfo := map[string]interface{}{
		"authority": authority,
		"pubkey": map[string]interface{}{
			"@type": "/cosmos.crypto.ed25519.PubKey",
			"key":   base64.StdEncoding.EncodeToString(pubKey.Bytes()),
		},
		"joined_height":     0,
		"consensus_address": consensusAddress,
	}

	var genDoc map[string]interface{}
	if err := json.Unmarshal(genesisBz, &genDoc); err != nil {
		return fmt.Errorf("parse genesis: %w", err)
	}
	appState, ok := genDoc["app_state"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("genesis has no app_state object")
	}
	network, ok := appState["network"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("genesis has no app_state.network object")
	}
	network["attester_infos"] = []interface{}{attesterInfo}

	updatedBz, err := json.MarshalIndent(genDoc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal genesis: %w", err)
	}
	return node.WriteFile(ctx, "config/genesis.json", updatedBz)
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

	// Test 3: Transfer GM-native "stake" from GM chain to Celestia. This is the
	// symmetric case where GM is the source: Celestia's 07-tendermint light
	// client must accept the attester-produced commit to verify the packet.
	s.T().Log("=== Testing transfer from GM chain to Celestia (GM-native) ===")

	gmNativeDenom := gmChain.Config.Denom
	gmToCelestiaIBCDenom := s.calculateIBCDenom(channel.PortID, channel.ChannelID, gmNativeDenom)

	initialGMStakeBalance := s.getBalance(ctx, gmChain, gmAddr, gmNativeDenom)
	initialCelestiaIBCBalance := s.getBalance(ctx, celestiaChain, celestiaAddr, gmToCelestiaIBCDenom)
	s.T().Logf("Initial GM stake balance: %s %s", initialGMStakeBalance.String(), gmNativeDenom)
	s.T().Logf("Initial Celestia IBC balance: %s %s", initialCelestiaIBCBalance.String(), gmToCelestiaIBCDenom)

	outboundMsg := transfertypes.NewMsgTransfer(
		channel.CounterpartyPort, // GM-side port
		channel.CounterpartyID,   // GM-side channel
		sdk.NewCoin(gmNativeDenom, transferAmount),
		gmWallet.GetFormattedAddress(),
		celestiaAddr.String(),
		clienttypes.ZeroHeight(),
		uint64(time.Now().Add(time.Hour).UnixNano()),
		"",
	)

	ctxTx3, cancelTx3 := context.WithTimeout(ctx, 2*time.Minute)
	defer cancelTx3()
	resp, err = gmChain.BroadcastMessages(ctxTx3, gmWallet, outboundMsg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), uint32(0), resp.Code, "Outbound GM→Celestia transfer failed: %s", resp.RawLog)

	s.T().Logf("Outbound GM→Celestia transfer broadcast successful. TX hash: %s", resp.TxHash)

	s.T().Log("Waiting for Celestia IBC balance to reflect GM-native inbound...")
	require.NoError(s.T(), s.waitForBalanceIncrease(ctx, celestiaChain, celestiaAddr, gmToCelestiaIBCDenom, initialCelestiaIBCBalance, transferAmount, 2*time.Minute))

	finalCelestiaIBCBalance := s.getBalance(ctx, celestiaChain, celestiaAddr, gmToCelestiaIBCDenom)
	s.T().Logf("Final Celestia IBC balance: %s %s", finalCelestiaIBCBalance.String(), gmToCelestiaIBCDenom)
	expectedCelestiaIBCBalance := initialCelestiaIBCBalance.Add(transferAmount)
	require.True(s.T(), finalCelestiaIBCBalance.Equal(expectedCelestiaIBCBalance),
		"Celestia IBC balance mismatch: expected %s, got %s", expectedCelestiaIBCBalance.String(), finalCelestiaIBCBalance.String())

	postOutboundGMStakeBalance := s.getBalance(ctx, gmChain, gmAddr, gmNativeDenom)
	s.T().Logf("GM stake balance after outbound transfer: %s %s", postOutboundGMStakeBalance.String(), gmNativeDenom)

	// Test 4: Return GM-originated tokens back to GM chain.
	s.T().Log("=== Returning GM-originated tokens to GM chain ===")

	returnOutboundMsg := transfertypes.NewMsgTransfer(
		channel.PortID,    // Celestia-side port
		channel.ChannelID, // Celestia-side channel
		sdk.NewCoin(gmToCelestiaIBCDenom, transferAmount),
		celestiaWallet.GetFormattedAddress(),
		gmAddr.String(),
		clienttypes.ZeroHeight(),
		uint64(time.Now().Add(time.Hour).UnixNano()),
		"",
	)

	ctxTx4, cancelTx4 := context.WithTimeout(ctx, 2*time.Minute)
	defer cancelTx4()
	resp, err = celestiaChain.BroadcastMessages(ctxTx4, celestiaWallet, returnOutboundMsg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), uint32(0), resp.Code, "Return Celestia→GM transfer failed: %s", resp.RawLog)

	s.T().Logf("Return Celestia→GM transfer broadcast successful. TX hash: %s", resp.TxHash)

	s.T().Log("Waiting for GM stake balance to restore...")
	require.NoError(s.T(), s.waitForBalanceIncrease(ctx, gmChain, gmAddr, gmNativeDenom, postOutboundGMStakeBalance, transferAmount, 2*time.Minute))

	restoredCelestiaIBCBalance := s.getBalance(ctx, celestiaChain, celestiaAddr, gmToCelestiaIBCDenom)
	s.T().Logf("Celestia IBC balance after returning tokens: %s %s", restoredCelestiaIBCBalance.String(), gmToCelestiaIBCDenom)
	require.True(s.T(), restoredCelestiaIBCBalance.Equal(initialCelestiaIBCBalance),
		"Celestia IBC balance mismatch after return: expected %s, got %s", initialCelestiaIBCBalance.String(), restoredCelestiaIBCBalance.String())

	finalGMStakeBalance := s.getBalance(ctx, gmChain, gmAddr, gmNativeDenom)
	s.T().Logf("Final GM stake balance: %s %s", finalGMStakeBalance.String(), gmNativeDenom)
	expectedFinalGMStakeBalance := postOutboundGMStakeBalance.Add(transferAmount)
	require.True(s.T(), finalGMStakeBalance.Equal(expectedFinalGMStakeBalance),
		"GM stake balance mismatch after return: expected %s, got %s",
		expectedFinalGMStakeBalance.String(), finalGMStakeBalance.String())

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
