// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

type owner struct{ uid, gid int }

// recordingFS is the real file system of a temporary directory with the
// owner changes recorded instead of made (a test is not root), and one
// operation made to fail on demand.
type recordingFS struct {
	hostsetup.OS
	mu     sync.Mutex
	owners map[string]owner
	// failOn names the operation that fails: "chown", "chmod", "write",
	// "sync", "close", "rename", "syncdir", "createtemp", "mkdir".
	failOn string
	// failPath restricts the failure to paths containing it; empty is any.
	failPath string
	events   []string
}

func newRecordingFS() *recordingFS { return &recordingFS{owners: map[string]owner{}} }

var errInjected = errors.New("injected failure")

func (r *recordingFS) fails(op, path string) error {
	if r.failOn == op && strings.Contains(path, r.failPath) {
		return &fs.PathError{Op: op, Path: path, Err: errInjected}
	}
	return nil
}

func (r *recordingFS) note(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, sprintf(format, args...))
}

func (r *recordingFS) setOwner(path string, uid, gid int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.owners[path] = owner{uid, gid}
}

func (r *recordingFS) ownerOf(path string) (owner, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.owners[path]
	return o, ok
}

func (r *recordingFS) CreateTemp(dir, pattern string) (hostsetup.File, error) {
	if err := r.fails("createtemp", dir); err != nil {
		return nil, err
	}
	f, err := r.OS.CreateTemp(dir, pattern)
	if err != nil {
		return nil, err
	}
	r.note("createtemp %s", f.Name())
	return &recordingFile{File: f, fsys: r}, nil
}

type recordingFile struct {
	hostsetup.File
	fsys *recordingFS
}

func (f *recordingFile) Chown(uid, gid int) error {
	if err := f.fsys.fails("chown", f.Name()); err != nil {
		return err
	}
	f.fsys.setOwner(f.Name(), uid, gid)
	f.fsys.note("chown %s %d:%d", f.Name(), uid, gid)
	return nil
}

func (f *recordingFile) Chmod(m os.FileMode) error {
	if err := f.fsys.fails("chmod", f.Name()); err != nil {
		return err
	}
	return f.File.Chmod(m)
}

func (f *recordingFile) Write(p []byte) (int, error) {
	if err := f.fsys.fails("write", f.Name()); err != nil {
		return 0, err
	}
	return f.File.Write(p)
}

func (f *recordingFile) Sync() error {
	if err := f.fsys.fails("sync", f.Name()); err != nil {
		return err
	}
	return f.File.Sync()
}

func (r *recordingFS) Rename(oldpath, newpath string) error {
	if err := r.fails("rename", newpath); err != nil {
		return err
	}
	if err := r.OS.Rename(oldpath, newpath); err != nil {
		return err
	}
	r.mu.Lock()
	if o, ok := r.owners[oldpath]; ok {
		r.owners[newpath] = o
		delete(r.owners, oldpath)
	}
	r.mu.Unlock()
	r.note("rename %s %s", oldpath, newpath)
	return nil
}

func (r *recordingFS) SyncDir(dir string) error {
	if err := r.fails("syncdir", dir); err != nil {
		return err
	}
	return r.OS.SyncDir(dir)
}

func (r *recordingFS) Mkdir(path string, perm os.FileMode) error {
	if err := r.fails("mkdir", path); err != nil {
		return err
	}
	return r.OS.Mkdir(path, perm)
}

func (r *recordingFS) Chown(path string, uid, gid int) error {
	if err := r.fails("chown", path); err != nil {
		return err
	}
	r.setOwner(path, uid, gid)
	r.note("chown %s %d:%d", path, uid, gid)
	return nil
}

func (r *recordingFS) Link(oldpath, newpath string) error {
	if err := r.OS.Link(oldpath, newpath); err != nil {
		return err
	}
	r.mu.Lock()
	if o, ok := r.owners[oldpath]; ok {
		r.owners[newpath] = o
	}
	r.mu.Unlock()
	r.note("link %s %s", oldpath, newpath)
	return nil
}

func (r *recordingFS) OpenRoot(path string) (hostsetup.Root, error) {
	root, err := r.OS.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	return &recordingRoot{Root: root, base: path, fsys: r}, nil
}

