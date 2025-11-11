package integration_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/celestiaorg/tastora/framework/docker"
	"github.com/celestiaorg/tastora/framework/docker/container"
	"github.com/celestiaorg/tastora/framework/docker/cosmos"
	"github.com/celestiaorg/tastora/framework/testutil/wait"
	"github.com/celestiaorg/tastora/framework/types"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module/testutil"
	"github.com/cosmos/cosmos-sdk/x/auth"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/bank"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	govmodule "github.com/cosmos/cosmos-sdk/x/gov"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	migrationmngr "github.com/evstack/ev-abci/modules/migrationmngr"
	migrationmngrtypes "github.com/evstack/ev-abci/modules/migrationmngr/types"
	"github.com/stretchr/testify/suite"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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

	migrationHeight uint64
}

func TestMigrationSuite(t *testing.T) {
	suite.Run(t, new(MigrationTestSuite))
}

func (s *MigrationTestSuite) SetupTest() {
	// set global address prefix for gm chain
	sdk.GetConfig().SetBech32PrefixForAccount("gm", "gmpub")

	// only setup docker infrastructure, not the chains
	s.dockerClient, s.networkID = docker.Setup(s.T())

	s.preMigrationTxHashes = []string{}
	s.preMigrationBalances = make(map[string]sdk.Coin)
	s.testWallets = []*types.Wallet{}
}

func (s *MigrationTestSuite) TearDownTest() {
	if s.chain != nil {
		if err := s.chain.Remove(context.Background()); err != nil {
			s.T().Logf("failed to remove chain: %s", err)
		}
	}
}

// TestCosmosToEvolveMigration tests the complete migration workflow
func (s *MigrationTestSuite) TestCosmosToEvolveMigration() {
	ctx := context.Background()
	t := s.T()

	t.Run("create_cosmos_sdk_chain", func(t *testing.T) {
		s.createAndStartSDKChain(ctx, 1)
	})

	t.Run("generate_test_transactions", func(t *testing.T) {
		s.generateTestTransactions(ctx)
	})

	t.Run("record_pre_migration_state", func(t *testing.T) {
		s.recordPreMigrationState(ctx)
	})

	t.Run("stop_sdk_chain", func(t *testing.T) {
		s.stopChainPreservingVolumes(ctx)
	})

	t.Run("setup_da_network", func(t *testing.T) {
		s.setupDANetwork(ctx)
	})

	t.Run("migrate_chain", func(t *testing.T) {
		s.recreateChainAndPerformMigration(ctx, 1)
	})

	t.Run("validate_migration_success", func(t *testing.T) {
		s.validateMigrationSuccess(ctx)
	})
}

// TestCosmosToEvolveMigration_MultiValidator_OnChainRequired verifies that when multiple
// validators exist, evolve-migrate requires the on-chain sequencer in migrationmngr state.
// This test asserts the command fails with a clear error until the sequencer is set on-chain.
//
// Running locally pre-requisites.
// from root of repo, build images with ibc disabled.
// - docker build . -f Dockerfile -t evolve-gm:latest --build-arg ENABLE_IBC=false
// - docker build . -f Dockerfile.cosmos-sdk -t cosmos-gm:test --build-arg ENABLE_IBC=false
func (s *MigrationTestSuite) TestCosmosToEvolveMigration_MultiValidator_GovSuccess() {
	ctx := context.Background()
	t := s.T()

	t.Run("create_cosmos_sdk_chain", func(t *testing.T) {
		s.createAndStartSDKChain(ctx, 3)
	})

	t.Run("generate_test_transactions", func(t *testing.T) {
		s.generateTestTransactions(ctx)
	})

	t.Run("record_pre_migration_state", func(t *testing.T) {
		s.recordPreMigrationState(ctx)
	})

	t.Run("submit_migration_proposal_and_vote", func(t *testing.T) {
		s.submitMigrationProposalAndVote(ctx)
	})

	t.Run("wait_for_halt", func(t *testing.T) {
		s.waitForMigrationHalt(ctx)
	})

	t.Run("stop_sdk_chain", func(t *testing.T) {
		s.stopChainPreservingVolumes(ctx)
	})

	t.Run("setup_da_network", func(t *testing.T) {
		s.setupDANetwork(ctx)
	})

	t.Run("migrate_chain", func(t *testing.T) {
		s.recreateChainAndPerformMigration(ctx, 3)
	})

	t.Run("validate_migration_success", func(t *testing.T) {
		s.validateMigrationSuccess(ctx)
	})
}

