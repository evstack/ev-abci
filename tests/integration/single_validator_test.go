package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/celestiaorg/tastora/framework/docker"
	"github.com/celestiaorg/tastora/framework/docker/cosmos"
	"github.com/celestiaorg/tastora/framework/docker/ibc"
	"github.com/celestiaorg/tastora/framework/docker/ibc/relayer"
	"github.com/celestiaorg/tastora/framework/testutil/wait"
	"github.com/celestiaorg/tastora/framework/types"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module/testutil"
	"github.com/cosmos/cosmos-sdk/x/auth"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/bank"
	govmodule "github.com/cosmos/cosmos-sdk/x/gov"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	ibctransfer "github.com/cosmos/ibc-go/v8/modules/apps/transfer"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	migrationmngr "github.com/evstack/ev-abci/modules/migrationmngr"
	migrationmngrtypes "github.com/evstack/ev-abci/modules/migrationmngr/types"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap/zaptest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// SingleValidatorSuite tests migration from N validators to 1 validator on CometBFT
type SingleValidatorSuite struct {
	DockerIntegrationTestSuite

	// chain instance that will undergo migration
	chain *cosmos.Chain

	// IBC counterparty chain
	ibcCounterpartyChain *cosmos.Chain
	hermes               *relayer.Hermes
	ibcConnection        ibc.Connection
	ibcChannel           ibc.Channel
	ibcDenom             string
	preMigrationIBCBal   sdk.Coin

	migrationHeight uint64
}

func TestSingleValSuite(t *testing.T) {
	suite.Run(t, new(SingleValidatorSuite))
}

func (s *SingleValidatorSuite) SetupTest() {
	sdk.GetConfig().SetBech32PrefixForAccount("gm", "gmpub")

	s.dockerClient, s.networkID = docker.Setup(s.T())
	s.logger = zaptest.NewLogger(s.T())
}

func (s *SingleValidatorSuite) TearDownTest() {
	if s.chain != nil {
		if err := s.chain.Remove(context.Background()); err != nil {
			s.T().Logf("failed to remove chain: %s", err)
		}
	}
	if s.ibcCounterpartyChain != nil {
		if err := s.ibcCounterpartyChain.Remove(context.Background()); err != nil {
			s.T().Logf("failed to remove IBC counterparty chain: %s", err)
		}
	}
}

// TestNTo1Migration tests reducing N validators to 1 validator while staying on CometBFT
//
// Running locally pre-requisites:
// from root of repo, build images with ibc enabled.
// - docker build . -f Dockerfile.cosmos-sdk -t cosmos-gm:test --build-arg ENABLE_IBC=true
func (s *SingleValidatorSuite) TestNTo1() {
	ctx := context.Background()
	t := s.T()

	t.Run("create_chains", func(t *testing.T) {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			s.chain = s.createAndStartChain(ctx, 3, "gm-1", "gm-1")
		}()

		go func() {
			defer wg.Done()
			s.ibcCounterpartyChain = s.createAndStartChain(ctx, 1, "gm-2", "gm-2")
		}()

		wg.Wait()
	})

	t.Run("setup_ibc_connection", func(t *testing.T) {
		s.setupIBCConnection(ctx)
	})

	t.Run("perform_ibc_transfers", func(t *testing.T) {
		s.performIBCTransfers(ctx)
	})

	t.Run("submit_migration_proposal", func(t *testing.T) {
		s.submitSingleValidatorMigrationProposal(ctx)
	})

	t.Run("wait_for_migration_completion", func(t *testing.T) {
		s.waitForMigrationCompletion(ctx)
	})

	t.Run("validate_single_validator", func(t *testing.T) {
		s.validateSingleValidatorSet(ctx)
	})

	t.Run("validate_chain_continues", func(t *testing.T) {
		s.validateChainProducesBlocks(ctx)
	})

	t.Run("validate_ibc_preserved", func(t *testing.T) {
		s.validateIBCStatePreserved(ctx)
	})
}

