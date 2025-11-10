package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPostTxCmd_NamespaceFallback(t *testing.T) {
	tests := []struct {
		name           string
		flagValue      string
		configValue    string
		expectedResult string
	}{
		{
			name:           "use flag value when provided",
			flagValue:      "0000000000000000000000000000000000000000000001",
			configValue:    "0000000000000000000000000000000000000000000002",
			expectedResult: "0000000000000000000000000000000000000000000001",
		},
		{
			name:           "use config value when flag is empty",
			flagValue:      "",
			configValue:    "0000000000000000000000000000000000000000000002",
			expectedResult: "0000000000000000000000000000000000000000000002",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			namespace := tt.flagValue
			if namespace == "" {
				namespace = tt.configValue
			}
			assert.Equal(t, tt.expectedResult, namespace)
		})
	}
}
