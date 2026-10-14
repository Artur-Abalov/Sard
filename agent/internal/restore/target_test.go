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
	// failMove is the name of an entry whose move fails with a mount point
	// on the way (EXDEV); asOwner, if not nil, is the owner every directory
	// seems to have.
	failMove string
	asOwner  *uint32
	// failChown and failStat make the owner change and the look at a
	// directory fail.
	failChown, failStat bool
	// failRemoveDir is the name of a directory that cannot be removed, failSync
	// makes the sync of a directory fail, failNames is the suffix of the path
	// of a directory that cannot be listed, and asFile makes every directory
	// seem to be a regular file.
	failRemoveDir, failNames string
	failSync, asFile         bool
	// failLock makes the lock of a directory fail as if another restore held
	// it; asMode is the permission every directory seems to have.
	failLock bool
	asMode   *fs.FileMode
	// files: what the files made in the directories do (A8c-3).
	files fileBehaviour
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

// Stat shows the owner a Chown recorded (a test is not root), or the owner
// the test made up.
func (d recordingDir) Stat() (fs.FileInfo, error) {
	info, err := d.Dir.Stat()
	if err != nil {
		return nil, err
	}
	if d.fsys.failStat {
		return nil, &fs.PathError{Op: "fstat", Path: d.Path(), Err: syscall.EIO}
	}
	d.fsys.mu.Lock()
	defer d.fsys.mu.Unlock()
	if d.fsys.asFile {
		return notADir{info}, nil
	}
	if d.fsys.asMode != nil {
		return withMode{notADir{info}, *d.fsys.asMode}, nil
	}
	if d.fsys.asOwner != nil {
		return ownedInfo{info, *d.fsys.asOwner}, nil
	}
	if o, ok := d.fsys.owners[d.Path()]; ok {
		return ownedInfo{info, uint32(o[0])}, nil
	}
	return info, nil
}

// notADir is the owner root, mode 0700 and the type of a regular file.
type notADir struct{ fs.FileInfo }

func (notADir) Mode() fs.FileMode { return 0o700 }
func (notADir) IsDir() bool       { return false }
func (n notADir) Sys() any {
	st := *n.FileInfo.Sys().(*syscall.Stat_t)
	st.Uid = 0
	return &st
}

// withMode is a directory of root with the permission given.
type withMode struct {
	notADir
	perm fs.FileMode
}

func (w withMode) Mode() fs.FileMode { return fs.ModeDir | w.perm }
func (withMode) IsDir() bool         { return true }

type ownedInfo struct {
	fs.FileInfo
	uid uint32
}

func (i ownedInfo) Sys() any {
	st := *i.FileInfo.Sys().(*syscall.Stat_t)
	st.Uid = i.uid
	return &st
}

func (d recordingDir) RenameNoReplace(oldName string, newDir hostsetup.Dir, newName string) error {
	if d.fsys.files.failRename {
		return &fs.PathError{Op: "renameat2", Path: newName, Err: fs.ErrExist}
	}
	if oldName == d.fsys.failMove {
		return &fs.PathError{Op: "renameat2", Path: newName, Err: hostsetup.ErrCrossDevice}
	}
	return d.Dir.RenameNoReplace(oldName, newDir, newName)
}

func (d recordingDir) RemoveDir(name string) error {
	if name == d.fsys.failRemoveDir {
		return &fs.PathError{Op: "unlinkat", Path: name, Err: syscall.EBUSY}
	}
	return d.Dir.RemoveDir(name)
}

func (d recordingDir) Lock() error {
	if d.fsys.failLock {
		return hostsetup.ErrBusy
	}
	return d.Dir.Lock()
}

func (d recordingDir) Sync() error {
	if d.fsys.failSync {
		return &fs.PathError{Op: "fsync", Path: d.Path(), Err: syscall.EIO}
	}
	return d.Dir.Sync()
}

func (d recordingDir) Names() ([]string, error) {
	if d.fsys.failNames != "" && strings.HasSuffix(d.Path(), d.fsys.failNames) {
		return nil, &fs.PathError{Op: "getdents", Path: d.Path(), Err: syscall.EIO}
	}
	return d.Dir.Names()
}

