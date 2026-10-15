// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// writesThenFails plays a restic that writes a.txt and then fails.
func writesThenFails(err error) func(context.Context, restic.RestoreRequest) (restic.RestoreSummary, error) {
	return func(_ context.Context, req restic.RestoreRequest) (restic.RestoreSummary, error) {
		if werr := writeFile(filepath.Join(restoreRoot(req), "srv/data/a.txt"), "AAA", 0o640); werr != nil {
			return restic.RestoreSummary{}, werr
		}
		return restic.RestoreSummary{Files: 1}, err
	}
}

// noTraces: the restore directory holds what it held before the step.
func noTraces(t *testing.T, h *host, before []string) {
	t.Helper()
	if after := tree(t, h.dir); !slices.Equal(before, after) {
		t.Errorf("traces left in the restore directory:\nbefore %v\nafter  %v", before, after)
	}
}

// Scenario: Сбой restic удаляет временный каталог, назначение не появляется.
func TestAFailureOfResticRemovesTheStagingDirectoryAndThereIsNoDestination(t *testing.T) {
	incomplete := &restic.RestoreError{Items: []restic.ItemError{{Item: "/srv/data/x", Message: "lchown: operation not permitted"}}}
	noSpace := &restic.RestoreError{Items: []restic.ItemError{{Item: "/srv/data/x", Message: "write: no space left on device"}}}
	for name, tt := range map[string]struct {
		err  error
		want string
	}{
		"the storage stops answering": {fmt.Errorf("restic restore: %w: %w", restic.ErrNetwork, &restic.ExitError{Code: 1, Message: "Fatal: connection refused"}), "BACKEND_UNAVAILABLE (restore)"},
		"no space":                    {fmt.Errorf("restic restore: %w", restic.ErrNoSpace), "TARGET_WRITE (restore)"},
		"quota":                       {fmt.Errorf("restic restore: %w", noSpace), "TARGET_WRITE (restore)"},
		"read-only":                   {restic.ErrReadOnly, "TARGET_WRITE (restore)"},
		"a file is not restored":      {incomplete, "RESTORE_INCOMPLETE (restore)"},
		"exit code 1":                 {&restic.ExitError{Code: 1, Message: "Fatal: boom"}, "RESTIC_FAILED (restore)"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHost(t)
			mustMkdirAll(t, h.dir)
			before := tree(t, h.dir)
			h.repo.restore = writesThenFails(tt.err)
			_, f := h.run(h.request())
			hasPrefix(t, f, tt.want)
			if !strings.HasSuffix(f.Error(), "nothing was restored") {
				t.Errorf("message %q", f.Error())
			}
			noTraces(t, h, before)
		})
	}
}

// Scenario: Неполное восстановление называет число и первые пути, но ничего не оставляет.
func TestAnIncompleteRestoreNamesTheNumberAndTheFirstPathsButLeavesNothing(t *testing.T) {
	h := newHost(t)
	var items []restic.ItemError
	for i := range 12 {
		items = append(items, restic.ItemError{Item: fmt.Sprintf("/srv/f%02d", i), Message: "lchown: operation not permitted"})
	}
	h.repo.restore = writesThenFails(&restic.RestoreError{Items: items})
	_, f := h.run(h.request())
	hasPrefix(t, f, "RESTORE_INCOMPLETE (restore)")
	mentions(t, f.Error(), "12", `"/srv/f00"`, `"/srv/f09"`)
	if strings.Contains(f.Error(), "/srv/f10") {
		t.Errorf("more than the first 10 paths are named: %q", f.Error())
	}
}

// Scenario: После отката пустое назначение, бывшее до шага, остаётся на месте.
func TestAnEmptyDestinationThatWasThereBeforeTheStepStaysAfterTheRollback(t *testing.T) {
	h := newHost(t)
	mustMkdirAll(t, h.dest())
	h.repo.restore = writesThenFails(errors.New("boom"))
	_, f := h.run(h.request())
	hasPrefix(t, f, "RESTIC_FAILED (restore)")
	if got := tree(t, h.dest()); !slices.Equal(got, []string{"."}) {
		t.Errorf("destination holds %v", got)
	}
	if got := tree(t, filepath.Join(h.dir, ".sard-staging")); len(got) != 0 {
		t.Errorf("staging holds %v", got)
	}
}

