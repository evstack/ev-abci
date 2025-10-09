package integration_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

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
	s.dockerClient, s.networkID = docker.DockerSetup(s.T())

	s.preMigrationTxHashes = []string{}
	s.preMigrationBalances = make(map[string]sdk.Coin)
	s.testWallets = []*types.Wallet{}
}

func (s *MigrationTestSuite) TearDownTest() {
	if s.chain != nil {
		s.T().Log("tearing down chain...")
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
		s.createAndStartSDKChain(ctx)
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
		s.recreateChainAndPerformMigration(ctx)
	})

	t.Run("validate_migration_success", func(t *testing.T) {
		s.validateMigrationSuccess(ctx)
	})
}

// TestCosmosToEvolveMigration_MultiValidator_OnChainRequired verifies that when multiple
// validators exist, evolve-migrate requires the on-chain sequencer in migrationmngr state.
// This test asserts the command fails with a clear error until the sequencer is set on-chain.
func (s *MigrationTestSuite) TestCosmosToEvolveMigration_MultiValidator_GovSuccess() {
	ctx := context.Background()
	t := s.T()

	t.Run("create_cosmos_sdk_chain", func(t *testing.T) {
		s.createAndStartSDKChain(ctx)
	})
	//
	//t.Run("generate_test_transactions", func(t *testing.T) {
	//	s.generateTestTransactions(ctx)
	//})
	//
	//t.Run("record_pre_migration_state", func(t *testing.T) {
	//	s.recordPreMigrationState(ctx)
	//})

	t.Run("submit_migration_proposal_and_vote", func(t *testing.T) {
		s.submitMigrationProposalAndVote(ctx)
	})

	t.Run("halt_wait", func(t *testing.T) {
		s.waitForMigrationHalt(ctx)
	})

	t.Run("stop_sdk_chain", func(t *testing.T) {
		s.stopChainPreservingVolumes(ctx)
	})

	t.Run("setup_da_network", func(t *testing.T) {
		s.setupDANetwork(ctx)
	})

	t.Run("migrate_chain", func(t *testing.T) {
		s.recreateChainAndPerformMigration(ctx)
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

	conn, err := grpc.Dial(networkInfo.External.GRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	s.Require().NoError(err)
	defer conn.Close()

	curHeight, err := s.chain.Height(ctx)
	s.Require().NoError(err)
	// Schedule migration sufficiently in the future to allow proposal
	// submission, deposits, and validator votes to complete.
	migrateAt := uint64(curHeight + 30)
	s.migrationHeight = migrateAt

	// verify gov params are correct
	govQC := govv1.NewQueryClient(conn)
	params, err := govQC.Params(ctx, &govv1.QueryParamsRequest{ParamsType: "voting"})
	s.Require().NoError(err)
	s.T().Logf("Gov params: %+v", params)

	faucet := s.chain.GetFaucetWallet()
	proposer := faucet.GetFormattedAddress()

	// Get sequencer pubkey from staking module to ensure it matches on-chain state
	// this is the source of truth that migration.go will use
	stakeQC := stakingtypes.NewQueryClient(conn)
	valsResp, err := stakeQC.Validators(ctx, &stakingtypes.QueryValidatorsRequest{})
	s.Require().NoError(err)
	s.Require().GreaterOrEqual(len(valsResp.Validators), 1, "need at least one validator")

	// use the first validator's pubkey from staking - this is the on-chain source of truth
	seqPubkeyAny := valsResp.Validators[0].ConsensusPubkey

	msg := &migrationmngrtypes.MsgMigrateToEvolve{
		Authority:   authtypes.NewModuleAddress(govtypes.ModuleName).String(),
		BlockHeight: migrateAt,
		Sequencer: migrationmngrtypes.Sequencer{
			Name:            "sequencer-node-1",
			ConsensusPubkey: seqPubkeyAny,
		},
		Attesters: nil,
	}

	deposit := sdk.NewCoins(sdk.NewInt64Coin("stake", 1_000_000))
	propMsg, err := govv1.NewMsgSubmitProposal(
		[]sdk.Msg{msg},
		deposit,
		proposer,
		"",                          // metadata
		"Migrate to Evolve",         // title
		"Set sequencer and migrate", // summary
		false,                       // expedited
	)
	s.Require().NoError(err)

	bc := cosmos.NewBroadcaster(s.chain)
	submitResp, err := bc.BroadcastMessages(ctx, faucet, propMsg)
	s.Require().NoError(err, submitResp.RawLog)
	s.Require().Equal(uint32(0), submitResp.Code, submitResp.RawLog)

	// Discover proposal ID via gRPC (any status) with a short retry to allow indexing
	var proposalID uint64
	for i := 0; i < 10 && proposalID == 0; i++ {
		props, err := govQC.Proposals(ctx, &govv1.QueryProposalsRequest{ProposalStatus: govv1.ProposalStatus_PROPOSAL_STATUS_UNSPECIFIED})
		s.Require().NoError(err)
		for _, p := range props.Proposals {
			if p.Id > proposalID {
				proposalID = p.Id
			}
		}
		if proposalID == 0 {
			time.Sleep(1 * time.Second)
		}
	}
	s.Require().True(proposalID > 0, "no proposals found after submission")

	// Vote YES with all validators by discovering local key names in each node
	s.voteYesAllValidators(ctx, proposalID)

	// wait for the proposal to finish voting period and be executed
	// poll every second for up to 30 seconds
	var finalProp *govv1.QueryProposalResponse
	err = wait.ForCondition(ctx, time.Minute*2, time.Second*5, func() (bool, error) {
		prop, err := govQC.Proposal(ctx, &govv1.QueryProposalRequest{ProposalId: proposalID})
		if err != nil {
			return false, err
		}

		s.T().Logf("Proposal %d status: %s (checking...)", proposalID, prop.Proposal.Status)

		// keep waiting while in deposit or voting period
		if prop.Proposal.Status == govv1.ProposalStatus_PROPOSAL_STATUS_DEPOSIT_PERIOD ||
			prop.Proposal.Status == govv1.ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD {
			return false, nil
		}

		// proposal is in a final state (passed, rejected, or failed)
		finalProp = prop
		return true, nil
	})
	s.Require().NoError(err, "timeout waiting for proposal to reach final state")

	// log proposal details for debugging
	s.T().Logf("Proposal %d final status: %s", proposalID, finalProp.Proposal.Status)
	s.T().Logf("Proposal details: final_tally_result=%+v, submit_time=%s, voting_end_time=%s",
		finalProp.Proposal.FinalTallyResult, finalProp.Proposal.SubmitTime, finalProp.Proposal.VotingEndTime)

	// query votes to debug
	votesResp, err := govQC.Votes(ctx, &govv1.QueryVotesRequest{ProposalId: proposalID})
	s.Require().NoError(err)
	s.T().Logf("Total votes: %d", len(votesResp.Votes))
	for i, vote := range votesResp.Votes {
		s.T().Logf("Vote %d: voter=%s, options=%+v", i, vote.Voter, vote.Options)
	}

	s.Require().Equal(govv1.ProposalStatus_PROPOSAL_STATUS_PASSED, finalProp.Proposal.Status, "proposal should have passed")
}

// setGovFastParams reduces gov timings/thresholds for test speed.
func setGovFastParams(ctx context.Context, node *cosmos.ChainNode) error {
	return config.Modify(ctx, node, "config/genesis.json", func(genDoc *map[string]interface{}) {
		appState, ok := (*genDoc)["app_state"].(map[string]interface{})
		if !ok {
			return
		}
		govState, ok := appState["gov"].(map[string]interface{})
		if !ok {
			return
		}
		if params, ok := govState["params"].(map[string]interface{}); ok {
			params["voting_period"] = "10s"
			params["max_deposit_period"] = "10s"
			params["min_deposit"] = []map[string]interface{}{{"denom": "stake", "amount": "1"}}
			params["quorum"] = "0.000000000000000000"
			params["threshold"] = "0.200000000000000000"
			params["veto_threshold"] = "0.334000000000000000"
			return
		}
		if dp, ok := govState["deposit_params"].(map[string]interface{}); ok {
			dp["min_deposit"] = []map[string]interface{}{{"denom": "stake", "amount": "1"}}
			dp["max_deposit_period"] = "10s"
		}
		if vp, ok := govState["voting_params"].(map[string]interface{}); ok {
			vp["voting_period"] = "10s"
		}
		if tp, ok := govState["tally_params"].(map[string]interface{}); ok {
			tp["quorum"] = "0.000000000000000000"
			tp["threshold"] = "0.200000000000000000"
			tp["veto_threshold"] = "0.334000000000000000"
		}
	})
}

// voteYesAllValidators iterates nodes, finds a local key name, and votes YES from each node.
func (s *MigrationTestSuite) voteYesAllValidators(ctx context.Context, proposalID uint64) {
	for i, n := range s.chain.GetNodes() {
		node := n.(*cosmos.ChainNode)

		// Get the node's internal RPC address and use it explicitly
		netInfo, err := node.GetNetworkInfo(ctx)
		s.Require().NoError(err)
		rpcAddr := fmt.Sprintf("tcp://%s", netInfo.Internal.RPCAddress())

		// Vote from the validator key explicitly
		from := "validator"

		stdout, stderr, err := node.Exec(ctx, []string{
			"gmd", "tx", "gov", "vote", strconv.FormatUint(proposalID, 10), "yes",
			"--from", from,
			"--home", node.HomeDir(),
			"--chain-id", s.chain.GetChainID(),
			"--keyring-backend", "test",
			"--node", rpcAddr,
			"--yes",
		}, nil)
		s.T().Logf("Vote from validator %d: stdout=%s, stderr=%s", i, stdout, stderr)
		s.Require().NoError(err, "vote tx failed from %s: %s", from, stderr)
	}
}

// waitForMigrationHalt waits until the chain reaches at least the migration
// height threshold and then verifies that block production halts shortly after.
// The test intends for the crash/halt, so this passes when halt is observed.
func (s *MigrationTestSuite) waitForMigrationHalt(ctx context.Context) {
	// Minimum height at which halt can occur in non-IBC mode.
	minExpected := int64(s.migrationHeight + 2)

	// Wait until we reach at least the minimum expected height.
	reachDeadline := time.Now().Add(2 * time.Minute)
	var baseline int64 = -1
	for time.Now().Before(reachDeadline) {
		h, err := s.chain.Height(ctx)
		if err == nil && h >= minExpected {
			baseline = h
			break
		}
		time.Sleep(1 * time.Second)
	}
	s.Require().True(baseline >= minExpected, "chain did not reach minimum expected halt height")

	// After reaching the baseline, be lenient: allow height to move a little,
	// but require it to stop increasing for a sustained window (5s) within
	// an overall timeout (20s).
	overallDeadline := time.Now().Add(20 * time.Second)
	stableSince := time.Time{}
	last := baseline

	for time.Now().Before(overallDeadline) {
		h, err := s.chain.Height(ctx)
		if err != nil {
			// Treat RPC errors as a signal of halt progress; count as stable.
			if stableSince.IsZero() {
				stableSince = time.Now()
			}
		} else {
			if h > last {
				// Height moved; reset stability timer and advance baseline.
				last = h
				stableSince = time.Time{}
			} else {
				// Height did not move.
				if stableSince.IsZero() {
					stableSince = time.Now()
				}
			}
		}

		if !stableSince.IsZero() && time.Since(stableSince) >= 5*time.Second {
			// Consider halted: height remained stable for 5s.
			return
		}

		time.Sleep(500 * time.Millisecond)
	}

	s.Require().FailNowf("chain did not halt within expected window", "last height=%d (min expected=%d)", last, minExpected)
}

func (s *MigrationTestSuite) createAndStartSDKChain(ctx context.Context) {
	s.chain = s.createCosmosSDKChain(ctx)
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
func (s *MigrationTestSuite) recreateChainAndPerformMigration(ctx context.Context) {
	s.T().Log("Recreating chain with evolve image...")

	// get DA connection details
	authToken, err := s.bridgeNode.GetAuthToken()
	s.Require().NoError(err)

	bridgeNetworkInfo, err := s.bridgeNode.GetNetworkInfo(ctx)
	s.Require().NoError(err)
	bridgeRPCAddress := bridgeNetworkInfo.Internal.RPCAddress()

	daAddress := fmt.Sprintf("http://%s", bridgeRPCAddress)

	// recreate using same builder config as createCosmosSDKChain but with evolve image and DA config
	evolveChain := s.createEvolveChain(ctx, authToken, daAddress)

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
func (s *MigrationTestSuite) getCosmosChainBuilder() *cosmos.ChainBuilder {
	// Include gov + migrationmngr so client-side encoding can marshal
	// gov v1 MsgSubmitProposal and migrationmngr MsgMigrateToEvolve
	testEncCfg := testutil.MakeTestEncodingConfig(
		auth.AppModuleBasic{},
		bank.AppModuleBasic{},
		govmodule.AppModuleBasic{},
		migrationmngr.AppModuleBasic{},
	)
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
		WithPostInit(
			setGovFastParams,
			func(ctx context.Context, node *cosmos.ChainNode) error {
				return config.Modify(ctx, node, "config/config.toml", func(cfg *cometcfg.Config) {
					cfg.TxIndex.Indexer = "kv"
				})
			}).
		WithNodes(
			cosmos.NewChainNodeConfigBuilder().Build(),
			cosmos.NewChainNodeConfigBuilder().Build(),
			cosmos.NewChainNodeConfigBuilder().Build(),
		)
}

// createCosmosSDKChain creates a cosmos-sdk chain without evolve modules
func (s *MigrationTestSuite) createCosmosSDKChain(ctx context.Context) *cosmos.Chain {
	cosmosChain, err := s.getCosmosChainBuilder().Build(ctx)
	s.Require().NoError(err)
	return cosmosChain
}

// createEvolveChain creates a chain with evolve chain which alligns with the existing cosmos sdk chain.
// the image is changed, and initialization is skipped as we are reusing existing volumes.
func (s *MigrationTestSuite) createEvolveChain(ctx context.Context, authToken, daAddress string) *cosmos.Chain {
	evolveChain, err := s.getCosmosChainBuilder().
		WithImage(getEvolveAppContainer()).
		WithSkipInit(true). // skip initalization as we have already done that previously when it was an sdk chain..
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
			cosmos.NewChainNodeConfigBuilder().WithAdditionalStartArgs(
				"--evnode.da.address", daAddress,
				"--evnode.da.gas_price", "0.000001",
				"--evnode.da.auth_token", authToken,
				"--evnode.rpc.address", "0.0.0.0:7331",
				"--evnode.da.namespace", "ev-header",
				"--evnode.da.data_namespace", "ev-data",
				"--log_level", "*:debug",
			).Build(),
			cosmos.NewChainNodeConfigBuilder().WithAdditionalStartArgs(
				"--evnode.da.address", daAddress,
				"--evnode.da.gas_price", "0.000001",
				"--evnode.da.auth_token", authToken,
				"--evnode.rpc.address", "0.0.0.0:7331",
				"--evnode.da.namespace", "ev-header",
				"--evnode.da.data_namespace", "ev-data",
				"--log_level", "*:debug",
			).Build(),
		).
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
	_, stderr, err := chain.GetNode().Exec(ctx, []string{
		"gmd", "evolve-migrate",
		"--home", chain.GetNode().HomeDir(),
	}, nil)

	s.Require().NoError(err, "migration command failed: %s", stderr)
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