// recordingRoot records the owner changes made inside a Root.
type recordingRoot struct {
	hostsetup.Root
	base string
	fsys *recordingFS
}

func (r *recordingRoot) Lchown(name string, uid, gid int) error {
	full := filepath.Join(r.base, name)
	if err := r.fsys.fails("chown", full); err != nil {
		return err
	}
	r.fsys.setOwner(full, uid, gid)
	return nil
}

// leftovers are the entries of dir that are not in want.
func leftovers(t *testing.T, dir string, want ...string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var extra []string
	for _, e := range entries {
		found := false
		for _, w := range want {
			found = found || w == e.Name()
		}
		if !found {
			extra = append(extra, e.Name())
		}
	}
	sort.Strings(extra)
	return extra
}

func TestWriteFileIsAtomicOwnedAndModed(t *testing.T) {
	dir := t.TempDir()
	rfs := newRecordingFS()
	path := filepath.Join(dir, "db")
	err := hostsetup.WriteFile(rfs, path, []byte("SECRET-MARKER"), hostsetup.Attrs{UID: 990, GID: 991, Mode: 0o600})
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "SECRET-MARKER" {
		t.Fatalf("content %q", data)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	if o, _ := rfs.ownerOf(path); o != (owner{990, 991}) {
		t.Fatalf("owner %+v", o)
	}
	if extra := leftovers(t, dir, "db"); len(extra) != 0 {
		t.Fatalf("leftovers %v", extra)
	}
}

func TestWriteFileModeDoesNotDependOnTheUmask(t *testing.T) {
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := hostsetup.WriteFile(newRecordingFS(), path, []byte("x"), hostsetup.Attrs{Mode: 0o640}); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", info.Mode())
	}
	syscall.Umask(0o777)
	path2 := filepath.Join(dir, "g")
	if err := hostsetup.WriteFile(newRecordingFS(), path2, []byte("x"), hostsetup.Attrs{Mode: 0o640}); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path2); info.Mode().Perm() != 0o640 {
		t.Fatalf("mode with umask 777: %v", info.Mode())
	}
}

func TestTheTemporaryFileIsOwnedBeforeItIsRenamed(t *testing.T) {
	dir := t.TempDir()
	rfs := newRecordingFS()
	if err := hostsetup.WriteFile(rfs, filepath.Join(dir, "db"), []byte("x"), hostsetup.Attrs{UID: 990, GID: 990, Mode: 0o600}); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range rfs.events {
		kinds = append(kinds, strings.Fields(e)[0])
	}
	if got := strings.Join(kinds, " "); got != "createtemp chown rename" {
		t.Fatalf("events = %s", got)
	}
	if !strings.Contains(rfs.events[0], filepath.Join(dir, ".db.tmp-")) {
		t.Fatalf("the temporary file is not next to the target: %s", rfs.events[0])
	}
}

func TestAFailureLeavesTheOldContentAndNoTemporaryFile(t *testing.T) {
	for _, op := range []string{"createtemp", "chown", "chmod", "write", "sync", "rename", "syncdir"} {
		t.Run(op, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "db")
			if err := os.WriteFile(path, []byte("OLD"), 0o600); err != nil {
				t.Fatal(err)
			}
			rfs := newRecordingFS()
			rfs.failOn = op
			err := hostsetup.WriteFile(rfs, path, []byte("NEW"), hostsetup.Attrs{UID: 990, GID: 990, Mode: 0o600})
			var we *hostsetup.WriteError
			if !errors.As(err, &we) || we.Path != path || !errors.Is(err, errInjected) {
				t.Fatalf("err = %v", err)
			}
			data, _ := os.ReadFile(path)
			wantOld := op != "syncdir" // the rename has happened when the directory sync fails
			if (string(data) == "OLD") != wantOld {
				t.Fatalf("content %q", data)
			}
			if extra := leftovers(t, dir, "db"); len(extra) != 0 {
				t.Fatalf("leftovers %v", extra)
			}
		})
	}
}

