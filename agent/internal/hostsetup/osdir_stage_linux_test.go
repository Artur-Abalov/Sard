// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

// A8c-2 (Р73): the primitives of the staging directory of repo restore.

func TestHandleIsTheDescriptorOfTheHeldDirectory(t *testing.T) {
	base := t.TempDir()
	h := heldDir(t, base).Handle()
	want, err := os.Stat(base)
	ok(t, err)
	got, err := h.Stat()
	ok(t, err)
	if !os.SameFile(want, got) {
		t.Fatalf("the handle is another directory")
	}
}

func TestRenameNoReplaceMovesAnEntryFromOneHeldDirectoryToAnother(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	ok(t, os.WriteFile(filepath.Join(from, "a"), []byte("A"), 0o640))
	ok(t, heldDir(t, from).RenameNoReplace("a", heldDir(t, to), "b"))
	if data, err := os.ReadFile(filepath.Join(to, "b")); err != nil || string(data) != "A" {
		t.Fatalf("b: %q, %v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(from, "a")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a is still there: %v", err)
	}
}

func TestRenameNoReplaceNeverReplacesAnExistingName(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	ok(t, os.WriteFile(filepath.Join(from, "a"), []byte("NEW"), 0o600))
	ok(t, os.MkdirAll(filepath.Join(to, "a"), 0o700))
	err := heldDir(t, from).RenameNoReplace("a", heldDir(t, to), "a")
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(from, "a")); err != nil || string(data) != "NEW" {
		t.Errorf("the entry moved or changed: %q, %v", data, err)
	}
	if info, err := os.Stat(filepath.Join(to, "a")); err != nil || !info.IsDir() {
		t.Errorf("the name in the target was replaced: %v, %v", info, err)
	}
}

func TestRenameNoReplaceMovesADirectoryWithItsContent(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	ok(t, os.MkdirAll(filepath.Join(from, "srv/data"), 0o750))
	ok(t, os.WriteFile(filepath.Join(from, "srv/data/f"), []byte("F"), 0o600))
	ok(t, heldDir(t, from).RenameNoReplace("srv", heldDir(t, to), "srv"))
	if data, err := os.ReadFile(filepath.Join(to, "srv/data/f")); err != nil || string(data) != "F" {
		t.Fatalf("f: %q, %v", data, err)
	}
}

// A mount point between the staging directory and the target is EXDEV.
func TestTheErrorOfAMoveAcrossDevicesIsErrCrossDevice(t *testing.T) {
	err := hostsetup.RenameError("srv", syscall.EXDEV)
	if !errors.Is(err, hostsetup.ErrCrossDevice) {
		t.Fatalf("err = %v", err)
	}
	if other := hostsetup.RenameError("srv", syscall.EACCES); errors.Is(other, hostsetup.ErrCrossDevice) || !errors.Is(other, syscall.EACCES) {
		t.Fatalf("other = %v", other)
	}
}

func TestRemoveDirTakesAnEmptyDirectoryAndOnlyThat(t *testing.T) {
	base := t.TempDir()
	ok(t, os.MkdirAll(filepath.Join(base, "empty"), 0o700))
	ok(t, os.MkdirAll(filepath.Join(base, "full"), 0o700))
	ok(t, os.WriteFile(filepath.Join(base, "full/f"), nil, 0o600))
	held := heldDir(t, base)
	ok(t, held.RemoveDir("empty"))
	if err := held.RemoveDir("full"); err == nil {
		t.Error("a directory with a file was removed")
	}
}