// Scenario: Отмена шага останавливает restic и удаляет временный каталог.
func TestAStoppedStepRemovesTheStagingDirectory(t *testing.T) {
	h := newHost(t)
	ctx, cancel := context.WithCancel(context.Background())
	h.repo.restore = func(ctx context.Context, req restic.RestoreRequest) (restic.RestoreSummary, error) {
		if err := writeFile(filepath.Join(restoreRoot(req), "a"), "A", 0o600); err != nil {
			return restic.RestoreSummary{}, err
		}
		cancel()
		<-ctx.Done()
		return restic.RestoreSummary{}, fmt.Errorf("restic restore: %w", ctx.Err())
	}
	_, f := h.engine.Restore(ctx, h.repo, h.request(), h.rep)
	hasPrefix(t, f, "INTERRUPTED (restore)")
	if _, err := os.Stat(h.dir); !os.IsNotExist(err) {
		t.Errorf("the restore directory made by the step is still there: %v", err)
	}
}

// Scenario: Сбой переноса удаляет временный каталог и созданных родителей.
func TestAFailedMoveRemovesTheStagingDirectoryAndTheParentsTheStepMade(t *testing.T) {
	for name, tt := range map[string]struct {
		err  error
		want string
	}{
		"ENOTEMPTY": {syscall.ENOTEMPTY, "TARGET_NOT_EMPTY (move)"},
		"EXDEV":     {syscall.EXDEV, "TARGET_INVALID (move)"},
		"EIO":       {syscall.EIO, "TARGET_WRITE (move)"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHost(t)
			mustMkdirAll(t, h.dir)
			before := tree(t, h.dir)
			h.faults.moveErr = tt.err
			_, f := h.run(h.request())
			hasPrefix(t, f, tt.want)
			noTraces(t, h, before)
		})
	}
}

// What appeared in the destination not by the agent is not touched.
func TestAForeignFileThatAppearedInTheDestinationIsNotTouched(t *testing.T) {
	h := newHost(t)
	h.faults.moveHook = func() {
		_ = writeFile(filepath.Join(h.dest(), "foreign.txt"), "F", 0o600)
	}
	_, f := h.run(h.request())
	hasPrefix(t, f, "TARGET_NOT_EMPTY (move)")
	if got, err := os.ReadFile(filepath.Join(h.dest(), "foreign.txt")); err != nil || string(got) != "F" {
		t.Errorf("foreign.txt: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(h.dir, ".sard-staging", "c1")); !os.IsNotExist(err) {
		t.Errorf("the staging directory is still there: %v", err)
	}
}

// Scenario: Откат не удаляет то, чего агент не создавал.
func TestTheRollbackDoesNotRemoveWhatTheAgentDidNotMake(t *testing.T) {
	h := newHost(t)
	h.repo.restore = func(_ context.Context, _ restic.RestoreRequest) (restic.RestoreSummary, error) {
		if err := writeFile(filepath.Join(h.dir, "s1/other"), "O", 0o600); err != nil {
			return restic.RestoreSummary{}, err
		}
		return restic.RestoreSummary{}, errors.New("boom")
	}
	_, f := h.run(h.request())
	hasPrefix(t, f, "RESTIC_FAILED (restore)")
	if _, err := os.Stat(filepath.Join(h.dir, "s1/other")); err != nil {
		t.Errorf("s1/other: %v", err)
	}
}

// Scenario: Неудачное удаление временного каталога — отдельный исход с тем, что осталось.
func TestAFailedRemovalOfTheStagingDirectoryIsACleanupFailureThatNamesWhatIsLeft(t *testing.T) {
	h := newHost(t)
	staging := filepath.Join(h.dir, ".sard-staging", "c1")
	h.faults.removeDirErr = map[string]error{staging: syscall.EIO}
	h.repo.restore = writesThenFails(errors.New("boom"))
	_, f := h.run(h.request())
	hasPrefix(t, f, "CLEANUP_FAILED (rollback)")
	mentions(t, f.Error(), "RESTIC_FAILED (restore)", "boom", staging, "by hand")
	if strings.Contains(f.Error(), "nothing was restored") {
		t.Errorf("a failed rollback says nothing was restored: %q", f.Error())
	}
	if !slices.Equal(f.Leftover, []string{staging}) {
		t.Errorf("leftover = %q", f.Leftover)
	}
	if _, err := os.Stat(h.dest()); !os.IsNotExist(err) {
		t.Errorf("destination: %v", err)
	}
	log := h.log.String()
	mentions(t, log, "level=ERROR", staging)
}
