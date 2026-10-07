// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

func newLayoutWithDir(t *testing.T) hostsetup.Layout {
	t.Helper()
	l := hostsetup.Layout{Config: filepath.Join(t.TempDir(), "agent.yaml")}
	ok(t, os.Mkdir(l.FragmentDir(), 0o750))
	return l
}

func TestSecondConfigLockIsRefusedAtOnce(t *testing.T) {
	l := newLayoutWithDir(t)
	unlock, f := hostsetup.LockConfig(os.OpenFile, l)
	if f != nil {
		t.Fatal(f)
	}
	defer unlock()
	_, f = hostsetup.LockConfig(os.OpenFile, l)
	if f == nil || f.Reason != repoinit.ConfigLocked || f.Class != repoinit.ClassTemporary || !strings.Contains(f.Detail, l.LockFile()) {
		t.Fatalf("second lock: %+v", f)
	}
}

func TestReleasingTheConfigLockRemovesItsFileAndFreesIt(t *testing.T) {
	l := newLayoutWithDir(t)
	unlock, f := hostsetup.LockConfig(os.OpenFile, l)
	if f != nil {
		t.Fatal(f)
	}
	unlock()
	if _, err := os.Stat(l.LockFile()); !os.IsNotExist(err) {
		t.Fatalf("the lock file stays: %v", err)
	}
	again, f := hostsetup.LockConfig(os.OpenFile, l)
	if f != nil {
		t.Fatalf("after release: %+v", f)
	}
	again()
}

func TestALeftoverLockFileWithoutAHolderDoesNotBlock(t *testing.T) {
	l := hostsetup.Layout{Config: filepath.Join(t.TempDir(), "agent.yaml")}
	if err := os.Mkdir(l.FragmentDir(), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.LockFile(), []byte("123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unlock, f := hostsetup.LockConfig(os.OpenFile, l)
	if f != nil {
		t.Fatalf("refusal %+v", f)
	}
	unlock()
}

func TestAConfigLockThatCannotBeCreatedIsAWriteFailure(t *testing.T) {
	l := hostsetup.Layout{Config: filepath.Join(t.TempDir(), "agent.yaml")}
	denied := func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: syscall.EACCES}
	}
	_, f := hostsetup.LockConfig(denied, l)
	if f == nil || f.Reason != repoinit.ConfigWrite || !strings.Contains(f.Detail, l.LockFile()) {
		t.Fatalf("refusal %+v", f)
	}
}
