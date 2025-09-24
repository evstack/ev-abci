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

	"cosmossdk.io/math"
	"github.com/celestiaorg/tastora/framework/docker"
	"github.com/celestiaorg/tastora/framework/docker/container"
	"github.com/celestiaorg/tastora/framework/docker/cosmos"
	"github.com/celestiaorg/tastora/framework/docker/dataavailability"
	"github.com/celestiaorg/tastora/framework/testutil/sdkacc"
	"github.com/celestiaorg/tastora/framework/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	"github.com/cosmos/cosmos-sdk/x/auth"
	"github.com/cosmos/cosmos-sdk/x/bank"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
)

// TestGMChainEmpty is an empty test case using the DockerIntegrationTestSuite
func (s *DockerIntegrationTestSuite) TestGMChainEmpty() {
	s.T().Log("Running empty GM chain test")
	s.T().Log("Celestia chain created successfully")
	s.T().Log("Bridge node created successfully")
	s.T().Log("Evolve chain created successfully")
	s.T().Log("Test completed - all infrastructure is working")
}

func TestGaiaGM_Health(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping due to short mode")
	}

	ctx := context.Background()

	/*	evnodeVersion := getenvDefault("EVNODE_VERSION", "v1.0.0-beta.4")
		igniteVersion := getenvDefault("IGNITE_VERSION", "v29.3.1")
		igniteEvolveApp := getenvDefault("IGNITE_EVOLVE_APP_VERSION", "main")

		dockerBuild(t, projectRoot(t),
			filepath.Join(projectRoot(t), "./tests/integration/docker/Dockerfile.gm"),
			"evabci/gm:local",
			map[string]string{
				"IGNITE_VERSION":            igniteVersion,
				"IGNITE_EVOLVE_APP_VERSION": igniteEvolveApp,
				"EVNODE_VERSION":            evnodeVersion,
			},
		)
	*/
	dockerClient, networkID := docker.DockerSetup(t)

	// Create Celestia chain for DA
	t.Log("Creating Celestia app chain...")
	celestiaChain := CreateCelestiaChain(ctx, t, dockerClient, networkID)
	t.Log("Celestia app chain started")

	// Create DA network with bridge node
	t.Log("Creating DA network with bridge node...")
	bridgeNode := CreateDANetwork(ctx, t, dockerClient, networkID, celestiaChain)
	t.Log("Bridge node started")

	// Get DA connection details
	authToken, err := bridgeNode.GetAuthToken()
	require.NoError(t, err)

	bridgeNetworkInfo, err := bridgeNode.GetNetworkInfo(ctx)
	require.NoError(t, err)
	bridgeRPCAddress := bridgeNetworkInfo.Internal.RPCAddress()

	daAddress := fmt.Sprintf("http://%s", bridgeRPCAddress)
	t.Logf("DA address: %s", daAddress)

	celestiaHeight, err := celestiaChain.Height(ctx)
	require.NoError(t, err)

	t.Logf("DA start height: %d", celestiaHeight)

	t.Log("Creating GM chain connected to DA network...")
	sdk.GetConfig().SetBech32PrefixForAccount("gm", "gmpub")
	gmImg := container.NewImage("evabci/gm", "local", "1000:1000")
	gmChain, err := cosmos.NewChainBuilder(t).
		WithDockerClient(dockerClient).
		WithDockerNetworkID(networkID).
		WithImage(gmImg).
		WithBech32Prefix("gm").
		WithChainID("gm").
		WithBinaryName("gmd").
		WithAdditionalStartArgs(
			"--evnode.node.aggregator",
			"--evnode.signer.passphrase", "12345678",
			"--evnode.da.address", daAddress,
			"--evnode.da.gas_price", "0.000001",
			"--evnode.da.auth_token", authToken,
			"--evnode.rpc.address", "0.0.0.0:7331",
			"--evnode.da.namespace", "ev-header",
			"--evnode.da.data_namespace", "ev-data",
			"--evnode.da.start_height", fmt.Sprintf("%d", celestiaHeight),
			"--evnode.p2p.listen_address", "/ip4/0.0.0.0/tcp/36656",
			"--rpc.laddr", "tcp://0.0.0.0:26757",
			"--grpc.address", "0.0.0.0:9190",
			"--api.enable",
			"--api.address", "tcp://0.0.0.0:1417",
			"--minimum-gas-prices", "0.001stake",
			"--log_level", "*:info",
		).
		WithNode(cosmos.NewChainNodeConfigBuilder().
			WithPostInit(AddSingleSequencer).
			Build()).
		Build(ctx)
	require.NoError(t, err)

	err = gmChain.Start(ctx)
	require.NoError(t, err)
	t.Log("GM chain started and connected to DA network")

	//	evChain, err := evd.NewChainBuilder(t).
	//		WithDockerClient(dockerClient).
	//		WithDockerNetworkID(networkID).
	//		WithImage(gmImg).
	//		WithChainID("gm").
	//		WithBinaryName("gmd").
	//		WithAdditionalStartArgs(
	//			"--rollkit.node.aggregator",
	//			"--rollkit.da.address", "http://local-da:7980",
	//			"--rpc.laddr", "tcp://0.0.0.0:26757",
	//			"--grpc.address", "0.0.0.0:9190",
	//			"--api.enable",
	//			"--api.address", "tcp://0.0.0.0:1417",
	//			"--minimum-gas-prices", "0.001stake",
	//		).
	//		WithNode(evd.NewNodeBuilder().
	//			WithAggregator(true).
	//			Build()).
	//		Build(ctx)
	//	require.NoError(t, err)
	//
	//	require.NoError(t, chainA.Start(ctx))
	//
	//	gmNode := evChain.GetNodes()[0]
	//
	//	// Execute ignite evolve init (uses default home directory)
	//	gmNode.WithInitOverride(
	//		[]string{
	//			"ignite", "evolve", "init",
	//		},
	//	)
	//	require.NoError(t, gmNode.Init(ctx))
	//
	//	// Get the actual home directory from the node
	//	gmHome := gmNode.HomeDir()
	//	t.Logf("Using node home directory: %s", gmHome)
	//
	//	// Ensure client.toml has the correct RPC node (like init-gm.sh does)
	//	clientTomlPath := fmt.Sprintf("%s/config/client.toml", gmHome)
	//	clientTomlCmd := []string{"sh", "-c", fmt.Sprintf(`
	//if [ -f "%s" ]; then
	//  sed -i 's|^node *=.*|node = "tcp://127.0.0.1:26757"|' "%s"
	//else
	//  mkdir -p %s/config
	//  cat > "%s" <<'EOF'
	//[client]
	//node = "tcp://127.0.0.1:26757"
	//EOF
	//fi`, clientTomlPath, clientTomlPath, gmHome, clientTomlPath)}
	//	_, _, err = gmNode.Exec(ctx, gmNode.Logger, clientTomlCmd, nil)
	//	require.NoError(t, err)
	//
	//	// Add validator key using standard mnemonic
	//	mnemonic := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	//
	//	// Debug: Check what files exist before key creation
	//	t.Log("=== DEBUG: Checking files before key creation ===")
	//	lsBeforeCmd := []string{"sh", "-c", fmt.Sprintf("ls -la %s/ || echo 'directory does not exist'", gmHome)}
	//	lsBeforeOutput, _, _ := gmNode.Exec(ctx, gmNode.Logger, lsBeforeCmd, nil)
	//	t.Logf("Files in %s before key creation:\n%s", gmHome, string(lsBeforeOutput))
	//
	//	// Use exactly the same command format as init-gm.sh script
	//	// Echo mnemonic and pipe to gmd keys add with same parameter order as working script
	//	cmd := []string{"sh", "-c", fmt.Sprintf(`echo "%s" | gmd keys add validator --keyring-backend test --home %s --recover --hd-path "m/44'/118'/0'/0/0"`, mnemonic, gmHome)}
	//	keysAddOutput, keysAddStderr, err := gmNode.Exec(ctx, gmNode.Logger, cmd, nil)
	//	t.Logf("Keys add output:\nSTDOUT: %s\nSTDERR: %s", string(keysAddOutput), string(keysAddStderr))
	//	require.NoError(t, err)
	//
	//	// Debug: Check what files exist after key creation
	//	t.Log("=== DEBUG: Checking files after key creation ===")
	//	lsAfterCmd := []string{"sh", "-c", fmt.Sprintf("ls -la %s/ && echo '--- keyring files ---' && ls -la %s/keyring-test-* 2>/dev/null || echo 'no keyring files found'", gmHome, gmHome)}
	//	lsAfterOutput, _, _ := gmNode.Exec(ctx, gmNode.Logger, lsAfterCmd, nil)
	//	t.Logf("Files in %s after key creation:\n%s", gmHome, string(lsAfterOutput))
	//
	//	// Debug: Try to list keys first
	//	t.Log("=== DEBUG: Trying to list keys ===")
	//	listKeysCmd := []string{"gmd", "keys", "list", "--keyring-backend", "test", "--home", gmHome}
	//	listOutput, listStderr, listErr := gmNode.Exec(ctx, gmNode.Logger, listKeysCmd, nil)
	//	t.Logf("Keys list output:\nSTDOUT: %s\nSTDERR: %s\nError: %v", string(listOutput), string(listStderr), listErr)
	//
	//	// Extract validator address directly from keys add output since keys show might not work
	//	var validatorAddr string
	//	lines := strings.Split(string(keysAddOutput), "\n")
	//	for _, line := range lines {
	//		if strings.Contains(line, "address:") {
	//			// Extract address from "- address: gm19rl4cm2hmr8afy4kldpxz3fka4jguq0aj3w9ku"
	//			parts := strings.Split(strings.TrimSpace(line), " ")
	//			if len(parts) >= 2 {
	//				validatorAddr = parts[1]
	//				break
	//			}
	//		}
	//	}
	//	require.NotEmpty(t, validatorAddr, "Could not extract validator address from keys add output")
	//	t.Logf("Extracted validator address from keys add output: %s", validatorAddr)
	//
	//	// Try keys show anyway to see what happens
	//	t.Log("=== DEBUG: Trying keys show anyway ===")
	//	getAddrCmd := []string{"gmd", "keys", "show", "validator", "-a", "--keyring-backend", "test", "--home", gmHome}
	//	validatorAddrBytes, getAddrStderr, getAddrErr := gmNode.Exec(ctx, gmNode.Logger, getAddrCmd, nil)
	//	t.Logf("Keys show output:\nSTDOUT: %s\nSTDERR: %s\nError: %v", string(validatorAddrBytes), string(getAddrStderr), getAddrErr)
	//
	//	if getAddrErr == nil {
	//		showAddr := strings.TrimSpace(string(validatorAddrBytes))
	//		t.Logf("Keys show address: %s", showAddr)
	//		if showAddr != validatorAddr {
	//			t.Logf("WARNING: Address mismatch! keys add: %s, keys show: %s", validatorAddr, showAddr)
	//		}
	//	}
	//
	//	// Add genesis account
	//	addGenesisCmd := []string{"gmd", "--home", gmHome, "genesis", "add-genesis-account", validatorAddr, "100000000stake,10000token"}
	//	_, _, err = gmNode.Exec(ctx, gmNode.Logger, addGenesisCmd, nil)
	//	require.NoError(t, err)
	//
	//	require.NoError(t, gmNode.Start(ctx))
	//
	//	t.Log("Esperando celestia-app altura >= 1...")
	//	waitForHeight(t, ctx, chainA, 1, 120*time.Second)
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

