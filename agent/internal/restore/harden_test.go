// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore_test

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/restore"
)

// Rules Р61, Р64, Р73 (A8c-2): what a failure of the restore says and what it
// leaves behind, edge by edge.

// openDescriptors are the files this process holds open whose path contains part.
func openDescriptors(t *testing.T, part string) []string {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	var held []string
	for _, e := range entries {
		if link, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name())); err == nil && strings.Contains(link, part) {
			held = append(held, link)
		}
	}
	return held
}

func TestAFailureThatIsNotTheFilesOwnKeepsWhatResticSaidAndAddsThePartialResult(t *testing.T) {
	cause := fmt.Errorf("restic restore: %w", restic.ErrNetwork)
	r := &fakeRestorer{err: cause}
	req, _ := request(t, r)
	_, f := restore.Run(context.Background(), r, req, repoTarget())
	plain := repoinit.FromRestic(context.Background(), cause, repoTarget())
	if want := plain.Detail + "; partially restored files may remain in /r"; f == nil || f.Detail != want {
		t.Fatalf("failure %v, want detail %q", f, want)
	}
}

func TestExactlyTenFilesNotRestoredAreAllNamedWithoutTheCut(t *testing.T) {
	var items []restic.ItemError
	for i := range 10 {
		items = append(items, restic.ItemError{Item: fmt.Sprintf("/srv/f%02d", i)})
	}
	r := &fakeRestorer{err: &restic.RestoreError{Items: items}}
	req, _ := request(t, r)
	_, f := restore.Run(context.Background(), r, req, repoTarget())
	if f == nil || strings.Contains(f.Detail, "first") || !strings.Contains(f.Detail, `"/srv/f09"`) || !strings.Contains(f.Detail, "could not restore 10 paths") {
		t.Fatalf("failure %v", f)
	}
}

func TestMoreThanTenFilesNotRestoredSayThatOnlyTheFirstTenAreNamed(t *testing.T) {
	var items []restic.ItemError
	for i := range 11 {
		items = append(items, restic.ItemError{Item: fmt.Sprintf("/srv/f%02d", i)})
	}
	r := &fakeRestorer{err: &restic.RestoreError{Items: items}}
	req, _ := request(t, r)
	_, f := restore.Run(context.Background(), r, req, repoTarget())
	if f == nil || !strings.Contains(f.Detail, "could not restore 11 paths (the first 10):") {
		t.Fatalf("failure %v", f)
	}
}

func TestATargetThatCannotBeRelativeIsRefusedWithTheFlagAndTheValue(t *testing.T) {
	_, f := restore.CheckTarget(newRecordingFS(), restore.Target{Path: "a/b"})
	wantFailure(t, f, refusal.TargetInvalid, `--target "a/b" must be an absolute path`)
}

func TestATargetThatCannotBeListedIsInvalidAndNamesTheTarget(t *testing.T) {
	dir := t.TempDir()
	fsys := newRecordingFS()
	fsys.failNames = filepath.Base(dir)
	_, f := restore.CheckTarget(fsys, restore.Target{Path: dir})
	wantFailure(t, f, refusal.TargetInvalid, "cannot read the target "+dir)
}

func TestALostAndFoundThatCannotBeListedMakesTheTargetInvalid(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, dir, "lost+found")
	fsys := newRecordingFS()
	fsys.failNames = "lost+found"
	_, f := restore.CheckTarget(fsys, restore.Target{Path: dir})
	wantFailure(t, f, refusal.TargetInvalid, "cannot read the target "+dir)
}

func TestARefusedTargetIsNotHeldAfterwards(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "keep.txt"), "x")
	mustWrite(t, filepath.Join(dir, "other/lost+found/x"), "x")
	for _, tgt := range []string{dir, filepath.Join(dir, "other")} {
		if _, f := restore.CheckTarget(newRecordingFS(), restore.Target{Path: tgt}); f == nil || f.Reason != refusal.TargetNotEmpty {
			t.Fatalf("%s: failure %v", tgt, f)
		}
	}
	if held := openDescriptors(t, dir); len(held) != 0 {
		t.Errorf("still open: %v", held)
	}
}

func TestALostAndFoundThatWasLookedIntoIsNotHeldAfterwards(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, dir, "lost+found")
	held, f := restore.CheckTarget(newRecordingFS(), restore.Target{Path: dir})
	if f != nil {
		t.Fatal(f)
	}
	defer held.Release()
	if open := openDescriptors(t, "lost+found"); len(open) != 0 {
		t.Errorf("still open: %v", open)
	}
}

