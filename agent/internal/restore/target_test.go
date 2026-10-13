// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restore"
)

// Rule Р59 (A8c-2): the target of repo restore.

// recordingFS is the file system of a temporary directory that notes the
// owner changes it is asked for (a test is not root) and fails a mkdir on demand.
type recordingFS struct {
	hostsetup.OS
	mu        sync.Mutex
	owners    map[string][2]int
	failMkdir string
}

func newRecordingFS() *recordingFS { return &recordingFS{owners: map[string][2]int{}} }

func (f *recordingFS) OpenRootDir() (hostsetup.Dir, error) {
	d, err := f.OS.OpenRootDir()
	if err != nil {
		return nil, err
	}
	return recordingDir{Dir: d, fsys: f}, nil
}

type recordingDir struct {
	hostsetup.Dir
	fsys *recordingFS
}

func (d recordingDir) wrap(next hostsetup.Dir, err error) (hostsetup.Dir, error) {
	if err != nil {
		return nil, err
	}
	return recordingDir{Dir: next, fsys: d.fsys}, nil
}

func (d recordingDir) Open(name string) (hostsetup.Dir, error) { return d.wrap(d.Dir.Open(name)) }

func (d recordingDir) Mkdir(name string, perm fs.FileMode) (hostsetup.Dir, error) {
	if name == d.fsys.failMkdir {
		return nil, &fs.PathError{Op: "mkdirat", Path: name, Err: syscall.ENOSPC}
	}
	return d.wrap(d.Dir.Mkdir(name, perm))
}

func (d recordingDir) Chown(uid, gid int) error {
	d.fsys.mu.Lock()
	defer d.fsys.mu.Unlock()
	d.fsys.owners[d.Path()] = [2]int{uid, gid}
	return nil
}

func mustMkdir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func check(t *testing.T, tgt restore.Target) (string, *refusal.Failure) {
	t.Helper()
	return restore.CheckTarget(newRecordingFS(), tgt)
}

func wantFailure(t *testing.T, f *refusal.Failure, reason refusal.Reason, mentions ...string) {
	t.Helper()
	if f == nil || f.Reason != reason || f.Class != refusal.ClassUsage {
		t.Fatalf("failure %v, want %s", f, reason)
	}
	for _, m := range mentions {
		if !strings.Contains(f.Detail, m) {
			t.Errorf("%q is not in %q", m, f.Detail)
		}
	}
}

func TestAMissingTargetIsFineAndNothingIsCreated(t *testing.T) {
	dir := t.TempDir()
	got, f := check(t, restore.Target{Path: filepath.Join(dir, "a/b")})
	if f != nil || got != filepath.Join(dir, "a/b") {
		t.Fatalf("got %q, %v", got, f)
	}
	if _, err := os.Stat(filepath.Join(dir, "a")); err == nil {
		t.Error("a was created")
	}
}

func TestTheTargetIsGivenInItsCleanForm(t *testing.T) {
	dir := t.TempDir()
	got, f := check(t, restore.Target{Path: dir + "/./restore/"})
	if f != nil || got != dir+"/restore" {
		t.Fatalf("got %q, %v", got, f)
	}
}

func TestARelativeTargetIsInvalid(t *testing.T) {
	_, f := check(t, restore.Target{Path: "restore"})
	wantFailure(t, f, refusal.TargetInvalid, "absolute")
}

func TestANonEmptyTargetNeedsOverwrite(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "keep.txt"), "x")
	_, f := check(t, restore.Target{Path: dir})
	wantFailure(t, f, refusal.TargetNotEmpty, dir, "--overwrite")
	if _, f := check(t, restore.Target{Path: dir, Overwrite: true}); f != nil {
		t.Fatalf("with overwrite: %v", f)
	}
}

func TestAnEmptyTargetIsFine(t *testing.T) {
	if _, f := check(t, restore.Target{Path: t.TempDir()}); f != nil {
		t.Fatal(f)
	}
}

// Н30
func TestOnlyAnEmptyLostAndFoundCountsAsEmpty(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, dir, "lost+found")
	if _, f := check(t, restore.Target{Path: dir}); f != nil {
		t.Fatalf("empty lost+found: %v", f)
	}
	mustWrite(t, filepath.Join(dir, "lost+found", "f"), "x")
	_, f := check(t, restore.Target{Path: dir})
	wantFailure(t, f, refusal.TargetNotEmpty)
}

func TestALostAndFoundThatIsAFileMakesTheTargetNonEmpty(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "lost+found"), "x")
	_, f := check(t, restore.Target{Path: dir})
	wantFailure(t, f, refusal.TargetNotEmpty)
}

func TestLostAndFoundBesideAnotherEntryMakesTheTargetNonEmpty(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, dir, "lost+found")
	mustWrite(t, filepath.Join(dir, "other"), "x")
	_, f := check(t, restore.Target{Path: dir})
	wantFailure(t, f, refusal.TargetNotEmpty)
}

func TestALinkInThePathIsInvalidAndNamesTheComponent(t *testing.T) {
	dir := t.TempDir()
	real := mustMkdir(t, dir, "real")
	if err := os.Symlink(real, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{filepath.Join(dir, "link", "r"), filepath.Join(dir, "link")} {
		_, f := check(t, restore.Target{Path: target})
		wantFailure(t, f, refusal.TargetInvalid, filepath.Join(dir, "link"))
	}
	if _, err := os.Stat(filepath.Join(real, "r")); err == nil {
		t.Error("something was created behind the link")
	}
}

func TestAFileInThePathIsInvalid(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "file"), "x")
	for _, target := range []string{filepath.Join(dir, "file"), filepath.Join(dir, "file", "r")} {
		_, f := check(t, restore.Target{Path: target})
		wantFailure(t, f, refusal.TargetInvalid, filepath.Join(dir, "file"))
	}
}

