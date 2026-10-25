// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/plugins/postgresql"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

var _ sdk.CopyRestorer = postgresql.Plugin{}

// V2: RestoreInto is the restore of R2b into a new database of the target;
// the copy is then reached by a config of the source on that database, which
// the schema of the source accepts.
func TestRestoreIntoRestoresAndPointsTheFingerprintAtTheNewDatabase(t *testing.T) {
	h := newPGHost(t)
	req := h.request()
	req.Plan.TLSMode = "verify-full"
	req.Plan.TLSRootCert = "/etc/sard/ca.pem"
	h.host.step = context.Background()
	c, err := h.plugin().RestoreInto(context.Background(), h.host, req.Plan.config(), sdk.RestoreRequest{
		CommandID: req.CommandID, SnapshotID: req.SnapshotID, Repository: req.Repository, Source: req.Source,
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Result.Database != rpNewDatabase || c.Root != "" {
		t.Errorf("copy = %+v", c)
	}
	var got map[string]any
	if err := json.Unmarshal(c.Config, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"host": "db2", "port": float64(5432), "database": rpNewDatabase, "user": "restorer", "password_ref": rpPasswordRef,
		"tls_mode": "verify-full", "tls_root_cert": "/etc/sard/ca.pem", "include_globals": false}
	if !maps.Equal(got, want) {
		t.Errorf("config = %v\nwant %v", got, want)
	}
	schema, err := pluginhost.CompileSchema("postgresql", postgresql.Plugin{}.ConfigSchema(), func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(c.Config); err != nil {
		t.Errorf("the config of the copy breaks the schema: %v", err)
	}
}

// The tools of the restore are those of the fingerprint of the copy: psql beside pg_restore.
func TestTheConfigOfTheCopyNamesTheToolsOfTheRestore(t *testing.T) {
	h := newPGHost(t)
	req := h.request()
	req.Plan.ClientPath = "/opt/pg/bin/pg_restore"
	h.host.step = context.Background()
	h.tools.nodes["/opt/pg/bin/pg_restore"], h.tools.nodes["/opt/pg/bin/psql"] = 0o755, 0o755
	c, err := h.plugin().RestoreInto(context.Background(), h.host, req.Plan.config(), sdk.RestoreRequest{CommandID: "c1", SnapshotID: req.SnapshotID, Repository: "main", Source: req.Source})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(c.Config, &got)
	if got["pg_dump_path"] != "/opt/pg/bin/pg_dump" {
		t.Errorf("pg_dump_path = %v", got["pg_dump_path"])
	}
}

func TestAFailedRestoreIntoIsTheFailureOfTheRestore(t *testing.T) {
	h := newPGHost(t)
	req := h.request()
	req.Plan.Name = ""
	h.host.step = context.Background()
	_, err := h.plugin().RestoreInto(context.Background(), h.host, req.Plan.config(), sdk.RestoreRequest{CommandID: "c1", SnapshotID: req.SnapshotID, Repository: "main", Source: req.Source})
	var re *sdk.RestoreError
	if !errors.As(err, &re) {
		t.Errorf("err = %v", err)
	}
}
