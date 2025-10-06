package server

import (
	_ "embed"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
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
			chainID, err := parseDAStartHeightFromGenesis(strings.NewReader(tc.json))
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
