// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

func TestSameFileTellsOneFileFromAnotherWithTheSameContent(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, "link")
	if err := os.Link(a, link); err != nil {
		t.Fatal(err)
	}
	info := func(p string) os.FileInfo {
		i, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		return i
	}
	if !hostsetup.SameFile(info(a), info(link)) || hostsetup.SameFile(info(a), info(b)) {
		t.Error("same file wrong")
	}
}

// described is a file info that reports another Sys() than its own.
type described struct {
	os.FileInfo
	sys any
}

func (d described) Sys() any { return d.sys }

func TestSameFileNeedsTheSameDeviceAndTheSameInode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	real, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	base := *real.Sys().(*syscall.Stat_t)
	with := func(change func(*syscall.Stat_t)) os.FileInfo {
		st := base
		change(&st)
		return described{real, &st}
	}
	same := with(func(*syscall.Stat_t) {})
	if !hostsetup.SameFile(same, with(func(*syscall.Stat_t) {})) {
		t.Error("one device and one inode are one file")
	}
	if hostsetup.SameFile(same, with(func(st *syscall.Stat_t) { st.Dev++ })) {
		t.Error("the same inode on another device is another file")
	}
	if hostsetup.SameFile(same, with(func(st *syscall.Stat_t) { st.Ino++ })) {
		t.Error("another inode is another file")
	}
}

func TestSameFileOfDescriptionsThatAreNotTheKernelsIsNotAPanic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	real, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	fake := described{real, nil}
	if hostsetup.SameFile(fake, real) || hostsetup.SameFile(real, fake) || hostsetup.SameFile(fake, fake) {
		t.Error("a description that is not the kernel's is never judged to be one file")
	}
}
