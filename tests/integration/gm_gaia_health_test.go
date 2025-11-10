package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	"github.com/BurntSushi/toml"
	"github.com/celestiaorg/tastora/framework/docker/container"
	"github.com/celestiaorg/tastora/framework/docker/cosmos"
	"github.com/celestiaorg/tastora/framework/docker/ibc"
	"github.com/celestiaorg/tastora/framework/docker/ibc/relayer"
	"github.com/celestiaorg/tastora/framework/testutil/config"
	"github.com/celestiaorg/tastora/framework/testutil/query"
	"github.com/celestiaorg/tastora/framework/testutil/sdkacc"
	"github.com/celestiaorg/tastora/framework/testutil/wait"
	"github.com/celestiaorg/tastora/framework/types"
	cometcfg "github.com/cometbft/cometbft/config"
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
	s.T().Log("Waiting for GM chain RPC to be ready...")
	var rpcReady bool
	for i := 0; i < 30; i++ { // 30 second timeout
		node := gmChain.GetNodes()[0]
		rpcClient, _ := node.GetRPCClient()
		if rpcClient != nil {
			// Test if RPC client is actually working by making a simple call
			_, err := rpcClient.Status(ctx)
			if err == nil {
				rpcReady = true
				s.T().Log("GM chain RPC is ready")
				break
			}
		}
		time.Sleep(1 * time.Second)
	}
	require.True(s.T(), rpcReady, "GM chain RPC failed to become ready within 30 seconds")

	kr, err := gmChain.GetNodes()[0].GetKeyring()
	require.NoError(s.T(), err)

	// List available keys
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

	// Extract validator key if found
	var validatorArmoredKey string
	if validatorKey != nil {
		s.T().Logf("Extracting validator key...")

		// Export private key in armor format (no passphrase for test keyring)
		armoredPrivKey, err := kr.ExportPrivKeyArmor("validator", "")
		require.NoError(s.T(), err, "failed to export validator private key")

		s.T().Logf("Validator private key exported successfully (armor format)")
		previewLen := 100
		if len(armoredPrivKey) < previewLen {
			previewLen = len(armoredPrivKey)
		}
		s.T().Logf("Armored key preview: %s...", armoredPrivKey[:previewLen])
		validatorArmoredKey = armoredPrivKey
	} else {
		s.T().Log("No validator key found in keyring")
	}

	attesterConfig, attesterNode := s.getAttester(ctx, gmChain, validatorArmoredKey)

	s.T().Logf("Starting attester node %s", attesterNode.Name())
	err = attesterNode.Start(ctx, attesterConfig)
	require.NoError(s.T(), err)
	s.T().Log("Attester node started successfully")

	hermes, err := relayer.NewHermes(ctx, s.dockerClient, s.T().Name(), s.networkID, 0, s.logger)
	require.NoError(s.T(), err, "failed to create hermes relayer")

	err = hermes.Init(ctx, []types.Chain{s.celestiaChain, gmChain})
	require.NoError(s.T(), err, "failed to initialize relayer")

	// Switch hermes to pull mode to avoid WebSocket connection issues
	err = s.configureHermesForPullMode(ctx, hermes)
	require.NoError(s.T(), err, "failed to configure hermes for pull mode")

	connection, channel := setupIBCConnection(s.T(), ctx, s.celestiaChain, gmChain, hermes)
	s.T().Logf("Established IBC connection %s and channel %s between Celestia and GM chain", connection.ConnectionID, channel.ChannelID)

	// Test IBC transfers
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

