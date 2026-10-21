// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package plugins_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	pgclientPkg   = module + "/plugins/postgresql/pgclient"
	postgresqlPkg = module + "/plugins/postgresql"
)

// agentGoFiles lists the Go files of the agent module, and the package of each.
func agentGoFiles(t *testing.T) map[string]string {
	t.Helper()
	out, err := exec.Command("go", "list", "-f", "{{.ImportPath}}\t{{.Dir}}\t{{join .GoFiles \"\\t\"}}", module+"/...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	files := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, "\t")
		for _, name := range fields[2:] {
			if name != "" {
				files[filepath.Join(fields[1], name)] = fields[0]
			}
		}
	}
	return files
}

// Scenario: Клиент PostgreSQL принадлежит плагину postgresql. The password
// reaches a PostgreSQL program through pgclient only (ADR 0065 §2, ПГ3).
func TestOnlyPgclientPutsThePasswordIntoTheEnvironment(t *testing.T) {
	files := agentGoFiles(t)
	if len(files) == 0 {
		t.Fatal("go list found no Go files: the rule checked nothing")
	}
	for path, pkg := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if pkg != pgclientPkg && bytes.Contains(data, []byte(`"PGPASSWORD="`)) {
			t.Errorf("%s sets PGPASSWORD itself; use pgclient.Env", path)
		}
	}
}

func TestOnlyThePluginPostgresqlImportsTheClientOfPostgreSQL(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", "{{.ImportPath}} {{join .Imports \" \"}}", module+"/...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		found = found || fields[0] == pgclientPkg
		for _, imp := range fields[1:] {
			if !mayImportClient(fields[0], imp) {
				t.Errorf("%s imports %s: only %s may import %s", fields[0], imp, postgresqlPkg, pgclientPkg)
			}
		}
	}
	if !found {
		t.Errorf("there is no package %s: the client of PostgreSQL lies inside the plugin", pgclientPkg)
	}
}

// mayImportClient: pkg imports imp, and if imp is a client package of a database
// it is the client of PostgreSQL and pkg is the plugin postgresql.
func mayImportClient(pkg, imp string) bool {
	return !strings.HasSuffix(imp, "/pgclient") || (imp == pgclientPkg && pkg == postgresqlPkg)
}
