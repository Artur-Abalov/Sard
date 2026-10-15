// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// Every directory and file the start and the settling open, they close.
func TestTheStartAndTheSettlingCloseEverythingTheyOpen(t *testing.T) {
	completed := func(t *testing.T, h *host) {
		if err := os.WriteFile(filepath.Join(command(h, "c1"), "marker"), []byte(`{"destination":"s1/x"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tests := map[string]func(t *testing.T, h *host){
		"a restore that completed": func(t *testing.T, h *host) {
			mustMkdirAll(t, command(h, "c1"))
			completed(t, h)
			h.engine.CleanStaging()
			h.engine.Settle("c1")
		},
		"a restore killed during restic": func(t *testing.T, h *host) {
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
		"a marker that cannot be read": func(t *testing.T, h *host) {
			mustMkdirAll(t, command(h, "c1"))
			completed(t, h)
			h.faults.readErr = map[string]error{filepath.Join(command(h, "c1"), "marker"): syscall.EIO}
			h.engine.CleanStaging()
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

func TestOnlyOneComponentCanNameTheDirectoryOfACommand(t *testing.T) {
	tests := map[string]bool{"c1": true, "a.b": true, "": false, ".": false, "..": false, "a/b": false, "../c": false}
	for id, want := range tests {
		if got := consolerestore.UsableName(id); got != want {
			t.Errorf("UsableName(%q) = %v, want %v", id, got, want)
		}
	}
}

// The console shows a restore in two phases, both before any byte is known.
func TestARestoreReportsPreparingAndThenRestoringWithNothingCountedYet(t *testing.T) {
	h := newHost(t)
	h.repo.restore = func(ctx context.Context, req restic.RestoreRequest) (restic.RestoreSummary, error) {
		return writesF(ctx, req)
	}
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	want := []event{
		{phase: agentv1.StepPhase_STEP_PHASE_PREPARING},
		{phase: agentv1.StepPhase_STEP_PHASE_RESTORING},
	}
	if got := h.rep.events; len(got) < 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("events = %+v, want to start with %+v", got, want)
	}
}

// A restic that leaves no root (it was removed under the engine) is a failure
// that leaves no trace, not a crash.
func TestAMissingRootAfterResticIsAFailureThatLeavesNoTrace(t *testing.T) {
	h := newHost(t)
	h.repo.restore = func(_ context.Context, req restic.RestoreRequest) (restic.RestoreSummary, error) {
		return restic.RestoreSummary{}, os.RemoveAll(restoreRoot(req))
	}
	before := tree(t, h.dir)
	_, f := h.run(h.request())
	if f == nil {
		t.Fatal("restored")
	}
	if _, err := os.Stat(h.dest()); !os.IsNotExist(err) {
		t.Errorf("destination: %v", err)
	}
	noTraces(t, h, before)
}

func TestTheStartLogsARestoreThatCompletedAndTheSettlingOfIt(t *testing.T) {
	h := killedAfterTheMove(t)
	h.engine.CleanStaging()
	mentions(t, h.log.String(), "level=INFO", "the restore completed before the agent was restarted", command(h, "c1"), "destination="+destRel)
	h.log.Reset()
	h.engine.Settle("c1")
	mentions(t, h.log.String(), "level=INFO", "removed the temporary directory of a restore", command(h, "c1"))
}

func TestTheStartLogsTheRemovalOfAnInterruptedRestore(t *testing.T) {
	h := newHost(t)
	leaveStaging(t, h, "c1")
	h.engine.CleanStaging()
	mentions(t, h.log.String(), "level=INFO", "removed the temporary directory of an interrupted restore", command(h, "c1"))
}

func TestSettleLogsAnErrorWhenStagingCannotBeOpened(t *testing.T) {
	h := newHost(t)
	mustMkdirAll(t, h.dir)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(h.dir, ".sard-staging")); err != nil {
		t.Fatal(err)
	}
	h.engine.Settle("c1")
	mentions(t, h.log.String(), "level=ERROR", filepath.Join(h.dir, ".sard-staging"))
}

func TestAStagingDirectoryThatCannotBeListedIsReportedAndNothingIsRemoved(t *testing.T) {
	h := newHost(t)
	leaveStaging(t, h, "c1")
	staging := filepath.Join(h.dir, ".sard-staging")
	h.faults.namesErr = map[string]error{staging: syscall.EIO}
	got := h.engine.CleanStaging()
	if len(got) != 1 || got[0].Verdict != consolerestore.CleanupFailed || got[0].Path != staging || got[0].Err == nil {
		t.Fatalf("evidence = %+v", got)
	}
	mentions(t, h.log.String(), "level=ERROR", staging)
	if _, err := os.Stat(command(h, "c1")); err != nil {
		t.Errorf("c1: %v", err)
	}
}

// The failures of the move say what happened, in words an operator can act on.
func TestAFailedMoveTellsWhatHappenedToTheDestination(t *testing.T) {
	tests := map[string]struct {
		err   error
		words []string
	}{
		"ENOTEMPTY": {syscall.ENOTEMPTY, []string{"is not empty", "something turned up in it while restic worked"}},
		"EXDEV":     {syscall.EXDEV, []string{"mount point inside the restore directory", "on another file system than the temporary directory"}},
		"EIO":       {syscall.EIO, []string{"cannot move the restored directory to", "input/output error"}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHost(t)
			h.faults.moveErr = tt.err
			_, f := h.run(h.request())
			if f == nil {
				t.Fatal("restored")
			}
			mentions(t, f.Detail, append([]string{h.dest()}, tt.words...)...)
		})
	}
}

func TestACleanupFailureOfARollbackSaysWhyInTheFailure(t *testing.T) {
	h := newHost(t)
	staging := command(h, "c1")
	h.faults.removeDirErr = map[string]error{staging: syscall.EIO}
	h.repo.restore = writesThenFails(errors.New("boom"))
	_, f := h.run(h.request())
	if f == nil {
		t.Fatal("restored")
	}
	mentions(t, f.Cleanup, "input/output error")
	mentions(t, f.Error(), "("+f.Cleanup+")")
}

func TestARestoreDirectoryThatCannotBeMadeNamesTheSettingOfTheUnit(t *testing.T) {
	h := newHost(t)
	h.faults.mkdirErr = map[string]error{h.dir: syscall.EROFS}
	_, f := h.run(h.request())
	hasPrefix(t, f, "TARGET_WRITE (check)")
	mentions(t, f.Error(), h.dir, "ReadWritePaths")
	if len(h.repo.requests) != 0 {
		t.Error("restic ran")
	}
}

func TestATemporaryRootThatCannotBeMadeLeavesNothing(t *testing.T) {
	h := newHost(t)
	mustMkdirAll(t, h.dir)
	before := tree(t, h.dir)
	root := filepath.Join(command(h, "c1"), "root")
	h.faults.mkdirErr = map[string]error{root: syscall.EROFS}
	_, f := h.run(h.request())
	hasPrefix(t, f, "TARGET_WRITE (check)")
	mentions(t, f.Error(), root)
	noTraces(t, h, before)
}

// --- the marker

func TestAMarkerThatCannotBeWrittenFailsTheRestoreAndLeavesNothing(t *testing.T) {
	for name, inject := range map[string]func(f *faults, marker string){
		"cannot be created": func(f *faults, m string) { f.createErr = map[string]error{m: syscall.EIO} },
		"cannot be written": func(f *faults, m string) { f.writeErr = map[string]error{m: syscall.ENOSPC} },
		"cannot be synced":  func(f *faults, m string) { f.fileErr = map[string]error{m: syscall.EIO} },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHost(t)
			inject(h.faults, filepath.Join(command(h, "c1"), "marker.tmp"))
			_, f := h.run(h.request())
			hasPrefix(t, f, "TARGET_WRITE (move)")
			mentions(t, f.Error(), "cannot write the marker of the restore")
			if _, err := os.Stat(h.dest()); !os.IsNotExist(err) {
				t.Errorf("destination: %v", err)
			}
			if got := tree(t, filepath.Join(h.dir, ".sard-staging")); len(got) > 1 {
				t.Errorf("staging holds %v", got)
			}
		})
	}
}

// A marker that was not made whole never stays as a temporary file.
func TestAMarkerThatFailsToBeRenamedLeavesNoTemporaryFile(t *testing.T) {
	h := newHost(t)
	dir, _, err := hostsetup.OpenDir(faultFS{hostsetup.OS{}, h.faults}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dir.Close() }()
	h.faults.renameErr = syscall.EIO
	if err := consolerestore.WriteMarker(dir, destRel, 1, 2); err == nil {
		t.Fatal("written")
	}
	if got := tree(t, dir.Path()); !slices.Equal(got, []string{"."}) {
		t.Errorf("the directory holds %v", got)
	}
}

// stuckAt is the evidence of the start on a host where c1 holds a marker the setup spoiled.
func stuckAt(t *testing.T, setup func(h *host)) (*host, consolerestore.Evidence) {
	t.Helper()
	h := newHost(t)
	mustMkdirAll(t, command(h, "c1"))
	setup(h)
	got := h.engine.CleanStaging()
	if len(got) != 1 || got[0].Verdict != consolerestore.CleanupFailed || got[0].Err == nil {
		t.Fatalf("evidence = %+v", got)
	}
	if _, err := os.Stat(command(h, "c1")); err != nil {
		t.Errorf("c1: %v", err)
	}
	h.faults.noLeaks(t)
	return h, got[0]
}

func TestAMarkerThatIsALinkIsNotEvidenceAndStays(t *testing.T) {
	stuckAt(t, func(h *host) {
		if err := os.Symlink("elsewhere", filepath.Join(command(h, "c1"), "marker")); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAMarkerThatCannotBeReadIsNotEvidenceAndStaysAndNamesTheError(t *testing.T) {
	_, ev := stuckAt(t, func(h *host) {
		m := filepath.Join(command(h, "c1"), "marker")
		if err := os.WriteFile(m, []byte(`{"destination":"d"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		h.faults.readErr = map[string]error{m: syscall.EIO}
	})
	if !errors.Is(ev.Err, syscall.EIO) {
		t.Errorf("the evidence names %v, not the read error", ev.Err)
	}
}

func TestAnUnreadableMarkerSaysSo(t *testing.T) {
	h := newHost(t)
	if err := writeFile(filepath.Join(command(h, "c1"), "marker"), "{not json", 0o600); err != nil {
		t.Fatal(err)
	}
	got := h.engine.CleanStaging()
	if len(got) != 1 || got[0].Err == nil {
		t.Fatalf("evidence = %+v", got)
	}
	mentions(t, got[0].Err.Error(), "the marker is unreadable")
}

// The marker goes first and the removal of it reaches the disk before root is
// touched: a kill in between never leaves a marker without root.
func TestTheRemovalOfTheMarkerIsSyncedBeforeRootIsRemoved(t *testing.T) {
	h := newHost(t)
	c1 := command(h, "c1")
	if err := writeFile(filepath.Join(c1, "root/srv/a.txt"), "A", 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c1, "marker"), []byte(`{"destination":"d"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h.engine.CleanStaging()
	at := func(want string, from int) int {
		for i := from; i < len(h.faults.ops); i++ {
			if strings.HasPrefix(h.faults.ops[i], want) {
				return i
			}
		}
		return -1
	}
	m := at("remove "+filepath.Join(c1, "marker"), 0)
	s := at("sync "+c1, m+1)
	r := at("remove "+filepath.Join(c1, "root"), s+1)
	if m < 0 || s < 0 || r < 0 {
		t.Errorf("want the marker removed, c1 synced, root removed, in this order: %v", h.faults.ops)
	}
}

// --- the destination

func TestADestinationPathCrossingALinkNamesTheLink(t *testing.T) {
	h := newHost(t)
	mustMkdirAll(t, h.dir)
	if err := os.Symlink(t.TempDir(), filepath.Join(h.dir, "s1")); err != nil {
		t.Fatal(err)
	}
	_, f := h.run(h.request())
	hasPrefix(t, f, "TARGET_INVALID (check)")
	mentions(t, f.Detail, filepath.Join(h.dir, "s1")+" is a symbolic link; the destination must not cross one")
}

func TestARestoreDirectoryThatIsALinkIsRefused(t *testing.T) {
	h := newHost(t)
	if err := os.Symlink(t.TempDir(), h.dir); err != nil {
		t.Fatal(err)
	}
	_, f := h.run(h.request())
	hasPrefix(t, f, "TARGET_INVALID (check)")
	mentions(t, f.Detail, "is a symbolic link")
	if len(h.repo.requests) != 0 {
		t.Error("restic ran")
	}
}

// What the operating system reports about a component, wrapped or not, is
// told as the component and what it is.
func TestADestinationComponentThatIsNotADirectoryIsToldPlainly(t *testing.T) {
	h := newHost(t)
	mustMkdirAll(t, h.dir)
	s1 := filepath.Join(h.dir, "s1")
	h.faults.openErr = map[string]error{s1: &hostsetup.NotDirError{Path: s1}}
	_, f := h.run(h.request())
	hasPrefix(t, f, "TARGET_INVALID (check)")
	if f.Detail != s1+" is not a directory" {
		t.Errorf("detail = %q", f.Detail)
	}
}

func TestADestinationThatCannotBeJudgedIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	tests := map[string]func(h *host){
		"the restore directory cannot be looked at": func(h *host) { h.faults.statErr = map[string]error{h.dir: syscall.EIO} },
		"a component cannot be looked at":           func(h *host) { h.faults.statErr = map[string]error{filepath.Join(h.dir, "s1"): syscall.EIO} },
		"the destination cannot be listed":          func(h *host) { h.faults.namesErr = map[string]error{h.dest(): syscall.EIO} },
		"a component cannot be opened":              func(h *host) { h.faults.openErr = map[string]error{filepath.Join(h.dir, "s1"): syscall.EIO} },
	}
	for name, inject := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHost(t)
			mustMkdirAll(t, h.dest())
			before := tree(t, h.dir)
			inject(h)
			_, f := h.run(h.request())
			hasPrefix(t, f, "TARGET_INVALID (check)")
			mentions(t, f.Detail, "input/output error")
			noTraces(t, h, before)
			if len(h.repo.requests) != 0 {
				t.Error("restic ran")
			}
		})
	}
}

// --- the repository as the messages describe it

func TestTheTargetOfTheOptionsDescribesTheRepositoryInTheMessages(t *testing.T) {
	h := newHost(t)
	var asked string
	h.engine = consolerestore.New(consolerestore.Options{
		Dir: h.dir, FS: faultFS{hostsetup.OS{}, h.faults},
		Owner:  hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid(), Mode: 0o700},
		Logger: slog.New(slog.NewTextHandler(h.log, nil)),
		Target: func(name string) repoinit.Target {
			asked = name
			return repoinit.Target{Name: name, Backend: "s3", Scrub: func(s string) string { return strings.ReplaceAll(s, "hunter2", "[hidden]") }}
		},
	})
	h.repo.listErr = fmt.Errorf("dial hunter2: %w", restic.ErrNetwork)
	_, f := h.run(h.request())
	if f == nil {
		t.Fatal("restored")
	}
	if asked != "main" {
		t.Errorf("target asked for %q", asked)
	}
	if strings.Contains(f.Detail, "hunter2") || !strings.Contains(f.Detail, "[hidden]") {
		t.Errorf("detail = %q", f.Detail)
	}
}

// A directory that cannot be synced after the marker went does not stop the
// cleanup, and the failure is logged for the operator.
func TestAFailedSyncOfTheMarkerRemovalIsLoggedAndTheCleanupGoesOn(t *testing.T) {
	h := newHost(t)
	leaveStaging(t, h, "c1")
	h.faults.syncErr = map[string]error{command(h, "c1"): syscall.EIO}
	if left := stuck(h.engine.CleanStaging()); len(left) != 0 {
		t.Fatalf("left = %+v", left)
	}
	if _, err := os.Stat(command(h, "c1")); !os.IsNotExist(err) {
		t.Errorf("c1: %v", err)
	}
	mentions(t, h.log.String(), "level=WARN", "could not be synced", command(h, "c1"), "input/output error")
}
