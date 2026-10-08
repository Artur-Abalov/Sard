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

func (r *recordingFS) OpenRootDir() (hostsetup.Dir, error) {
	d, err := r.OS.OpenRootDir()
	if err != nil {
		return nil, err
	}
	return recordingDir{Dir: d, fsys: r}, nil
}

// recordingDir records the owner changes and the creations of a walk.
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

func (d recordingDir) Mkdir(name string, perm os.FileMode) (hostsetup.Dir, error) {
	if err := d.fsys.fails("mkdir", filepath.Join(d.Path(), name)); err != nil {
		return nil, err
	}
	return d.wrap(d.Dir.Mkdir(name, perm))
}

func (d recordingDir) Chown(uid, gid int) error {
	if err := d.fsys.fails("chown", d.Path()); err != nil {
		return err
	}
	d.fsys.setOwner(d.Path(), uid, gid)
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
	parent := t.TempDir()
	old := syscall.Umask(0o077) // not 0o777: a directory nobody may open cannot be opened by its owner either, only by root
	defer syscall.Umask(old)
	dir := filepath.Join(parent, "agent.d")
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
	tmp, err := hostsetup.StageFile(hostsetup.OS{}, filepath.Join(dir, "x"), []byte("v"), hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid(), Mode: 0o600})
	ok(t, err)
	hostsetup.DiscardFile(hostsetup.OS{}, tmp)
	if extra := leftovers(t, dir); len(extra) != 0 {
		t.Fatalf("leftovers %v", extra)
	}
}

func TestAFailedCommitRemovesTheStagedFile(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "x")
	tmp, err := hostsetup.StageFile(hostsetup.OS{}, final, []byte("v"), hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid(), Mode: 0o600})
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
	swapped bool
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
	base string
	fsys *swappingFS
}

func (r *swappingRoot) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := r.Root.ReadDir(name)
	if name == "." && !r.fsys.swapped {
		r.fsys.swapped = true
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
	full := filepath.Join(r.base, name) // "." is the tree itself
	dir, err := filepath.EvalSymlinks(filepath.Dir(full))
	if err == nil {
		r.fsys.landed = append(r.fsys.landed, filepath.Join(dir, filepath.Base(full)))
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
	if !fsys.swapped {
		t.Fatal("the swap did not happen: the test proves nothing")
	}
	resolvedRoot, _ := filepath.EvalSymlinks(root)
	for _, p := range fsys.landed {
		if !strings.HasPrefix(p, resolvedRoot+string(filepath.Separator)) && p != resolvedRoot {
			t.Errorf("an owner change landed outside the tree: %s", p)
		}
	}
}

func TestChownTreeRefusesARootThatIsASymbolicLink(t *testing.T) {
	base := t.TempDir()
	target, link := filepath.Join(base, "etc"), filepath.Join(base, "backup")
	ok(t, os.MkdirAll(target, 0o755))
	ok(t, os.WriteFile(filepath.Join(target, "passwd"), nil, 0o600))
	ok(t, os.Symlink(target, link))
	rfs := newRecordingFS()
	err := hostsetup.ChownTree(rfs, link, 990, 990)
	var we *hostsetup.WriteError
	if !errors.As(err, &we) || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("err = %v", err)
	}
	if len(rfs.owners) != 0 {
		t.Fatalf("an owner change landed behind the link: %v", rfs.owners)
	}
}

// otherRootFS opens another directory than the one asked for, as if the
// root had been replaced between the look and the opening.
type otherRootFS struct {
	hostsetup.OS
	other string
}

func (f otherRootFS) OpenRoot(string) (hostsetup.Root, error) { return f.OS.OpenRoot(f.other) }

func TestChownTreeRefusesARootReplacedWhileItIsOpened(t *testing.T) {
	base := t.TempDir()
	root, other := filepath.Join(base, "repo"), filepath.Join(base, "other")
	ok(t, os.Mkdir(root, 0o755))
	ok(t, os.Mkdir(other, 0o755))
	err := hostsetup.ChownTree(otherRootFS{other: other}, root, 990, 990)
	if err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Fatalf("err = %v", err)
	}
}

// hookFS runs after on every directory the walk opens or makes, with its
// path, so a test can swap it for a link the way the service user could.
type hookFS struct {
	hostsetup.OS
	after func(path string)
}

func (f hookFS) OpenRootDir() (hostsetup.Dir, error) {
	d, err := f.OS.OpenRootDir()
	if err != nil {
		return nil, err
	}
	return hookDir{Dir: d, fsys: f}, nil
}

type hookDir struct {
	hostsetup.Dir
	fsys hookFS
}

func (d hookDir) done(next hostsetup.Dir, err error) (hostsetup.Dir, error) {
	if err != nil {
		return nil, err
	}
	d.fsys.after(next.Path())
	return hookDir{Dir: next, fsys: d.fsys}, nil
}

func (d hookDir) Open(name string) (hostsetup.Dir, error) { return d.done(d.Dir.Open(name)) }

