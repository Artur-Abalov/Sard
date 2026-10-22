// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

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

// PlanRestore implements sdk.Restorer. The plugin restores a database itself
// (sdk.RestoreRunner), so the plan names no directory: the call only reads the
// config, and an invalid one rejects the step before anything runs.
func (Plugin) PlanRestore(cfg sdk.Config) (sdk.RestorePlan, error) {
	_, err := parseRestore(cfg)
	return sdk.RestorePlan{}, err
}

// restoreSettings are what a restore into a new database needs from the config.
type restoreSettings struct {
	// to is the server the database is created on, and the tool that restores it.
	to target
	// database is the name of the new database: 1-63 bytes, no NUL.
	database string
	// globals also restores the global objects of the cluster (roles).
	globals bool
}

// parseRestore reads the restore config. The schema counts characters, this
// the bytes PostgreSQL limits.
func parseRestore(cfg sdk.Config) (restoreSettings, error) {
	var c restoreConfig
	if err := json.Unmarshal(cfg, &c); err != nil {
		return restoreSettings{}, &sdk.ConfigError{Violations: []sdk.Violation{{Message: err.Error()}}}
	}
	v := tooLong("/host", []string{c.Host}, maxHostBytes, false)
	v = append(v, tooLong("/user", []string{c.User}, maxNameBytes, false)...)
	v = append(v, checkDatabaseName(c.Database)...)
	if len(v) > 0 {
		return restoreSettings{}, &sdk.ConfigError{Violations: v}
	}
	port := defaultPort
	if c.Port != nil {
		port = *c.Port
	}
	return restoreSettings{
		to:       target{Host: c.Host, Port: port, User: c.User, PasswordRef: c.PasswordRef, TLSMode: c.TLSMode, TLSRootCert: c.TLSRootCert, ClientPath: c.PgRestorePath},
		database: c.Database, globals: c.RestoreGlobals,
	}, nil
}

func checkDatabaseName(name string) []sdk.Violation {
	switch {
	case len(name) < 1 || len(name) > maxNameBytes:
		return []sdk.Violation{{Path: "/database", Message: fmt.Sprintf("want 1-%d bytes, got %d", maxNameBytes, len(name))}}
	case strings.ContainsRune(name, 0):
		return []sdk.Violation{{Path: "/database", Message: "must not contain a NUL byte"}}
	}
	return nil
}