// The removal goes by descriptors and never through a link: what a link
// points at is not touched.
func TestRemoveTreeRemovesEverythingBelowAndNeverFollowsALink(t *testing.T) {
	base, outside := t.TempDir(), t.TempDir()
	ok(t, os.WriteFile(filepath.Join(outside, "precious"), []byte("P"), 0o600))
	tree := filepath.Join(base, "stage")
	ok(t, os.MkdirAll(filepath.Join(tree, "root/a/b"), 0o700))
	ok(t, os.WriteFile(filepath.Join(tree, "root/a/b/f"), nil, 0o600))
	ok(t, os.Symlink(outside, filepath.Join(tree, "root/a/link")))
	ok(t, syscall.Mkfifo(filepath.Join(tree, "root/fifo"), 0o600))
	ok(t, hostsetup.RemoveTree(heldDir(t, base), "stage"))
	if _, err := os.Lstat(tree); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the tree is still there: %v", err)
	}
	if names, err := os.ReadDir(outside); err != nil || len(names) != 1 {
		t.Errorf("what the link pointed at: %v, %v", names, err)
	}
}

func TestRemoveTreeOfALinkRemovesTheLinkOnly(t *testing.T) {
	base, outside := t.TempDir(), t.TempDir()
	ok(t, os.WriteFile(filepath.Join(outside, "precious"), nil, 0o600))
	ok(t, os.Symlink(outside, filepath.Join(base, "stage")))
	ok(t, hostsetup.RemoveTree(heldDir(t, base), "stage"))
	if names, _ := os.ReadDir(outside); !slices.ContainsFunc(names, func(e fs.DirEntry) bool { return e.Name() == "precious" }) {
		t.Error("the content behind the link was removed")
	}
	if _, err := os.Lstat(filepath.Join(base, "stage")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the link is still there: %v", err)
	}
}

func TestOwnerOfIsTheUserAndGroupOfAFileInfo(t *testing.T) {
	info, err := os.Stat(t.TempDir())
	ok(t, err)
	uid, gid, found := hostsetup.OwnerOf(info)
	if !found || int(uid) != os.Getuid() || int(gid) != os.Getgid() && gid != 0 {
		t.Errorf("owner %d:%d (%v)", uid, gid, found)
	}
}

func TestRemoveTreeOfAnEntryThatIsGoneIsFine(t *testing.T) {
	ok(t, hostsetup.RemoveTree(heldDir(t, t.TempDir()), "absent"))
}

// failingDir is a directory some of whose steps fail.
type failingDir struct {
	hostsetup.Dir
	names, remove error
}

func (d failingDir) Open(name string) (hostsetup.Dir, error) {
	child, err := d.Dir.Open(name)
	if err != nil {
		return nil, err
	}
	return failingDir{Dir: child, names: d.names, remove: d.remove}, nil
}

func (d failingDir) Names() ([]string, error) {
	if d.names != nil {
		return nil, d.names
	}
	return d.Dir.Names()
}

func (d failingDir) Remove(name string) error {
	if d.remove != nil {
		return d.remove
	}
	return d.Dir.Remove(name)
}

func TestRemoveTreeReportsWhatCannotBeListedOrRemoved(t *testing.T) {
	errList, errRemove := errors.New("cannot list"), errors.New("cannot remove")
	base := t.TempDir()
	ok(t, os.MkdirAll(filepath.Join(base, "stage/sub"), 0o700))
	ok(t, os.WriteFile(filepath.Join(base, "stage/sub/f"), nil, 0o600))
	if err := hostsetup.RemoveTree(failingDir{Dir: heldDir(t, base), names: errList}, "stage"); !errors.Is(err, errList) {
		t.Errorf("listing: %v", err)
	}
	if err := hostsetup.RemoveTree(failingDir{Dir: heldDir(t, base), remove: errRemove}, "stage"); !errors.Is(err, errRemove) {
		t.Errorf("removing: %v", err)
	}
}

func TestRemoveTreeOfAnEntryItCannotOpenIsTheErrorOfTheOpen(t *testing.T) {
	base := t.TempDir()
	closed := heldDir(t, base)
	ok(t, closed.Close())
	if err := hostsetup.RemoveTree(closed, "stage"); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v", err)
	}
}
