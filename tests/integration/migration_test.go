package integration_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"testing"

	"cosmossdk.io/math"
	"github.com/celestiaorg/tastora/framework/docker"
	"github.com/celestiaorg/tastora/framework/docker/container"
	"github.com/celestiaorg/tastora/framework/docker/cosmos"
	"github.com/celestiaorg/tastora/framework/testutil/config"
	"github.com/celestiaorg/tastora/framework/testutil/wait"
	"github.com/celestiaorg/tastora/framework/types"
	cometcfg "github.com/cometbft/cometbft/config"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module/testutil"
	"github.com/cosmos/cosmos-sdk/x/auth"
	"github.com/cosmos/cosmos-sdk/x/bank"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/suite"
)

// MigrationTestSuite tests the migration from cosmos-sdk to evolve
type MigrationTestSuite struct {
	DockerIntegrationTestSuite

	// chain instance that will be updated during migration
	chain *cosmos.Chain

	// pre-migration state for validation
	preMigrationTxHashes []string
	preMigrationBalances map[string]sdk.Coin
	testWallets          []*types.Wallet
}

func TestMigrationSuite(t *testing.T) {
	suite.Run(t, new(MigrationTestSuite))
}

func (s *MigrationTestSuite) SetupTest() {
	// set global address prefix for gm chain
	sdk.GetConfig().SetBech32PrefixForAccount("gm", "gmpub")

	// only setup docker infrastructure, not the chains
	s.dockerClient, s.networkID = docker.DockerSetup(s.T())

	s.preMigrationTxHashes = []string{}
	s.preMigrationBalances = make(map[string]sdk.Coin)
	s.testWallets = []*types.Wallet{}
}

// TestCosmosToEvolveMigration tests the complete migration workflow
func (s *MigrationTestSuite) TestCosmosToEvolveMigration() {
	ctx := context.Background()

	// Phase 1: Start with cosmos-sdk chain
	s.chain = s.createCosmosSDKChain(ctx)
	s.Require().NotNil(s.chain)

	err := s.chain.Start(ctx)
	s.Require().NoError(err)

	s.T().Log("Cosmos SDK chain started successfully")

	// Phase 2: Generate test transactions and record state
	s.generateTestTransactions(ctx)
	s.recordPreMigrationState(ctx)

	// Phase 3: Stop chain preserving volumes
	s.stopChainPreservingVolumes(ctx)

	// Phase 4: Setup DA network (before recreating with evolve image)
	s.setupDANetwork(ctx)

	// Phase 5: Execute migration (includes recreating with evolve image and DA config)
	s.executeMigrationCommand(ctx)

	// Phase 6: Validate migration and restart with full DA configuration
	s.restartEvolveChainWithDA(ctx)

	// Phase 7: Validate migration success
	s.validateMigrationSuccess(ctx)

	s.T().Log("Migration test completed successfully!")
}

// getCosmosSDKAppContainer returns the cosmos-sdk container image
func getCosmosSDKAppContainer() container.Image {
	imageRepo := os.Getenv("COSMOS_SDK_IMAGE_REPO")
	if imageRepo == "" {
		imageRepo = "cosmos-gm"
	}

	imageTag := os.Getenv("COSMOS_SDK_IMAGE_TAG")
	if imageTag == "" {
		imageTag = "test"
	}
	return container.NewImage(imageRepo, imageTag, "10001:10001")
}

// createCosmosSDKChain creates a cosmos-sdk chain without evolve modules
func (s *MigrationTestSuite) createCosmosSDKChain(ctx context.Context) *cosmos.Chain {
	testEncCfg := testutil.MakeTestEncodingConfig(auth.AppModuleBasic{}, bank.AppModuleBasic{})

	cosmosChain, err := cosmos.NewChainBuilder(s.T()).
		WithEncodingConfig(&testEncCfg).
		WithImage(getCosmosSDKAppContainer()).
		WithDenom("stake").
		WithDockerClient(s.dockerClient).
		WithName("evolve").
		WithDockerNetworkID(s.networkID).
		WithChainID("evolve-test").
		WithBech32Prefix("gm").
		WithBinaryName("gmd").
		WithGasPrices(fmt.Sprintf("0.00%s", "stake")).
		WithNodes(
			cosmos.NewChainNodeConfigBuilder().
				WithPostInit(func(ctx context.Context, node *cosmos.ChainNode) error {
					return config.Modify(ctx, node, "config/config.toml", func(cfg *cometcfg.Config) {
						cfg.TxIndex.Indexer = "kv"
					})
				}).
				Build(),
		).
		Build(ctx)

	s.Require().NoError(err)
	return cosmosChain
}