func (d recordingDir) Chown(uid, gid int) error {
	if d.fsys.failChown {
		return &fs.PathError{Op: "fchown", Path: d.Path(), Err: syscall.EPERM}
	}
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

// check judges the target and lets go of it again; it returns the clean path.
func check(t *testing.T, tgt restore.Target) (string, *refusal.Failure) {
	t.Helper()
	held, f := restore.CheckTarget(newRecordingFS(), tgt)
	if f != nil {
		return "", f
	}
	held.Release()
	return held.Path, nil
}

// openTarget is CheckTarget and Create: the target held, made if it was missing.
func openTarget(fsys hostsetup.FS, path string) (*restore.Held, *refusal.Failure) {
	held, f := restore.CheckTarget(fsys, restore.Target{Path: path})
	if f != nil {
		return nil, f
	}
	if f := held.Create(); f != nil {
		return nil, f
	}
	return held, nil
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
	wantFailure(t, f, refusal.TargetInvalid, "absolute", `--target "restore"`)
}

func TestANonEmptyTargetIsRefused(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "keep.txt"), "x")
	_, f := check(t, restore.Target{Path: dir})
	wantFailure(t, f, refusal.TargetNotEmpty, dir, "empty or new directory")
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
func TestTheRootNeedsAllowRootTargetAndIsNeverEmpty(t *testing.T) {
	for _, root := range []string{"/", "/.", "//"} {
		_, f := check(t, restore.Target{Path: root})
		wantFailure(t, f, refusal.RootTargetRefused, "--allow-root-target")
		_, f = check(t, restore.Target{Path: root, AllowRoot: true})
		wantFailure(t, f, refusal.TargetNotEmpty, "/", "empty or new directory")
	}
}

func TestACreatedTargetBelongsToRootWithMode0700EvenWithUmask000(t *testing.T) {
	dir := t.TempDir()
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	fsys := newRecordingFS()
	held, f := openTarget(fsys, filepath.Join(dir, "a/b/c"))
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
	held, f := openTarget(fsys, dir)
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
	first, f := openTarget(newRecordingFS(), dir)
	if f != nil {
		t.Fatal(f)
	}
	_, f = openTarget(newRecordingFS(), dir)
	if f == nil || f.Reason != refusal.TargetBusy || f.Class != refusal.ClassTemporary || !strings.Contains(f.Detail, dir) {
		t.Fatalf("second: %v", f)
	}
	first.Release()
	again, f := openTarget(newRecordingFS(), dir)
	if f != nil {
		t.Fatalf("after release: %v", f)
	}
	again.Release()
}

func TestAbandonRemovesTheDirectoriesThisRunCreatedWhenTheyAreEmpty(t *testing.T) {
	dir := t.TempDir()
	held, f := openTarget(newRecordingFS(), filepath.Join(dir, "a/b"))
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
	held, f := openTarget(newRecordingFS(), filepath.Join(dir, "a/b"))
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
	_, f := openTarget(fsys, filepath.Join(dir, "a/b/c"))
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
	_, f := openTarget(newRecordingFS(), filepath.Join(dir, "f"))
	wantFailure(t, f, refusal.TargetInvalid)
}

// Р79: an existing target is locked while it is walked, before it is judged
// empty: the second command is told the target is busy, not that it is full.
func TestAnExistingTargetThatAnotherRestoreHoldsIsBusyEvenWhenItIsNotEmpty(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "partial"), "x")
	other, _, err := hostsetup.OpenDir(hostsetup.OS{}, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	if err := other.Lock(); err != nil {
		t.Fatal(err)
	}
	_, f := check(t, restore.Target{Path: dir})
	if f == nil || f.Reason != refusal.TargetBusy || !strings.Contains(f.Detail, dir) {
		t.Fatalf("failure %v", f)
	}
}

func TestAnExistingTargetIsHeldFromTheCheckOn(t *testing.T) {
	dir := t.TempDir()
	held, f := restore.CheckTarget(newRecordingFS(), restore.Target{Path: dir})
	if f != nil {
		t.Fatal(f)
	}
	defer held.Release()
	_, f = restore.CheckTarget(newRecordingFS(), restore.Target{Path: dir})
	if f == nil || f.Reason != refusal.TargetBusy {
		t.Fatalf("second check: %v", f)
	}
}

// Р73: what an interrupted restore left behind makes the target non-empty,
// and the refusal names it.
func TestALeftoverStagingDirectoryIsNamedAsTheRemainsOfAnInterruptedRestore(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, dir, ".sard-restore-x1y2")
	_, f := check(t, restore.Target{Path: dir})
	wantFailure(t, f, refusal.TargetNotEmpty, filepath.Join(dir, ".sard-restore-x1y2"), "interrupted restore", "remove")
}
