package integration_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"testing"

	"cosmossdk.io/math"
	"github.com/celestiaorg/go-square/v2/share"
	"github.com/celestiaorg/tastora/framework/docker"
	"github.com/celestiaorg/tastora/framework/testutil/broadcast"
	"github.com/celestiaorg/tastora/framework/testutil/sdkacc"
	"github.com/celestiaorg/tastora/framework/testutil/wait"
	"github.com/celestiaorg/tastora/framework/testutil/wallet"
	"github.com/celestiaorg/tastora/framework/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	denom = "stake"
)

func TestDockerIntegrationTestSuite(t *testing.T) {
	suite.Run(t, new(DockerIntegrationTestSuite))
}

func generateValidNamespace() string {
	return hex.EncodeToString(share.RandomBlobNamespace().Bytes())
}

// queryBankBalance queries the balance of an address using RPC calls.
func queryBankBalance(ctx context.Context, grpcAddress string, walletAddress string, denom string) (*sdk.Coin, error) {
	conn, err := grpc.Dial(grpcAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to dial gRPC: %w", err)
	}
	defer conn.Close()

	// Create bank query client
	bankClient := banktypes.NewQueryClient(conn)

	// Query balance
	resp, err := bankClient.Balance(ctx, &banktypes.QueryBalanceRequest{
		Address: walletAddress,
		Denom:   denom,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query balance: %w", err)
	}

	return resp.Balance, nil
}

// sendFunds sends funds from one wallet to another using bank transfer
func sendFunds(ctx context.Context, chain *docker.Chain, fromWallet, toWallet types.Wallet, amount sdk.Coins, nodeIdx int) error {
	fromAddress, err := sdkacc.AddressFromWallet(fromWallet)
	if err != nil {
		return fmt.Errorf("failed to get sender address: %w", err)
	}

	toAddress, err := sdkacc.AddressFromWallet(toWallet)
	if err != nil {
		return fmt.Errorf("failed to get destination address: %w", err)
	}

	chainNode := chain.GetNodes()[nodeIdx]

	msg := banktypes.NewMsgSend(fromAddress, toAddress, amount)
	resp, err := broadcast.MessagesForNode(ctx, fromWallet, chain, chainNode.(*docker.ChainNode), msg)
	if err != nil {
		return fmt.Errorf("failed to broadcast transaction: %w", err)
	}

	if resp.Code != 0 {
		return fmt.Errorf("transaction failed with code %d: %s", resp.Code, resp.RawLog)
	}

	return nil
}

// TestLivenessWithCelestiaDA tests the liveness of rollkit with Celestia DA
func (suite *DockerIntegrationTestSuite) TestLivenessWithCelestiaDA() {
	ctx := context.Background()
	// Test block production - wait for rollkit chain to produce blocks
	suite.T().Log("Testing block production...")
	suite.Require().NoError(wait.ForBlocks(ctx, 5, suite.rollkitChain))

	// Test transaction submission and query
	suite.T().Log("Testing transaction submission and query...")
	testTransactionSubmissionAndQuery(suite.T(), ctx, suite.rollkitChain)

	suite.T().Log("Test completed successfully")
}

// testTransactionSubmissionAndQuery tests sending transactions and querying results using tastora API
func testTransactionSubmissionAndQuery(t *testing.T, ctx context.Context, rollkitChain *docker.Chain) {
	// hack to get around global, need to set the address prefix before use.
	sdk.GetConfig().SetBech32PrefixForAccount("gm", "gmpub")

	bobsWallet, err := wallet.CreateAndFund(ctx, "bob", sdk.NewCoins(sdk.NewCoin(denom, math.NewInt(1000))), rollkitChain)
	require.NoError(t, err, "failed to create bob wallet")

	carolsWallet, err := rollkitChain.CreateWallet(ctx, "carol")
	require.NoError(t, err, "failed to create carol wallet")

	t.Log("Querying Bob's initial balance...")
	initialBalance, err := queryBankBalance(ctx, rollkitChain.GetGRPCAddress(), bobsWallet.GetFormattedAddress(), denom)
	require.NoError(t, err, "failed to query bob's initial balance")
	require.True(t, initialBalance.Amount.Equal(math.NewInt(1000)), "bob should have 1000 tokens")

	t.Logf("Sending 100%s from Bob to Carol...", denom)
	transferAmount := sdk.NewCoins(sdk.NewCoin(denom, math.NewInt(100)))

	// send funds broadcasting to a node that is not the aggregator.
	err = sendFunds(ctx, rollkitChain, bobsWallet, carolsWallet, transferAmount, 1)
	require.NoError(t, err, "failed to send funds from Bob to Carol")

	finalBalance, err := queryBankBalance(ctx, rollkitChain.GetGRPCAddress(), bobsWallet.GetFormattedAddress(), denom)
	require.NoError(t, err, "failed to query bob's final balance")

	expectedBalance := initialBalance.Amount.Sub(math.NewInt(100))
	require.True(t, finalBalance.Amount.Equal(expectedBalance), "final balance should be exactly initial minus 100")

	carolBalance, err := queryBankBalance(ctx, rollkitChain.GetGRPCAddress(), carolsWallet.GetFormattedAddress(), denom)
	require.NoError(t, err, "failed to query carol's balance")
	require.True(t, carolBalance.Amount.Equal(math.NewInt(100)), "carol should have received 100 tokens")
}