// generateTestTransactions creates test wallets and sends transactions
func (s *MigrationTestSuite) generateTestTransactions(ctx context.Context) {
	s.T().Log("Generating test transactions...")

	// create test wallets
	faucetWallet := s.chain.GetFaucetWallet()

	aliceWallet, err := s.chain.CreateWallet(ctx, "alice")
	s.Require().NoError(err)

	bobWallet, err := s.chain.CreateWallet(ctx, "bob")
	s.Require().NoError(err)

	s.testWallets = []*types.Wallet{faucetWallet, aliceWallet, bobWallet}

	// fund alice and bob from faucet
	fundAmount := sdk.NewCoins(sdk.NewCoin("stake", math.NewInt(1000000)))

	txResp, err := s.chain.BroadcastMessages(ctx, faucetWallet,
		banktypes.NewMsgSend(
			sdk.MustAccAddressFromBech32(faucetWallet.GetFormattedAddress()),
			sdk.MustAccAddressFromBech32(aliceWallet.GetFormattedAddress()),
			fundAmount))
	s.Require().NoError(err)
	s.Require().Equal(uint32(0), txResp.Code)
	s.preMigrationTxHashes = append(s.preMigrationTxHashes, txResp.TxHash)

	txResp, err = s.chain.BroadcastMessages(ctx, faucetWallet,
		banktypes.NewMsgSend(
			sdk.MustAccAddressFromBech32(faucetWallet.GetFormattedAddress()),
			sdk.MustAccAddressFromBech32(bobWallet.GetFormattedAddress()),
			fundAmount))
	s.Require().NoError(err)
	s.Require().Equal(uint32(0), txResp.Code)
	s.preMigrationTxHashes = append(s.preMigrationTxHashes, txResp.TxHash)

	// send transaction between alice and bob
	transferAmount := sdk.NewCoins(sdk.NewCoin("stake", math.NewInt(100000)))
	txResp, err = s.chain.BroadcastMessages(ctx, aliceWallet,
		banktypes.NewMsgSend(
			sdk.MustAccAddressFromBech32(aliceWallet.GetFormattedAddress()),
			sdk.MustAccAddressFromBech32(bobWallet.GetFormattedAddress()),
			transferAmount))
	s.Require().NoError(err)
	s.Require().Equal(uint32(0), txResp.Code)
	s.preMigrationTxHashes = append(s.preMigrationTxHashes, txResp.TxHash)

	s.T().Logf("Generated %d test transactions", len(s.preMigrationTxHashes))
}

// recordPreMigrationState stores current chain state for validation
func (s *MigrationTestSuite) recordPreMigrationState(ctx context.Context) {
	s.T().Log("Recording pre-migration state...")

	// get current balances for all test wallets
	networkInfo, err := s.chain.GetNode().GetNetworkInfo(ctx)
	s.Require().NoError(err)

	for _, wallet := range s.testWallets {
		balance, err := queryBankBalance(ctx,
			networkInfo.External.GRPCAddress(),
			wallet.GetFormattedAddress(),
			"stake")
		s.Require().NoError(err)
		s.preMigrationBalances[wallet.GetFormattedAddress()] = *balance
	}

	// record current block height
	height, err := s.chain.Height(ctx)
	s.Require().NoError(err)
	s.T().Logf("Pre-migration block height: %d", height)
	s.T().Logf("Recorded balances for %d wallets", len(s.preMigrationBalances))
}

// stopChainPreservingVolumes removes containers while preserving volumes
func (s *MigrationTestSuite) stopChainPreservingVolumes(ctx context.Context) {
	s.T().Log("Removing containers while preserving volumes...")

	// remove containers while preserving volumes
	err := s.chain.Remove(ctx, types.WithPreserveVolumes())
	s.Require().NoError(err)
	s.T().Log("Containers removed, volumes preserved")
}

// executeMigrationCommand recreates the chain with evolve image and runs migration via PostInit
func (s *MigrationTestSuite) executeMigrationCommand(ctx context.Context) {
	s.T().Log("Recreating chain with evolve image to run migration...")

	// recreate the chain with evolve image which has the migration command
	// migration will run automatically via PostInit
	s.recreateChainWithEvolveImage(ctx)

	s.T().Log("Migration command completed successfully")
}

