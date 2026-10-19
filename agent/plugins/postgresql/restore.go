// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	_ "embed"
	"encoding/json"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

//go:embed restore_schema.json
var restoreSchema []byte

var _ sdk.Restorer = Plugin{}

// RestoreConfigSchema implements sdk.Restorer: the restore goes into a new
// database of a server (R2b).
func (Plugin) RestoreConfigSchema() []byte { return restoreSchema }

// restoreConfig is a valid document of the restore schema.
type restoreConfig struct {
	Host           string `json:"host"`
	Port           *int   `json:"port"`
	Database       string `json:"database"`
	User           string `json:"user"`
	PasswordRef    string `json:"password_ref"`
	TLSMode        string `json:"tls_mode"`
	TLSRootCert    string `json:"tls_root_cert"`
	RestoreGlobals bool   `json:"restore_globals"`
	PgRestorePath  string `json:"pg_restore_path"`
}

// PlanRestore implements sdk.Restorer. The schema counts characters, the plan
// the bytes PostgreSQL limits.
func (Plugin) PlanRestore(cfg sdk.Config) (sdk.RestorePlan, error) {
	var c restoreConfig
	if err := json.Unmarshal(cfg, &c); err != nil {
		return sdk.RestorePlan{}, &sdk.ConfigError{Violations: []sdk.Violation{{Message: err.Error()}}}
	}
	v := tooLong("/host", []string{c.Host}, maxHostBytes, false)
	v = append(v, tooLong("/user", []string{c.User}, maxNameBytes, false)...)
	if len(v) > 0 {
		return sdk.RestorePlan{}, &sdk.ConfigError{Violations: v}
	}
	port := defaultPort
	if c.Port != nil {
		port = *c.Port
	}
	plan := sdk.RestorePlan{Postgres: &sdk.PostgresRestore{
		Host: c.Host, Port: port, User: c.User, PasswordRef: c.PasswordRef, TLSMode: c.TLSMode, TLSRootCert: c.TLSRootCert,
		Name: c.Database, Globals: c.RestoreGlobals, ClientPath: c.PgRestorePath,
	}}
	return plan, plan.Validate()
}
