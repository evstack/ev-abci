package server

import (
	"os"
	"path/filepath"
	"testing"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	"gotest.tools/v3/assert"

	migrationtypes "github.com/evstack/ev-abci/modules/migrationmngr/types"
	networktypes "github.com/evstack/ev-abci/modules/network/types"
)

func createClientContext(t *testing.T) client.Context {
	t.Helper()
	// Create interface registry and codec
	interfaceRegistry := codectypes.NewInterfaceRegistry()

	// Register interfaces for modules
	migrationtypes.RegisterInterfaces(interfaceRegistry)
	networktypes.RegisterInterfaces(interfaceRegistry)

	protoCodec := codec.NewProtoCodec(interfaceRegistry)
	txConfig := authtx.NewTxConfig(protoCodec, authtx.DefaultSignModes)

	return client.Context{}.WithTxConfig(txConfig)
}

func TestDecodeTxFromJSON(t *testing.T) {
	tests := []struct {
		name    string
		jsonStr string
		wantErr bool
	}{
		{
			name: "valid empty transaction",
			jsonStr: `{
				"body": {
					"messages": [],
					"memo": "",
					"timeout_height": "0",
					"extension_options": [],
					"non_critical_extension_options": []
				},
				"auth_info": {
					"signer_infos": [],
					"fee": {
						"amount": [],
						"gas_limit": "200000",
						"payer": "",
						"granter": ""
					}
				},
				"signatures": []
			}`,
			wantErr: false,
		},
		{
			name:    "invalid json",
			jsonStr: `{invalid}`,
			wantErr: true,
		},
		{
			name:    "empty string",
			jsonStr: "",
			wantErr: true,
		},
		{
			name: "transaction with fee",
			jsonStr: `{
				"body": {
					"messages": [],
					"memo": "test memo",
					"timeout_height": "0",
					"extension_options": [],
					"non_critical_extension_options": []
				},
				"auth_info": {
					"signer_infos": [],
					"fee": {
						"amount": [{"denom": "stake", "amount": "200"}],
						"gas_limit": "200000",
						"payer": "",
						"granter": ""
					}
				},
				"signatures": []
			}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			txBytes, err := decodeTxFromJSON(createClientContext(t), tt.jsonStr)

			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error but got none")
				return
			}

			assert.NilError(t, err)
			assert.Assert(t, len(txBytes) > 0, "expected non-empty transaction bytes")

			// Verify we can decode the bytes back
			interfaceRegistry := codectypes.NewInterfaceRegistry()
			migrationtypes.RegisterInterfaces(interfaceRegistry)
			networktypes.RegisterInterfaces(interfaceRegistry)
			protoCodec := codec.NewProtoCodec(interfaceRegistry)
			txConfig := authtx.NewTxConfig(protoCodec, authtx.DefaultSignModes)

			decodedTx, err := txConfig.TxDecoder()(txBytes)
			assert.NilError(t, err)
			assert.Assert(t, decodedTx != nil)
		})
	}
}

func TestDecodeTxFromFile(t *testing.T) {
	// Create a temporary directory
	tmpDir := t.TempDir()

	validTxJSON := `{
		"body": {
			"messages": [],
			"memo": "test from file",
			"timeout_height": "0",
			"extension_options": [],
			"non_critical_extension_options": []
		},
		"auth_info": {
			"signer_infos": [],
			"fee": {
				"amount": [],
				"gas_limit": "200000",
				"payer": "",
				"granter": ""
			}
		},
		"signatures": []
	}`

	tests := []struct {
		name        string
		setupFile   func() string
		wantErr     bool
		errContains string
	}{
		{
			name: "valid transaction file",
			setupFile: func() string {
				filePath := filepath.Join(tmpDir, "valid_tx.json")
				err := os.WriteFile(filePath, []byte(validTxJSON), 0644)
				assert.NilError(t, err)
				return filePath
			},
			wantErr: false,
		},
		{
			name: "non-existent file",
			setupFile: func() string {
				return filepath.Join(tmpDir, "nonexistent.json")
			},
			wantErr:     true,
			errContains: "reading file",
		},
		{
			name: "invalid json in file",
			setupFile: func() string {
				filePath := filepath.Join(tmpDir, "invalid.json")
				err := os.WriteFile(filePath, []byte(`{invalid json}`), 0644)
				assert.NilError(t, err)
				return filePath
			},
			wantErr:     true,
			errContains: "parsing JSON",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := tt.setupFile()
			txBytes, err := decodeTxFromFile(createClientContext(t), filePath)

			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error but got none")
				if tt.errContains != "" {
					assert.ErrorContains(t, err, tt.errContains)
				}
				return
			}

			assert.NilError(t, err)
			assert.Assert(t, len(txBytes) > 0, "expected non-empty transaction bytes")
		})
	}
}

func TestRoundTripEncodeDecode(t *testing.T) {
	// Create a simple transaction
	interfaceRegistry := codectypes.NewInterfaceRegistry()
	migrationtypes.RegisterInterfaces(interfaceRegistry)
	networktypes.RegisterInterfaces(interfaceRegistry)
	protoCodec := codec.NewProtoCodec(interfaceRegistry)
	txConfig := authtx.NewTxConfig(protoCodec, authtx.DefaultSignModes)

	txBuilder := txConfig.NewTxBuilder()
	txBuilder.SetMemo("test round trip")
	txBuilder.SetGasLimit(200000)
	txBuilder.SetFeeAmount(sdk.NewCoins(sdk.NewCoin("stake", math.NewInt(200))))

	// Encode to bytes
	txEncoder := txConfig.TxEncoder()
	txBytes, err := txEncoder(txBuilder.GetTx())
	assert.NilError(t, err)

	// Encode to JSON
	txJSONEncoder := txConfig.TxJSONEncoder()
	txJSON, err := txJSONEncoder(txBuilder.GetTx())
	assert.NilError(t, err)

	// Decode from JSON back to bytes using our function
	decodedBytes, err := decodeTxFromJSON(createClientContext(t), string(txJSON))
	assert.NilError(t, err)

	// Verify the bytes match
	assert.DeepEqual(t, txBytes, decodedBytes)

	// Verify we can decode back to transaction
	txDecoder := txConfig.TxDecoder()
	decodedTx, err := txDecoder(decodedBytes)
	assert.NilError(t, err)

	// Verify memo matches
	txWithMemo, ok := decodedTx.(sdk.TxWithMemo)
	assert.Assert(t, ok, "transaction should implement TxWithMemo")
	assert.Equal(t, "test round trip", txWithMemo.GetMemo())
}

func TestAutoDetectFileVsJSON(t *testing.T) {
	tmpDir := t.TempDir()

	validTxJSON := `{
		"body": {
			"messages": [],
			"memo": "auto-detect test",
			"timeout_height": "0",
			"extension_options": [],
			"non_critical_extension_options": []
		},
		"auth_info": {
			"signer_infos": [],
			"fee": {
				"amount": [],
				"gas_limit": "200000",
				"payer": "",
				"granter": ""
			}
		},
		"signatures": []
	}`

	// Create a test file
	filePath := filepath.Join(tmpDir, "test_tx.json")
	err := os.WriteFile(filePath, []byte(validTxJSON), 0644)
	assert.NilError(t, err)

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "detect file path",
			input:   filePath,
			wantErr: false,
		},
		{
			name:    "detect JSON string",
			input:   validTxJSON,
			wantErr: false,
		},
		{
			name:    "non-existent file treated as invalid JSON",
			input:   "/nonexistent/path/to/file.json",
			wantErr: true,
		},
		{
			name:    "invalid JSON string",
			input:   "{invalid json}",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var txData []byte
			var err error

			// Simulate the auto-detect logic from postTxRunE
			if _, statErr := os.Stat(tt.input); statErr == nil {
				// Input is a file path
				txData, err = decodeTxFromFile(createClientContext(t), tt.input)
			} else {
				// Input is a JSON string
				txData, err = decodeTxFromJSON(createClientContext(t), tt.input)
			}

			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error but got none")
				return
			}

			assert.NilError(t, err)
			assert.Assert(t, len(txData) > 0, "expected non-empty transaction bytes")
		})
	}
}

func TestTransactionJSONStructure(t *testing.T) {
	// Test that we can properly marshal and unmarshal transaction JSON
	txJSON := `{
		"body": {
			"messages": [],
			"memo": "structure test",
			"timeout_height": "100",
			"extension_options": [],
			"non_critical_extension_options": []
		},
		"auth_info": {
			"signer_infos": [],
			"fee": {
				"amount": [{"denom": "stake", "amount": "1000"}],
				"gas_limit": "300000",
				"payer": "",
				"granter": ""
			}
		},
		"signatures": []
	}`

	// Decode to bytes
	txBytes, err := decodeTxFromJSON(createClientContext(t), txJSON)
	assert.NilError(t, err)

	// Decode bytes to transaction
	interfaceRegistry := codectypes.NewInterfaceRegistry()
	migrationtypes.RegisterInterfaces(interfaceRegistry)
	networktypes.RegisterInterfaces(interfaceRegistry)
	protoCodec := codec.NewProtoCodec(interfaceRegistry)
	txConfig := authtx.NewTxConfig(protoCodec, authtx.DefaultSignModes)

	decodedTx, err := txConfig.TxDecoder()(txBytes)
	assert.NilError(t, err)

	// Verify structure
	txWithMemo, ok := decodedTx.(sdk.TxWithMemo)
	assert.Assert(t, ok)
	assert.Equal(t, "structure test", txWithMemo.GetMemo())

	feeTx, ok := decodedTx.(sdk.FeeTx)
	assert.Assert(t, ok)
	assert.Equal(t, uint64(300000), feeTx.GetGas())
	assert.Equal(t, "1000stake", feeTx.GetFee().String())
}
