// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"encoding/json"
	"fmt"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Limits in bytes; the schema counts characters (F1 ПГ6).
const (
	maxHostBytes    = 255
	maxNameBytes    = 63
	maxPatternBytes = 1024
	// maxKeyTableBytes is schema.table: two names and the dot.
	maxKeyTableBytes = 2*maxNameBytes + 1
	defaultPort      = 5432
	tlsDisable       = "disable"
	tlsRequire       = "require"
)

// config is a valid document of the schema.
type config struct {
	Host                 string   `json:"host"`
	Port                 *int     `json:"port"`
	Database             string   `json:"database"`
	User                 string   `json:"user"`
	PasswordRef          string   `json:"password_ref"`
	TLSMode              string   `json:"tls_mode"`
	TLSRootCert          string   `json:"tls_root_cert"`
	ExcludeSchemas       []string `json:"exclude_schemas"`
	ExcludeTables        []string `json:"exclude_tables"`
	KeyTables            []string `json:"key_tables"`
	PgDumpPath           string   `json:"pg_dump_path"`
	IncludeGlobals       *bool    `json:"include_globals"`
	GlobalsRolePasswords bool     `json:"globals_role_passwords"`
	// grant is what the binding of the secret allows, once it is read.
	grant sdk.Grant
}

// parse reads the config and reports what the schema cannot express: a
// string longer than its limit in bytes.
func parse(cfg sdk.Config) (config, error) {
	var c config
	if err := json.Unmarshal(cfg, &c); err != nil {
		return c, &sdk.ConfigError{Violations: []sdk.Violation{{Message: err.Error()}}}
	}
	v := tooLong("/host", []string{c.Host}, maxHostBytes, false)
	v = append(v, tooLong("/database", []string{c.Database}, maxNameBytes, false)...)
	v = append(v, tooLong("/user", []string{c.User}, maxNameBytes, false)...)
	v = append(v, tooLong("/exclude_schemas", c.ExcludeSchemas, maxPatternBytes, true)...)
	v = append(v, tooLong("/exclude_tables", c.ExcludeTables, maxPatternBytes, true)...)
	v = append(v, tooLong("/key_tables", c.KeyTables, maxKeyTableBytes, true)...)
	if len(v) > 0 {
		return c, &sdk.ConfigError{Violations: v}
	}
	return c, nil
}

// tooLong lists the values longer than limit bytes. A list item is named by
// its index, a single value by the field.
func tooLong(field string, values []string, limit int, list bool) []sdk.Violation {
	var out []sdk.Violation
	for i, s := range values {
		if len(s) <= limit {
			continue
		}
		path := field
		if list {
			path = fmt.Sprintf("%s/%d", field, i)
		}
		out = append(out, sdk.Violation{Path: path, Message: fmt.Sprintf("longer than %d bytes", limit)})
	}
	return out
}

func (c config) port() int {
	if c.Port == nil {
		return defaultPort
	}
	return *c.Port
}

func (c config) globals() bool { return c.IncludeGlobals == nil || *c.IncludeGlobals }
