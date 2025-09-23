package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/celestiaorg/tastora/framework/docker"
	"github.com/celestiaorg/tastora/framework/docker/container"
	"github.com/celestiaorg/tastora/framework/docker/cosmos"
	cfgutil "github.com/celestiaorg/tastora/framework/testutil/config"
	servercfg "github.com/cosmos/cosmos-sdk/server/config"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	"github.com/cosmos/cosmos-sdk/x/auth"
	"github.com/cosmos/cosmos-sdk/x/bank"
	"github.com/stretchr/testify/require"
)

func TestGaiaGM_Health(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping due to short mode")
	}

	repoRoot := projectRoot(t)
	ctx := context.Background()

	evnodeVersion := getenvDefault("EVNODE_VERSION", "v1.0.0-beta.4")
	igniteVersion := getenvDefault("IGNITE_VERSION", "v29.3.1")
	igniteEvolveApp := getenvDefault("IGNITE_EVOLVE_APP_VERSION", "main")
	celestiaAppVersion := getenvDefault("CELESTIA_APP_VERSION", "v4.0.10")

	dockerBuild(t, repoRoot,
		filepath.Join(repoRoot, "./tests/integration/docker/Dockerfile.gm"),
		"evabci/gm:local",
		map[string]string{
			"IGNITE_VERSION":            igniteVersion,
			"IGNITE_EVOLVE_APP_VERSION": igniteEvolveApp,
			"EVNODE_VERSION":            evnodeVersion,
		},
	)

	dockerClient, networkID := docker.DockerSetup(t)

	chainAImg := container.NewImage("ghcr.io/celestiaorg/celestia-app", celestiaAppVersion, "10001:10001")
	encCfg := moduletestutil.MakeTestEncodingConfig(auth.AppModuleBasic{}, bank.AppModuleBasic{})
	chainA, err := cosmos.NewChainBuilderWithTestName(t, t.Name()).
		WithDockerClient(dockerClient).
		WithDockerNetworkID(networkID).
		WithImage(chainAImg).
		WithName("celestia-app").
		WithChainID("celestia-local").
		WithBinaryName("celestia-appd").
		WithBech32Prefix("celestia").
		WithDenom("utia").
		WithGasPrices("0.000001utia").
		WithEncodingConfig(&encCfg).
		WithAdditionalStartArgs(
			"--force-no-bbr",
			"--grpc.enable",
			"--grpc.address", "0.0.0.0:9090",
			"--rpc.grpc_laddr=tcp://0.0.0.0:9098",
			"--timeout-commit", "1s",
		).
		WithEnv("BLST_PORTABLE=1").
		WithNode(cosmos.NewChainNodeConfigBuilder().Build()).
		WithPostInit(func(ctx context.Context, node *cosmos.ChainNode) error {
			return cfgutil.Modify(ctx, node, "config/app.toml", func(cfg *servercfg.Config) {
				cfg.MinGasPrices = "0.000001utia"
				cfg.GRPC.Enable = true
				cfg.GRPC.Address = "0.0.0.0:9090"
				cfg.API.Enable = true
			})
		}).
		Build(ctx)
	require.NoError(t, err)

	err = chainA.Start(ctx)
	require.NoError(t, err)

	gmImg := container.NewImage("evabci/gm", "local", "10001:10001")
	gmChain, err := cosmos.NewChainBuilder(t).
		WithDockerClient(dockerClient).
		WithDockerNetworkID(networkID).
		WithImage(gmImg).
		WithChainID("gm").
		WithBinaryName("gmd").
		WithAdditionalStartArgs(
			"--rollkit.node.aggregator",
			"--rollkit.da.address", "http://local-da:7980",
			"--rpc.laddr", "tcp://0.0.0.0:26757",
			"--grpc.address", "0.0.0.0:9190",
			"--api.enable",
			"--api.address", "tcp://0.0.0.0:1417",
			"--minimum-gas-prices", "0.001stake",
		).
		WithNode(cosmos.NewChainNodeConfigBuilder().
			WithPostInit(AddSingleSequencer).
			Build()).
		Build(ctx)
	require.NoError(t, err)

	err = gmChain.Start(ctx)
	require.NoError(t, err)

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
