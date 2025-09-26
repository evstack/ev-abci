package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	"github.com/celestiaorg/tastora/framework/docker/container"
	"github.com/celestiaorg/tastora/framework/docker/cosmos"
	"github.com/celestiaorg/tastora/framework/docker/ibc"
	"github.com/celestiaorg/tastora/framework/docker/ibc/relayer"
	"github.com/celestiaorg/tastora/framework/testutil/sdkacc"
	"github.com/celestiaorg/tastora/framework/types"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module/testutil"
	"github.com/cosmos/cosmos-sdk/x/auth"
	"github.com/cosmos/cosmos-sdk/x/bank"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"
)

// TestAttesterSystem is an empty test case using the DockerIntegrationTestSuite
func (s *DockerIntegrationTestSuite) TestAttesterSystem() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	//s.buildGMImage()
	//s.buildAttesterImage()

	gmChain := s.getGmChain(ctx)

	// Start GM chain in a goroutine
	go func() {
		s.T().Log("Starting GM chain...")
		err := gmChain.Start(ctx)
		if err != nil {
			s.T().Errorf("Failed to start GM chain: %v", err)
		}
	}()

	// Wait 10 seconds for GM chain to initialize
	time.Sleep(10 * time.Second)

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

	err = hermes.Init(ctx, s.celestiaChain, gmChain)
	require.NoError(s.T(), err, "failed to initialize relayer")

	connection, channel := setupIBCConnection(s.T(), ctx, s.celestiaChain, gmChain, hermes)
	s.T().Logf("Established IBC connection %s and channel %s between Celestia and GM chain", connection.ConnectionID, channel.ChannelID)
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
	attesterConfig.GMNodeURL = fmt.Sprintf("http://%s:26657", gmNodeInfo.Internal.Hostname)
	attesterConfig.GMAPIUrl = fmt.Sprintf("http://%s:1417", gmNodeInfo.Internal.Hostname)

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
	testEncCfg := testutil.MakeTestEncodingConfig(auth.AppModuleBasic{}, bank.AppModuleBasic{})
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

func (s *DockerIntegrationTestSuite) buildGMImage() {
	evnodeVersion := getenvDefault("EVNODE_VERSION", "v1.0.0-beta.4")
	igniteVersion := getenvDefault("IGNITE_VERSION", "v29.3.1")
	igniteEvolveApp := getenvDefault("IGNITE_EVOLVE_APP_VERSION", "main")

	dockerBuild(s.T(), projectRoot(s.T()),
		filepath.Join(projectRoot(s.T()), "./tests/integration/docker/Dockerfile.gm"),
		"evabci/gm:local",
		map[string]string{
			"IGNITE_VERSION":            igniteVersion,
			"IGNITE_EVOLVE_APP_VERSION": igniteEvolveApp,
			"EVNODE_VERSION":            evnodeVersion,
		},
	)
}

func (s *DockerIntegrationTestSuite) buildAttesterImage() {
	dockerBuild(s.T(), projectRoot(s.T()),
		filepath.Join(projectRoot(s.T()), "./tests/integration/docker/Dockerfile.attester"),
		"evabci/attester:local",
		map[string]string{},
	)
}

func (s *DockerIntegrationTestSuite) getGmChain(ctx context.Context) *cosmos.Chain {
	daAddress, authToken, daStartHeight, err := s.getDANetworkParams(ctx)
	require.NoError(s.T(), err)

	s.T().Log("Creating GM chain connected to DA network...")
	sdk.GetConfig().SetBech32PrefixForAccount("celestia", "celestiapub")
	gmImg := container.NewImage("evabci/gm", "local", "1000:1000")
	testEncCfg := testutil.MakeTestEncodingConfig(auth.AppModuleBasic{}, bank.AppModuleBasic{})
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
			"--evnode.da.start_height", daStartHeight,
			"--evnode.p2p.listen_address", "/ip4/0.0.0.0/tcp/36656",
			"--rpc.laddr", "tcp://0.0.0.0:26657",
			"--evnode.attester-mode", "true",
			"--grpc.address", "0.0.0.0:9090",
			"--api.enable",
			"--minimum-gas-prices", "0.001stake",
			"--log_level", "*:info",
		).
		WithNode(cosmos.NewChainNodeConfigBuilder().
			WithPostInit(AddSingleSequencer).
			Build()).
		Build(ctx)
	require.NoError(s.T(), err)

	return gmChain
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func dockerBuild(t *testing.T, contextDir, dockerfile, tag string, args map[string]string) {
	t.Helper()
	cmdArgs := []string{"build", "-f", dockerfile, "-t", tag}
	for k, v := range args {
		cmdArgs = append(cmdArgs, "--build-arg", fmt.Sprintf("%s=%s", k, v))
	}
	cmdArgs = append(cmdArgs, contextDir)
	cmd := exec.Command("docker", cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "docker build failed: %s", tag)
}

func projectRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)

	return filepath.Clean(filepath.Join(wd, "..", ".."))
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