// submitMigrationProposalAndVote prepares and submits a gov proposal to migrate,
// votes YES from faucet, and waits briefly for execution.
func (s *MigrationTestSuite) submitMigrationProposalAndVote(ctx context.Context) {
	networkInfo, err := s.chain.GetNode().GetNetworkInfo(ctx)
	s.Require().NoError(err)

	conn, err := grpc.NewClient(networkInfo.External.GRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	s.Require().NoError(err)
	defer func() {
		_ = conn.Close()
	}()

	curHeight, err := s.chain.Height(ctx)
	s.Require().NoError(err)
	// Schedule migration sufficiently in the future to allow proposal
	// submission, deposits, and validator votes to complete.
	migrateAt := uint64(curHeight + 30)
	s.migrationHeight = migrateAt
	s.T().Logf("Current height: %d, Migration scheduled for height: %d", curHeight, migrateAt)

	faucet := s.chain.GetFaucetWallet()
	proposer := faucet.GetFormattedAddress()

	msg := &migrationmngrtypes.MsgMigrateToEvolve{
		Authority:   authtypes.NewModuleAddress(govtypes.ModuleName).String(),
		BlockHeight: migrateAt,
		Sequencer: migrationmngrtypes.Sequencer{
			Name:            "sequencer-node-1",
			ConsensusPubkey: s.getSequencerPubKey(ctx, conn),
		},
	}

	propMsg, err := govv1.NewMsgSubmitProposal(
		[]sdk.Msg{msg},
		sdk.NewCoins(sdk.NewInt64Coin("stake", 10_000_000_000)), // deposit
		proposer,
		"",                          // metadata
		"Migrate to Evolve",         // title
		"Set sequencer and migrate", // summary
		false,                       // expedited
	)
	s.Require().NoError(err)

	prop, err := s.chain.SubmitAndVoteOnGovV1Proposal(ctx, propMsg, govv1.VoteOption_VOTE_OPTION_YES)
	s.Require().NoError(err)
	s.Require().Equal(govv1.ProposalStatus_PROPOSAL_STATUS_PASSED, prop.Status, "proposal did not pass")
}

// getSequencerPubKey fetches the intended sequencer's consensus pubkey Any from the chain.
func (s *MigrationTestSuite) getSequencerPubKey(ctx context.Context, conn *grpc.ClientConn) *codectypes.Any {
	// Determine the intended sequencer to align with the node that will run
	// in aggregator mode (validator index 0 when we restart as evolve).
	// We fetch the operator (valoper) address from the first validator node's keyring,
	// then find the matching validator on-chain to get its consensus pubkey Any.

	stakeQC := stakingtypes.NewQueryClient(conn)
	valsResp, err := stakeQC.Validators(ctx, &stakingtypes.QueryValidatorsRequest{})
	s.Require().NoError(err)
	s.Require().GreaterOrEqual(len(valsResp.Validators), 1, "need at least one validator")

	val0 := s.chain.GetNode()
	stdout, stderr, err := val0.Exec(ctx, []string{
		"gmd", "keys", "show", "--address", "validator",
		"--home", val0.HomeDir(),
		"--keyring-backend", "test",
		"--bech", "val",
	}, nil)
	s.Require().NoError(err, "failed to get valoper address from node 0: %s", stderr)
	val0Oper := string(bytes.TrimSpace(stdout))

	// Find matching validator by operator address and use its consensus pubkey Any
	var seqPubkeyAny *codectypes.Any
	for _, v := range valsResp.Validators {
		if v.OperatorAddress == val0Oper {
			seqPubkeyAny = v.ConsensusPubkey
			break
		}
	}
	s.Require().NotNil(seqPubkeyAny, "could not find validator matching val-0 operator address %s", val0Oper)
	return seqPubkeyAny
}

// waitForMigrationHalt waits until the chain reaches the halt height
// and verifies that block production stops.
func (s *MigrationTestSuite) waitForMigrationHalt(ctx context.Context) {
	haltHeight := int64(s.migrationHeight + 2)

	// wait until we reach the halt height
	err := wait.ForCondition(ctx, 2*time.Minute, 5*time.Second, func() (bool, error) {
		h, err := s.chain.Height(ctx)
		if err != nil {
			return false, err
		}
		return h >= haltHeight, nil
	})
	s.Require().NoError(err, "chain did not reach halt height %d", haltHeight)

	baseline, err := s.chain.Height(ctx)
	s.Require().NoError(err)

	// wait longer than a block time to ensure halt
	time.Sleep(10 * time.Second)

	// verify height hasn't increased
	current, err := s.chain.Height(ctx)
	if err == nil {
		s.Require().Equal(baseline, current, "chain did not halt, height increased from %d to %d", baseline, current)
	}
}

func (s *MigrationTestSuite) createAndStartSDKChain(ctx context.Context, numNodes int) {
	s.chain = s.createCosmosSDKChain(ctx, numNodes)
	s.Require().NotNil(s.chain)

	err := s.chain.Start(ctx)
	s.Require().NoError(err)
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

	s.recordTestTransaction(ctx, faucetWallet, aliceWallet, math.NewInt(1_000_000))
	s.recordTestTransaction(ctx, faucetWallet, bobWallet, math.NewInt(1_000_000))
	s.recordTestTransaction(ctx, aliceWallet, bobWallet, math.NewInt(10_000))

	s.T().Logf("Generated %d test transactions", len(s.preMigrationTxHashes))
}

// recordPreMigrationState stores current chain state for validation
func (s *MigrationTestSuite) recordPreMigrationState(ctx context.Context) {
	s.T().Log("Recording pre-migration state...")

	// get current balances for all test wallets
	networkInfo, err := s.chain.GetNode().GetNetworkInfo(ctx)
	s.Require().NoError(err)

	for _, wallet := range s.testWallets {
		balance, err := queryBankBalance(ctx, networkInfo.External.GRPCAddress(), wallet.GetFormattedAddress(), "stake")
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

// setupDANetwork starts the celestia DA network for evolve chain
func (s *MigrationTestSuite) setupDANetwork(ctx context.Context) {
	s.T().Log("Setting up DA network...")

	// reuse existing celestia setup from testsuite_test.go
	s.celestiaChain = s.CreateCelestiaChain(ctx)
	s.T().Log("Celestia app chain started")

	s.bridgeNode = s.CreateDANetwork(ctx)
	s.T().Log("Bridge node started")

	// reset bech32 prefix back to gm after DA network setup
	sdk.GetConfig().SetBech32PrefixForAccount("gm", "gmpub")
}

// recreateChainAndPerformMigration recreates the chain with evolve image and DA config, reusing existing volumes
func (s *MigrationTestSuite) recreateChainAndPerformMigration(ctx context.Context, numNodes int) {
	s.T().Log("Recreating chain with evolve image...")

	// get DA connection details
	authToken, err := s.bridgeNode.GetAuthToken()
	s.Require().NoError(err)

	bridgeNetworkInfo, err := s.bridgeNode.GetNetworkInfo(ctx)
	s.Require().NoError(err)
	bridgeRPCAddress := bridgeNetworkInfo.Internal.RPCAddress()

	daAddress := fmt.Sprintf("http://%s", bridgeRPCAddress)

	// recreate using same builder config as createCosmosSDKChain but with evolve image and DA config
	evolveChain := s.createEvolveChain(ctx, authToken, daAddress, numNodes)

	s.performMigration(ctx, evolveChain)
	s.T().Log("Migration command completed successfully")

	// Now start the chain with migrated state
	err = evolveChain.Start(ctx)
	s.Require().NoError(err)

	// overwrite the chain variable so that it now points to the evolve chain.
	s.chain = evolveChain
	s.T().Log("Chain recreated with evolve image and migration completed")
}

// validateMigrationSuccess verifies that migration worked correctly
func (s *MigrationTestSuite) validateMigrationSuccess(ctx context.Context) {
	s.T().Log("Validating migration success...")

	s.validateOldTransactions(ctx)

	s.validateBalancesPreserved(ctx)

	s.sendNewTransactions(ctx)

	s.validateNewBlockProduction(ctx)

	s.T().Log("All migration validations passed!")
}

// getCosmosChainBuilder returns a chain builder for cosmos-sdk chain
func (s *MigrationTestSuite) getCosmosChainBuilder(numNodes int) *cosmos.ChainBuilder {
	// Include gov + migrationmngr so client-side encoding can marshal
	// gov v1 MsgSubmitProposal and migrationmngr MsgMigrateToEvolve
	testEncCfg := testutil.MakeTestEncodingConfig(
		auth.AppModuleBasic{},
		bank.AppModuleBasic{},
		govmodule.AppModuleBasic{},
		migrationmngr.AppModuleBasic{},
	)

	var nodes []cosmos.ChainNodeConfig
	for i := 0; i < numNodes; i++ {
		nodes = append(nodes, cosmos.NewChainNodeConfigBuilder().Build())
	}

	return cosmos.NewChainBuilder(s.T()).
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
		WithNodes(nodes...)
}

// createCosmosSDKChain creates a cosmos-sdk chain without evolve modules
func (s *MigrationTestSuite) createCosmosSDKChain(ctx context.Context, numNodes int) *cosmos.Chain {
	cosmosChain, err := s.getCosmosChainBuilder(numNodes).Build(ctx)
	s.Require().NoError(err)
	return cosmosChain
}

// createEvolveChain creates a chain with evolve chain which alligns with the existing cosmos sdk chain.
// the image is changed, and initialization is skipped as we are reusing existing volumes.
func (s *MigrationTestSuite) createEvolveChain(ctx context.Context, authToken, daAddress string, numNodes int) *cosmos.Chain {
	sequencer := cosmos.NewChainNodeConfigBuilder().
		WithPostInit(writePasshraseFile("12345678")). // ensure the passhrase file exists.
		WithAdditionalStartArgs(
			"--evnode.node.aggregator",
			// the filepath is determined based on the name in the chain builder.
			"--evnode.signer.passphrase_file", fmt.Sprintf("/var/cosmos-chain/evolve/%s", passphraseFile),
			"--evnode.da.address", daAddress,
			"--evnode.da.gas_price", "0.000001",
			"--evnode.da.auth_token", authToken,
			"--evnode.rpc.address", "0.0.0.0:7331",
			"--evnode.da.namespace", "ev-header",
			"--evnode.da.data_namespace", "ev-data",
			"--evnode.p2p.listen_address", "/ip4/0.0.0.0/tcp/36656",
			"--log_level", "*:info",
		).
		Build()

	nodeConfigs := []cosmos.ChainNodeConfig{sequencer}
	for i := 1; i < numNodes; i++ {
		nodeConfigs = append(nodeConfigs, cosmos.NewChainNodeConfigBuilder().WithAdditionalStartArgs(
			"--evnode.da.address", daAddress,
			"--evnode.da.gas_price", "0.000001",
			"--evnode.da.auth_token", authToken,
			"--evnode.rpc.address", "0.0.0.0:7331",
			"--evnode.da.namespace", "ev-header",
			"--evnode.da.data_namespace", "ev-data",
			"--log_level", "*:debug",
		).Build())
	}

	evolveChain, err := s.getCosmosChainBuilder(numNodes).
		WithImage(getEvolveAppContainer()).
		WithSkipInit(true). // skip initalization as we have already done that previously when it was an sdk chain.
		WithNodes(nodeConfigs...).
		Build(ctx)
	s.Require().NoError(err)
	return evolveChain
}

// recordTestTransaction sends a transaction and records its hash
func (s *MigrationTestSuite) recordTestTransaction(ctx context.Context, fromWallet, destWallet *types.Wallet, amount math.Int) {
	fundAmount := sdk.NewCoins(sdk.NewCoin("stake", amount))
	txResp, err := s.chain.BroadcastMessages(ctx, fromWallet,
		banktypes.NewMsgSend(
			sdk.MustAccAddressFromBech32(fromWallet.GetFormattedAddress()),
			sdk.MustAccAddressFromBech32(destWallet.GetFormattedAddress()),
			fundAmount))
	s.Require().NoError(err)
	s.Require().Equal(uint32(0), txResp.Code)
	s.preMigrationTxHashes = append(s.preMigrationTxHashes, txResp.TxHash)
}

// performMigration runs the migration command on the given chain node.
func (s *MigrationTestSuite) performMigration(ctx context.Context, chain *cosmos.Chain) {
	for _, node := range chain.GetNodes() {
		_, stderr, err := node.Exec(ctx, []string{
			"gmd", "evolve-migrate",
			"--home", chain.GetNode().HomeDir(),
		}, nil)
		s.Require().NoError(err, "migration command failed: %s", stderr)
	}
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
		s.T().Logf("Old transaction %d (%s) successfully queried", i, txHash)
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
		s.T().Logf("Balance preserved for %s: %s", address, currentBalance.Amount)
	}
}

// sendNewTransactions tests transaction functionality on evolve chain
func (s *MigrationTestSuite) sendNewTransactions(ctx context.Context) {
	s.T().Log("Sending new transactions on evolve chain...")

	alice := s.testWallets[1]
	bob := s.testWallets[2]

	broadcaster := cosmos.NewBroadcaster(s.chain)
	transferAmount := sdk.NewCoins(sdk.NewCoin("stake", math.NewInt(50000)))
	txResp, err := broadcaster.BroadcastMessages(ctx, alice,
		banktypes.NewMsgSend(
			sdk.MustAccAddressFromBech32(alice.GetFormattedAddress()),
			sdk.MustAccAddressFromBech32(bob.GetFormattedAddress()),
			transferAmount))
	s.Require().NoError(err)
	s.Require().Equal(uint32(0), txResp.Code)

	s.T().Logf("New transaction successful on evolve chain: %s", txResp.TxHash)
}

// validateNewBlockProduction verifies the chain is producing new blocks
func (s *MigrationTestSuite) validateNewBlockProduction(ctx context.Context) {
	s.T().Log("Validating new block production...")

	initialHeight, err := s.chain.Height(ctx)
	s.Require().NoError(err)

	err = wait.ForBlocks(ctx, 3, s.chain)
	s.Require().NoError(err)

	finalHeight, err := s.chain.Height(ctx)
	s.Require().NoError(err)
	s.Require().Greater(finalHeight, initialHeight)

	s.T().Logf("Chain producing new blocks: %d -> %d", initialHeight, finalHeight)
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
