package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"cosmossdk.io/math"
	evnodev1connect "github.com/evstack/ev-node/types/pb/evnode/v1/v1connect"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/celestiaorg/tastora/framework/docker"
	"github.com/celestiaorg/tastora/framework/docker/container"
	"github.com/celestiaorg/tastora/framework/docker/cosmos"
	"github.com/celestiaorg/tastora/framework/docker/dataavailability"
	"github.com/celestiaorg/tastora/framework/testutil/config"
	"github.com/celestiaorg/tastora/framework/testutil/sdkacc"
	"github.com/celestiaorg/tastora/framework/testutil/wait"
	"github.com/celestiaorg/tastora/framework/types"
	cometcfg "github.com/cometbft/cometbft/config"
	"github.com/cometbft/cometbft/crypto"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	cmprivval "github.com/cometbft/cometbft/privval"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module/testutil"
	"github.com/cosmos/cosmos-sdk/x/auth"
	"github.com/cosmos/cosmos-sdk/x/bank"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/ibc-go/v8/modules/apps/transfer"
	"github.com/moby/moby/client"
	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

const (
	celestiaAppTag   = "v4.0.10"
	celestiaNodeTag  = "v0.23.5"
	celestiaAppRepo  = "ghcr.io/celestiaorg/celestia-app"
	celestiaNodeRepo = "ghcr.io/celestiaorg/celestia-node"
)

// DockerIntegrationTestSuite provides common functionality for integration tests using Docker
type DockerIntegrationTestSuite struct {
	suite.Suite

	dockerClient  *client.Client
	networkID     string
	celestiaChain *cosmos.Chain
	daNetwork     *dataavailability.Network
	evolveChain   *cosmos.Chain
	bridgeNode    *dataavailability.Node
	logger        *zap.Logger
}

// SetupTest initializes the Docker environment for each test
// celestia is deployed, a single bridge node and the rollkit chain.
func (s *DockerIntegrationTestSuite) SetupTest() {
	ctx := context.Background()
	s.logger = zaptest.NewLogger(s.T())
	s.T().Cleanup(func() {
		_ = s.logger.Sync()
	})
	s.dockerClient, s.networkID = docker.DockerSetup(s.T())

	s.celestiaChain = s.CreateCelestiaChain(ctx)
	s.T().Log("Celestia app chain started")

	s.bridgeNode = s.CreateDANetwork(ctx)
	s.T().Log("Bridge node started")
}

