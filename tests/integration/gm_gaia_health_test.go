package integration_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/celestiaorg/tastora/framework/docker"
	"github.com/celestiaorg/tastora/framework/docker/container"
	"github.com/celestiaorg/tastora/framework/docker/cosmos"
	evd "github.com/celestiaorg/tastora/framework/docker/evstack"
	cfgutil "github.com/celestiaorg/tastora/framework/testutil/config"
	"github.com/celestiaorg/tastora/framework/types"
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

	evnodeVersion := getenvDefault("EVNODE_VERSION", "v1.0.0-beta.2.0.20250917144924-05372840f308")
	igniteVersion := getenvDefault("IGNITE_VERSION", "v29.3.1")
	igniteEvolveApp := getenvDefault("IGNITE_EVOLVE_APP_VERSION", "main")
	celestiaAppVersion := getenvDefault("CELESTIA_APP_VERSION", "v4.0.10")

	dockerBuild(t, repoRoot,
		filepath.Join(repoRoot, ".github/integration-tests/docker/Dockerfile.gm"),
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

	gmImg := container.NewImage("evabci/gm", "local", "10001:10001")
	evChain, err := evd.NewChainBuilder(t).
		WithDockerClient(dockerClient).
		WithDockerNetworkID(networkID).
		WithImage(gmImg).
		WithChainID("gm").
		WithBinaryName("gmd").
		WithEnv("BLST_PORTABLE=1").
		WithAdditionalStartArgs(
			"--rpc.laddr", "tcp://0.0.0.0:26757",
			"--grpc.address", "0.0.0.0:9190",
			"--api.enable",
			"--api.address", "tcp://0.0.0.0:1417",
			"--minimum-gas-prices", "0.001stake",
		).
		WithNode(evd.NewNodeBuilder().
			WithAggregator(true).
			WithPostInit(func(ctx context.Context, node *evd.Node) error {
				clientToml := []byte("[client]\nnode = \"tcp://127.0.0.1:26757\"\n")
				return node.WriteFile(ctx, "config/client.toml", clientToml)
			}).
			Build()).
		Build(ctx)
	require.NoError(t, err)

	require.NoError(t, chainA.Start(ctx))

	gmNode := evChain.GetNodes()[0]
	require.NoError(t, gmNode.Init(ctx))
	require.NoError(t, gmNode.Start(ctx))

	t.Log("Esperando celestia-app altura >= 1...")
	waitForHeight(t, ctx, chainA, 1, 120*time.Second)
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

func waitForHeight(t *testing.T, ctx context.Context, chain types.Chain, minHeight int64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last int64
	for time.Now().Before(deadline) {
		h, err := chain.Height(ctx)
		if err == nil && h >= minHeight {
			return
		}
		last = h
		time.Sleep(3 * time.Second)
	}
	require.FailNowf(t, "height timeout", "chain did not reach height %d (last=%d) within %s", minHeight, last, timeout)
}

func projectRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)

	return filepath.Clean(filepath.Join(wd, "..", ".."))
}
