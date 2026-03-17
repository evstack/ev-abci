// Package v1 contains the module configuration types for symslashing.
package v1

import (
	_ "cosmossdk.io/depinject/appconfig/v1alpha1"
)

// Module is the config object for the symslashing module.
type Module struct {
	// authority defines the custom module authority. If not set, defaults to the governance module.
	Authority string
}

func (m *Module) GetAuthority() string {
	if m != nil {
		return m.Authority
	}
	return ""
}
