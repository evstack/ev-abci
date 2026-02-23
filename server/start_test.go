package server

import (
	"context"
	_ "embed"
	"errors"
	"strings"
	"testing"

	"cosmossdk.io/log"
	abci "github.com/cometbft/cometbft/abci/types"
	sdkserver "github.com/cosmos/cosmos-sdk/server"
	serverconfig "github.com/cosmos/cosmos-sdk/server/config"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"github.com/evstack/ev-node/pkg/genesis"
)

// mockQuerier implements abciQuerier for testing.
type mockQuerier struct {
	resp *abci.ResponseQuery
	err  error
}

func (m *mockQuerier) Query(_ context.Context, _ *abci.RequestQuery) (*abci.ResponseQuery, error) {
	return m.resp, m.err
}

func TestDetectNetworkModule(t *testing.T) {
	nopLogger := log.NewNopLogger()

	tests := []struct {
		name     string
		querier  abciQuerier
		expected bool
	}{
		{
			name:     "module present - returns true",
			querier:  &mockQuerier{resp: &abci.ResponseQuery{Code: 0, Value: []byte("params")}, err: nil},
			expected: true,
		},
		{
			name:     "module absent - non-zero code",
			querier:  &mockQuerier{resp: &abci.ResponseQuery{Code: 1, Log: "unknown query path"}, err: nil},
			expected: false,
		},
		{
			name:     "query error",
			querier:  &mockQuerier{resp: nil, err: errors.New("app not ready")},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := detectNetworkModule(context.Background(), tc.querier, nopLogger)
			require.Equal(t, tc.expected, result)
		})
	}
}

func TestParseDAStartHeightFromGenesis(t *testing.T) {
	testCases := []struct {
		name             string
		json             string
		expDAStartHeight uint64
		expError         string
	}{
		{
			"success",
			`{
				"state": {
				  "accounts": {
					"a": {}
				  }
				},
				"da_start_height": 12
			}`,
			12,
			"",
		},
		{
			"nested",
			`{
				"state": {
				  "accounts": {
					"a": {}
				  },
				  "da_start_height": 12
				}
			}`,
			0,
			"",
		},
		{
			"not exist",
			`{
				"state": {
				  "accounts": {
					"a": {}
				  }
				},
				"da-start-height": 21
			}`,
			0,
			"",
		},
		{
			"invalid type",
			`{
				"da_start_height": "1",
			}`,
			0,
			"json: cannot unmarshal string into Go value of type uint64",
		},
		{
			"invalid json",
			`[ " ': }`,
			0,
			"expected {, got [",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			chainID, err := parseFieldFromGenesis(strings.NewReader(tc.json), "da_start_height")
			if len(tc.expError) > 0 {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expDAStartHeight, chainID)
			}
		})
	}
}

func TestGetJSONTag(t *testing.T) {
	testCases := []struct {
		name      string
		fieldName string
		expTag    string
		expError  bool
	}{
		{
			name:      "DAStartHeight field",
			fieldName: "DAStartHeight",
			expTag:    "da_start_height",
			expError:  false,
		},
		{
			name:      "DAEpochForcedInclusion field",
			fieldName: "DAEpochForcedInclusion",
			expTag:    "da_epoch_forced_inclusion",
			expError:  false,
		},
		{
			name:      "ChainID field",
			fieldName: "ChainID",
			expTag:    "chain_id",
			expError:  false,
		},
		{
			name:      "non-existent field",
			fieldName: "NonExistentField",
			expTag:    "",
			expError:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tag, err := getJSONTag(genesis.Genesis{}, tc.fieldName)
			if tc.expError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expTag, tag)
			}
		})
	}
}

