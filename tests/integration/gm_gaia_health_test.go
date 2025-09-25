package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/celestiaorg/tastora/framework/docker/container"
	"github.com/celestiaorg/tastora/framework/docker/cosmos"
	"github.com/celestiaorg/tastora/framework/docker/file"
	"github.com/celestiaorg/tastora/framework/testutil/sdkacc"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"
)

// TestAttesterSystem is an empty test case using the DockerIntegrationTestSuite
func (s *DockerIntegrationTestSuite) TestAttesterSystem() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.buildGMImage()
	s.buildAttesterImage()

	daAddress, authToken, daStartHeight, err := s.getDANetworkParams(ctx)
	require.NoError(s.T(), err)

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
			"--evnode.da.start_height", daStartHeight,
			"--evnode.p2p.listen_address", "/ip4/0.0.0.0/tcp/36656",
			"--rpc.laddr", "tcp://0.0.0.0:26657",
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

	// Create attester configuration
	attesterConfig := DefaultAttesterConfig()
	// Get the internal network addresses for the GM chain
	gmNodes := gmChain.GetNodes()
	require.NotEmpty(s.T(), gmNodes, "no GM chain nodes available")

	gmNode := gmNodes[0]
	gmNodeInfo, err := gmNode.GetNetworkInfo(ctx)
	require.NoError(s.T(), err)

	privValidatorKeyJSON, err := gmNode.ReadFile(ctx, "config/priv_validator_key.json")
	require.NoError(s.T(), err, "unable to read priv_validator_key.json from GM node")
	s.T().Logf("retrieved priv_validator_key.json (%d bytes)", len(privValidatorKeyJSON))
	validatorKeyPath := filepath.Join(s.T().TempDir(), "priv_validator_key.json")
	require.NoError(s.T(), os.WriteFile(validatorKeyPath, privValidatorKeyJSON, 0o600))
	attesterConfig.ValidatorKeyPath = validatorKeyPath

	privValidatorStateJSON, err := gmNode.ReadFile(ctx, "data/priv_validator_state.json")
	require.NoError(s.T(), err, "unable to read priv_validator_state.json from GM node")
	s.T().Logf("retrieved priv_validator_state.json (%d bytes)", len(privValidatorStateJSON))
	validatorStatePath := filepath.Join(s.T().TempDir(), "priv_validator_state.json")
	require.NoError(s.T(), os.WriteFile(validatorStatePath, privValidatorStateJSON, 0o600))
	attesterConfig.ValidatorStatePath = validatorStatePath

	attesterAccAddr, err := deriveAttesterAccount(attesterConfig.Mnemonic)
	require.NoError(s.T(), err, "failed to derive attester account address")

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
	require.NoError(s.T(), attesterNode.WriteFileWithOptions(
		ctx,
		"config/priv_validator_key.json",
		privValidatorKeyJSON,
		file.WithOwner(attesterNode.Image.UIDGID),
		file.WithFileMode(0o600),
	))
	require.NoError(s.T(), attesterNode.WriteFileWithOptions(
		ctx,
		"data/priv_validator_state.json",
		privValidatorStateJSON,
		file.WithOwner(attesterNode.Image.UIDGID),
		file.WithFileMode(0o600),
	))

	s.T().Logf("Starting attester node %s", attesterNode.Name())
	err = attesterNode.Start(ctx, attesterConfig)
	require.NoError(s.T(), err)

	s.T().Log("Attester node started successfully")
}

func deriveAttesterAccount(mnemonic string) (sdk.AccAddress, error) {
	derivedPriv, err := hd.Secp256k1.Derive()(mnemonic, "", hd.CreateHDPath(118, 0, 0).String())
	if err != nil {
		return nil, fmt.Errorf("failed to derive private key: %w", err)
	}
	privKey := &secp256k1.PrivKey{Key: derivedPriv}
	return sdk.AccAddress(privKey.PubKey().Address()), nil
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
