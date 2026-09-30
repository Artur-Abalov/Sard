// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit_test

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

// recordingOpen is os.OpenFile that remembers the files it opened and lets
// a test act between the open and the lock check.
type recordingOpen struct {
	opened  []*os.File
	removed bool
}

func (r *recordingOpen) open(name string, flag int, perm os.FileMode) (*os.File, error) {
	f, err := os.OpenFile(name, flag, perm)
	if err == nil {
		r.opened = append(r.opened, f)
		if r.removed {
			_ = os.Remove(name)
		}
	}
	return f, err
}

func closed(f *os.File) bool {
	_, err := f.Stat()
	return errors.Is(err, os.ErrClosed)
}

func TestTheLockFileHoldsThePIDOfTheHolder(t *testing.T) {
	path := repoinit.LockPath(t.TempDir(), "main")
	unlock, err := repoinit.Lock(os.OpenFile, path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	data, err := os.ReadFile(path)
	if want := strconv.Itoa(os.Getpid()) + "\n"; err != nil || string(data) != want {
		t.Fatalf("%q, %v, want %q", data, err, want)
	}
}

func TestReleasingTheLockClosesItsFile(t *testing.T) {
	var rec recordingOpen
	unlock, err := repoinit.Lock(rec.open, repoinit.LockPath(t.TempDir(), "main"))
	if err != nil {
		t.Fatal(err)
	}
	if closed(rec.opened[0]) {
		t.Fatal("the file is closed while the lock is held")
	}
	unlock()
	if !closed(rec.opened[0]) {
		t.Fatal("the file stays open after release")
	}
}

func TestARefusedLockLeavesNoFileOpen(t *testing.T) {
	path := repoinit.LockPath(t.TempDir(), "main")
	unlock, err := repoinit.Lock(os.OpenFile, path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	var rec recordingOpen
	if _, err := repoinit.Lock(rec.open, path); !errors.Is(err, repoinit.ErrLockHeld) {
		t.Fatalf("err = %v", err)
	}
	if !closed(rec.opened[0]) {
		t.Fatal("the refused lock left its file open")
	}
}

func TestALockOnAFileThatWasRemovedInTheMeantimeIsRefused(t *testing.T) {
	rec := recordingOpen{removed: true}
	path := repoinit.LockPath(t.TempDir(), "main")
	unlock, err := repoinit.Lock(rec.open, path)
	if !errors.Is(err, repoinit.ErrLockHeld) || unlock != nil {
		t.Fatalf("err = %v", err)
	}
	if !closed(rec.opened[0]) {
		t.Fatal("the refused lock left its file open")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatal(fmt.Sprint("a file is left at ", path))
	}
}