func TestTheStagingDirectoryIsOfRootAndADirectory(t *testing.T) {
	fsys := newRecordingFS()
	fsys.asFile = true
	_, _, f := staged(t, fsys, t.TempDir(), names("x1y2"))
	if f == nil || f.Reason != refusal.TargetWrite || !strings.Contains(f.Detail, "is not a directory of root with mode 0700") {
		t.Fatalf("failure %v", f)
	}
}

func TestTheStagingDirectoryIsJudgedBeforeItsRootIsMade(t *testing.T) {
	fsys := newRecordingFS()
	other := uint32(990)
	fsys.asOwner = &other
	_, _, f := staged(t, fsys, t.TempDir(), names("x1y2"))
	if f == nil || !strings.Contains(f.Detail, "x1y2 is not a directory of root") || strings.Contains(f.Detail, "x1y2/root") {
		t.Fatalf("failure %v", f)
	}
}

func TestARootDirectoryThatCannotBeMadeIsATargetWriteFailureAndLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	fsys := newRecordingFS()
	fsys.failMkdir = "root"
	_, _, f := staged(t, fsys, dir, names("x1y2"))
	if f == nil || f.Reason != refusal.TargetWrite || !strings.Contains(f.Detail, "cannot make "+filepath.Join(dir, ".sard-restore-x1y2")+"/root") {
		t.Fatalf("failure %v", f)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left behind: %v", entries)
	}
}

func TestEightNamesAreTriedForTheStagingDirectoryAndNoMore(t *testing.T) {
	dir := t.TempDir()
	held, f := openTarget(newRecordingFS(), dir)
	if f != nil {
		t.Fatal(f)
	}
	defer held.Release()
	mustMkdir(t, dir, ".sard-restore-taken")
	tried := 0
	_, f = held.Stage(func() string { tried++; return "taken" })
	if f == nil || f.Reason != refusal.TargetWrite || !strings.Contains(f.Detail, "cannot find a free name") || tried != 8 {
		t.Fatalf("failure %v after %d names", f, tried)
	}
}

func TestADiscardedStagingDirectoryHoldsNoDescriptor(t *testing.T) {
	_, stage, f := staged(t, newRecordingFS(), t.TempDir(), names("x1y2"))
	if f != nil {
		t.Fatal(f)
	}
	if len(openDescriptors(t, ".sard-restore-x1y2")) != 2 {
		t.Fatalf("the staging directory and root are held: %v", openDescriptors(t, ".sard-restore-x1y2"))
	}
	if err := stage.Discard(); err != nil {
		t.Fatal(err)
	}
	if open := openDescriptors(t, ".sard-restore-x1y2"); len(open) != 0 {
		t.Errorf("still open: %v", open)
	}
}

func TestAStagingDirectoryThatCannotBeRemovedAfterACommitIsATargetWriteFailure(t *testing.T) {
	fsys := newRecordingFS()
	fsys.failRemoveDir = ".sard-restore-x1y2"
	dir := t.TempDir()
	_, stage, f := staged(t, fsys, dir, names("x1y2"))
	if f != nil {
		t.Fatal(f)
	}
	f = stage.Commit()
	if f == nil || f.Reason != refusal.TargetWrite || !strings.Contains(f.Detail, "cannot remove the temporary directory "+stagingPath(dir)) {
		t.Fatalf("failure %v", f)
	}
}

func TestTheFailureOfAMoveIsNotReplacedByTheFailureToRemoveTheStagingDirectory(t *testing.T) {
	fsys := newRecordingFS()
	fsys.failMove = "srv"
	fsys.failRemoveDir = ".sard-restore-x1y2"
	dir := t.TempDir()
	_, stage, f := staged(t, fsys, dir, names("x1y2"))
	if f != nil {
		t.Fatal(f)
	}
	mustWrite(t, filepath.Join(stagingPath(dir), "root/srv/a.txt"), "A")
	if f = stage.Commit(); f == nil || f.Reason != refusal.TargetInvalid {
		t.Fatalf("failure %v", f)
	}
}

func TestATargetThatCannotBeSyncedAfterTheMovesIsATargetWriteFailure(t *testing.T) {
	fsys := newRecordingFS()
	fsys.failSync = true
	dir := t.TempDir()
	_, stage, f := staged(t, fsys, dir, names("x1y2"))
	if f != nil {
		t.Fatal(f)
	}
	f = stage.Commit()
	if f == nil || f.Reason != refusal.TargetWrite || !strings.Contains(f.Detail, "cannot sync the target "+dir) {
		t.Fatalf("failure %v", f)
	}
}

