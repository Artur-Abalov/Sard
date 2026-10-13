// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restore"
)

// Rule Р73 (A8c-2): restic writes into a directory of root inside the
// target, and the entries are moved into the target afterwards.

// names returns a source of the names the staging directory is called by.
func names(list ...string) func() string {
	return func() string {
		next := list[0]
		if len(list) > 1 {
			list = list[1:]
		}
		return next
	}
}

// staged holds the target dir and makes the staging directory in it.
func staged(t *testing.T, fsys *recordingFS, dir string, random func() string) (*restore.Held, *restore.Staging, *refusal.Failure) {
	t.Helper()
	held, f := openTarget(fsys, dir)
	if f != nil {
		t.Fatal(f)
	}
	t.Cleanup(held.Release)
	stage, f := held.Stage(random)
	return held, stage, f
}

func TestTheStagingDirectoryAndItsRootBelongToRootWithMode0700EvenWithUmask000(t *testing.T) {
	dir := t.TempDir()
	defer syscall.Umask(syscall.Umask(0))
	fsys := newRecordingFS()
	_, stage, f := staged(t, fsys, dir, names("x1y2"))
	if f != nil {
		t.Fatal(f)
	}
	for _, p := range []string{".sard-restore-x1y2", ".sard-restore-x1y2/root"} {
		full := filepath.Join(dir, p)
		info, err := os.Stat(full)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v, %v", p, info, err)
		}
		if got := fsys.owners[full]; got != [2]int{0, 0} {
			t.Errorf("%s owner %v", p, got)
		}
	}
	if stage.Path() != filepath.Join(dir, ".sard-restore-x1y2") {
		t.Errorf("path %q", stage.Path())
	}
}

func TestAStagingNameThatIsTakenIsReplacedByAnotherName(t *testing.T) {
	dir := t.TempDir()
	held, f := openTarget(newRecordingFS(), dir)
	if f != nil {
		t.Fatal(f)
	}
	defer held.Release()
	mustMkdir(t, dir, ".sard-restore-taken") // turned up after the target was judged
	stage, f := held.Stage(names("taken", "free"))
	if f != nil || filepath.Base(stage.Path()) != ".sard-restore-free" {
		t.Fatalf("stage %v, failure %v", stage, f)
	}
}

func TestAStagingDirectoryThatCannotBeMadeIsATargetWriteFailureAndLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	fsys := newRecordingFS()
	fsys.failMkdir = ".sard-restore-x1y2"
	_, _, f := staged(t, fsys, dir, names("x1y2"))
	if f == nil || f.Reason != refusal.TargetWrite || f.Class != refusal.ClassWrite || !strings.Contains(f.Detail, dir) {
		t.Fatalf("failure %v", f)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left behind: %v", entries)
	}
}

func TestAStagingDirectoryOfAnotherOwnerIsNotUsedAndIsRemoved(t *testing.T) {
	dir := t.TempDir()
	fsys := newRecordingFS()
	other := uint32(990)
	fsys.asOwner = &other
	_, _, f := staged(t, fsys, dir, names("x1y2"))
	if f == nil || f.Reason != refusal.TargetWrite || !strings.Contains(f.Detail, "990") {
		t.Fatalf("failure %v", f)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left behind: %v", entries)
	}
}

func TestAStagingDirectoryWhoseOwnerCannotBeSetOrSeenIsNotUsedAndLeavesNothing(t *testing.T) {
	for name, arrange := range map[string]func(*recordingFS){
		"owner": func(f *recordingFS) { f.failChown = true },
		"stat":  func(f *recordingFS) { f.failStat = true },
	} {
		dir := t.TempDir()
		fsys := newRecordingFS()
		held, f := openTarget(fsys, dir)
		if f != nil {
			t.Fatal(f)
		}
		arrange(fsys)
		_, f = held.Stage(names("x1y2"))
		held.Release()
		if f == nil || f.Reason != refusal.TargetWrite {
			t.Errorf("%s: failure %v", name, f)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("%s: left behind: %v", name, entries)
		}
	}
}

