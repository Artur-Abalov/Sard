// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package files

import (
	"encoding/json"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// RestoreConfigSchema implements sdk.Restorer.
func (Plugin) RestoreConfigSchema() []byte { return restoreSchema }

// restoreConfig is a valid document of the restore schema.
type restoreConfig struct {
	Destination string   `json:"destination"`
	Paths       []string `json:"paths"`
}

// PlanRestore implements sdk.Restorer. The schema counts characters, the
// plan counts the bytes the file system limits (sdk.RestorePlan.Validate).
func (Plugin) PlanRestore(cfg sdk.Config) (sdk.RestorePlan, error) {
	var c restoreConfig
	if err := json.Unmarshal(cfg, &c); err != nil {
		return sdk.RestorePlan{}, &sdk.ConfigError{Violations: []sdk.Violation{{Message: err.Error()}}}
	}
	plan := sdk.RestorePlan{Destination: c.Destination, Paths: c.Paths}
	return plan, plan.Validate()
}
