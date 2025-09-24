package integration_test

import (
	"context"
	"fmt"
	"github.com/celestiaorg/tastora/framework/docker/container"
	"os"
	"testing"

	"cosmossdk.io/math"
	"github.com/celestiaorg/tastora/framework/docker/cosmos"
	"github.com/celestiaorg/tastora/framework/testutil/wait"
	"github.com/celestiaorg/tastora/framework/testutil/wallet"
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

// TestLivenessWithCelestiaDA tests the liveness of rollkit with Celestia DA
func (s *DockerIntegrationTestSuite) TestLivenessWithCelestiaDA() {
	ctx := context.Background()
	// Test block production - wait for rollkit chain to produce blocks
	s.T().Log("Testing block production...")
	s.Require().NoError(wait.ForBlocks(ctx, 5, s.evolveChain))

	// Test transaction submission and query
	s.T().Log("Testing transaction submission and query...")
	s.testTransactionSubmissionAndQuery(s.T(), ctx, s.evolveChain)

	s.T().Log("Test completed successfully")
}

// testTransactionSubmissionAndQuery tests sending transactions and querying results using tastora API
func (s *DockerIntegrationTestSuite) testTransactionSubmissionAndQuery(t *testing.T, ctx context.Context, rollkitChain *cosmos.Chain) {
	// hack to get around global, need to set the address prefix before use.
	sdk.GetConfig().SetBech32PrefixForAccount("gm", "gmpub")

	bobsWallet, err := wallet.CreateAndFund(ctx, "bob", sdk.NewCoins(sdk.NewCoin(denom, math.NewInt(1000))), rollkitChain)
	require.NoError(t, err, "failed to create bob wallet")

	carolsWallet, err := rollkitChain.CreateWallet(ctx, "carol")
	require.NoError(t, err, "failed to create carol wallet")

	t.Log("Querying Bob's initial balance...")
	networkInfo, err := rollkitChain.GetNodes()[0].GetNetworkInfo(ctx)
	require.NoError(t, err, "failed to get network info")
	initialBalance, err := queryBankBalance(ctx, networkInfo.External.GRPCAddress(), bobsWallet.GetFormattedAddress(), denom)
	require.NoError(t, err, "failed to query bob's initial balance")
	require.True(t, initialBalance.Amount.Equal(math.NewInt(1000)), "bob should have 1000 tokens")

	t.Logf("Sending 100%s from Bob to Carol...", denom)
	transferAmount := sdk.NewCoins(sdk.NewCoin(denom, math.NewInt(100)))

	// send funds broadcasting to a node that is not the aggregator.
	err = s.sendFunds(ctx, rollkitChain, bobsWallet, carolsWallet, transferAmount, 1)
	require.NoError(t, err, "failed to send funds from Bob to Carol")

	finalBalance, err := queryBankBalance(ctx, networkInfo.External.GRPCAddress(), bobsWallet.GetFormattedAddress(), denom)
	require.NoError(t, err, "failed to query bob's final balance")

	expectedBalance := initialBalance.Amount.Sub(math.NewInt(100))
	require.True(t, finalBalance.Amount.Equal(expectedBalance), "final balance should be exactly initial minus 100")

	carolBalance, err := queryBankBalance(ctx, networkInfo.External.GRPCAddress(), carolsWallet.GetFormattedAddress(), denom)
	require.NoError(t, err, "failed to query carol's balance")
	require.True(t, carolBalance.Amount.Equal(math.NewInt(100)), "carol shouldaddFollowerNode have received 100 tokens")
}

// getEvolveAppContainer returns the evolve app container image.
// uses the EVOLVE_IMAGE_REPO and EVOLVE_IMAGE_TAG environment variables.
func getEvolveAppContainer() container.Image {
	// get image repo and tag from environment variables
	imageRepo := os.Getenv("EVOLVE_IMAGE_REPO")
	if imageRepo == "" {
		imageRepo = "ev-node" // fallback default
	}

	imageTag := os.Getenv("EVOLVE_IMAGE_TAG")
	if imageTag == "" {
		imageTag = "latest" // fallback default
	}
	return container.NewImage(imageRepo, imageTag, "10001:10001")
}