// createAndStartChain creates a cosmos-sdk chain
func (s *SingleValidatorSuite) createAndStartChain(ctx context.Context, numValidators int, name, chainID string) *cosmos.Chain {
	s.T().Logf("Creating chain with %d validators...", numValidators)

	testEncCfg := testutil.MakeTestEncodingConfig(
		auth.AppModuleBasic{},
		bank.AppModuleBasic{},
		govmodule.AppModuleBasic{},
		migrationmngr.AppModuleBasic{},
		ibctransfer.AppModuleBasic{},
	)

	var nodes []cosmos.ChainNodeConfig
	for i := 0; i < numValidators; i++ {
		nodes = append(nodes, cosmos.NewChainNodeConfigBuilder().Build())
	}

	chain, err := cosmos.NewChainBuilder(s.T()).
		WithEncodingConfig(&testEncCfg).
		WithImage(getCosmosSDKAppContainer()).
		WithDenom("stake").
		WithDockerClient(s.dockerClient).
		WithName(name).
		WithDockerNetworkID(s.networkID).
		WithChainID(chainID).
		WithBech32Prefix("gm").
		WithBinaryName("gmd").
		WithGasPrices(fmt.Sprintf("0.00%s", "stake")).
		WithNodes(nodes...).
		Build(ctx)
	s.Require().NoError(err)

	err = chain.Start(ctx)
	s.Require().NoError(err)

	// wait for a few blocks to ensure chain is producing blocks and Docker DNS is propagated
	err = wait.ForBlocks(ctx, 3, chain)
	s.Require().NoError(err)

	s.T().Log("Chain created and started")
	return chain
}

// setupIBCConnection establishes IBC connection between the two chains
func (s *SingleValidatorSuite) setupIBCConnection(ctx context.Context) {
	s.T().Log("Setting up IBC connection...")

	var err error
	s.hermes, err = relayer.NewHermes(ctx, s.dockerClient, "single-val-test", s.networkID, 0, s.logger)
	s.Require().NoError(err)

	err = s.hermes.Init(ctx, []types.Chain{s.ibcCounterpartyChain, s.chain}, func(cfg *relayer.HermesConfig) {
		for i := range cfg.Chains {
			cfg.Chains[i].EventSource = map[string]interface{}{
				"mode":     "pull",
				"interval": "200ms",
			}
			cfg.Chains[i].ClockDrift = "60s"
		}
	})
	s.Require().NoError(err)

	err = s.hermes.CreateClients(ctx, s.ibcCounterpartyChain, s.chain)
	s.Require().NoError(err)

	s.ibcConnection, err = s.hermes.CreateConnections(ctx, s.ibcCounterpartyChain, s.chain)
	s.Require().NoError(err)

	err = wait.ForBlocks(ctx, 2, s.ibcCounterpartyChain, s.chain)
	s.Require().NoError(err)

	channelOpts := ibc.CreateChannelOptions{
		SourcePortName: "transfer",
		DestPortName:   "transfer",
		Order:          ibc.OrderUnordered,
		Version:        "ics20-1",
	}

	s.ibcChannel, err = s.hermes.CreateChannel(ctx, s.ibcCounterpartyChain, s.ibcConnection, channelOpts)
	s.Require().NoError(err)

	s.T().Logf("IBC connection established: %s <-> %s", s.ibcConnection.ConnectionID, s.ibcConnection.CounterpartyID)
}

