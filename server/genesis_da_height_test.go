package server

import (
	"context"
	"testing"
	"time"

	"cosmossdk.io/log"
	"github.com/evstack/ev-node/pkg/config"
	"github.com/evstack/ev-node/pkg/genesis"
	"github.com/stretchr/testify/assert"
)

func TestPrefillGenesisDaHeight(t *testing.T) {
	ctx := context.Background()
	logger := log.NewNopLogger()

	// Create test rollkit genesis at height 1
	rollkitGenesis := genesis.NewGenesis(
		"test-chain",
		1, // Initial height 1 = at genesis
		time.Now(),
		[]byte("test-sequencer-addr"),
	)

	tests := []struct {
		name            string
		cfg             *config.Config
		migrationGenesis *evolveMigrationGenesis
		rollkitGenesis  *genesis.Genesis
		expectSkip      bool
		expectReason    string
	}{
		{
			name: "should skip when node is aggregator",
			cfg: &config.Config{
				Node: config.NodeConfig{Aggregator: true},
				DA:   config.DAConfig{StartHeight: 0},
			},
			rollkitGenesis: &rollkitGenesis,
			expectSkip:     true,
			expectReason:   "node is aggregator",
		},
		{
			name: "should skip when DA start height already set",
			cfg: &config.Config{
				Node: config.NodeConfig{Aggregator: false},
				DA:   config.DAConfig{StartHeight: 100},
			},
			rollkitGenesis: &rollkitGenesis,
			expectSkip:     true,
			expectReason:   "DA start height already set",
		},
		{
			name: "should skip when migration scenario",
			cfg: &config.Config{
				Node: config.NodeConfig{Aggregator: false},
				DA:   config.DAConfig{StartHeight: 0},
			},
			migrationGenesis: &evolveMigrationGenesis{
				ChainID:       "test-chain",
				InitialHeight: 1,
			},
			rollkitGenesis: &rollkitGenesis,
			expectSkip:     true,
			expectReason:   "migration scenario",
		},
		{
			name: "should skip when not at genesis",
			cfg: &config.Config{
				Node: config.NodeConfig{Aggregator: false},
				DA:   config.DAConfig{StartHeight: 0},
			},
			rollkitGenesis: &genesis.Genesis{
				ChainID:         "test-chain",
				InitialHeight:   100, // Not at genesis
				StartTime:       time.Now(),
				ProposerAddress: []byte("test-sequencer-addr"),
			},
			expectSkip:   true,
			expectReason: "not at genesis",
		},
		{
			name: "should attempt prefill when conditions are met",
			cfg: &config.Config{
				Node: config.NodeConfig{Aggregator: false},
				DA:   config.DAConfig{StartHeight: 0},
			},
			rollkitGenesis: &rollkitGenesis,
			expectSkip:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset config for each test
			originalStartHeight := tt.cfg.DA.StartHeight

			// Call the prefill function
			err := prefillGenesisDaHeight(
				ctx,
				tt.cfg,
				nil, // DA client is nil, but we expect early returns for most tests
				tt.rollkitGenesis,
				tt.migrationGenesis,
				logger,
			)

			if tt.expectSkip {
				// For skip scenarios, the config should not be modified
				assert.Equal(t, originalStartHeight, tt.cfg.DA.StartHeight,
					"DA start height should not be modified when skipping")
				
				// Error should be nil for skip scenarios
				assert.NoError(t, err, "should not return error for valid skip scenarios")
			} else {
				// For non-skip scenarios with nil DA client, we expect an error
				// since getGenesisDaHeight will fail with "not yet implemented"
				assert.Error(t, err, "should return error when GetGenesisDaHeight is not available")
				assert.Contains(t, err.Error(), "not yet implemented",
					"error should indicate GetGenesisDaHeight is not yet implemented")
				
				// Config should not be modified when RPC fails
				assert.Equal(t, originalStartHeight, tt.cfg.DA.StartHeight,
					"DA start height should not be modified when RPC fails")
			}
		})
	}
}

func TestGetGenesisDaHeight(t *testing.T) {
	ctx := context.Background()

	// Test that the function returns the expected error until the RPC is implemented
	height, err := getGenesisDaHeight(ctx, nil)
	
	assert.Error(t, err, "should return error when GetGenesisDaHeight RPC is not available")
	assert.Contains(t, err.Error(), "not yet implemented", 
		"error should indicate the method is not yet implemented")
	assert.Equal(t, uint64(0), height, "height should be 0 when error occurs")
}