func TestEnsureDirCreatesWithOwnerAndModeWhateverTheUmask(t *testing.T) {
	old := syscall.Umask(0o777)
	defer syscall.Umask(old)
	dir := filepath.Join(t.TempDir(), "agent.d")
	rfs := newRecordingFS()
	created, err := hostsetup.EnsureDir(rfs, dir, hostsetup.Attrs{UID: 0, GID: 990, Mode: 0o750})
	if err != nil || !created {
		t.Fatalf("created %v, err %v", created, err)
	}
	if info, _ := os.Stat(dir); !info.IsDir() || info.Mode().Perm() != 0o750 {
		t.Fatalf("info %v", info)
	}
	if o, _ := rfs.ownerOf(dir); o != (owner{0, 990}) {
		t.Fatalf("owner %+v", o)
	}
}

func TestEnsureDirLeavesAnExistingDirectoryAlone(t *testing.T) {
	dir := t.TempDir()
	rfs := newRecordingFS()
	created, err := hostsetup.EnsureDir(rfs, dir, hostsetup.Attrs{UID: 5, GID: 5, Mode: 0o700})
	if err != nil || created {
		t.Fatalf("created %v, err %v", created, err)
	}
	if _, changed := rfs.ownerOf(dir); changed {
		t.Fatal("the owner of an existing directory was changed")
	}
}

func TestEnsureDirRefusesAFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	ok(t, os.WriteFile(file, nil, 0o600))
	var we *hostsetup.WriteError
	if _, err := hostsetup.EnsureDir(newRecordingFS(), file, hostsetup.Attrs{Mode: 0o700}); !errors.As(err, &we) || we.Path != file {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureDirReportsEveryFailureWithThePath(t *testing.T) {
	base := t.TempDir()
	for _, op := range []string{"mkdir", "chown"} {
		rfs := newRecordingFS()
		rfs.failOn = op
		dir := filepath.Join(base, "d-"+op)
		var we *hostsetup.WriteError
		if _, err := hostsetup.EnsureDir(rfs, dir, hostsetup.Attrs{Mode: 0o700}); !errors.As(err, &we) || we.Path != dir || !errors.Is(err, errInjected) {
			t.Fatalf("%s: err = %v", op, err)
		}
	}
}

func TestEnsureDirDoesNotCreateAMissingParent(t *testing.T) {
	var we *hostsetup.WriteError
	missing := filepath.Join(t.TempDir(), "missing", "child")
	if _, err := hostsetup.EnsureDir(newRecordingFS(), missing, hostsetup.Attrs{Mode: 0o700}); !errors.As(err, &we) {
		t.Fatalf("err = %v", err)
	}
}

func TestRemoveFileRemovesAndReports(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x")
	ok(t, os.WriteFile(path, nil, 0o600))
	if removed, err := hostsetup.RemoveFile(hostsetup.OS{}, path); err != nil || !removed {
		t.Fatalf("removed %v, err %v", removed, err)
	}
	if removed, err := hostsetup.RemoveFile(hostsetup.OS{}, path); err != nil || removed {
		t.Fatalf("second: removed %v, err %v", removed, err)
	}
}

func TestRemoveFileReportsAnythingButAMissingFile(t *testing.T) {
	notEmpty := filepath.Join(t.TempDir(), "d")
	ok(t, os.MkdirAll(filepath.Join(notEmpty, "child"), 0o755))
	var we *hostsetup.WriteError
	if _, err := hostsetup.RemoveFile(hostsetup.OS{}, notEmpty); !errors.As(err, &we) || we.Path != notEmpty {
		t.Fatalf("err = %v", err)
	}
}

// repoTree is a repository directory: nested directories, files of
// various modes and a dangling link.
func repoTree(t *testing.T) (root string, files map[string]os.FileMode) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "repo")
	for _, d := range []string{"data/00", "keys"} {
		ok(t, os.MkdirAll(filepath.Join(root, d), 0o755))
	}
	files = map[string]os.FileMode{"config": 0o400, "keys/k": 0o444, "data/00/blob": 0o600}
	for rel, mode := range files {
		ok(t, os.WriteFile(filepath.Join(root, rel), []byte("x"), mode))
	}
	ok(t, os.Symlink("/nonexistent", filepath.Join(root, "link")))
	return root, files
}