// performIBCTransfers performs IBC transfers to establish IBC state
func (s *SingleValidatorSuite) performIBCTransfers(ctx context.Context) {
	s.T().Log("Performing IBC transfer...")

	transferAmount := math.NewInt(1_000_000)
	ibcChainWallet := s.ibcCounterpartyChain.GetFaucetWallet()
	gmWallet := s.chain.GetFaucetWallet()

	s.ibcDenom = s.calculateIBCDenom(s.ibcChannel.CounterpartyPort, s.ibcChannel.CounterpartyID, "stake")

	err := s.hermes.Start(ctx)
	s.Require().NoError(err)

	err = wait.ForBlocks(ctx, 2, s.ibcCounterpartyChain, s.chain)
	s.Require().NoError(err)

	transferMsg := transfertypes.NewMsgTransfer(
		s.ibcChannel.PortID,
		s.ibcChannel.ChannelID,
		sdk.NewCoin("stake", transferAmount),
		ibcChainWallet.GetFormattedAddress(),
		gmWallet.GetFormattedAddress(),
		clienttypes.ZeroHeight(),
		uint64(time.Now().Add(time.Hour).UnixNano()),
		"",
	)

	ctxTx, cancelTx := context.WithTimeout(ctx, 2*time.Minute)
	defer cancelTx()
	resp, err := s.ibcCounterpartyChain.BroadcastMessages(ctxTx, ibcChainWallet, transferMsg)
	s.Require().NoError(err)
	s.Require().Equal(uint32(0), resp.Code)

	s.T().Log("Waiting for IBC transfer...")
	networkInfo, err := s.chain.GetNode().GetNetworkInfo(ctx)
	s.Require().NoError(err)

	err = wait.ForCondition(ctx, 2*time.Minute, 2*time.Second, func() (bool, error) {
		balance, err := queryBankBalance(ctx,
			networkInfo.External.GRPCAddress(),
			gmWallet.GetFormattedAddress(),
			s.ibcDenom)
		if err != nil {
			return false, nil
		}
		return balance.Amount.GTE(transferAmount), nil
	})
	s.Require().NoError(err)

	ibcBalance, err := queryBankBalance(ctx,
		networkInfo.External.GRPCAddress(),
		gmWallet.GetFormattedAddress(),
		s.ibcDenom)
	s.Require().NoError(err)
	s.preMigrationIBCBal = *ibcBalance
	s.T().Logf("IBC transfer complete: %s %s", ibcBalance.Amount, s.ibcDenom)
}

