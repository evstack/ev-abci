package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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
	t.Run("height 0 returns empty block ID without RPC", func(t *testing.T) {
		blockID, err := getOriginalBlockID(context.Background(), "tcp://localhost:26657", 0)
		require.NoError(t, err)
		assert.Empty(t, blockID.Hash)
		assert.Empty(t, blockID.PartSetHeader.Hash)
		assert.Equal(t, uint32(0), blockID.PartSetHeader.Total)
	})

	t.Run("height 1 returns empty block ID without RPC", func(t *testing.T) {
		blockID, err := getOriginalBlockID(context.Background(), "tcp://localhost:26657", 1)
		require.NoError(t, err)
		assert.Empty(t, blockID.Hash)
		assert.Empty(t, blockID.PartSetHeader.Hash)
		assert.Equal(t, uint32(0), blockID.PartSetHeader.Total)
	})

	t.Run("height greater than 1 reads block ID from RPC", func(t *testing.T) {
		blockHash := strings.Repeat("ab", 32)
		partSetHash := strings.Repeat("cd", 32)
		nodeURL := newAttesterRPCTestServer(t, `{
			"result": {
				"block_id": {
					"hash": "`+blockHash+`",
					"parts": {
						"hash": "`+partSetHash+`",
						"total": 2
					}
				},
				"block": {
					"header": {
						"height": "2",
						"time": "2026-04-22T12:00:00Z",
						"chain_id": "gm"
					}
				}
			}
		}`)

		blockID, err := getOriginalBlockID(context.Background(), nodeURL, 2)
		require.NoError(t, err)
		assert.Equal(t, strings.Repeat("\xab", 32), string(blockID.Hash))
		assert.Equal(t, strings.Repeat("\xcd", 32), string(blockID.PartSetHeader.Hash))
		assert.Equal(t, uint32(2), blockID.PartSetHeader.Total)
	})

	t.Run("invalid node URL returns error", func(t *testing.T) {
		_, err := getOriginalBlockID(context.Background(), "%", 2)
		require.Error(t, err)
		require.Contains(t, err.Error(), "parse node URL")
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		nodeURL := newAttesterRPCTestServer(t, `{`)

		_, err := getOriginalBlockID(context.Background(), nodeURL, 2)
		require.Error(t, err)
		require.Contains(t, err.Error(), "decoding response")
	})

	t.Run("invalid block hash returns error", func(t *testing.T) {
		nodeURL := newAttesterRPCTestServer(t, `{
			"result": {
				"block_id": {
					"hash": "not-hex",
					"parts": {
						"hash": "`+strings.Repeat("cd", 32)+`",
						"total": 1
					}
				}
			}
		}`)

		_, err := getOriginalBlockID(context.Background(), nodeURL, 2)
		require.Error(t, err)
		require.Contains(t, err.Error(), "decoding block ID hash")
	})

	t.Run("invalid part set hash returns error", func(t *testing.T) {
		nodeURL := newAttesterRPCTestServer(t, `{
			"result": {
				"block_id": {
					"hash": "`+strings.Repeat("ab", 32)+`",
					"parts": {
						"hash": "not-hex",
						"total": 1
					}
				}
			}
		}`)

		_, err := getOriginalBlockID(context.Background(), nodeURL, 2)
		require.Error(t, err)
		require.Contains(t, err.Error(), "decoding part set header hash")
	})
}

func TestGetLatestHeight(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     int64
		wantErr  string
	}{
		{
			name: "valid height",
			response: `{
				"result": {
					"block": {
						"header": {
							"height": "42"
						}
					}
				}
			}`,
			want: 42,
		},
		{
			name: "empty height returns zero",
			response: `{
				"result": {
					"block": {
						"header": {}
					}
				}
			}`,
			want: 0,
		},
		{
			name: "invalid height returns error",
			response: `{
				"result": {
					"block": {
						"header": {
							"height": "not-a-number"
						}
					}
				}
			}`,
			wantErr: "parse height",
		},
		{
			name:     "invalid JSON returns error",
			response: `{`,
			wantErr:  "decode block",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nodeURL := newAttesterRPCTestServer(t, tc.response)

			got, err := getLatestHeight(nodeURL)
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("invalid node URL returns error", func(t *testing.T) {
		_, err := getLatestHeight("%")
		require.Error(t, err)
		require.Contains(t, err.Error(), "parse node URL")
	})
}

func TestGetEvolveHeader(t *testing.T) {
	t.Run("valid response builds Evolve header", func(t *testing.T) {
		nodeURL := newAttesterRPCTestServer(t, `{
			"result": {
				"block": {
					"header": {
						"version": {
							"block": "1",
							"app": "7"
						},
						"height": "42",
						"time": "2026-04-22T12:00:00Z",
						"last_block_id": {
							"hash": "`+strings.Repeat("11", 32)+`"
						},
						"last_commit_hash": "`+strings.Repeat("22", 32)+`",
						"data_hash": "`+strings.Repeat("33", 32)+`",
						"validators_hash": "`+strings.Repeat("44", 32)+`",
						"next_validators_hash": "`+strings.Repeat("55", 32)+`",
						"consensus_hash": "`+strings.Repeat("66", 32)+`",
						"app_hash": "`+strings.Repeat("77", 32)+`",
						"last_results_hash": "`+strings.Repeat("88", 32)+`",
						"evidence_hash": "`+strings.Repeat("99", 32)+`",
						"proposer_address": "`+strings.Repeat("aa", 20)+`",
						"chain_id": "gm"
					}
				}
			}
		}`)

		header, err := getEvolveHeader(nodeURL, 42)
		require.NoError(t, err)
		require.NotNil(t, header)
		assert.Equal(t, uint64(42), header.Height())
		assert.Equal(t, "gm", header.ChainID())
		assert.Equal(t, uint64(7), header.Version.App)
		assert.Equal(t, strings.Repeat("\x33", 32), string(header.DataHash))
		assert.Equal(t, strings.Repeat("\x44", 32), string(header.ValidatorHash))
		assert.Equal(t, strings.Repeat("\x77", 32), string(header.AppHash))
		assert.Equal(t, strings.Repeat("\xaa", 20), string(header.ProposerAddress))
	})

	t.Run("invalid node URL returns error", func(t *testing.T) {
		_, err := getEvolveHeader("%", 42)
		require.Error(t, err)
		require.Contains(t, err.Error(), "parse node URL")
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		nodeURL := newAttesterRPCTestServer(t, `{`)

		_, err := getEvolveHeader(nodeURL, 42)
		require.Error(t, err)
		require.Contains(t, err.Error(), "decoding response")
	})

	t.Run("invalid height returns error", func(t *testing.T) {
		nodeURL := newAttesterRPCTestServer(t, `{
			"result": {
				"block": {
					"header": {
						"height": "not-a-number",
						"time": "2026-04-22T12:00:00Z"
					}
				}
			}
		}`)

		_, err := getEvolveHeader(nodeURL, 42)
		require.Error(t, err)
		require.Contains(t, err.Error(), "parsing height")
	})

	t.Run("invalid timestamp returns error", func(t *testing.T) {
		nodeURL := newAttesterRPCTestServer(t, `{
			"result": {
				"block": {
					"header": {
						"height": "42",
						"time": "not-a-time"
					}
				}
			}
		}`)

		_, err := getEvolveHeader(nodeURL, 42)
		require.Error(t, err)
		require.Contains(t, err.Error(), "parsing time")
	})
}

func newAttesterRPCTestServer(t *testing.T, response string) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(server.Close)

	return "tcp://" + strings.TrimPrefix(server.URL, "http://")
}
