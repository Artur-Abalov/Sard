// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

var _ sdk.CopyRestorer = Plugin{}

// copyConfig is a config of the source that points at the restored database:
// Fingerprint measures the copy through it, with psql beside pg_restore (its
// pg_dump_path only names that directory: Fingerprint needs no pg_dump).
type copyConfig struct {
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Database       string `json:"database"`
	User           string `json:"user"`
	PasswordRef    string `json:"password_ref"`
	TLSMode        string `json:"tls_mode,omitempty"`
	TLSRootCert    string `json:"tls_root_cert,omitempty"`
	PgDumpPath     string `json:"pg_dump_path,omitempty"`
	IncludeGlobals bool   `json:"include_globals"`
}

// RestoreInto implements sdk.CopyRestorer: the restore of R2b into a new
// database of the target (Restore), all or nothing; the copy is that database.
func (p Plugin) RestoreInto(ctx context.Context, h sdk.RestoreHost, target sdk.Config, req sdk.RestoreRequest) (sdk.RestoredCopy, error) {
	out, err := p.Restore(ctx, h, target, req)
	if err != nil {
		return sdk.RestoredCopy{}, err
	}
	settings, err := parseRestore(target) // Restore read it already: it is valid
	if err != nil {
		return sdk.RestoredCopy{}, err
	}
	to := settings.to
	c := copyConfig{Host: to.Host, Port: to.Port, Database: out.Database, User: to.User, PasswordRef: to.PasswordRef, TLSMode: to.TLSMode, TLSRootCert: to.TLSRootCert}
	if to.ClientPath != "" {
		c.PgDumpPath = filepath.Join(filepath.Dir(to.ClientPath), "pg_dump")
	}
	cfg, err := json.Marshal(c)
	if err != nil {
		return sdk.RestoredCopy{}, err
	}
	return sdk.RestoredCopy{Config: cfg, Result: out}, nil
}
