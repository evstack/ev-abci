// Package v1 contains the module configuration types for symstaking.
package v1

import (
	_ "cosmossdk.io/depinject/appconfig/v1alpha1"
)

// Module is the config object for the symstaking module.
type Module struct {
	// authority defines the custom module authority. If not set, defaults to the governance module.
	Authority string
	// relay_rpc_address is the gRPC address of the Symbiotic relay sidecar.
	RelayRpcAddress string
	// key_file is the path to the validator keys file (for mock mode).
	KeyFile string
}

// GetAuthority returns the module authority.
func (m *Module) GetAuthority() string {
	if m != nil {
		return m.Authority
	}
	return ""
}

// GetRelayRpcAddress returns the relay RPC address.
func (m *Module) GetRelayRpcAddress() string {
	if m != nil {
		return m.RelayRpcAddress
	}
	return ""
}

// GetKeyFile returns the key file path.
func (m *Module) GetKeyFile() string {
	if m != nil {
		return m.KeyFile
	}
	return ""
}