func TestMapCosmosPruningToEvNode(t *testing.T) {
	testCases := []struct {
		name                 string
		cosmosPruning        string
		cosmosKeepRecent     string
		cosmosInterval       string
		evnodeBlockTime      string
		expEvnodePruningMode string
		expEvnodeKeepRecent  string
		expEvnodeInterval    string
	}{
		{
			name:                 "nothing maps to disabled",
			cosmosPruning:        "nothing",
			cosmosKeepRecent:     "0",
			cosmosInterval:       "0",
			evnodeBlockTime:      "1s",
			expEvnodePruningMode: "disabled",
			expEvnodeKeepRecent:  "0",
			expEvnodeInterval:    "", // 0 interval is skipped
		},
		{
			name:                 "default maps to disabled",
			cosmosPruning:        "default",
			cosmosKeepRecent:     "362880",
			cosmosInterval:       "10",
			evnodeBlockTime:      "6s",
			expEvnodePruningMode: "disabled",
			expEvnodeKeepRecent:  "362880",
			expEvnodeInterval:    "1m0s", // 10 blocks * 6s = 60s
		},
		{
			name:                 "everything maps to all",
			cosmosPruning:        "everything",
			cosmosKeepRecent:     "2",
			cosmosInterval:       "10",
			evnodeBlockTime:      "1s",
			expEvnodePruningMode: "all",
			expEvnodeKeepRecent:  "2",
			expEvnodeInterval:    "10s", // 10 blocks * 1s = 10s
		},
		{
			name:                 "custom maps to metadata",
			cosmosPruning:        "custom",
			cosmosKeepRecent:     "100",
			cosmosInterval:       "5",
			evnodeBlockTime:      "2s",
			expEvnodePruningMode: "metadata",
			expEvnodeKeepRecent:  "100",
			expEvnodeInterval:    "10s", // 5 blocks * 2s = 10s
		},
		{
			name:                 "empty values are not mapped",
			cosmosPruning:        "",
			cosmosKeepRecent:     "",
			cosmosInterval:       "",
			evnodeBlockTime:      "1s",
			expEvnodePruningMode: "",
			expEvnodeKeepRecent:  "",
			expEvnodeInterval:    "",
		},
		{
			name:                 "unknown value maps to disabled",
			cosmosPruning:        "unknown",
			cosmosKeepRecent:     "50",
			cosmosInterval:       "15",
			evnodeBlockTime:      "1s",
			expEvnodePruningMode: "disabled",
			expEvnodeKeepRecent:  "50",
			expEvnodeInterval:    "15s", // 15 blocks * 1s = 15s
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			v := viper.New()
			v.Set(sdkserver.FlagPruning, tc.cosmosPruning)
			v.Set(sdkserver.FlagPruningKeepRecent, tc.cosmosKeepRecent)
			v.Set(sdkserver.FlagPruningInterval, tc.cosmosInterval)
			v.Set("evnode.node.block_time", tc.evnodeBlockTime)

			mapCosmosPruningToEvNode(v)

			if tc.expEvnodePruningMode != "" {
				require.Equal(t, tc.expEvnodePruningMode, v.GetString("evnode.pruning.pruning_mode"))
			} else {
				require.Empty(t, v.GetString("evnode.pruning.pruning_mode"))
			}

			if tc.expEvnodeKeepRecent != "" {
				require.Equal(t, tc.expEvnodeKeepRecent, v.GetString("evnode.pruning.pruning_keep_recent"))
			} else {
				require.Empty(t, v.GetString("evnode.pruning.pruning_keep_recent"))
			}

			if tc.expEvnodeInterval != "" {
				require.Equal(t, tc.expEvnodeInterval, v.GetString("evnode.pruning.pruning_interval"))
			} else {
				require.Empty(t, v.GetString("evnode.pruning.pruning_interval"))
			}
		})
	}
}

func TestMapCosmosPruningToEvNode_WithDefaultConfig(t *testing.T) {
	// Test using cosmos-sdk's DefaultConfig to ensure realistic integration
	cosmosConfig := serverconfig.DefaultConfig()

	v := viper.New()
	v.Set(sdkserver.FlagPruning, cosmosConfig.Pruning)
	v.Set(sdkserver.FlagPruningKeepRecent, cosmosConfig.PruningKeepRecent)
	v.Set(sdkserver.FlagPruningInterval, cosmosConfig.PruningInterval)
	v.Set("evnode.node.block_time", "6s")

	mapCosmosPruningToEvNode(v)

	// DefaultConfig has pruning="default", which should map to "disabled"
	require.Equal(t, "disabled", v.GetString("evnode.pruning.pruning_mode"))
	require.Equal(t, cosmosConfig.PruningKeepRecent, v.GetString("evnode.pruning.pruning_keep_recent"))
	// DefaultConfig has interval="0", which should not set a value
	require.Empty(t, v.GetString("evnode.pruning.pruning_interval"))
}