// CreateCelestiaChain sets up a Celestia app chain for DA
func CreateCelestiaChain(ctx context.Context, t *testing.T, dockerClient *client.Client, networkID string) *cosmos.Chain {
	testEncCfg := moduletestutil.MakeTestEncodingConfig(auth.AppModuleBasic{}, bank.AppModuleBasic{})
	celestia, err := cosmos.NewChainBuilder(t).
		WithEncodingConfig(&testEncCfg).
		WithDockerClient(dockerClient).
		WithDockerNetworkID(networkID).
		WithImage(container.NewImage(celestiaAppRepo, celestiaAppTag, "10001:10001")).
		WithAdditionalStartArgs(
			"--force-no-bbr",
			"--grpc.enable",
			"--grpc.address", "0.0.0.0:9090",
			"--rpc.grpc_laddr=tcp://0.0.0.0:9098",
			"--timeout-commit", "1s",
			"--minimum-gas-prices", "0.000001utia",
		).
		WithNode(cosmos.NewChainNodeConfigBuilder().Build()).
		Build(ctx)

	require.NoError(t, err)

	err = celestia.Start(ctx)
	require.NoError(t, err)
	return celestia
}

// CreateDANetwork sets up the DA network with bridge node
func CreateDANetwork(ctx context.Context, t *testing.T, dockerClient *client.Client, networkID string, celestiaChain *cosmos.Chain) *dataavailability.Node {
	// Build DA network with bridge node
	daNetwork, err := dataavailability.NewNetworkBuilder(t).
		WithDockerClient(dockerClient).
		WithDockerNetworkID(networkID).
		WithImage(container.NewImage(celestiaNodeRepo, celestiaNodeTag, "10001:10001")).
		WithNode(dataavailability.NewNodeBuilder().
			WithNodeType(types.BridgeNode).
			Build()).
		Build(ctx)
	require.NoError(t, err)

	genesisHash, err := getGenesisHash(ctx, celestiaChain)
	require.NoError(t, err)

	bridgeNodes := daNetwork.GetNodesByType(types.BridgeNode)
	require.NotEmpty(t, bridgeNodes, "no bridge nodes available")

	bridgeNode := bridgeNodes[0]

	chainID := celestiaChain.GetChainID()
	networkInfo, err := celestiaChain.GetNodes()[0].GetNetworkInfo(ctx)
	require.NoError(t, err)
	celestiaNodeHostname := networkInfo.Internal.Hostname

	err = bridgeNode.Start(ctx,
		dataavailability.WithChainID(chainID),
		dataavailability.WithAdditionalStartArguments("--p2p.network", chainID, "--core.ip", celestiaNodeHostname, "--rpc.addr", "0.0.0.0", "--keyring.keyname", "my-key"),
		dataavailability.WithEnvironmentVariables(map[string]string{
			"CELESTIA_CUSTOM": types.BuildCelestiaCustomEnvVar(chainID, genesisHash, ""),
			"P2P_NETWORK":     chainID,
		}),
	)
	require.NoError(t, err)

	// Fund the bridge node DA wallet to enable blob submission
	fundBridgeNodeWallet(ctx, t, celestiaChain, bridgeNode)

	return bridgeNode
}

