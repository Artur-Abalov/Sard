// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package verifier_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/verifier"
)

func statfs(flags int64, avail uint64, bsize int64, err error) func(string, *syscall.Statfs_t) error {
	return func(_ string, st *syscall.Statfs_t) error {
		st.Flags, st.Bavail, st.Bsize = flags, avail, bsize
		return err
	}
}

func TestTheVolumeIsWhatStatfsSaysOfIt(t *testing.T) {
	const noexec = 0x8 // ST_NOEXEC
	for _, c := range []struct {
		flags int64
		want  verifier.Volume
	}{{noexec | 0x1, verifier.Volume{Free: 4096 * 10, NoExec: true}}, {0x1, verifier.Volume{Free: 4096 * 10}}} {
		d := verifier.OSDisk{Statfs: statfs(c.flags, 10, 4096, nil)}
		if got, err := d.Volume("/w"); err != nil || got != c.want {
			t.Errorf("flags %#x: %+v, %v", c.flags, got, err)
		}
	}
	d := verifier.OSDisk{Statfs: statfs(0, 0, 0, syscall.ENOENT)}
	if _, err := d.Volume("/w"); !errors.Is(err, syscall.ENOENT) {
		t.Fatalf("err = %v", err)
	}
}

func TestADirectoryOfAJobIsNewAndPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "job")
	d := verifier.OSDisk{}
	if err := d.MakeDir(dir); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("mode %v, %v", info.Mode(), err)
	}
	if err := d.MakeDir(dir); !errors.Is(err, os.ErrExist) {
		t.Fatalf("an existing directory was reused: %v", err)
	}
}

// restic restores the modes of the snapshot: a directory without write
// permission must not keep the copy on the disk.
func TestACopyWithReadOnlyDirectoriesIsRemoved(t *testing.T) {
	work := t.TempDir()
	job := filepath.Join(work, "job")
	inner := filepath.Join(job, "copy", "etc")
	if err := os.MkdirAll(inner, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, "f"), []byte("data"), 0o400); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{inner, filepath.Dir(inner)} {
		if err := os.Chmod(p, 0o500); err != nil {
			t.Fatal(err)
		}
	}
	d := verifier.OSDisk{}

	if err := d.RemoveAll(job); err != nil {
		t.Fatal(err)
	}

	if names, err := d.Entries(work); err != nil || len(names) != 0 {
		t.Fatalf("left %q, %v", names, err)
	}
}

func TestTheEntriesOfAMissingWorkDirectoryAreNone(t *testing.T) {
	d := verifier.OSDisk{}
	if names, err := d.Entries(filepath.Join(t.TempDir(), "none")); err != nil || names != nil {
		t.Fatalf("%q, %v", names, err)
	}
	work := t.TempDir()
	for _, n := range []string{"b", "a"} {
		if err := os.Mkdir(filepath.Join(work, n), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if names, err := d.Entries(work); err != nil || !slices.Equal(names, []string{"a", "b"}) {
		t.Fatalf("%q, %v", names, err)
	}
}

func TestAWorkDirectoryThatCannotBeReadIsAnError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (verifier.OSDisk{}).Entries(file); err == nil {
		t.Fatal("a file listed as a directory")
	}
}
