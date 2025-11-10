package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrivateKeyFromMnemonic(t *testing.T) {
	tests := []struct {
		name        string
		mnemonic    string
		expectError bool
	}{
		{
			name:        "valid mnemonic",
			mnemonic:    "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
			expectError: false,
		},
		{
			name:        "empty mnemonic",
			mnemonic:    "",
			expectError: true,
		},
		{
			name:        "invalid mnemonic",
			mnemonic:    "invalid mnemonic phrase",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			privKey, err := privateKeyFromMnemonic(tt.mnemonic)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, privKey)
			} else {
				require.NoError(t, err)
				require.NotNil(t, privKey)
				assert.NotNil(t, privKey.PubKey())
			}
		})
	}
}

func TestGetOriginalBlockID(t *testing.T) {
	tests := []struct {
		name        string
		height      int64
		expectEmpty bool
	}{
		{
			name:        "height 0",
			height:      0,
			expectEmpty: true,
		},
		{
			name:        "height 1",
			height:      1,
			expectEmpty: true,
		},
		{
			name:        "height 2 - requires RPC call",
			height:      2,
			expectEmpty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.expectEmpty {
				blockID, err := getOriginalBlockID(context.Background(), "tcp://localhost:26657", tt.height)
				require.NoError(t, err)
				assert.Empty(t, blockID.Hash)
				assert.Empty(t, blockID.PartSetHeader.Hash)
				assert.Equal(t, uint32(0), blockID.PartSetHeader.Total)
			} else {
				// For height > 1, we would need a running node to test
				// This test would fail without a node, so we skip the actual call
				t.Skip("Requires running node for integration test")
			}
		})
	}
}