func (d hookDir) Mkdir(name string, perm os.FileMode) (hostsetup.Dir, error) {
	return d.done(d.Dir.Mkdir(name, perm))
}

// Chown does not change the owner (a test is not root).
func (d hookDir) Chown(int, int) error { return nil }

// linkLayout is base/home/repos -> base/var, with base/var/backups.
func linkLayout(t *testing.T) (base string) {
	t.Helper()
	base = t.TempDir()
	ok(t, os.MkdirAll(filepath.Join(base, "home"), 0o755))
	ok(t, os.MkdirAll(filepath.Join(base, "var", "backups"), 0o755))
	ok(t, os.Symlink(filepath.Join(base, "var"), filepath.Join(base, "home", "repos")))
	return base
}

func TestChownTreeRefusesAPathThatPassesThroughASymbolicLink(t *testing.T) {
	base := linkLayout(t)
	rfs := newRecordingFS()
	link := filepath.Join(base, "home", "repos")
	err := hostsetup.ChownTree(rfs, filepath.Join(link, "backups"), 990, 990)
	var nd *hostsetup.NotDirError
	if !errors.As(err, &nd) || !nd.Symlink || nd.Path != link {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), link+" is a symbolic link; give the resolved path") {
		t.Fatalf("err = %v", err)
	}
	if len(rfs.owners) != 0 {
		t.Fatalf("owners changed: %v", rfs.owners)
	}
}

func TestOpenDirNeverCreatesBehindASymbolicLink(t *testing.T) {
	base := linkLayout(t)
	mk := &hostsetup.Make{Parents: hostsetup.Attrs{Mode: 0o755}, Last: hostsetup.Attrs{Mode: 0o700}}
	_, _, err := hostsetup.OpenDir(newRecordingFS(), filepath.Join(base, "home", "repos", "new", "deeper"), mk)
	var nd *hostsetup.NotDirError
	if !errors.As(err, &nd) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "var", "new")); err == nil {
		t.Fatal("a directory was created behind the link")
	}
}

func TestOpenDirRefusesAFileInTheWayAndAnUnknownRelativePath(t *testing.T) {
	base := t.TempDir()
	ok(t, os.WriteFile(filepath.Join(base, "f"), nil, 0o600))
	var nd *hostsetup.NotDirError
	_, _, err := hostsetup.OpenDir(newRecordingFS(), filepath.Join(base, "f", "x"), nil)
	if !errors.As(err, &nd) || nd.Symlink || !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := hostsetup.OpenDir(newRecordingFS(), "relative", nil); err == nil {
		t.Fatal("a relative path was walked")
	}
}

func TestOpenDirCreatesMissingComponentsWithTheirOwnersAndReportsThem(t *testing.T) {
	base := t.TempDir()
	rfs := newRecordingFS()
	mk := &hostsetup.Make{Parents: hostsetup.Attrs{Mode: 0o755}, Last: hostsetup.Attrs{UID: 990, GID: 990, Mode: 0o700}}
	target := filepath.Join(base, "a", "b", "repo")
	d, created, err := hostsetup.OpenDir(rfs, target, mk)
	ok(t, err)
	_ = d.Close()
	want := []string{filepath.Join(base, "a"), filepath.Join(base, "a", "b"), target}
	if strings.Join(created, "|") != strings.Join(want, "|") {
		t.Fatalf("created %v", created)
	}
	assertDirMode(t, filepath.Join(base, "a"), 0o755)
	assertDirMode(t, target, 0o700)
	if o, _ := rfs.ownerOf(target); o != (owner{990, 990}) {
		t.Fatalf("owner %+v", o)
	}
}

func assertDirMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != want {
		t.Fatalf("%s: %v %v", path, info, err)
	}
}

func TestOpenDirOfAMissingPathWithoutMakeIsNotExist(t *testing.T) {
	_, _, err := hostsetup.OpenDir(newRecordingFS(), filepath.Join(t.TempDir(), "absent", "x"), nil)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
}

func TestACreationFailureNamesTheDirectory(t *testing.T) {
	base := t.TempDir()
	rfs := newRecordingFS()
	rfs.failOn, rfs.failPath = "mkdir", "b"
	_, created, err := hostsetup.OpenDir(rfs, filepath.Join(base, "a", "b"), &hostsetup.Make{Parents: hostsetup.Attrs{Mode: 0o755}, Last: hostsetup.Attrs{Mode: 0o700}})
	var we *hostsetup.WriteError
	if !errors.As(err, &we) || we.Path != filepath.Join(base, "a", "b") || len(created) != 1 {
		t.Fatalf("err = %v, created %v", err, created)
	}
}

