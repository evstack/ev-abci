package server

import (
	_ "embed"
	"strings"
	"testing"

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