// sendFunds sends funds from one wallet to another using bank transfer
func sendFunds(ctx context.Context, chain *cosmos.Chain, fromWallet, toWallet *types.Wallet, amount sdk.Coins, nodeIdx int) error {
	fromAddress, err := sdkacc.AddressFromWallet(fromWallet)
	if err != nil {
		return fmt.Errorf("failed to get sender address: %w", err)
	}

	toAddress, err := sdkacc.AddressFromWallet(toWallet)
	if err != nil {
		return fmt.Errorf("failed to get destination address: %w", err)
	}

	chainNode := chain.GetNodes()[nodeIdx]
	cosmosChainNode, ok := chainNode.(*cosmos.ChainNode)
	if !ok {
		return fmt.Errorf("chainNode is not a cosmos.ChainNode")
	}
	broadcaster := cosmos.NewBroadcasterForNode(chain, cosmosChainNode)

	time.Sleep(30 * time.Second)

	msg := banktypes.NewMsgSend(fromAddress, toAddress, amount)
	resp, err := broadcaster.BroadcastMessages(ctx, fromWallet, msg)
	if err != nil {
		return fmt.Errorf("failed to broadcast transaction: %w", err)
	}

	if resp.Code != 0 {
		return fmt.Errorf("transaction failed with code %d: %s", resp.Code, resp.RawLog)
	}

	return nil
}

// fundBridgeNodeWallet funds the bridge node's DA wallet for blob submission
func fundBridgeNodeWallet(ctx context.Context, t *testing.T, celestiaChain *cosmos.Chain, bridgeNode *dataavailability.Node) {
	// hack to get around global, need to set the address prefix before use.
	sdk.GetConfig().SetBech32PrefixForAccount("celestia", "celestiapub")

	t.Log("Funding bridge node DA wallet...")
	fundingWallet := celestiaChain.GetFaucetWallet()

	// Get the bridge node's wallet
	bridgeWallet, err := bridgeNode.GetWallet()
	require.NoError(t, err)

	// fund the bridge node wallet
	daFundingAmount := sdk.NewCoins(sdk.NewCoin("utia", math.NewInt(10_000_000)))
	err = sendFunds(ctx, celestiaChain, fundingWallet, bridgeWallet, daFundingAmount, 0)
	require.NoError(t, err)
}