func TestTheEntriesOfTheRootDirectoryAreMovedIntoTheTargetAndTheStagingDirectoryGoes(t *testing.T) {
	dir := t.TempDir()
	_, stage, f := staged(t, newRecordingFS(), dir, names("x1y2"))
	if f != nil {
		t.Fatal(f)
	}
	root := filepath.Join(stage.Path(), "root")
	mustWrite(t, filepath.Join(root, "srv/data/a.txt"), "A")
	mustWrite(t, filepath.Join(root, "app.dump"), "D")
	if f := stage.Commit(); f != nil {
		t.Fatal(f)
	}
	for p, want := range map[string]string{"srv/data/a.txt": "A", "app.dump": "D"} {
		if data, err := os.ReadFile(filepath.Join(dir, p)); err != nil || string(data) != want {
			t.Errorf("%s: %q, %v", p, data, err)
		}
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, ".sard-restore-*")); len(entries) != 0 {
		t.Errorf("staging left: %v", entries)
	}
}

func TestANameThatTurnedUpInTheTargetIsNotReplacedAndTheRestIsRemoved(t *testing.T) {
	dir := t.TempDir()
	_, stage, f := staged(t, newRecordingFS(), dir, names("x1y2"))
	if f != nil {
		t.Fatal(f)
	}
	root := filepath.Join(stage.Path(), "root")
	mustWrite(t, filepath.Join(root, "srv/a.txt"), "NEW")
	mustWrite(t, filepath.Join(root, "zzz"), "Z")
	mustWrite(t, filepath.Join(dir, "srv/theirs"), "THEIRS") // turned up while restic worked
	f = stage.Commit()
	if f == nil || f.Reason != refusal.TargetWrite || f.Class != refusal.ClassWrite {
		t.Fatalf("failure %v", f)
	}
	for _, want := range []string{filepath.Join(dir, "srv"), "stay in the target", "removed"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("%q is not in %q", want, f.Detail)
		}
	}
	assertTheirsIsIntact(t, dir)
	if entries, _ := filepath.Glob(filepath.Join(dir, ".sard-restore-*")); len(entries) != 0 {
		t.Errorf("staging left: %v", entries)
	}
}

// assertTheirsIsIntact: the directory that turned up in the target was not
// changed, and none of the snapshot's files went into it.
func assertTheirsIsIntact(t *testing.T, dir string) {
	t.Helper()
	if data, err := os.ReadFile(filepath.Join(dir, "srv/theirs")); err != nil || string(data) != "THEIRS" {
		t.Errorf("the directory that turned up was changed: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "srv/a.txt")); err == nil {
		t.Error("the snapshot's file went into their directory")
	}
}

func TestAMountPointOnTheWayOfAMoveIsAnInvalidTarget(t *testing.T) {
	dir := t.TempDir()
	fsys := newRecordingFS()
	fsys.failMove = "srv"
	_, stage, f := staged(t, fsys, dir, names("x1y2"))
	if f != nil {
		t.Fatal(f)
	}
	mustWrite(t, filepath.Join(stage.Path(), "root/srv/a.txt"), "A")
	f = stage.Commit()
	if f == nil || f.Reason != refusal.TargetInvalid || f.Class != refusal.ClassUsage || !strings.Contains(f.Detail, "mount point inside the target") {
		t.Fatalf("failure %v", f)
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, ".sard-restore-*")); len(entries) != 0 {
		t.Errorf("staging left: %v", entries)
	}
}

func TestDiscardRemovesTheStagingDirectoryWithoutFollowingALinkInIt(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(outside, "precious"), "P")
	_, stage, f := staged(t, newRecordingFS(), dir, names("x1y2"))
	if f != nil {
		t.Fatal(f)
	}
	root := filepath.Join(stage.Path(), "root")
	mustWrite(t, filepath.Join(root, "partial"), "x")
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := stage.Discard(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stage.Path()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("staging left: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "precious")); err != nil {
		t.Errorf("the content behind the link: %v", err)
	}
}

func TestTheStagingDirectoryIsHandedOnByItsDescriptor(t *testing.T) {
	dir := t.TempDir()
	_, stage, f := staged(t, newRecordingFS(), dir, names("x1y2"))
	if f != nil {
		t.Fatal(f)
	}
	got, err := stage.Dir().Handle().Stat()
	want, _ := os.Stat(stage.Path())
	if err != nil || !os.SameFile(got, want) {
		t.Fatalf("the descriptor is not the staging directory: %v", err)
	}
	_ = hostsetup.ErrBusy
}
