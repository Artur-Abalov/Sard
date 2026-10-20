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

// agentGoFiles lists the Go files of the agent module, without the SDK:
// it is a separate module and the one place that builds PGPASSWORD.
func agentGoFiles(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-f", "{{.ImportPath}}\t{{.Dir}}\t{{join .GoFiles \"\\t\"}}", module+"/...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, "\t")
		if isSDK(fields[0]) {
			continue
		}
		for _, name := range fields[2:] {
			if name != "" {
				files = append(files, filepath.Join(fields[1], name))
			}
		}
	}
	return files
}

// The password reaches a PostgreSQL program through sdk/pgclient only (ADR 0065 §2).
func TestOnlyPgclientPutsThePasswordIntoTheEnvironment(t *testing.T) {
	files := agentGoFiles(t)
	if len(files) == 0 {
		t.Fatal("go list found no Go files: the rule checked nothing")
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(`"PGPASSWORD="`)) {
			t.Errorf("%s sets PGPASSWORD itself; use pgclient.Env", path)
		}
	}
}
