package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	cmtcfg "github.com/cometbft/cometbft/config"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	cmttypes "github.com/cometbft/cometbft/types"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"

	"github.com/evstack/ev-node/pkg/genesis"
)

// getAppGenesis returns the app genesis based on its location in the config.
func getAppGenesis(cfg *cmtcfg.Config) (*genutiltypes.AppGenesis, error) {
	appGenesis, err := genutiltypes.AppGenesisFromFile(cfg.GenesisFile())
	if err != nil {
		return nil, fmt.Errorf("failed to read genesis from file %s: %w", cfg.GenesisFile(), err)
	}

	return appGenesis, nil
}

const evolveGenesisFilename = "ev_genesis.json"

// loadEvolveMigrationGenesis loads a minimal evolve genesis from a migration genesis file.
// Returns nil if no migration genesis is found (normal startup scenario).
func loadEvolveMigrationGenesis(rootDir string) (*evolveMigrationGenesis, error) {
	genesisPath := filepath.Join(rootDir, evolveGenesisFilename)
	if _, err := os.Stat(genesisPath); os.IsNotExist(err) {
		return nil, nil // no migration genesis found
	}

	genesisBytes, err := os.ReadFile(genesisPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read evolve migration genesis: %w", err)
	}

	var migrationGenesis evolveMigrationGenesis
	// using cmtjson for unmarshalling to ensure compatibility with cometbft genesis format
	if err := cmtjson.Unmarshal(genesisBytes, &migrationGenesis); err != nil {
		return nil, fmt.Errorf("failed to unmarshal evolve migration genesis: %w", err)
	}

	return &migrationGenesis, nil
}

// createEvolveGenesisFromCometBFT creates a evolve genesis from cometbft genesis.
// This is used for normal startup scenarios where a full cometbft genesis document
// is available and contains all the necessary information.
func createEvolveGenesisFromCometBFT(cmtGenDoc *cmttypes.GenesisDoc, daStartHeight, forcedInclusionEpochSize uint64) *genesis.Genesis {
	gen := genesis.NewGenesis(
		cmtGenDoc.ChainID,
		uint64(cmtGenDoc.InitialHeight),
		cmtGenDoc.GenesisTime,
		cmtGenDoc.Validators[0].Address.Bytes(), // use the first validator as sequencer
	)

	if daStartHeight > 0 {
		gen.DAStartHeight = daStartHeight
	}

	if forcedInclusionEpochSize > 0 {
		gen.DAEpochForcedInclusion = forcedInclusionEpochSize
	}

	return &gen
}

// getJSONTag extracts the JSON tag value for a given field name using reflection.
func getJSONTag(v any, fieldName string) (string, error) {
	t := reflect.TypeOf(v)
	field, ok := t.FieldByName(fieldName)
	if !ok {
		return "", fmt.Errorf("field %s not found in type %s", fieldName, t.Name())
	}

	jsonTag := field.Tag.Get("json")
	if jsonTag == "" {
		return "", fmt.Errorf("field %s does not have a json tag", fieldName)
	}

	// Handle tags like "field_name,omitempty" by taking only the first part
	if idx := strings.Index(jsonTag, ","); idx != -1 {
		jsonTag = jsonTag[:idx]
	}

	return jsonTag, nil
}

// getDaStartHeight parses the da_start_height from the genesis file.
func getDaStartHeight(cfg *cmtcfg.Config) (uint64, error) {
	fieldTag, err := getJSONTag(genesis.Genesis{}, "DAStartHeight")
	if err != nil {
		return 0, fmt.Errorf("failed to get JSON tag for DAStartHeight: %w", err)
	}

	genFile, err := os.Open(filepath.Clean(cfg.GenesisFile()))
	if err != nil {
		return 0, fmt.Errorf("failed to open genesis file %s: %w", cfg.GenesisFile(), err)
	}

	daStartHeight, err := parseFieldFromGenesis(bufio.NewReader(genFile), fieldTag)
	if err != nil {
		return 0, err
	}

	if err := genFile.Close(); err != nil {
		return 0, fmt.Errorf("failed to close genesis file %s: %v", genFile.Name(), err)
	}

	return daStartHeight, nil
}

// getDaEpoch parses the da_epoch_forced_inclusion from the genesis file.
func getDaEpoch(cfg *cmtcfg.Config) (uint64, error) {
	fieldTag, err := getJSONTag(genesis.Genesis{}, "DAEpochForcedInclusion")
	if err != nil {
		return 0, fmt.Errorf("failed to get JSON tag for DAEpochForcedInclusion: %w", err)
	}

	genFile, err := os.Open(filepath.Clean(cfg.GenesisFile()))
	if err != nil {
		return 0, fmt.Errorf("failed to open genesis file %s: %w", cfg.GenesisFile(), err)
	}

	daEpochSize, err := parseFieldFromGenesis(bufio.NewReader(genFile), fieldTag)
	if err != nil {
		return 0, err
	}

	if err := genFile.Close(); err != nil {
		return 0, fmt.Errorf("failed to close genesis file %s: %v", genFile.Name(), err)
	}

	return daEpochSize, nil
}

// parseFieldFromGenesis parses given fields from a genesis JSON file, aborting early after finding the field.
// For efficiency, it's recommended to place the field before any large entries in the JSON file.
// Returns no error when the field is not found.
// Logic based on https://github.com/cosmos/cosmos-sdk/blob/v0.50.14/x/genutil/types/chain_id.go#L18.
func parseFieldFromGenesis(r io.Reader, fieldName string) (uint64, error) {
	dec := json.NewDecoder(r)

	t, err := dec.Token()
	if err != nil {
		return 0, err
	}
	if t != json.Delim('{') {
		return 0, fmt.Errorf("expected {, got %s", t)
	}

	for dec.More() {
		t, err = dec.Token()
		if err != nil {
			return 0, err
		}
		key, ok := t.(string)
		if !ok {
			return 0, fmt.Errorf("expected string for the key type, got %s", t)
		}

		if key == fieldName {
			var chainID uint64
			if err := dec.Decode(&chainID); err != nil {
				return 0, err
			}
			return chainID, nil
		}

		// skip the value
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return 0, err
		}
	}

	return 0, nil
}
