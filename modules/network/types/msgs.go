package types

import (
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const TypeMsgAttest = "attest"
const TypeMsgJoinAttesterSet = "join_attester_set"
const TypeMsgLeaveAttesterSet = "leave_attester_set"
const TypeMsgUpdateParams = "update_params"

var _ sdk.Msg = &MsgAttest{}
var _ sdk.Msg = &MsgJoinAttesterSet{}
var _ sdk.Msg = &MsgLeaveAttesterSet{}
var _ sdk.Msg = &MsgUpdateParams{}

// NewMsgAttest creates a new MsgAttest instance
func NewMsgAttest(authority string, consensusAddress string, height int64, vote []byte) *MsgAttest {
	return &MsgAttest{
		Authority:        authority,
		ConsensusAddress: consensusAddress,
		Height:           height,
		Vote:             vote,
	}
}

// NewMsgJoinAttesterSet creates a new MsgJoinAttesterSet instance
func NewMsgJoinAttesterSet(authority string, consensusAddress string, pubkey cryptotypes.PubKey) (*MsgJoinAttesterSet, error) {
	pubkeyAny, err := codectypes.NewAnyWithValue(pubkey)
	if err != nil {
		return nil, err
	}

	return &MsgJoinAttesterSet{
		Authority:        authority,
		ConsensusAddress: consensusAddress,
		Pubkey:           pubkeyAny,
	}, nil
}

// NewMsgLeaveAttesterSet creates a new MsgLeaveAttesterSet instance
func NewMsgLeaveAttesterSet(authority string, consensusAddress string) *MsgLeaveAttesterSet {
	return &MsgLeaveAttesterSet{
		Authority:        authority,
		ConsensusAddress: consensusAddress,
	}
}

// NewMsgUpdateParams creates a new MsgUpdateParams instance
func NewMsgUpdateParams(authority string, params Params) *MsgUpdateParams {
	return &MsgUpdateParams{
		Authority: authority,
		Params:    params,
	}
}