// setupDANetwork starts the celestia DA network for evolve chain
func (s *MigrationTestSuite) setupDANetwork(ctx context.Context) {
	s.T().Log("Setting up DA network...")

	// reuse existing celestia setup from testsuite_test.go
	s.DockerIntegrationTestSuite.celestiaChain = s.CreateCelestiaChain(ctx)
	s.T().Log("Celestia app chain started")

	s.DockerIntegrationTestSuite.bridgeNode = s.CreateDANetwork(ctx)
	s.T().Log("Bridge node started")

	// reset bech32 prefix back to gm after DA network setup
	sdk.GetConfig().SetBech32PrefixForAccount("gm", "gmpub")
}

// recreateChainWithEvolveImage recreates the chain with evolve image and DA config, reusing existing volumes
func (s *MigrationTestSuite) recreateChainWithEvolveImage(ctx context.Context) {
	s.T().Log("Recreating chain with evolve image...")

	// get DA connection details
	authToken, err := s.DockerIntegrationTestSuite.bridgeNode.GetAuthToken()
	s.Require().NoError(err)

	bridgeNetworkInfo, err := s.DockerIntegrationTestSuite.bridgeNode.GetNetworkInfo(ctx)
	s.Require().NoError(err)
	bridgeRPCAddress := bridgeNetworkInfo.Internal.RPCAddress()

	daAddress := fmt.Sprintf("http://%s", bridgeRPCAddress)

	celestiaHeight, err := s.DockerIntegrationTestSuite.celestiaChain.Height(ctx)
	s.Require().NoError(err)
	daStartHeight := fmt.Sprintf("%d", celestiaHeight)

	testEncCfg := testutil.MakeTestEncodingConfig(auth.AppModuleBasic{}, bank.AppModuleBasic{})

	// recreate using same builder config as createCosmosSDKChain but with evolve image and DA config
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
		WithGasPrices(fmt.Sprintf("0.00%s", "stake")).
		WithSkipInit(true). // skip initalization as we have already done that previously.
		WithNodes(
			cosmos.NewChainNodeConfigBuilder().
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
				Build(),
		).
		Build(ctx)

	s.Require().NoError(err)

	// Run migration command
	s.T().Log("Running migration command...")
	stdout, stderr, err := evolveChain.GetNode().Exec(ctx, []string{
		"gmd", "evolve-migrate",
		"--home", evolveChain.GetNode().HomeDir(),
	}, nil)
	if err != nil {
		s.T().Logf("Migration stdout: %s", string(stdout))
		s.T().Logf("Migration stderr: %s", string(stderr))
		s.Require().NoError(fmt.Errorf("migration failed: %w", err))
	}
	s.T().Log("Migration command completed successfully")

	// Verify the ev_genesis.json file was created
	lsStdout, lsStderr, err := evolveChain.GetNode().Exec(ctx, []string{"ls", "-la", evolveChain.GetNode().HomeDir() + "/ev_genesis.json"}, nil)
	if err != nil {
		s.T().Logf("ls stdout: %s", string(lsStdout))
		s.T().Logf("ls stderr: %s", string(lsStderr))
		s.Require().NoError(err, "ev_genesis.json not found after migration")
	}

	// Set DA start height in ev_genesis.json
	s.T().Log("Setting DA start height in ev_genesis.json...")
	err = setDAStartHeightEV(daStartHeight)(ctx, evolveChain.GetNode())
	s.Require().NoError(err, "failed to set DA start height in ev_genesis.json")

	// Now start the chain with migrated state
	err = evolveChain.Start(ctx)
	s.Require().NoError(err)

	s.chain = evolveChain
	s.T().Log("Chain recreated with evolve image and migration completed")
}

// restartEvolveChainWithDA waits for the chain to sync and produce blocks
func (s *MigrationTestSuite) restartEvolveChainWithDA(ctx context.Context) {
	s.T().Log("Waiting for evolve chain to sync...")

	// wait for the chain to sync and produce a few blocks
	err := wait.ForBlocks(ctx, 3, s.chain)
	s.Require().NoError(err)
	s.T().Log("Evolve chain synced and producing blocks")
}

