// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

// openFDs counts the descriptors of this process: a handle a function
// should have closed shows as one more.
func openFDs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	ok(t, err)
	return len(entries)
}

// assertNoLeak runs f and fails if it left a descriptor open.
func assertNoLeak(t *testing.T, what string, f func()) {
	t.Helper()
	before := openFDs(t)
	f()
	if after := openFDs(t); after != before {
		t.Errorf("%s left %d descriptors open", what, after-before)
	}
}

func TestOpenRootOfAMissingDirectoryIsTheOpenError(t *testing.T) {
	root, err := hostsetup.OS{}.OpenRoot(filepath.Join(t.TempDir(), "absent"))
	if root != nil || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("root = %v, err = %v", root, err)
	}
}

func TestOpenDirRefusesARelativePathWithoutWalkingIt(t *testing.T) {
	_, _, err := hostsetup.OpenDir(hostsetup.OS{}, "tmp", nil)
	if err == nil || err.Error() != "tmp is not an absolute path" {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenDirNamesTheFileThatIsInTheWay(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "f")
	ok(t, os.WriteFile(file, nil, 0o600))
	_, _, err := hostsetup.OpenDir(hostsetup.OS{}, filepath.Join(file, "x"), nil)
	var nd *hostsetup.NotDirError
	if !errors.As(err, &nd) || nd.Path != file || nd.Symlink {
		t.Fatalf("err = %v", err)
	}
	_, _, err = hostsetup.OpenDir(hostsetup.OS{}, file, nil)
	if !errors.As(err, &nd) || nd.Path != file {
		t.Fatalf("a file as the last component: %v", err)
	}
}

func TestOpenDirClosesEveryDirectoryItPassesAndHandsOverTheLast(t *testing.T) {
	base := t.TempDir()
	ok(t, os.MkdirAll(filepath.Join(base, "a", "b"), 0o755))
	before := openFDs(t)
	d, _, err := hostsetup.OpenDir(hostsetup.OS{}, filepath.Join(base, "a", "b"), nil)
	ok(t, err)
	if after := openFDs(t); after != before+1 {
		t.Errorf("a walk holds %d descriptors, want 1", after-before)
	}
	ok(t, d.Close())
	assertNoLeak(t, "a walk that fails halfway", func() {
		_, _, err := hostsetup.OpenDir(hostsetup.OS{}, filepath.Join(base, "a", "none", "c"), nil)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("err = %v", err)
		}
	})
}

// chownFailsDir is a directory of the real file system whose owner cannot
// be changed.
type chownFailsDir struct{ hostsetup.Dir }

func (d chownFailsDir) Chown(int, int) error { return errInjected }

func (d chownFailsDir) Open(name string) (hostsetup.Dir, error) {
	next, err := d.Dir.Open(name)
	if err != nil {
		return nil, err
	}
	return chownFailsDir{next}, nil
}

func (d chownFailsDir) Mkdir(name string, perm os.FileMode) (hostsetup.Dir, error) {
	next, err := d.Dir.Mkdir(name, perm)
	if err != nil {
		return nil, err
	}
	return chownFailsDir{next}, nil
}

type chownFailsFS struct{ hostsetup.OS }

func (f chownFailsFS) OpenRootDir() (hostsetup.Dir, error) {
	d, err := f.OS.OpenRootDir()
	if err != nil {
		return nil, err
	}
	return chownFailsDir{d}, nil
}

func TestADirectoryThatCannotBeGivenItsOwnerIsClosedAndReported(t *testing.T) {
	base := t.TempDir()
	mk := &hostsetup.Make{Parents: hostsetup.Attrs{Mode: 0o755}, Last: hostsetup.Attrs{Mode: 0o700}}
	assertNoLeak(t, "a directory made and refused", func() {
		d, _, err := hostsetup.OpenDir(chownFailsFS{}, filepath.Join(base, "new"), mk)
		var we *hostsetup.WriteError
		if d != nil || !errors.As(err, &we) || !errors.Is(err, errInjected) {
			t.Errorf("dir = %v, err = %v", d, err)
		}
	})
}

func TestEnsureDirClosesTheParentItHeld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tls")
	a := hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid(), Mode: 0o700}
	assertNoLeak(t, "EnsureDir", func() {
		created, err := hostsetup.EnsureDir(hostsetup.OS{}, path, a)
		if err != nil || !created {
			t.Errorf("created %v, err %v", created, err)
		}
	})
}

