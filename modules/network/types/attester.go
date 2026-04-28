package types

import (
	"fmt"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

func NewAttesterInfo(authority string, pk cryptotypes.PubKey, joinedHeight int64) (*AttesterInfo, error) {
	any, err := codectypes.NewAnyWithValue(pk)
	if err != nil {
		return nil, fmt.Errorf("pack pubkey: %w", err)
	}
	return &AttesterInfo{
		Authority:        authority,
		Pubkey:           any,
		JoinedHeight:     joinedHeight,
		ConsensusAddress: sdk.ConsAddress(pk.Address()).String(),
	}, nil
}

// GetPubKey extracts the cryptotypes.PubKey from the Any field.
func (a AttesterInfo) GetPubKey() (cryptotypes.PubKey, error) {
	if a.Pubkey == nil {
		return nil, fmt.Errorf("pubkey not set")
	}
	pk, ok := a.Pubkey.GetCachedValue().(cryptotypes.PubKey)
	if ok {
		return pk, nil
	}
	return nil, fmt.Errorf("pubkey cached value not cryptotypes.PubKey")
}

// UnpackInterfaces ensures GetCachedValue works after unmarshaling.
func (a AttesterInfo) UnpackInterfaces(unpacker codectypes.AnyUnpacker) error {
	var pk cryptotypes.PubKey
	return unpacker.UnpackAny(a.Pubkey, &pk)
}