// validateMigrationSuccess verifies that migration worked correctly
func (s *MigrationTestSuite) validateMigrationSuccess(ctx context.Context) {
	s.T().Log("Validating migration success...")

	// verify old transactions are still queryable
	s.validateOldTransactions(ctx)

	// verify balances are preserved
	s.validateBalancesPreserved(ctx)

	// send new transactions on evolve chain
	s.sendNewTransactions(ctx)

	// verify chain is producing new blocks
	s.validateNewBlockProduction(ctx)

	s.T().Log("All migration validations passed!")
}

// validateOldTransactions verifies old transactions are still accessible
func (s *MigrationTestSuite) validateOldTransactions(ctx context.Context) {
	s.T().Log("Validating old transactions are accessible...")

	client, err := s.chain.GetNode().GetRPCClient()
	s.Require().NoError(err)

	for i, txHash := range s.preMigrationTxHashes {
		s.T().Logf("Querying transaction %d with hash: %s", i, txHash)

		// convert hex string to bytes
		txHashBytes, err := hex.DecodeString(txHash)
		s.Require().NoError(err, "Failed to decode tx hash %s", txHash)

		tx, err := client.Tx(ctx, txHashBytes, false)
		s.Require().NoError(err, "Failed to query transaction %d with hash %s", i, txHash)
		s.Require().NotNil(tx)
		s.T().Logf("✅ Old transaction %d (%s) successfully queried", i, txHash)
	}
}

// validateBalancesPreserved verifies account balances are maintained
func (s *MigrationTestSuite) validateBalancesPreserved(ctx context.Context) {
	s.T().Log("Validating balances are preserved...")

	networkInfo, err := s.chain.GetNode().GetNetworkInfo(ctx)
	s.Require().NoError(err)

	for address, expectedBalance := range s.preMigrationBalances {
		currentBalance, err := queryBankBalance(ctx,
			networkInfo.External.GRPCAddress(),
			address,
			"stake")
		s.Require().NoError(err)
		s.Require().Equal(expectedBalance.Amount, currentBalance.Amount,
			"Balance mismatch for address %s", address)
		s.T().Logf("✅ Balance preserved for %s: %s", address, currentBalance.Amount)
	}
}

// sendNewTransactions tests transaction functionality on evolve chain
func (s *MigrationTestSuite) sendNewTransactions(ctx context.Context) {
    s.T().Log("Sending new transactions on evolve chain...")

	if len(s.testWallets) < 2 {
		s.T().Skip("Not enough test wallets for new transactions")
		return
	}

	alice := s.testWallets[1] // assuming alice is at index 1
	bob := s.testWallets[2]   // assuming bob is at index 2

    // Use a per-node broadcaster to avoid relying on a faucet wallet
    broadcaster := cosmos.NewBroadcasterForNode(s.chain, s.chain.GetNode())

    transferAmount := sdk.NewCoins(sdk.NewCoin("stake", math.NewInt(50000)))
    txResp, err := broadcaster.BroadcastMessages(ctx, alice,
        banktypes.NewMsgSend(
            sdk.MustAccAddressFromBech32(alice.GetFormattedAddress()),
            sdk.MustAccAddressFromBech32(bob.GetFormattedAddress()),
            transferAmount))
	s.Require().NoError(err)
	s.Require().Equal(uint32(0), txResp.Code)

	s.T().Logf("✅ New transaction successful on evolve chain: %s", txResp.TxHash)
}

// validateNewBlockProduction verifies the chain is producing new blocks
func (s *MigrationTestSuite) validateNewBlockProduction(ctx context.Context) {
	s.T().Log("Validating new block production...")

	initialHeight, err := s.chain.Height(ctx)
	s.Require().NoError(err)

	// wait for a few new blocks using tastora's wait utility
	err = wait.ForBlocks(ctx, 3, s.chain)
	s.Require().NoError(err)

	finalHeight, err := s.chain.Height(ctx)
	s.Require().NoError(err)
	s.Require().Greater(finalHeight, initialHeight)

	s.T().Logf("✅ Chain producing new blocks: %d -> %d", initialHeight, finalHeight)
}

// addSingleSequencerWithTxIndex modifies the genesis file and enables tx indexing
func addSingleSequencerWithTxIndex(ctx context.Context, node *cosmos.ChainNode) error {
	// first call the existing addSingleSequencer function
	err := addSingleSequencer(ctx, node)
	if err != nil {
		return err
	}

	// enable tx indexing using config.Modify
	return config.Modify(ctx, node, "config/config.toml", func(cfg *cometcfg.Config) {
		// enable tx indexing
		cfg.TxIndex.Indexer = "kv"
	})
}