func TestATargetInsideOrEqualToAnAvoidedDirectoryIsInvalid(t *testing.T) {
	dir := t.TempDir()
	repo := mustMkdir(t, dir, "base")
	mustMkdir(t, dir, "base-other")
	for _, target := range []string{repo, filepath.Join(repo, "r")} {
		_, f := check(t, restore.Target{Path: target, Avoid: []string{repo}})
		wantFailure(t, f, refusal.TargetInvalid, repo)
	}
	if _, f := check(t, restore.Target{Path: filepath.Join(dir, "base-other"), Avoid: []string{repo}}); f != nil {
		t.Fatalf("a sibling with the same prefix: %v", f)
	}
}

// Н31
func TestTheRootNeedsAllowRootTargetAndOverwrite(t *testing.T) {
	for _, root := range []string{"/", "/.", "//"} {
		_, f := check(t, restore.Target{Path: root, Overwrite: true})
		wantFailure(t, f, refusal.RootTargetRefused, "--allow-root-target")
		_, f = check(t, restore.Target{Path: root, AllowRoot: true})
		wantFailure(t, f, refusal.TargetNotEmpty, "--overwrite")
		got, f := check(t, restore.Target{Path: root, AllowRoot: true, Overwrite: true})
		if f != nil || got != "/" {
			t.Errorf("%q with both: %q, %v", root, got, f)
		}
	}
}

func TestACreatedTargetBelongsToRootWithMode0700EvenWithUmask000(t *testing.T) {
	dir := t.TempDir()
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	fsys := newRecordingFS()
	held, f := restore.OpenTarget(fsys, filepath.Join(dir, "a/b/c"))
	if f != nil {
		t.Fatal(f)
	}
	defer held.Release()
	for _, p := range []string{"a", "a/b", "a/b/c"} {
		full := filepath.Join(dir, p)
		info, err := os.Stat(full)
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v, %v", p, info, err)
		}
		if got := fsys.owners[full]; got != [2]int{0, 0} {
			t.Errorf("%s owner %v", p, got)
		}
	}
}

func TestAnExistingTargetIsLeftAsItIs(t *testing.T) {
	dir := t.TempDir()
	fsys := newRecordingFS()
	held, f := restore.OpenTarget(fsys, dir)
	if f != nil {
		t.Fatal(f)
	}
	defer held.Release()
	if len(fsys.owners) != 0 {
		t.Errorf("owners changed: %v", fsys.owners)
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() == 0o700 {
		t.Log("t.TempDir is 0700 already")
	}
}

func TestASecondRestoreIntoTheSameTargetIsBusy(t *testing.T) {
	dir := t.TempDir()
	first, f := restore.OpenTarget(newRecordingFS(), dir)
	if f != nil {
		t.Fatal(f)
	}
	_, f = restore.OpenTarget(newRecordingFS(), dir)
	if f == nil || f.Reason != refusal.TargetBusy || f.Class != refusal.ClassTemporary || !strings.Contains(f.Detail, dir) {
		t.Fatalf("second: %v", f)
	}
	first.Release()
	again, f := restore.OpenTarget(newRecordingFS(), dir)
	if f != nil {
		t.Fatalf("after release: %v", f)
	}
	again.Release()
}

func TestAbandonRemovesTheDirectoriesThisRunCreatedWhenTheyAreEmpty(t *testing.T) {
	dir := t.TempDir()
	held, f := restore.OpenTarget(newRecordingFS(), filepath.Join(dir, "a/b"))
	if f != nil {
		t.Fatal(f)
	}
	held.Abandon()
	if _, err := os.Stat(filepath.Join(dir, "a")); err == nil {
		t.Error("a is still there")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("the existing directory is gone: %v", err)
	}
}

func TestAbandonKeepsWhatHoldsFiles(t *testing.T) {
	dir := t.TempDir()
	held, f := restore.OpenTarget(newRecordingFS(), filepath.Join(dir, "a/b"))
	if f != nil {
		t.Fatal(f)
	}
	mustWrite(t, filepath.Join(dir, "a/b/partial"), "x")
	held.Abandon()
	if _, err := os.Stat(filepath.Join(dir, "a/b/partial")); err != nil {
		t.Fatal(err)
	}
}

func TestADirectoryThatCannotBeCreatedIsATargetWriteFailureAndLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	fsys := newRecordingFS()
	fsys.failMkdir = "c"
	_, f := restore.OpenTarget(fsys, filepath.Join(dir, "a/b/c"))
	if f == nil || f.Reason != refusal.TargetWrite || f.Class != refusal.ClassWrite {
		t.Fatalf("failure %v", f)
	}
	if _, err := os.Stat(filepath.Join(dir, "a")); err == nil {
		t.Error("a was left behind")
	}
}

func TestAFileTurnedUpInThePathBetweenTheCheckAndTheOpenIsInvalid(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "f"), "x")
	_, f := restore.OpenTarget(newRecordingFS(), filepath.Join(dir, "f"))
	wantFailure(t, f, refusal.TargetInvalid)
}