// submitSingleValidatorMigrationProposal submits a proposal to migrate to single validator
func (s *SingleValidatorSuite) submitSingleValidatorMigrationProposal(ctx context.Context) {
	s.T().Log("Submitting single validator migration proposal...")

	networkInfo, err := s.chain.GetNode().GetNetworkInfo(ctx)
	s.Require().NoError(err)

	conn, err := grpc.NewClient(networkInfo.External.GRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	s.Require().NoError(err)
	defer conn.Close()

	curHeight, err := s.chain.Height(ctx)
	s.Require().NoError(err)

	// schedule migration 30 blocks in the future to allow governance
	migrateAt := uint64(curHeight + 30)
	s.migrationHeight = migrateAt
	s.T().Logf("Current height: %d, Migration at: %d", curHeight, migrateAt)

	// get the first validator's pubkey (this will be the remaining validator)
	sequencerPubkey := s.getValidatorPubKey(ctx, conn, 0)

	faucet := s.chain.GetFaucetWallet()
	msg := &migrationmngrtypes.MsgMigrateToEvolve{
		Authority:   authtypes.NewModuleAddress(govtypes.ModuleName).String(),
		BlockHeight: migrateAt,
		Sequencer: migrationmngrtypes.Sequencer{
			Name:            "validator-0",
			ConsensusPubkey: sequencerPubkey,
		},
		// stay on CometBFT instead of halting for rollup migration
		StayOnComet: true,
	}

	propMsg, err := govv1.NewMsgSubmitProposal(
		[]sdk.Msg{msg},
		sdk.NewCoins(sdk.NewInt64Coin("stake", 10_000_000_000)),
		faucet.GetFormattedAddress(),
		"",
		"Migrate to Single Validator",
		"Reduce validator set to single validator for chain sunset",
		false,
	)
	s.Require().NoError(err)

	prop, err := s.chain.SubmitAndVoteOnGovV1Proposal(ctx, propMsg, govv1.VoteOption_VOTE_OPTION_YES)
	s.Require().NoError(err)
	s.Require().Equal(govv1.ProposalStatus_PROPOSAL_STATUS_PASSED, prop.Status)
	s.T().Log("Proposal passed")
}

// getValidatorPubKey gets the consensus pubkey for a validator by index
func (s *SingleValidatorSuite) getValidatorPubKey(ctx context.Context, conn *grpc.ClientConn, validatorIndex int) *codectypes.Any {
	stakeQC := stakingtypes.NewQueryClient(conn)
	valsResp, err := stakeQC.Validators(ctx, &stakingtypes.QueryValidatorsRequest{})
	s.Require().NoError(err)
	s.Require().GreaterOrEqual(len(valsResp.Validators), validatorIndex+1)

	nodes := s.chain.GetNodes()
	node := nodes[validatorIndex].(*cosmos.ChainNode)

	stdout, stderr, err := node.Exec(ctx, []string{
		"gmd", "keys", "show", "--address", "validator",
		"--home", node.HomeDir(),
		"--keyring-backend", "test",
		"--bech", "val",
	}, nil)
	s.Require().NoError(err, "failed to get valoper address: %s", stderr)
	valOperAddr := string(bytes.TrimSpace(stdout))

	for _, v := range valsResp.Validators {
		if v.OperatorAddress == valOperAddr {
			return v.ConsensusPubkey
		}
	}

	s.Require().Fail("validator not found", "could not find validator at index %d", validatorIndex)
	return nil
}

// waitForMigrationCompletion waits for the 30-block migration window to complete
func (s *SingleValidatorSuite) waitForMigrationCompletion(ctx context.Context) {
	s.T().Log("Waiting for migration to complete...")

	// migration should complete at migrationHeight + IBCSmoothingFactor (30 blocks)
	targetHeight := int64(s.migrationHeight + 30)

	err := wait.ForCondition(ctx, 5*time.Minute, 5*time.Second, func() (bool, error) {
		h, err := s.chain.Height(ctx)
		if err != nil {
			return false, err
		}
		s.T().Logf("Current height: %d, Target: %d", h, targetHeight)
		return h >= targetHeight, nil
	})
	s.Require().NoError(err)

	// wait a few more blocks to ensure migration is fully complete
	err = wait.ForBlocks(ctx, 3, s.chain)
	s.Require().NoError(err)

	s.T().Log("Migration window completed")
}

// validateSingleValidatorSet validates that only 1 validator remains active
func (s *SingleValidatorSuite) validateSingleValidatorSet(ctx context.Context) {
	s.T().Log("Validating single validator set...")

	networkInfo, err := s.chain.GetNode().GetNetworkInfo(ctx)
	s.Require().NoError(err)

	conn, err := grpc.NewClient(networkInfo.External.GRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	s.Require().NoError(err)
	defer conn.Close()

	stakeQC := stakingtypes.NewQueryClient(conn)

	// check bonded validators
	bondedResp, err := stakeQC.Validators(ctx, &stakingtypes.QueryValidatorsRequest{
		Status: stakingtypes.BondStatus_name[int32(stakingtypes.Bonded)],
	})
	s.Require().NoError(err)
	s.T().Logf("Bonded validators: %d", len(bondedResp.Validators))
	s.Require().Equal(1, len(bondedResp.Validators), "should have exactly 1 bonded validator")

	// check unbonding validators
	unbondingResp, err := stakeQC.Validators(ctx, &stakingtypes.QueryValidatorsRequest{
		Status: stakingtypes.BondStatus_name[int32(stakingtypes.Unbonding)],
	})
	s.Require().NoError(err)
	s.T().Logf("Unbonding validators: %d", len(unbondingResp.Validators))

	// check unbonded validators
	unbondedResp, err := stakeQC.Validators(ctx, &stakingtypes.QueryValidatorsRequest{
		Status: stakingtypes.BondStatus_name[int32(stakingtypes.Unbonded)],
	})
	s.Require().NoError(err)
	s.T().Logf("Unbonded validators: %d", len(unbondedResp.Validators))

	s.T().Log("Validator set validated: 1 bonded validator")
}

// validateChainProducesBlocks validates the chain continues to produce blocks
func (s *SingleValidatorSuite) validateChainProducesBlocks(ctx context.Context) {
	s.T().Log("Validating chain produces blocks...")

	initialHeight, err := s.chain.Height(ctx)
	s.Require().NoError(err)

	err = wait.ForBlocks(ctx, 5, s.chain)
	s.Require().NoError(err)

	finalHeight, err := s.chain.Height(ctx)
	s.Require().NoError(err)
	s.Require().Greater(finalHeight, initialHeight)

	s.T().Logf("Chain producing blocks: %d -> %d", initialHeight, finalHeight)
}

// validateIBCStatePreserved validates IBC state is preserved after migration
func (s *SingleValidatorSuite) validateIBCStatePreserved(ctx context.Context) {
	s.T().Log("Validating IBC state preserved...")

	networkInfo, err := s.chain.GetNode().GetNetworkInfo(ctx)
	s.Require().NoError(err)

	gmWallet := s.chain.GetFaucetWallet()
	currentIBCBalance, err := queryBankBalance(ctx,
		networkInfo.External.GRPCAddress(),
		gmWallet.GetFormattedAddress(),
		s.ibcDenom)
	s.Require().NoError(err)
	s.Require().Equal(s.preMigrationIBCBal.Amount, currentIBCBalance.Amount)

	s.T().Logf("IBC balance preserved: %s %s", currentIBCBalance.Amount, s.ibcDenom)

	// perform IBC transfer back to verify IBC still works after migration
	s.T().Log("Performing IBC transfer back to verify IBC functionality...")

	transferAmount := math.NewInt(100_000)
	ibcChainWallet := s.ibcCounterpartyChain.GetFaucetWallet()

	// get counterparty network info to query balance
	counterpartyNetworkInfo, err := s.ibcCounterpartyChain.GetNode().GetNetworkInfo(ctx)
	s.Require().NoError(err)

	// get initial balance on counterparty chain
	initialCounterpartyBalance, err := queryBankBalance(ctx,
		counterpartyNetworkInfo.External.GRPCAddress(),
		ibcChainWallet.GetFormattedAddress(),
		"stake")
	s.Require().NoError(err)

	// transfer IBC tokens back from gm-1 to gm-2
	transferMsg := transfertypes.NewMsgTransfer(
		s.ibcChannel.CounterpartyPort,
		s.ibcChannel.CounterpartyID,
		sdk.NewCoin(s.ibcDenom, transferAmount),
		gmWallet.GetFormattedAddress(),
		ibcChainWallet.GetFormattedAddress(),
		clienttypes.ZeroHeight(),
		uint64(time.Now().Add(time.Hour).UnixNano()),
		"",
	)

	ctxTx, cancelTx := context.WithTimeout(ctx, 2*time.Minute)
	defer cancelTx()
	resp, err := s.chain.BroadcastMessages(ctxTx, gmWallet, transferMsg)
	s.Require().NoError(err)
	s.Require().Equal(uint32(0), resp.Code, "IBC transfer transaction failed")

	s.T().Log("Waiting for IBC transfer to complete...")

	// wait for transfer to complete on counterparty chain
	err = wait.ForCondition(ctx, 2*time.Minute, 2*time.Second, func() (bool, error) {
		balance, err := queryBankBalance(ctx,
			counterpartyNetworkInfo.External.GRPCAddress(),
			ibcChainWallet.GetFormattedAddress(),
			"stake")
		if err != nil {
			return false, nil
		}
		expectedBalance := initialCounterpartyBalance.Amount.Add(transferAmount)
		return balance.Amount.GTE(expectedBalance), nil
	})
	s.Require().NoError(err)

	// verify final balance on counterparty chain
	finalCounterpartyBalance, err := queryBankBalance(ctx,
		counterpartyNetworkInfo.External.GRPCAddress(),
		ibcChainWallet.GetFormattedAddress(),
		"stake")
	s.Require().NoError(err)
	expectedFinalBalance := initialCounterpartyBalance.Amount.Add(transferAmount)
	s.Require().Equal(expectedFinalBalance, finalCounterpartyBalance.Amount)

	s.T().Logf("IBC transfer back successful: %s stake received on counterparty chain", transferAmount)
}

// calculateIBCDenom calculates the IBC denomination
func (s *SingleValidatorSuite) calculateIBCDenom(portID, channelID, baseDenom string) string {
	prefixedDenom := transfertypes.GetPrefixedDenom(portID, channelID, baseDenom)
	return transfertypes.ParseDenomTrace(prefixedDenom).IBCDenom()
}