func TestChownTreeReachesEveryEntry(t *testing.T) {
	root, _ := repoTree(t)
	rfs := newRecordingFS()
	ok(t, hostsetup.ChownTree(rfs, root, 990, 990))
	for _, p := range []string{"", "data", "data/00", "keys", "config", "keys/k", "data/00/blob", "link"} {
		path := filepath.Join(root, p)
		if o, found := rfs.ownerOf(path); !found || o != (owner{990, 990}) {
			t.Errorf("%s: owner %+v (set %v)", path, o, found)
		}
	}
}

func TestChownTreeLeavesTheModesAlone(t *testing.T) {
	root, files := repoTree(t)
	ok(t, hostsetup.ChownTree(newRecordingFS(), root, 990, 990))
	for rel, mode := range files {
		if info, _ := os.Stat(filepath.Join(root, rel)); info.Mode().Perm() != mode {
			t.Errorf("%s: mode %v, want %v", rel, info.Mode().Perm(), mode)
		}
	}
}

func TestChownTreeStopsAtTheFirstFailureAndNamesThePath(t *testing.T) {
	root := t.TempDir()
	ok(t, os.WriteFile(filepath.Join(root, "f"), nil, 0o600))
	rfs := newRecordingFS()
	rfs.failOn, rfs.failPath = "chown", filepath.Join(root, "f")
	var we *hostsetup.WriteError
	if err := hostsetup.ChownTree(rfs, root, 1, 1); !errors.As(err, &we) || we.Path != filepath.Join(root, "f") {
		t.Fatalf("err = %v", err)
	}
}

func TestChownTreeOfAMissingRootIsAnError(t *testing.T) {
	var we *hostsetup.WriteError
	if err := hostsetup.ChownTree(newRecordingFS(), filepath.Join(t.TempDir(), "absent"), 1, 1); !errors.As(err, &we) {
		t.Fatalf("err = %v", err)
	}
}

func TestOSFileSystemWritesAndReadsBack(t *testing.T) {
	dir := t.TempDir()
	var real hostsetup.OS
	path := filepath.Join(dir, "a")
	ok(t, hostsetup.WriteFile(real, path, []byte("v"), hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid(), Mode: 0o600}))
	if data, err := real.ReadFile(path); err != nil || string(data) != "v" {
		t.Fatalf("ReadFile %q %v", data, err)
	}
	if info, err := real.Stat(path); err != nil || info.Size() != 1 {
		t.Fatalf("Stat %v %v", info, err)
	}
	if entries, err := real.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Fatalf("ReadDir %v %v", entries, err)
	}
}

func TestOSFileSystemChangesOwnerModeAndRemoves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a")
	ok(t, os.WriteFile(path, nil, 0o600))
	var real hostsetup.OS
	ok(t, real.Chown(path, os.Getuid(), os.Getgid()))
	ok(t, real.Chmod(path, 0o640))
	ok(t, real.Remove(path))
}

func TestStagedFileIsOwnedAndFilledBeforeItIsCommitted(t *testing.T) {
	dir := t.TempDir()
	rfs := newRecordingFS()
	final := filepath.Join(dir, "restic-x.pass")
	tmp, err := hostsetup.StageFile(rfs, final, []byte("candidate"), hostsetup.Attrs{UID: 990, GID: 990, Mode: 0o600})
	ok(t, err)
	if filepath.Dir(tmp) != dir || tmp == final {
		t.Fatalf("temporary file %s", tmp)
	}
	if data, _ := os.ReadFile(tmp); string(data) != "candidate" {
		t.Fatalf("content %q", data)
	}
	if o, _ := rfs.ownerOf(tmp); o != (owner{990, 990}) {
		t.Fatalf("owner %+v", o)
	}
	if _, err := os.Stat(final); !os.IsNotExist(err) {
		t.Fatal("the target exists before the commit")
	}
}

