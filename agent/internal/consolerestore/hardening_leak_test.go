// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore_test

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Every directory and file the engine opens, it closes: a long-running agent
// leaks no descriptors, whatever the outcome of the restore.
func TestTheEngineClosesEverythingItOpens(t *testing.T) {
	tests := map[string]func(t *testing.T, h *host){
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
		"the start after a restore that completed": func(t *testing.T, h *host) {
			h = killedAfterTheMove(t)
			h.faults.noLeaks(t) // the first run
			h.engine.CleanStaging()
			h.engine.Settle("c1")
		},
		"the start after a restore killed during restic": func(t *testing.T, h *host) {
			leaveStaging(t, h, "c1")
			mustMkdirAll(t, filepath.Join(command(h, "c1"), "root"))
			h.engine.CleanStaging()
		},
		"a staging directory that cannot be listed": func(t *testing.T, h *host) {
			mustMkdirAll(t, command(h, "c1"))
			h.faults.namesErr = map[string]error{filepath.Join(h.dir, ".sard-staging"): syscall.EIO}
			h.engine.CleanStaging()
			h.engine.Settle("c1")
		},
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHost(t)
			run(t, h)
			h.faults.noLeaks(t)
		})
	}
}
