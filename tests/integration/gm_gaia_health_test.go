package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/celestiaorg/tastora/framework/docker/container"
	"github.com/celestiaorg/tastora/framework/docker/cosmos"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

// TestAttesterSystem is an empty test case using the DockerIntegrationTestSuite
func (s *DockerIntegrationTestSuite) TestAttesterSystem() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.buildGMImage()

	authToken, err := s.bridgeNode.GetAuthToken()
	require.NoError(s.T(), err)

	bridgeNetworkInfo, err := s.bridgeNode.GetNetworkInfo(ctx)
	require.NoError(s.T(), err)
	bridgeRPCAddress := bridgeNetworkInfo.Internal.RPCAddress()

	daAddress := fmt.Sprintf("http://%s", bridgeRPCAddress)
	s.T().Logf("DA address: %s", daAddress)

	celestiaHeight, err := s.celestiaChain.Height(ctx)
	require.NoError(s.T(), err)

	s.T().Logf("DA start height: %d", celestiaHeight)

	s.T().Log("Creating GM chain connected to DA network...")
	sdk.GetConfig().SetBech32PrefixForAccount("gm", "gmpub")
	gmImg := container.NewImage("evabci/gm", "local", "1000:1000")
	gmChain, err := cosmos.NewChainBuilder(s.T()).
		WithDockerClient(s.dockerClient).
		WithDockerNetworkID(s.networkID).
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
	require.NoError(s.T(), err)

	err = gmChain.Start(ctx)
	require.NoError(s.T(), err)
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
