// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins/postgresql"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// host is the sdk.Host of a step run without the agent.
type host struct {
	mu   sync.Mutex
	logs []string
}

func (*host) Secret(name string) ([]byte, error) {
	if name != "pg-app" {
		return nil, &sdk.SecretError{Name: name}
	}
	return []byte(P + "\n"), nil
}

func (*host) Progress(uint64, uint64) {}

func (h *host) Log(_ sdk.Level, text string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logs = append(h.logs, text)
}

// scripts writes fake pg_dump, psql and pg_dumpall into a directory of the
// test; they answer like version 18 and print the password they got.
func scripts(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("pg_dump", `case "$1" in --version) echo "pg_dump (PostgreSQL) 18.0";; *) printf 'PGDMP'; printf '%s' "$PGPASSWORD";; esac`)
	write("pg_dumpall", `case "$1" in --version) echo "pg_dumpall (PostgreSQL) 18.0";; *) echo "CREATE ROLE r;";; esac`)
	write("psql", `echo "180000|f|t|backup"`)
	return dir
}

// Without a Runner and an FS the plugin starts the processes of the host and
// looks at its file system.
func TestPluginWithoutRunnerAndFSUsesTheHost(t *testing.T) {
	dir := scripts(t)
	p := postgresql.Plugin{AgentVersion: "1.2.3"}
	h := &host{}
	cfg := sdk.Config(js(k(o{"pg_dump_path": filepath.Join(dir, "pg_dump"), "include_globals": true})))
	if err := p.Prepare(context.Background(), h, cfg); err != nil {
		t.Fatal(err)
	}
	d, err := p.Dump(context.Background(), h, cfg)
	if err != nil || d.Filename != "app.dump" || len(d.Extra) != 1 || string(d.Extra[0].Content) != "CREATE ROLE r;\n" {
		t.Fatalf("Dump = %+v, %v", d, err)
	}
	var out bytes.Buffer
	if err := p.Stream(context.Background(), h, cfg, d, &out); err != nil || out.String() != "PGDMP"+P {
		t.Fatalf("Stream: %v, %q", err, out.String())
	}
}

// The reason of a file system error is the operating system's, without the path.
func TestHostFileSystemErrorsAreSpelledOut(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for path, reason := range map[string]string{
		filepath.Join(dir, "none", "pg_dump"): "no such file or directory",
		filepath.Join(file, "pg_dump"):        "no such file or directory",
		dir:                                   "is a directory",
		file:                                  "permission denied",
	} {
		err := postgresql.Plugin{}.Prepare(context.Background(), &host{}, sdk.Config(js(k(o{"pg_dump_path": path}))))
		if err == nil || !strings.Contains(err.Error(), reason) {
			t.Errorf("%s: err = %v, want %q", path, err, reason)
		}
	}
}

// Dump asks for a step that was prepared.
func TestDumpWithoutPrepareFails(t *testing.T) {
	if _, err := (postgresql.Plugin{}).Dump(context.Background(), &host{}, sdk.Config(js(k()))); err == nil {
		t.Error("Dump of a step that was not prepared succeeded")
	}
}
