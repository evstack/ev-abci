package relay

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"

	v1 "github.com/symbioticfi/relay/api/client/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	abci "github.com/cometbft/cometbft/abci/types"
	cryptoProto "github.com/cometbft/cometbft/proto/tendermint/crypto"
)

// Client wraps the Symbiotic relay gRPC client.
type Client struct {
	conn   *grpc.ClientConn
	client *v1.SymbioticClient
	mu     sync.RWMutex

	// Mock mode fields
	mockMode       bool
	mockEpoch      uint64
	mockValidators []ValidatorInfo
}

// ValidatorInfo contains validator information from the relay.
type ValidatorInfo struct {
	PubKey []byte
	Power  int64
}

// Config holds configuration for the relay client.
type Config struct {
	// RPCAddress is the gRPC address of the relay sidecar.
	RPCAddress string
	// KeyFile is the path to the validator keys file (for mock mode).
	KeyFile string
}

// NewClient creates a new relay client.
func NewClient(cfg Config) (*Client, error) {
	if cfg.RPCAddress == "" && cfg.KeyFile == "" {
		return nil, fmt.Errorf("either RPCAddress or KeyFile must be provided")
	}

	// If KeyFile is provided, use mock client
	if cfg.KeyFile != "" {
		return newMockClient(cfg.KeyFile)
	}

	// Connect to real relay sidecar
	conn, err := grpc.NewClient(cfg.RPCAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to relay: %w", err)
	}

	return &Client{
		conn:   conn,
		client: v1.NewSymbioticClient(conn),
	}, nil
}

// Close closes the relay client connection.
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// GetCurrentEpoch fetches the current epoch from the relay.
func (c *Client) GetCurrentEpoch(ctx context.Context) (uint64, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.mockMode {
		return c.mockEpoch, nil
	}

	if c.client == nil {
		return 0, fmt.Errorf("relay client not initialized")
	}

	resp, err := c.client.GetCurrentEpoch(ctx, &v1.GetCurrentEpochRequest{})
	if err != nil {
		return 0, fmt.Errorf("failed to get current epoch: %w", err)
	}

	return resp.GetEpoch(), nil
}

// GetValidatorSet fetches the validator set for a given epoch.
func (c *Client) GetValidatorSet(ctx context.Context, epoch uint64) ([]abci.ValidatorUpdate, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.mockMode {
		updates := make([]abci.ValidatorUpdate, 0, len(c.mockValidators))
		for _, v := range c.mockValidators {
			updates = append(updates, abci.ValidatorUpdate{
				PubKey: cryptoProto.PublicKey{Sum: &cryptoProto.PublicKey_Ed25519{Ed25519: v.PubKey}},
				Power:  v.Power,
			})
		}
		return updates, nil
	}

	if c.client == nil {
		return nil, fmt.Errorf("relay client not initialized")
	}

	resp, err := c.client.GetValidatorSet(ctx, &v1.GetValidatorSetRequest{
		Epoch: &epoch,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get validator set: %w", err)
	}

	valSet := resp.GetValidatorSet()
	if valSet == nil {
		return nil, fmt.Errorf("empty validator set")
	}

	validators := valSet.GetValidators()
	updates := make([]abci.ValidatorUpdate, 0, len(validators))
	for _, v := range validators {
		// Find the Ed25519 key with the validator key tag (tag 43 by default)
		keys := v.GetKeys()
		var ed25519Key []byte
		for _, key := range keys {
			// Key tag >> 4 == 2 means Ed25519 key type
			if key.GetTag()>>4 == 2 {
				ed25519Key = key.GetPayload()
				break
			}
		}
		if ed25519Key == nil {
			continue
		}

		// Parse voting power from string
		power, err := strconv.ParseInt(v.GetVotingPower(), 10, 64)
		if err != nil {
			power = 1 // Default power
		}

		updates = append(updates, abci.ValidatorUpdate{
			PubKey: cryptoProto.PublicKey{Sum: &cryptoProto.PublicKey_Ed25519{Ed25519: ed25519Key}},
			Power:  power,
		})
	}

	return updates, nil
}

// SignMessage requests the relay to sign a message.
func (c *Client) SignMessage(ctx context.Context, keyTag uint32, message []byte) (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.mockMode {
		// Return a mock request ID
		return fmt.Sprintf("mock-request-%x", message[:8]), nil
	}

	if c.client == nil {
		return "", fmt.Errorf("relay client not initialized")
	}

	resp, err := c.client.SignMessage(ctx, &v1.SignMessageRequest{
		KeyTag:  keyTag,
		Message: message,
	})
	if err != nil {
		return "", fmt.Errorf("failed to sign message: %w", err)
	}

	return resp.GetRequestId(), nil
}

// Mock client implementation for testing

func newMockClient(keyFile string) (*Client, error) {
	data, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read key file: %w", err)
	}

	var keyData struct {
		Epoch      uint64 `json:"epoch"`
		Validators []struct {
			PublicKey string `json:"public_key"`
			Power     int64  `json:"power"`
		} `json:"validators"`
	}

	if err := json.Unmarshal(data, &keyData); err != nil {
		return nil, fmt.Errorf("failed to parse key file: %w", err)
	}

	validators := make([]ValidatorInfo, 0, len(keyData.Validators))
	for _, v := range keyData.Validators {
		// Parse hex public key
		pubKeyBytes, err := hex.DecodeString(v.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("failed to decode public key: %w", err)
		}
		validators = append(validators, ValidatorInfo{
			PubKey: pubKeyBytes,
			Power:  v.Power,
		})
	}

	return &Client{
		mockMode:       true,
		mockEpoch:      keyData.Epoch,
		mockValidators: validators,
	}, nil
}

// ConfigFromEnv creates a Config from environment variables.
func ConfigFromEnv() Config {
	return Config{
		RPCAddress: os.Getenv("SYMBIOTIC_RELAY_RPC"),
		KeyFile:    os.Getenv("SYMBIOTIC_KEY_FILE"),
	}
}