func TestChownTreeClosesWhatItOpened(t *testing.T) {
	dir := t.TempDir()
	ok(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	assertNoLeak(t, "ChownTree", func() {
		if err := hostsetup.ChownTree(hostsetup.OS{}, dir, os.Getuid(), os.Getgid()); err != nil {
			t.Errorf("err = %v", err)
		}
	})
	other := t.TempDir()
	assertNoLeak(t, "ChownTree of a root replaced while it is opened", func() {
		err := hostsetup.ChownTree(otherRootFS{other: other}, dir, os.Getuid(), os.Getgid())
		if err == nil || !strings.Contains(err.Error(), "replaced") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestStageFileThatFailsToWriteClosesAndRemovesTheTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	rfs := newRecordingFS()
	rfs.failOn = "chmod"
	assertNoLeak(t, "StageFile", func() {
		if _, err := hostsetup.StageFile(rfs, filepath.Join(dir, "x"), []byte("v"), hostsetup.Attrs{Mode: 0o600}); err == nil {
			t.Error("the write did not fail")
		}
	})
	if extra := leftovers(t, dir); len(extra) != 0 {
		t.Fatalf("leftovers %v", extra)
	}
}

func TestCommitNewFileDropsTheStagedNameWhateverTheOutcome(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "x")
	tmp, err := hostsetup.StageFile(hostsetup.OS{}, final, []byte("new"), hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid(), Mode: 0o600})
	ok(t, err)
	ok(t, hostsetup.CommitNewFile(hostsetup.OS{}, tmp, final))
	if extra := leftovers(t, dir, "x"); len(extra) != 0 {
		t.Fatalf("after a commit: leftovers %v", extra)
	}
	tmp, err = hostsetup.StageFile(hostsetup.OS{}, final, []byte("newer"), hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid(), Mode: 0o600})
	ok(t, err)
	var we *hostsetup.WriteError
	if err := hostsetup.CommitNewFile(hostsetup.OS{}, tmp, final); !errors.As(err, &we) || !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v", err)
	}
	if extra := leftovers(t, dir, "x"); len(extra) != 0 {
		t.Fatalf("after a refused commit: leftovers %v", extra)
	}
	if data, _ := os.ReadFile(final); string(data) != "new" {
		t.Fatalf("the existing file was replaced: %q", data)
	}
}

// watchFS opens Roots that record which names were given a new owner
// (without changing any: a test is not root) and can fail every Lstat.
type watchFS struct {
	hostsetup.OS
	failLstat bool
	chowned   []string
}

func (f *watchFS) OpenRoot(path string) (hostsetup.Root, error) {
	r, err := f.OS.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	return watchRoot{Root: r, fsys: f}, nil
}

type watchRoot struct {
	hostsetup.Root
	fsys *watchFS
}

func (r watchRoot) Lchown(name string, _, _ int) error {
	r.fsys.chowned = append(r.fsys.chowned, name)
	return nil
}

func (r watchRoot) Lstat(name string) (fs.FileInfo, error) {
	if r.fsys.failLstat {
		return nil, errInjected
	}
	return r.Root.Lstat(name)
}

func TestChownTreeGivesAnEntryWhoseNamesCannotBeCountedToo(t *testing.T) {
	dir := t.TempDir()
	ok(t, os.WriteFile(filepath.Join(dir, "file"), nil, 0o600))
	w := &watchFS{failLstat: true}
	ok(t, hostsetup.ChownTree(w, dir, 990, 990))
	if want := []string{".", "file"}; !slices.Equal(w.chowned, want) {
		t.Fatalf("chowned %v, want %v", w.chowned, want)
	}
}

// A hard link to a symbolic link is two names of the link itself; what the
// link points to is not touched either way, so both get the owner.
func TestChownTreeGivesEveryNameOfASymbolicLink(t *testing.T) {
	dir := t.TempDir()
	ok(t, os.Symlink("/etc/passwd", filepath.Join(dir, "a")))
	ok(t, os.Link(filepath.Join(dir, "a"), filepath.Join(dir, "b")))
	ok(t, os.WriteFile(filepath.Join(dir, "c"), nil, 0o600))
	ok(t, os.Link(filepath.Join(dir, "c"), filepath.Join(dir, "d")))
	w := &watchFS{}
	ok(t, hostsetup.ChownTree(w, dir, 990, 990))
	if want := []string{".", "a", "b"}; !slices.Equal(w.chowned, want) {
		t.Fatalf("chowned %v, want %v (a file with two names is left alone)", w.chowned, want)
	}
}