// CreateCelestiaChain sets up a Celestia app chain for DA and stores it in the suite
func (s *DockerIntegrationTestSuite) CreateCelestiaChain(ctx context.Context) *cosmos.Chain {
	testEncCfg := testutil.MakeTestEncodingConfig(auth.AppModuleBasic{}, bank.AppModuleBasic{}, transfer.AppModuleBasic{})
	celestia, err := cosmos.NewChainBuilder(s.T()).
		WithEncodingConfig(&testEncCfg).
		WithDockerClient(s.dockerClient).
		WithDockerNetworkID(s.networkID).
		WithImage(container.NewImage(celestiaAppRepo, celestiaAppTag, "10001:10001")).
		WithAdditionalStartArgs(
			"--force-no-bbr",
			"--grpc.enable",
			"--grpc.address", "0.0.0.0:9090",
			"--rpc.grpc_laddr=tcp://0.0.0.0:9098",
			"--timeout-commit", "1s",
			"--minimum-gas-prices", "0.000001utia",
		).
		WithPostInit(func(ctx context.Context, node *cosmos.ChainNode) error {
			// 1) Ensure ABCI responses and tx events are retained and indexed for Hermes
			if err := config.Modify(ctx, node, "config/config.toml", func(cfg *cometcfg.Config) {
				cfg.Storage.DiscardABCIResponses = false
				// Enable key-value tx indexer so Hermes can query IBC packet events
				cfg.TxIndex.Indexer = "kv"
				// Increase RPC BroadcastTxCommit timeout to accommodate CI slowness
				if cfg.RPC != nil {
					cfg.RPC.TimeoutBroadcastTxCommit = 120000000000 // 120s in nanoseconds for toml marshal
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
		WithNode(cosmos.NewChainNodeConfigBuilder().Build()).
		Build(ctx)

	s.Require().NoError(err)

	err = celestia.Start(ctx)
	s.Require().NoError(err)
	return celestia
}

// CreateDANetwork sets up the DA network with bridge and full nodes and stores it in the suite
func (s *DockerIntegrationTestSuite) CreateDANetwork(ctx context.Context) *dataavailability.Node {
	// Build DA network with bridge node
	daNetwork, err := dataavailability.NewNetworkBuilder(s.T()).
		WithDockerClient(s.dockerClient).
		WithDockerNetworkID(s.networkID).
		WithImage(container.NewImage(celestiaNodeRepo, celestiaNodeTag, "10001:10001")).
		WithNode(dataavailability.NewNodeBuilder().
			WithNodeType(types.BridgeNode).
			Build()).
		Build(ctx)
	s.Require().NoError(err)

	s.daNetwork = daNetwork

	genesisHash, err := getGenesisHash(ctx, s.celestiaChain)
	s.Require().NoError(err)

	bridgeNodes := daNetwork.GetNodesByType(types.BridgeNode)
	s.Require().NotEmpty(bridgeNodes, "no bridge nodes available")

	bridgeNode := bridgeNodes[0]

	chainID := s.celestiaChain.GetChainID()
	networkInfo, err := s.celestiaChain.GetNodes()[0].GetNetworkInfo(ctx)
	s.Require().NoError(err)
	celestiaNodeHostname := networkInfo.Internal.Hostname

	err = bridgeNode.Start(ctx,
		dataavailability.WithChainID(chainID),
		// my-key is the name of the keyring generated by the bridge node, we can fund this key so that we can submit blobs.
		dataavailability.WithAdditionalStartArguments("--p2p.network", chainID, "--core.ip", celestiaNodeHostname, "--rpc.addr", "0.0.0.0", "--keyring.keyname", "my-key"),
		dataavailability.WithEnvironmentVariables(map[string]string{
			"CELESTIA_CUSTOM": types.BuildCelestiaCustomEnvVar(chainID, genesisHash, ""),
			"P2P_NETWORK":     chainID,
		}),
	)
	s.Require().NoError(err)

	// Fund the bridge node DA wallet to enable blob submission
	s.fundBridgeNodeWallet(ctx, bridgeNode)

	return bridgeNode
}

// CreateEvolveChain sets up the rollkit chain connected to the DA network and returns it
func (s *DockerIntegrationTestSuite) CreateEvolveChain(ctx context.Context) *cosmos.Chain {
	// Get DA connection details
	authToken, err := s.bridgeNode.GetAuthToken()
	s.Require().NoError(err)

	bridgeNetworkInfo, err := s.bridgeNode.GetNetworkInfo(ctx)
	s.Require().NoError(err)
	bridgeRPCAddress := bridgeNetworkInfo.Internal.RPCAddress()

	daAddress := fmt.Sprintf("http://%s", bridgeRPCAddress)

	celestiaHeight, err := s.celestiaChain.Height(ctx)
	s.Require().NoError(err)
	daStartHeight := fmt.Sprintf("%d", celestiaHeight)

	// bank and auth modules required to deal with bank send tx's
	testEncCfg := testutil.MakeTestEncodingConfig(auth.AppModuleBasic{}, bank.AppModuleBasic{})
	// Create chain with only the aggregator node initially
	evolveChain, err := cosmos.NewChainBuilder(s.T()).
		WithEncodingConfig(&testEncCfg).
		WithImage(getEvolveAppContainer()).
		WithDenom("stake").
		WithDockerClient(s.dockerClient).
		WithName("evolve").
		WithDockerNetworkID(s.networkID).
		WithChainID("evolve-test").
		WithBech32Prefix("gm").
		WithBinaryName("gmd").
		// explicitly set 0 gas so that we can make exact assertions when sending balances.
		WithGasPrices(fmt.Sprintf("0.00%s", "stake")).
		WithAdditionalExposedPorts("7331").
		WithNodes(
			cosmos.NewChainNodeConfigBuilder().
				// Create aggregator node with rollkit-specific start arguments
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
					"--log_level", "*:info",
				).
				WithPostInit(addSingleSequencer, setDAStartHeight(daStartHeight)).
				Build(),
		).
		Build(ctx)

	s.Require().NoError(err)

	// Start the aggregator node first so that we can query the p2p
	err = evolveChain.Start(ctx)
	s.Require().NoError(err)

	// wait for aggregator to produce just 1 block to ensure it's running
	s.T().Log("Waiting for aggregator to produce blocks...")
	s.Require().NoError(wait.ForBlocks(ctx, 1, evolveChain))

	// Get aggregator's network info to construct ev-node RPC address
	// Use External address since we're calling from the test host
	networkInfo, err := evolveChain.GetNode().GetNetworkInfo(ctx)
	s.Require().NoError(err)

	s.T().Logf("NetworkInfo - External: IP=%s, Hostname=%s", networkInfo.External.IP, networkInfo.External.Hostname)

	evNodePort, ok := networkInfo.ExtraPortMappings["7331"]
	s.Require().True(ok, "failed to get ev-node RPC port mapping")

	aggregatorPeer := s.GetNodeMultiAddr(ctx, networkInfo.External.Hostname+":"+evNodePort)
	s.T().Logf("Aggregator peer: %s", aggregatorPeer)

	s.T().Logf("Adding first follower node...")
	s.addFollowerNode(ctx, evolveChain, daAddress, authToken, daStartHeight, aggregatorPeer)

	// wait for first follower to sync before adding second
	s.T().Logf("Waiting for first follower to sync...")
	s.waitForFollowerSync(ctx, evolveChain)
	s.T().Logf("Adding second follower node...")
	s.addFollowerNode(ctx, evolveChain, daAddress, authToken, daStartHeight, aggregatorPeer)

	//wait for all follower nodes to sync with aggregator
	s.T().Logf("Waiting for all follower nodes to sync...")
	s.waitForFollowerSync(ctx, evolveChain)

	return evolveChain
}

// GetNodeMultiAddr queries the ev-node RPC to get the actual p2p multiaddr
// Returns the multiaddr in the format: /ip4/{IP}/tcp/36656/p2p/{PEER_ID}
func (s *DockerIntegrationTestSuite) GetNodeMultiAddr(ctx context.Context, rpcAddress string) string {
	baseURL := fmt.Sprintf("http://%s", rpcAddress)

	httpClient := &http.Client{Timeout: 10 * time.Second}
	p2pClient := evnodev1connect.NewP2PServiceClient(httpClient, baseURL)

	// call GetNetInfo
	resp, err := p2pClient.GetNetInfo(ctx, connect.NewRequest(&emptypb.Empty{}))
	s.Require().NoError(err, "failed to call GetNetInfo RPC")

	netInfo := resp.Msg.GetNetInfo()
	s.Require().NotNil(netInfo, "netInfo is nil")

	s.Require().NotEmpty(netInfo.GetListenAddresses(), "no listen addresses returned")

	// find the non-localhost address
	var multiAddr string
	for _, addr := range netInfo.GetListenAddresses() {
		if !strings.Contains(addr, "127.0.0.1") {
			multiAddr = addr
			break
		}
	}

	s.Require().NotEmpty(multiAddr, "no non-localhost listen address found")
	s.T().Logf("Selected multiaddr: %s", multiAddr)
	return multiAddr
}

// addFollowerNode adds a follower node to the evolve chain.
func (s *DockerIntegrationTestSuite) addFollowerNode(ctx context.Context, evolveChain *cosmos.Chain, daAddress, authToken, _, aggregatorPeer string) {
	err := evolveChain.AddNode(ctx, cosmos.NewChainNodeConfigBuilder().
		WithAdditionalStartArgs(
			"--evnode.da.address", daAddress,
			"--evnode.da.gas_price", "0.000001",
			"--evnode.da.auth_token", authToken,
			"--evnode.rpc.address", "0.0.0.0:7331",
			"--evnode.da.namespace", "ev-header",
			"--evnode.da.data_namespace", "ev-data",
			"--evnode.p2p.listen_address", "/ip4/0.0.0.0/tcp/36656",
			"--evnode.p2p.peers", aggregatorPeer,
			"--log_level", "*:debug",
		).
		Build())
	s.Require().NoError(err)
}

// waitForFollowerSync waits for all follower nodes to sync with the aggregator
func (s *DockerIntegrationTestSuite) waitForFollowerSync(ctx context.Context, evolveChain *cosmos.Chain) {
	nodes := evolveChain.GetNodes()
	if len(nodes) <= 1 {
		return
	}

	syncCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// convert nodes to Heighter interface
	aggregator := nodes[0].(wait.Heighter)
	followers := make([]wait.Heighter, 0)
	for i := 1; i < len(nodes); i++ {
		followers = append(followers, nodes[i].(wait.Heighter))
	}

	s.T().Logf("Waiting for %d follower nodes to sync with aggregator...", len(followers))
	err := wait.ForInSync(syncCtx, aggregator, followers...)
	s.Require().NoError(err, "followers failed to sync with aggregator")
	s.T().Logf("All follower nodes are now in sync with aggregator")
}

// getGenesisHash retrieves the genesis hash from the celestia chain
func getGenesisHash(ctx context.Context, chain types.Chain) (string, error) {
	node := chain.GetNodes()[0]
	c, err := node.GetRPCClient()
	if err != nil {
		return "", fmt.Errorf("failed to get node client: %w", err)
	}

	first := int64(1)
	block, err := c.Block(ctx, &first)
	if err != nil {
		return "", fmt.Errorf("failed to get block: %w", err)
	}

	genesisHash := block.Block.Header.Hash().String()
	if genesisHash == "" {
		return "", fmt.Errorf("genesis hash is empty")
	}

	return genesisHash, nil
}

// sendFunds sends funds from one wallet to another using bank transfer
func (s *DockerIntegrationTestSuite) sendFunds(ctx context.Context, chain *cosmos.Chain, fromWallet, toWallet *types.Wallet, amount sdk.Coins, nodeIdx int) error {
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

// setDAStartHeight creates a PostInit function that sets the DA start height in the genesis file
func setDAStartHeight(daStartHeight string) func(context.Context, *cosmos.ChainNode) error {
	return func(ctx context.Context, node *cosmos.ChainNode) error {
		daHeight, err := strconv.ParseUint(daStartHeight, 10, 64)
		if err != nil {
			return fmt.Errorf("failed to parse da start height: %w", err)
		}

		return config.Modify(ctx, node, "config/genesis.json", func(genDoc *map[string]interface{}) {
			evolveGenesis, ok := (*genDoc)["evolve"].(map[string]interface{})
			if !ok {
				return
			}

			evolveGenesis["da_start_height"] = daHeight
		})
	}
}

// addSingleSequencer modifies the genesis file to ensure single sequencer setup
func addSingleSequencer(ctx context.Context, node *cosmos.ChainNode) error {
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

	// set consensus validators to only include the first validator
	consensus, ok := genDoc["consensus"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("genesis.json does not contain a valid 'consensus' object")
	}
	consensus["validators"] = []map[string]interface{}{
		{
			"name":    "Rollkit Sequencer",
			"address": pubKey.Address(),
			"pub_key": map[string]interface{}{
				"type":  "tendermint/PubKeyEd25519",
				"value": pubKey.Bytes(),
			},
			"power": "5",
		},
	}

	updatedGenesis, err := json.MarshalIndent(genDoc, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal genesis: %w", err)
	}

	err = node.WriteFile(ctx, "config/genesis.json", updatedGenesis)
	if err != nil {
		return fmt.Errorf("failed to write genesis.json: %w", err)
	}

	return nil
}

// getPubKey returns the validator public key
func getPubKey(ctx context.Context, chainNode *cosmos.ChainNode) (crypto.PubKey, error) {
	keyJSONBytes, err := chainNode.ReadFile(ctx, "config/priv_validator_key.json")
	if err != nil {
		return nil, err
	}
	var pvKey cmprivval.FilePVKey
	if err = cmtjson.Unmarshal(keyJSONBytes, &pvKey); err != nil {
		return nil, fmt.Errorf("failed to unmarshal priv_validator_key.json: %w", err)
	}
	return pvKey.PubKey, nil
}

// fundBridgeNodeWallet funds the bridge node's DA wallet for blob submission
func (s *DockerIntegrationTestSuite) fundBridgeNodeWallet(ctx context.Context, bridgeNode *dataavailability.Node) {
	// hack to get around global, need to set the address prefix before use.
	sdk.GetConfig().SetBech32PrefixForAccount("celestia", "celestiapub")

	s.T().Log("Funding bridge node DA wallet...")
	fundingWallet := s.celestiaChain.GetFaucetWallet()

	// Get the bridge node's wallet
	bridgeWallet, err := bridgeNode.GetWallet()
	s.Require().NoError(err)

	// fund the bridge node wallet
	daFundingAmount := sdk.NewCoins(sdk.NewCoin("utia", math.NewInt(10_000_000)))
	err = s.sendFunds(ctx, s.celestiaChain, fundingWallet, bridgeWallet, daFundingAmount, 0)
	s.Require().NoError(err)
}

// getDANetworkParams returns the DA network parameters useful for creating an evolve chain
func (s *DockerIntegrationTestSuite) getDANetworkParams(ctx context.Context) (daAddress, authToken, daStartHeight string, err error) {
	authToken, err = s.bridgeNode.GetAuthToken()
	if err != nil {
		return
	}

	bridgeNetworkInfo, err := s.bridgeNode.GetNetworkInfo(ctx)
	if err != nil {
		return
	}
	daAddress = fmt.Sprintf("http://%s", bridgeNetworkInfo.Internal.RPCAddress())

	celestiaHeight, err := s.celestiaChain.Height(ctx)
	if err != nil {
		return
	}
	daStartHeight = fmt.Sprintf("%d", celestiaHeight)
	return
}

var validContainerCharsRE = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)

// SanitizeContainerName returns name with any
// invalid characters replaced with underscores.
// Subtests will include slashes, and there may be other
// invalid characters too.
func SanitizeContainerName(name string) string {
	return validContainerCharsRE.ReplaceAllLiteralString(name, "_")
}
