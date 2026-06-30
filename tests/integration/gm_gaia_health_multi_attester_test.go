package integration_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateAttesterIdentitiesCreatesDistinctOperatorsAndConsensusKeys(t *testing.T) {
	identities, err := generateAttesterIdentities(4)
	require.NoError(t, err)
	require.Len(t, identities, 4)

	operatorAddresses := map[string]struct{}{}
	consensusAddresses := map[string]struct{}{}
	for _, identity := range identities {
		require.NotEmpty(t, identity.OperatorArmor)
		require.NotEmpty(t, identity.OperatorAddress.String())
		require.NotEmpty(t, identity.ConsensusAddress)
		require.NotEmpty(t, identity.PrivValidatorKeyJSON)
		require.NotEmpty(t, identity.PrivValidatorStateJSON)

		operatorAddresses[identity.OperatorAddress.String()] = struct{}{}
		consensusAddresses[identity.ConsensusAddress] = struct{}{}
	}
	require.Len(t, operatorAddresses, 4)
	require.Len(t, consensusAddresses, 4)
}

func TestSetGenesisAttestersWritesAllGeneratedAttesters(t *testing.T) {
	identities, err := generateAttesterIdentities(4)
	require.NoError(t, err)

	genesis := []byte(`{"app_state":{"network":{"params":{}}}}`)
	updated, err := setGenesisAttesters(genesis, identities)
	require.NoError(t, err)

	var genDoc map[string]interface{}
	require.NoError(t, json.Unmarshal(updated, &genDoc))
	appState := genDoc["app_state"].(map[string]interface{})
	network := appState["network"].(map[string]interface{})
	attesterInfos := network["attester_infos"].([]interface{})
	require.Len(t, attesterInfos, 4)

	for i, rawInfo := range attesterInfos {
		info := rawInfo.(map[string]interface{})
		require.Equal(t, identities[i].OperatorAddress.String(), info["authority"])
		require.Equal(t, identities[i].ConsensusAddress, info["consensus_address"])
		require.Equal(t, float64(0), info["joined_height"])

		pubkey := info["pubkey"].(map[string]interface{})
		require.Equal(t, "/cosmos.crypto.ed25519.PubKey", pubkey["@type"])
		pubKeyBytes, err := base64.StdEncoding.DecodeString(pubkey["key"].(string))
		require.NoError(t, err)
		require.Equal(t, identities[i].ConsensusPubKey.Bytes(), pubKeyBytes)
	}
}

func TestValidatorSetFromAttestersUsesGeneratedConsensusKeys(t *testing.T) {
	identities, err := generateAttesterIdentities(4)
	require.NoError(t, err)

	valSet := validatorSetFromAttesters(identities)
	require.Len(t, valSet.Validators, 4)
	require.Equal(t, int64(4), valSet.TotalVotingPower())

	expectedAddresses := map[string]struct{}{}
	for _, identity := range identities {
		expectedAddresses[identity.ConsensusPubKey.Address().String()] = struct{}{}
	}
	for _, validator := range valSet.Validators {
		_, ok := expectedAddresses[validator.Address.String()]
		require.True(t, ok, "validator %s is not one of the generated attesters", validator.Address.String())
	}
}
