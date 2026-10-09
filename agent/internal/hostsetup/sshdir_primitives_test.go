// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

// heldDir is dir opened the way the commands hold it.
func heldDir(t *testing.T, dir string) hostsetup.Dir {
	t.Helper()
	d, _, err := hostsetup.OpenDir(hostsetup.OS{}, dir, nil)
	ok(t, err)
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// Р38: a new file is made with O_EXCL|O_NOFOLLOW relative to the held directory.
func TestCreateFileNeverWritesThroughALinkOrOverAFile(t *testing.T) {
	base := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	ok(t, os.WriteFile(victim, []byte("root:x\n"), 0o600))
	ok(t, os.Symlink(victim, filepath.Join(base, "link")))
	ok(t, os.Symlink(filepath.Join(base, "created-by-root"), filepath.Join(base, "dangling")))
	ok(t, os.WriteFile(filepath.Join(base, "file"), []byte("mine"), 0o600))
	d := heldDir(t, base)
	for _, name := range []string{"link", "dangling", "file"} {
		f, err := d.CreateFile(name, 0o600)
		if f != nil || !errors.Is(err, fs.ErrExist) {
			t.Errorf("%s: file %v, err %v", name, f, err)
		}
	}
	if data, _ := os.ReadFile(victim); string(data) != "root:x\n" {
		t.Fatalf("the victim was written: %q", data)
	}
	if _, err := os.Lstat(filepath.Join(base, "created-by-root")); err == nil {
		t.Fatal("the target of the dangling link was created")
	}
	if data, _ := os.ReadFile(filepath.Join(base, "file")); string(data) != "mine" {
		t.Fatalf("the file was changed: %q", data)
	}
}

func TestCreateFileMakesAFileTheDirectoryOwnsWithTheModeAskedFor(t *testing.T) {
	base := t.TempDir()
	d := heldDir(t, base)
	f, err := d.CreateFile("new", 0o600)
	ok(t, err)
	if f.Name() != filepath.Join(base, "new") {
		t.Errorf("name %q", f.Name())
	}
	_, err = f.Write([]byte("v"))
	ok(t, err)
	ok(t, f.Chmod(0o600))
	ok(t, f.Close())
	if data, _ := os.ReadFile(filepath.Join(base, "new")); string(data) != "v" {
		t.Fatalf("content %q", data)
	}
}

// Р38: an existing file is read without following a link and without blocking.
func TestOpenFileReadsARegularFileAndRefusesALinkWithoutFollowingIt(t *testing.T) {
	base := t.TempDir()
	ok(t, os.WriteFile(filepath.Join(base, "known_hosts"), []byte("line\n"), 0o600))
	ok(t, os.Symlink(filepath.Join(base, "known_hosts"), filepath.Join(base, "link")))
	ok(t, os.Symlink(filepath.Join(base, "absent"), filepath.Join(base, "dangling")))
	d := heldDir(t, base)
	f, err := d.OpenFile("known_hosts")
	ok(t, err)
	data, err := io.ReadAll(f)
	ok(t, err)
	info, err := f.Stat()
	ok(t, err)
	ok(t, f.Close())
	if string(data) != "line\n" || !info.Mode().IsRegular() {
		t.Fatalf("data %q, mode %v", data, info.Mode())
	}
	for _, name := range []string{"link", "dangling"} {
		if _, err := d.OpenFile(name); !errors.Is(err, syscall.ELOOP) {
			t.Errorf("%s: err %v", name, err)
		}
	}
	if _, err := d.OpenFile("none"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing file: %v", err)
	}
}

func TestOpenFileOfAFIFONoOneWritesToReturnsAtOnce(t *testing.T) {
	base := t.TempDir()
	ok(t, syscall.Mkfifo(filepath.Join(base, "fifo"), 0o600))
	d := heldDir(t, base)
	done := make(chan error, 1)
	go func() {
		f, err := d.OpenFile("fifo")
		if err == nil {
			info, serr := f.Stat()
			err = serr
			if serr == nil && info.Mode().IsRegular() {
				err = errors.New("a FIFO is regular")
			}
			_ = f.Close()
		}
		done <- err
	}()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// The private key is looked at, never opened for reading, by root.
func TestLstatSeesALinkAsALinkAndAFileAsAFile(t *testing.T) {
	base := t.TempDir()
	ok(t, os.WriteFile(filepath.Join(base, "key"), []byte("k"), 0o600))
	ok(t, os.Symlink(filepath.Join(base, "absent"), filepath.Join(base, "dangling")))
	ok(t, os.Mkdir(filepath.Join(base, "dir"), 0o700))
	d := heldDir(t, base)
	for name, want := range map[string]func(fs.FileMode) bool{
		"key":      fs.FileMode.IsRegular,
		"dangling": func(m fs.FileMode) bool { return m&fs.ModeSymlink != 0 },
		"dir":      fs.FileMode.IsDir,
	} {
		info, err := d.Lstat(name)
		if err != nil || !want(info.Mode()) {
			t.Errorf("%s: %v, %v", name, info, err)
		}
	}
	if _, err := d.Lstat("none"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing name: %v", err)
	}
}

func TestRenameRemoveAndSyncWorkRelativeToTheHeldDirectory(t *testing.T) {
	base := t.TempDir()
	ok(t, os.WriteFile(filepath.Join(base, "a"), []byte("1"), 0o600))
	ok(t, os.WriteFile(filepath.Join(base, "b"), []byte("2"), 0o600))
	d := heldDir(t, base)
	ok(t, d.Rename("a", "c"))
	ok(t, d.Remove("b"))
	ok(t, d.Sync())
	entries, err := os.ReadDir(base)
	ok(t, err)
	if len(entries) != 1 || entries[0].Name() != "c" {
		t.Fatalf("entries %v", entries)
	}
	if err := d.Remove("b"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("removing what is not there: %v", err)
	}
}

// The directory held stays the one that was opened when its name is
// swapped for a link.
func TestAHeldDirectoryStaysTheOneOpenedWhenItsNameIsSwappedForALink(t *testing.T) {
	base := t.TempDir()
	dir, outside := filepath.Join(base, "ssh"), filepath.Join(base, "outside")
	ok(t, os.Mkdir(dir, 0o700))
	ok(t, os.Mkdir(outside, 0o700))
	d := heldDir(t, dir)
	ok(t, os.Rename(dir, filepath.Join(base, "moved")))
	ok(t, os.Symlink(outside, dir))
	f, err := d.CreateFile("first", 0o600)
	ok(t, err)
	ok(t, f.Close())
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("a file was created outside: %v", entries)
	}
	if _, err := os.Lstat(filepath.Join(base, "moved", "first")); err != nil {
		t.Fatalf("the file is not in the directory held: %v", err)
	}
}
