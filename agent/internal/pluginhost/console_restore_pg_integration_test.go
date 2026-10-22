// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build integration

package pluginhost_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/restorejournal"
	"github.com/Artur-Abalov/sard/agent/plugins/postgresql"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// The restore of a database (R2b, R2c-2) by the postgresql plugin through the host, with the pinned restic and
// a local repository: the dump comes out of restic dump byte for byte into the standard input of pg_restore,
// and restic dump that read the table of contents is stopped. pg_restore and psql are scripts of the test.

// scriptTools writes pg_restore (it saves the dump it is given to out) and psql (it answers like a healthy server).
func scriptTools(t *testing.T, out string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("pg_restore", `
if [ "$1" = "--version" ]; then echo "pg_restore (PostgreSQL) 16.4"; exit 0; fi
if [ "$1" = "-l" ]; then head -c 5 >/dev/null; echo "; header"; echo "1; 1259 1 TABLE public t app"; echo "2; 1259 2 TABLE public u app"; exit 0; fi
cat > `+out+`
`)
	write("psql", `
sql=$(cat)
case "$sql" in
*server_version_num*) echo "160004|f|t|restorer" ;;
*pg_control_system*) echo 42 ;;
*"exists(select 1"*) echo f ;;
*max_locks*) echo 6400 ;;
*"select oid from pg_database"*) echo 20001 ;;
*"from pg_class"*) echo 3 ;;
esac
`)
	return dir
}

// pgHandler is the handler of the plugin postgresql over the repository, with the tools of the test.
func pgHandler(t *testing.T, repo restic.Repository, tools string) *pluginhost.Handlers {
	t.Helper()
	state := t.TempDir()
	reg, err := sdk.NewRegistry(postgresql.Plugin{AgentVersion: "test", Environ: func() []string { return []string{"PATH=" + tools + ":/usr/bin:/bin"} }})
	if err != nil {
		t.Fatal(err)
	}
	password := filepath.Join(t.TempDir(), "pg")
	if err := os.WriteFile(password, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	journal := pluginhost.NewJournal(restorejournal.New(restorejournal.Options{
		Dir: filepath.Join(state, "restore-journal"), FS: hostsetup.OS{}, Owner: hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid(), Mode: 0o700},
	}), slog.New(slog.DiscardHandler))
	handlers, err := pluginhost.NewHandlers(reg, pluginhost.NewSecrets(map[string]string{"pg": password}, os.ReadFile),
		func(string, string, io.Writer) (restic.Repository, bool) { return repo, true }, filepath.Join(state, "restore"),
		pluginhost.WithRestoreJournal(journal))
	if err != nil {
		t.Fatal(err)
	}
	return handlers
}

func TestIntegrationADatabaseIsStreamedFromResticIntoPgRestoreByteForByte(t *testing.T) {
	r := newRepo(t)
	archive := "PGDMP" + strings.Repeat("x", 3<<20)
	sum, err := r.cli.Backup(context.Background(), restic.BackupRequest{
		Tags:          []string{"sard.source=s2", "postgresql.part=database", "postgresql.database=app", "postgresql.pg_dump_version=16.4", "postgresql.server_version=16.4"},
		StdinFilename: "app.dump",
		Stdin:         func(_ context.Context, w io.Writer) error { _, err := io.WriteString(w, archive); return err },
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored.dump")
	tools := scriptTools(t, restored)
	handler, _ := pgHandler(t, r.cli, tools).Handler("postgresql")
	res, err := handler.Run(context.Background(), &agentv1.RunStep{
		CommandId: "c1", Plugin: "postgresql", Action: agentv1.Action_ACTION_RESTORE, RepositoryName: "it", SnapshotId: sum.SnapshotID,
		Tags:       map[string]string{"sard.source": "s2"},
		ConfigJson: `{"host": "db", "user": "restorer", "tls_mode": "disable", "password_ref": "pg", "database": "app_restored"}`,
	}, &reporter{})
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(restored)
	if err != nil || !bytes.Equal(got, []byte(archive)) {
		t.Errorf("the dump differs: %d bytes, %v", len(got), err)
	}
	if out := res.GetRestore(); out.GetDatabase() != "app_restored" || out.GetTablesRestored() != 3 {
		t.Errorf("output %v", out)
	}
	if leftovers := processesOf(tools); len(leftovers) != 0 {
		t.Errorf("processes left: %v", leftovers)
	}
}
