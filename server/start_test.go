package server

import (
	_ "embed"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"github.com/evstack/ev-node/pkg/genesis"
)

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
		name                   string
		cosmosPruning          string
		cosmosKeepRecent       string
		cosmosInterval         string
		expEvnodePruningMode   string
		expEvnodeKeepRecent    string
		expEvnodeInterval      string
	}{
		{
			name:                   "nothing maps to disabled",
			cosmosPruning:          "nothing",
			cosmosKeepRecent:       "0",
			cosmosInterval:         "0",
			expEvnodePruningMode:   "disabled",
			expEvnodeKeepRecent:    "0",
			expEvnodeInterval:      "0",
		},
		{
			name:                   "default maps to all",
			cosmosPruning:          "default",
			cosmosKeepRecent:       "362880",
			cosmosInterval:         "10",
			expEvnodePruningMode:   "all",
			expEvnodeKeepRecent:    "362880",
			expEvnodeInterval:      "10",
		},
		{
			name:                   "everything maps to all",
			cosmosPruning:          "everything",
			cosmosKeepRecent:       "2",
			cosmosInterval:         "10",
			expEvnodePruningMode:   "all",
			expEvnodeKeepRecent:    "2",
			expEvnodeInterval:      "10",
		},
		{
			name:                   "custom maps to all",
			cosmosPruning:          "custom",
			cosmosKeepRecent:       "100",
			cosmosInterval:         "5",
			expEvnodePruningMode:   "all",
			expEvnodeKeepRecent:    "100",
			expEvnodeInterval:      "5",
		},
		{
			name:                   "empty values are not mapped",
			cosmosPruning:          "",
			cosmosKeepRecent:       "",
			cosmosInterval:         "",
			expEvnodePruningMode:   "",
			expEvnodeKeepRecent:    "",
			expEvnodeInterval:      "",
		},
		{
			name:                   "unknown value maps to all",
			cosmosPruning:          "unknown",
			cosmosKeepRecent:       "50",
			cosmosInterval:         "15",
			expEvnodePruningMode:   "all",
			expEvnodeKeepRecent:    "50",
			expEvnodeInterval:      "15",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			v := viper.New()
			v.Set("pruning", tc.cosmosPruning)
			v.Set("pruning-keep-recent", tc.cosmosKeepRecent)
			v.Set("pruning-interval", tc.cosmosInterval)

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
