// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

func leaveStaging(t *testing.T, h *host, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := writeFile(filepath.Join(h.dir, ".sard-staging", n, "srv/a.txt"), "A", 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// Scenario: При старте агент удаляет остатки временных каталогов и пишет об этом.
func TestAtStartTheAgentRemovesTheStagingDirectoriesAndLogsEachOne(t *testing.T) {
	h := newHost(t)
	leaveStaging(t, h, "c1", "c2")
	if err := os.WriteFile(filepath.Join(h.dir, ".sard-staging", "stray"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if left := h.engine.CleanStaging(); len(left) != 0 {
		t.Fatalf("left = %+v", left)
	}
	if got := tree(t, filepath.Join(h.dir, ".sard-staging")); !slices.Equal(got, []string{"."}) {
		t.Errorf("staging holds %v", got)
	}
	log := h.log.String()
	for _, name := range []string{"c1", "c2", "stray"} {
		if n := strings.Count(log, filepath.Join(h.dir, ".sard-staging", name)); n != 1 {
			t.Errorf("%s is logged %d times:\n%s", name, n, log)
		}
	}
}

func TestWithoutARestoreDirectoryThereIsNothingToClean(t *testing.T) {
	h := newHost(t)
	if left := h.engine.CleanStaging(); len(left) != 0 {
		t.Fatalf("left = %+v", left)
	}
	if _, err := os.Stat(h.dir); !os.IsNotExist(err) {
		t.Errorf("the restore directory was made: %v", err)
	}
}

// Scenario: Неудачная уборка при старте называется в результате прерванного шага.
func TestAStagingDirectoryThatCannotBeRemovedAtStartIsLeftAndLoggedAsAnError(t *testing.T) {
	h := newHost(t)
	leaveStaging(t, h, "c1", "c2")
	c1 := filepath.Join(h.dir, ".sard-staging", "c1")
	h.faults.removeDirErr = map[string]error{c1: syscall.EIO}
	left := h.engine.CleanStaging()
	if len(left) != 1 || left[0].Name != "c1" || left[0].Path != c1 || left[0].Err == nil {
		t.Fatalf("left = %+v", left)
	}
	if _, err := os.Stat(filepath.Join(h.dir, ".sard-staging", "c2")); !os.IsNotExist(err) {
		t.Errorf("c2 was not removed: %v", err)
	}
	mentions(t, h.log.String(), "level=ERROR", c1)
}

func TestACleanupNeverFollowsALinkOfTheStagingDirectory(t *testing.T) {
	h := newHost(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := writeFile(filepath.Join(outside, "keep"), "K", 0o600); err != nil {
		t.Fatal(err)
	}
	mustMkdirAll(t, h.dir)
	if err := os.Symlink(outside, filepath.Join(h.dir, ".sard-staging")); err != nil {
		t.Fatal(err)
	}
	left := h.engine.CleanStaging()
	if len(left) != 1 || left[0].Path != filepath.Join(h.dir, ".sard-staging") {
		t.Fatalf("left = %+v", left)
	}
	if _, err := os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Errorf("the target of the link was cleaned: %v", err)
	}
}