func TestAFailureOfResticThatLeavesTheStagingDirectoryAddsItToTheDetail(t *testing.T) {
	fsys := newRecordingFS()
	fsys.failRemoveDir = ".sard-restore-x1y2"
	r := &fakeRestorer{err: fmt.Errorf("restic restore: %w", restic.ErrNoSpace)}
	dir := t.TempDir()
	_, stage, f := staged(t, fsys, dir, names("x1y2"))
	if f != nil {
		t.Fatal(f)
	}
	r.staging = stagingPath(dir)
	_, f = restore.Run(context.Background(), r, restore.Request{Snapshot: theSnapshot(), Target: "/r", Staging: stage}, repoTarget())
	if f == nil || f.Reason != refusal.TargetWrite ||
		!strings.Contains(f.Detail, "no space left on device") || !strings.Contains(f.Detail, "; the temporary directory could not be removed: "+stagingPath(dir)) {
		t.Fatalf("failure %v", f)
	}
}

func TestWhenTheMoveAfterSomeFilesCouldNotBeRestoredFailsTheMoveIsTold(t *testing.T) {
	fsys := newRecordingFS()
	fsys.failMove = "srv"
	r := &fakeRestorer{writes: map[string]string{"srv/ok": "x"}, err: &restic.RestoreError{Items: []restic.ItemError{{Item: "/srv/bad"}}}}
	dir := t.TempDir()
	_, stage, f := staged(t, fsys, dir, names("x1y2"))
	if f != nil {
		t.Fatal(f)
	}
	r.staging = stagingPath(dir)
	_, f = restore.Run(context.Background(), r, restore.Request{Snapshot: theSnapshot(), Target: "/r", Staging: stage}, repoTarget())
	if f == nil || f.Reason != refusal.TargetInvalid {
		t.Fatalf("failure %v", f)
	}
}

func TestTheStagingDirectoryHasTheModeOfRootOnly(t *testing.T) {
	fsys := newRecordingFS()
	mode := fs.FileMode(0o750)
	fsys.asMode = &mode
	_, _, f := staged(t, fsys, t.TempDir(), names("x1y2"))
	if f == nil || f.Reason != refusal.TargetWrite || !strings.Contains(f.Detail, "is not a directory of root with mode 0700") {
		t.Fatalf("failure %v", f)
	}
}

func TestARootDirectoryThatCannotBeListedIsATargetWriteFailure(t *testing.T) {
	fsys := newRecordingFS()
	fsys.failNames = "x1y2/root"
	dir := t.TempDir()
	_, stage, f := staged(t, fsys, dir, names("x1y2"))
	if f != nil {
		t.Fatal(f)
	}
	f = stage.Commit()
	if f == nil || f.Reason != refusal.TargetWrite || !strings.Contains(f.Detail, "cannot read "+stagingPath(dir)+"/root") {
		t.Fatalf("failure %v", f)
	}
}

func TestAnUnknownSourceNeverMatchesASnapshotWithoutOne(t *testing.T) {
	list := []restore.Snapshot{{ID: id("aaaa1111"), ShortID: "aaaa1111"}}
	if _, f := restore.Select(list, "aaaa1111", "base", "s1"); f == nil || f.Reason != refusal.SnapshotUnknown {
		t.Fatalf("by id: failure %v", f)
	}
	if _, f := restore.Select(list, "latest", "base", "s1"); f == nil || f.Reason != refusal.SnapshotUnknown {
		t.Fatalf("latest: failure %v", f)
	}
}

// Р79: a target made by the restore that turns out to be held by another
// restore is TARGET_BUSY and what this run made goes again.
func TestACreatedTargetThatIsHeldByAnotherRestoreIsBusyAndLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	fsys := newRecordingFS()
	held, f := restore.CheckTarget(fsys, restore.Target{Path: filepath.Join(dir, "a/b")})
	if f != nil {
		t.Fatal(f)
	}
	fsys.failLock = true
	f = held.Create()
	if f == nil || f.Reason != refusal.TargetBusy {
		t.Fatalf("failure %v", f)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left behind: %v", entries)
	}
	if open := openDescriptors(t, dir); len(open) != 0 {
		t.Errorf("still open: %v", open)
	}
}

func TestAFileThatTookTheMissingTargetsPlaceIsInvalidNotAWriteFailure(t *testing.T) {
	dir := t.TempDir()
	held, f := restore.CheckTarget(newRecordingFS(), restore.Target{Path: filepath.Join(dir, "a/b")})
	if f != nil {
		t.Fatal(f)
	}
	mustWrite(t, filepath.Join(dir, "a"), "x") // turned up after the target was judged
	f = held.Create()
	wantFailure(t, f, refusal.TargetInvalid, "not a directory")
}