func (s *DockerIntegrationTestSuite) configureHermesForPullMode(ctx context.Context, hermes *relayer.Hermes) error {
	s.T().Log("Configuring hermes to use pull mode and increased clock drift...")

	// Read the current config
	configBz, err := hermes.ReadFile(ctx, ".hermes/config.toml")
	if err != nil {
		return fmt.Errorf("failed to read hermes config: %w", err)
	}

	// Unmarshal into map
	var c map[string]interface{}
	if err := toml.Unmarshal(configBz, &c); err != nil {
		return fmt.Errorf("failed to unmarshal hermes config: %w", err)
	}

	// Update chains to use pull mode and increase clock drift
	chains, ok := c["chains"].([]map[string]interface{})
	if !ok {
		return fmt.Errorf("chains not found in config or not in expected format")
	}

	for i := range chains {
		// Update event_source to pull mode with tighter interval
		chains[i]["event_source"] = map[string]interface{}{
			"mode":     "pull",
			"interval": "200ms",
		}

		// Update clock_drift to handle timing differences between chains
		chains[i]["clock_drift"] = "60s"
	}

	// Remarshal into bytes
	updatedConfigBz, err := toml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal updated hermes config: %w", err)
	}

	// Write the updated config
	err = hermes.WriteFile(ctx, ".hermes/config.toml", updatedConfigBz)
	if err != nil {
		return fmt.Errorf("failed to write updated hermes config: %w", err)
	}

	s.T().Log("Successfully configured hermes to use pull mode with 60s clock drift tolerance")
	return nil
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
		WithImage(gmImg).
		WithDenom("stake").
		WithBech32Prefix("celestia").
		WithChainID("gm").
		WithBinaryName("gmd").
		WithGasPrices(fmt.Sprintf("0.001%s", "stake")).
		WithAdditionalStartArgs(
			"--evnode.node.aggregator",
			"--evnode.signer.passphrase", "12345678",
			"--evnode.da.address", daAddress,
			"--evnode.da.gas_price", "0.000001",
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
		WithPostInit(func(ctx context.Context, node *cosmos.ChainNode) error {
			// 1) Ensure ABCI responses and tx events are retained and indexed for Hermes
			if err := config.Modify(ctx, node, "config/config.toml", func(cfg *cometcfg.Config) {
				cfg.Storage.DiscardABCIResponses = false
				// Enable key-value tx indexer so Hermes can query IBC packet events
				cfg.TxIndex.Indexer = "kv"
				// Increase RPC BroadcastTxCommit timeout to accommodate CI slowness
				if cfg.RPC != nil {
					cfg.RPC.TimeoutBroadcastTxCommit = 120000000000 // 120s in nanoseconds
				}
			}); err != nil {
				return err
			}
			// 2) Ensure app-level index-events include IBC packet events
			appToml, err := node.ReadFile(ctx, "config/app.toml")
			if err != nil {
				return err
			}
			var appCfg map[string]interface{}
			if err := toml.Unmarshal(appToml, &appCfg); err != nil {
				return err
			}
			appCfg["index-events"] = []string{
				"message.action",
				"send_packet",
				"recv_packet",
				"write_acknowledgement",
				"acknowledge_packet",
				"timeout_packet",
			}
			updated, err := toml.Marshal(appCfg)
			if err != nil {
				return err
			}
			return node.WriteFile(ctx, "config/app.toml", updated)
		}).
		WithNode(cosmos.NewChainNodeConfigBuilder().
			WithPostInit(AddSingleSequencer).
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

// setupIBCConnection establishes a complete IBC connection and channel
func setupIBCConnection(t *testing.T, ctx context.Context, chainA, chainB types.Chain, hermes *relayer.Hermes) (ibc.Connection, ibc.Channel) {
	// create clients
	err := hermes.CreateClients(ctx, chainA, chainB)
	require.NoError(t, err)

	// create connections
	connection, err := hermes.CreateConnections(ctx, chainA, chainB)
	require.NoError(t, err)
	require.NotEmpty(t, connection.ConnectionID, "Connection ID should not be empty")

	// give chains a moment to persist connection state and client updates
	_ = wait.ForBlocks(ctx, 2, chainA, chainB)

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
	// Transfer amount
	transferAmount := sdkmath.NewInt(1_000_000)

	// Get wallets for both chains
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

	// Start Hermes relayer
	s.T().Log("Starting Hermes relayer...")
	err = hermes.Start(ctx)
	require.NoError(s.T(), err)

	// Allow Hermes to sync initial heights before sending packets
	_ = wait.ForBlocks(ctx, 2, celestiaChain, gmChain)

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
