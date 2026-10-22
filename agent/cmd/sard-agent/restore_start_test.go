// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

// R2a (ВК14). Scenario: При старте агент удаляет остатки временных каталогов и пишет об этом.
func TestAtStartTheAgentRemovesTheTemporaryDirectoriesOfInterruptedRestores(t *testing.T) {
	var staging string
	code, log := startAgentWith(t, func(dir string) string {
		staging = filepath.Join(dir, "restore", ".sard-staging")
		for _, d := range []string{"c1/srv", "c2"} {
			if err := os.MkdirAll(filepath.Join(staging, d), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		writeFile(t, filepath.Join(staging, "c1", "srv", "a.txt"), []byte("A"), 0o600)
		writeFile(t, filepath.Join(staging, "c2", "b.txt"), []byte("B"), 0o600)
		return "restore: {dir: " + filepath.Join(dir, "restore") + "}\n"
	})
	if code != 0 {
		t.Fatalf("code = %d, log:\n%s", code, log)
	}
	entries, err := os.ReadDir(staging)
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging: %v, %v", entries, err)
	}
	for _, id := range []string{"c1", "c2"} {
		if n := strings.Count(log, filepath.Join(staging, id)); n != 1 {
			t.Errorf("%s is logged %d times:\n%s", id, n, log)
		}
	}
}

// Scenario: Register сообщает каталог восстановления — the agent knows the directory of the host.
func TestTheAgentAnnouncesTheRestoreDirectoryOfTheConfig(t *testing.T) {
	cfg, err := config.Parse([]byte("server:\n  address: s:1\nrestore:\n  dir: /srv/restore\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := buildAgent(cfg, "db1", "restic", "", nil, nil).RestoreDir; got != "/srv/restore" {
		t.Errorf("RestoreDir = %q", got)
	}
	cfg, _ = config.Parse([]byte("server:\n  address: s:1\n"))
	if got := buildAgent(cfg, "db1", "restic", "", nil, nil).RestoreDir; got != "/var/lib/sard-agent/restore" {
		t.Errorf("default RestoreDir = %q", got)
	}
}
