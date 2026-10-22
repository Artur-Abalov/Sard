// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Every directory and file the engine opens, it closes: a long-running agent
// leaks no descriptors, whatever the outcome of the restore.
func TestTheEngineClosesEverythingItOpens(t *testing.T) {
	for name, run := range leakCases {
		t.Run(name, func(t *testing.T) {
			h := newHost(t)
			run(t, h)
			h.faults.noLeaks(t)
		})
	}
}

// leakCases are the ways a restore, or the start after one, ends.
var leakCases = map[string]func(t *testing.T, h *host){
	"a restore": func(t *testing.T, h *host) {
		if _, f := h.run(h.request()); f != nil {
			t.Fatal(f)
		}
	},
	"a restore into an existing empty destination": func(t *testing.T, h *host) {
		mustMkdirAll(t, h.dest())
		if _, f := h.run(h.request()); f != nil {
			t.Fatal(f)
		}
	},
	"a refused non-empty destination": func(t *testing.T, h *host) {
		mustMkdirAll(t, h.dest())
		if err := os.WriteFile(filepath.Join(h.dest(), "keep"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, f := h.run(h.request()); f == nil {
			t.Fatal("restored")
		}
	},
	"a mount point in the destination path": func(t *testing.T, h *host) {
		mustMkdirAll(t, h.dest())
		h.faults.device = map[string]uint64{filepath.Join(h.dir, "s1"): 999999}
		if _, f := h.run(h.request()); f == nil {
			t.Fatal("restored")
		}
	},
	"a failure of restic": func(t *testing.T, h *host) {
		h.repo.restore = writesThenFails(errors.New("boom"))
		if _, f := h.run(h.request()); f == nil {
			t.Fatal("restored")
		}
	},
	"a failed move": func(t *testing.T, h *host) {
		h.faults.moveErr = syscall.ENOTEMPTY
		if _, f := h.run(h.request()); f == nil {
			t.Fatal("restored")
		}
	},
	"the start after a restore that completed": func(t *testing.T, _ *host) {
		killed := killedAfterTheMove(t)
		killed.faults.noLeaks(t) // the first run
		killed.engine.CleanStaging()
		killed.engine.Settle("c1")
		killed.faults.noLeaks(t) // the start
	},
	"the start after a restore killed during restic": func(t *testing.T, h *host) {
		leaveStaging(t, h, "c1")
		mustMkdirAll(t, filepath.Join(c1Dir(h), "root"))
		h.engine.CleanStaging()
	},
	"a staging directory that cannot be listed": func(t *testing.T, h *host) {
		mustMkdirAll(t, c1Dir(h))
		h.faults.namesErr = map[string]error{filepath.Join(h.dir, ".sard-staging"): syscall.EIO}
		h.engine.CleanStaging()
		h.engine.Settle("c1")
	},
}

// A directory that cannot be synced does not stop the cleanup, but the
// operator is told: the order of marker and root then rests on the journal of
// the filesystem.
func TestAFailedSyncOfTheCommandDirectoryIsLoggedAndTheCleanupGoesOn(t *testing.T) {
	h := newHost(t)
	c1 := c1Dir(h)
	if err := writeFile(filepath.Join(c1, "root/srv/a.txt"), "A", 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(c1, "marker"), `{"destination":"d"}`, 0o600); err != nil {
		t.Fatal(err)
	}
	h.faults.syncErr = map[string]error{c1: syscall.EIO}
	got := h.engine.CleanStaging()
	if len(got) != 1 || got[0].Err != nil {
		t.Fatalf("evidence = %+v", got)
	}
	if _, err := os.Stat(c1); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the directory was not removed: %v", err)
	}
	log := h.log.String()
	if !strings.Contains(log, "level=WARN") || !strings.Contains(log, c1) {
		t.Fatalf("want a WARN naming %s; log:\n%s", c1, log)
	}
}