// A component swapped for a link after it was opened does not redirect what
// comes next: the held descriptor wins.
func TestAComponentSwappedForALinkAfterItIsOpenedDoesNotRedirectTheWalk(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	ok(t, os.Mkdir(outside, 0o755))
	ok(t, os.MkdirAll(filepath.Join(base, "a"), 0o755))
	swap := func(p string) {
		if p == filepath.Join(base, "a") {
			ok(t, os.Rename(p, p+".moved"))
			ok(t, os.Symlink(outside, p))
		}
	}
	mk := &hostsetup.Make{Parents: hostsetup.Attrs{Mode: 0o755}, Last: hostsetup.Attrs{Mode: 0o700}}
	d, _, err := hostsetup.OpenDir(hookFS{after: swap}, filepath.Join(base, "a", "b"), mk)
	ok(t, err)
	_ = d.Close()
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("something was created behind the link: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(base, "a.moved", "b")); err != nil {
		t.Fatalf("the directory was not made in the directory that was held: %v", err)
	}
}

// A directory just made and swapped for a link redirects neither the owner
// change nor the next creation.
func TestADirectoryMadeAndSwappedForALinkRedirectsNothing(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	ok(t, os.Mkdir(outside, 0o755))
	made := filepath.Join(base, "a")
	swap := func(p string) {
		if p == made {
			ok(t, os.Rename(p, p+".moved"))
			ok(t, os.Symlink(outside, p))
		}
	}
	mk := &hostsetup.Make{Parents: hostsetup.Attrs{Mode: 0o755}, Last: hostsetup.Attrs{UID: 990, GID: 990, Mode: 0o700}}
	d, _, err := hostsetup.OpenDir(hookFS{after: swap}, filepath.Join(made, "b"), mk)
	ok(t, err)
	_ = d.Close()
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("something was created behind the link: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(made+".moved", "b")); err != nil {
		t.Fatalf("the next directory was not made inside the made one: %v", err)
	}
}

type noRootDirFS struct{ hostsetup.OS }

func (noRootDirFS) OpenRootDir() (hostsetup.Dir, error) { return nil, errors.New("no root") }

func TestOpenDirReportsARootThatCannotBeOpened(t *testing.T) {
	if _, _, err := hostsetup.OpenDir(noRootDirFS{}, "/x", nil); err == nil || !strings.Contains(err.Error(), "no root") {
		t.Fatalf("err = %v", err)
	}
}

// The new directory is swapped for a link to somewhere else right after it
// is made; its mode and owner must be set on the directory held, never on
// whatever the path leads to afterwards.
func TestEnsureDirNeverChangesWhatALinkPointsTo(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "etc")
	ok(t, os.Mkdir(outside, 0o755))
	ok(t, os.Chmod(outside, 0o755))
	made := filepath.Join(base, "tls")
	swapped := false
	swap := func(p string) {
		if p == made {
			swapped = true
			ok(t, os.Rename(p, p+".moved"))
			ok(t, os.Symlink(outside, p))
		}
	}
	created, err := hostsetup.EnsureDir(hookFS{after: swap}, made, hostsetup.Attrs{Mode: 0o700})
	ok(t, err)
	if !created || !swapped {
		t.Fatalf("created %v, swapped %v: the test proves nothing", created, swapped)
	}
	assertDirMode(t, outside, 0o755)
	assertDirMode(t, made+".moved", 0o700)
}

func TestEnsureDirThroughALinkedParentOfTheConfigStillWorks(t *testing.T) {
	base := t.TempDir()
	real, link := filepath.Join(base, "real"), filepath.Join(base, "etc-sard")
	ok(t, os.Mkdir(real, 0o755))
	ok(t, os.Symlink(real, link))
	created, err := hostsetup.EnsureDir(newRecordingFS(), filepath.Join(link, "agent.d"), hostsetup.Attrs{Mode: 0o750})
	if err != nil || !created {
		t.Fatalf("created %v, err %v", created, err)
	}
	assertDirMode(t, filepath.Join(real, "agent.d"), 0o750)
}

func TestEnsureDirOfAParentThatDoesNotExistIsAWriteError(t *testing.T) {
	var we *hostsetup.WriteError
	_, err := hostsetup.EnsureDir(newRecordingFS(), filepath.Join(t.TempDir(), "no", "dir"), hostsetup.Attrs{Mode: 0o700})
	if !errors.As(err, &we) {
		t.Fatalf("err = %v", err)
	}
}

// A hard link planted in the tree to a file that is not the tree's would
// hand that file over: files with more than one name are left alone.
func TestChownTreeLeavesAFileWithAnotherNameAlone(t *testing.T) {
	base := t.TempDir()
	root, victim := filepath.Join(base, "repo"), filepath.Join(base, "shadow")
	ok(t, os.Mkdir(root, 0o755))
	ok(t, os.WriteFile(victim, []byte("root:x"), 0o600))
	ok(t, os.WriteFile(filepath.Join(root, "own"), nil, 0o600))
	ok(t, os.Link(victim, filepath.Join(root, "planted")))
	rfs := newRecordingFS()
	ok(t, hostsetup.ChownTree(rfs, root, 990, 990))
	if _, changed := rfs.ownerOf(filepath.Join(root, "planted")); changed {
		t.Fatal("the planted hard link was given away")
	}
	if _, changed := rfs.ownerOf(filepath.Join(root, "own")); !changed {
		t.Fatal("a plain file was not given")
	}
}
