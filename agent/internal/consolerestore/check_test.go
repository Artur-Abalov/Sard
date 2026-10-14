// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore_test

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// nothingWritten: D_R was not even created and restic did not run.
func nothingWritten(t *testing.T, h *host) {
	t.Helper()
	if _, err := os.Stat(h.dir); !os.IsNotExist(err) {
		t.Errorf("the restore directory exists: %v", err)
	}
	if len(h.repo.requests) != 0 {
		t.Errorf("restic restore ran %d times", len(h.repo.requests))
	}
}

// Scenario: Неизвестный снимок — отказ без записи.
func TestAnUnknownSnapshotIsRefusedWithoutWriting(t *testing.T) {
	h := newHost(t)
	h.repo.snapshots = nil
	_, f := h.run(h.request())
	hasPrefix(t, f, "SNAPSHOT_UNKNOWN (check)")
	mentions(t, f.Error(), snapshotID, "nothing was restored")
	nothingWritten(t, h)
}

// Scenario: Снимок другого источника — отказ без записи.
func TestASnapshotOfAnotherSourceIsRefusedWithoutWriting(t *testing.T) {
	h := newHost(t)
	snap := snapshotF()
	snap.Tags = []string{"sard.source=s7"}
	h.repo.snapshots = []restic.Snapshot{snap}
	_, f := h.run(h.request())
	hasPrefix(t, f, "SNAPSHOT_UNKNOWN (check)")
	mentions(t, f.Error(), "s1")
	nothingWritten(t, h)
}

// Scenario: Путь, которого нет в снимке, — отказ до записи.
func TestAPathThatIsNotInTheSnapshotIsRefusedBeforeWriting(t *testing.T) {
	h := newHost(t)
	req := h.request()
	req.Plan.Paths = []string{"/srv/data", "/srv/nothing"}
	h.repo.paths["/srv/data"] = true
	_, f := h.run(req)
	hasPrefix(t, f, "PATH_NOT_IN_SNAPSHOT (check)")
	mentions(t, f.Error(), "/srv/nothing")
	nothingWritten(t, h)
}

// Scenario: Недоступное хранилище до начала записи ничего не создаёт.
func TestAnUnreachableStorageBeforeWritingCreatesNothing(t *testing.T) {
	for name, tt := range map[string]struct {
		listErr, pathErr error
	}{
		"listing":    {listErr: fmt.Errorf("restic snapshots: %w", restic.ErrNetwork)},
		"path check": {pathErr: fmt.Errorf("restic ls: %w", restic.ErrNetwork)},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHost(t)
			h.repo.listErr, h.repo.pathErr = tt.listErr, tt.pathErr
			req := h.request()
			req.Plan.Paths = []string{"/srv/data"}
			_, f := h.run(req)
			hasPrefix(t, f, "BACKEND_UNAVAILABLE (check)")
			nothingWritten(t, h)
		})
	}
}

func TestOnlyTheSnapshotsOfTheSourceAreListed(t *testing.T) {
	h := newHost(t)
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	if len(h.repo.listed) != 1 || len(h.repo.listed[0]) != 1 || h.repo.listed[0][0] != "sard.source=s1" {
		t.Errorf("listed with tags %q", h.repo.listed)
	}
}

func TestOtherFailuresOfResticWhileCheckingAreToldByTheirKind(t *testing.T) {
	for name, tt := range map[string]struct {
		err  error
		want string
	}{
		"wrong password":      {restic.ErrWrongPassword, "WRONG_PASSWORD (check)"},
		"no repository":       {restic.ErrNoRepository, "REPOSITORY_NOT_INITIALIZED (check)"},
		"unexpected output":   {restic.ErrBadOutput, "RESTIC_OUTPUT_UNEXPECTED (check)"},
		"fatal":               {&restic.ExitError{Code: 1, Message: "Fatal: boom"}, "RESTIC_FAILED (check)"},
		"something else":      {errors.New("boom"), "RESTIC_FAILED (check)"},
		"a locked repository": {restic.ErrLocked, "RESTIC_FAILED (check)"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHost(t)
			h.repo.listErr = tt.err
			_, f := h.run(h.request())
			hasPrefix(t, f, tt.want)
			nothingWritten(t, h)
		})
	}
}