func TestACommittedStagedFileIsTheTargetWithItsOwner(t *testing.T) {
	dir := t.TempDir()
	rfs := newRecordingFS()
	final := filepath.Join(dir, "restic-x.pass")
	tmp, err := hostsetup.StageFile(rfs, final, []byte("candidate"), hostsetup.Attrs{UID: 990, GID: 990, Mode: 0o600})
	ok(t, err)
	ok(t, hostsetup.CommitFile(rfs, tmp, final))
	if data, _ := os.ReadFile(final); string(data) != "candidate" {
		t.Fatalf("committed content %q", data)
	}
	if o, _ := rfs.ownerOf(final); o != (owner{990, 990}) {
		t.Fatalf("committed owner %+v", o)
	}
	if extra := leftovers(t, dir, "restic-x.pass"); len(extra) != 0 {
		t.Fatalf("leftovers %v", extra)
	}
}

func TestADiscardedStagedFileLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	tmp, err := hostsetup.StageFile(hostsetup.OS{}, filepath.Join(dir, "x"), []byte("v"), hostsetup.Attrs{Mode: 0o600})
	ok(t, err)
	hostsetup.DiscardFile(hostsetup.OS{}, tmp)
	if extra := leftovers(t, dir); len(extra) != 0 {
		t.Fatalf("leftovers %v", extra)
	}
}

func TestAFailedCommitRemovesTheStagedFile(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "x")
	tmp, err := hostsetup.StageFile(hostsetup.OS{}, final, []byte("v"), hostsetup.Attrs{Mode: 0o600})
	ok(t, err)
	rfs := newRecordingFS()
	rfs.failOn = "rename"
	var we *hostsetup.WriteError
	if err := hostsetup.CommitFile(rfs, tmp, final); !errors.As(err, &we) || we.Path != final {
		t.Fatalf("err = %v", err)
	}
	if extra := leftovers(t, dir); len(extra) != 0 {
		t.Fatalf("leftovers %v", extra)
	}
}

func TestSyncDirOfAMissingDirectoryIsTheOpenError(t *testing.T) {
	err := hostsetup.OS{}.SyncDir(filepath.Join(t.TempDir(), "absent"))
	if err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("err = %v", err)
	}
}

// swappingFS swaps the directory "sub" of the tree for a link to a place
// outside it right after the tree has been listed, and records where every
// owner change really lands.
type swappingFS struct {
	hostsetup.OS
	outside string
	landed  []string
}

func (f *swappingFS) OpenRoot(path string) (hostsetup.Root, error) {
	root, err := f.OS.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	return &swappingRoot{Root: root, base: path, fsys: f}, nil
}

type swappingRoot struct {
	hostsetup.Root
	base    string
	fsys    *swappingFS
	swapped bool
}

func (r *swappingRoot) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := r.Root.ReadDir(name)
	if name == "." && !r.swapped {
		r.swapped = true
		sub := filepath.Join(r.base, "sub")
		_ = os.RemoveAll(sub)
		_ = os.Symlink(r.fsys.outside, sub)
	}
	return entries, err
}

func (r *swappingRoot) Lchown(name string, uid, gid int) error {
	if err := r.Root.Lchown(name, os.Getuid(), os.Getgid()); err != nil {
		return err
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(filepath.Join(r.base, name)))
	if err == nil {
		r.fsys.landed = append(r.fsys.landed, filepath.Join(dir, filepath.Base(name)))
	}
	return err
}

func TestChownTreeNeverLeavesTheTreeThroughASwappedDirectory(t *testing.T) {
	base := t.TempDir()
	root, outside := filepath.Join(base, "repo"), filepath.Join(base, "etc")
	ok(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	ok(t, os.WriteFile(filepath.Join(root, "sub", "inner"), nil, 0o600))
	ok(t, os.MkdirAll(outside, 0o755))
	ok(t, os.WriteFile(filepath.Join(outside, "passwd"), nil, 0o600))
	fsys := &swappingFS{outside: outside}
	_ = hostsetup.ChownTree(fsys, root, 990, 990)
	resolvedRoot, _ := filepath.EvalSymlinks(root)
	for _, p := range fsys.landed {
		if !strings.HasPrefix(p, resolvedRoot+string(filepath.Separator)) && p != resolvedRoot {
			t.Errorf("an owner change landed outside the tree: %s", p)
		}
	}
}
