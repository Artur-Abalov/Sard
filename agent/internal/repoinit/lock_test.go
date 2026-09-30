// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

func TestTheLockOfARepositoryLivesNextToItsPasswordFile(t *testing.T) {
	a := repoinit.LockPath("/etc/sard/main.pass", "main")
	b := repoinit.LockPath("/etc/sard/main.pass", "offsite")
	c := repoinit.LockPath("/etc/sard/main.pass", "../evil/x")
	if filepath.Dir(a) != "/etc/sard" || a == b || filepath.Dir(c) != "/etc/sard" {
		t.Fatalf("paths = %q %q %q", a, b, c)
	}
}

func TestASecondLockOfTheSameRepositoryIsRefusedUntilTheFirstIsReleased(t *testing.T) {
	path := repoinit.LockPath(filepath.Join(t.TempDir(), "main.pass"), "main")
	unlock, err := repoinit.Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repoinit.Lock(path); !errors.Is(err, repoinit.ErrLockHeld) {
		t.Fatalf("second lock: %v", err)
	}
	unlock()
	if _, err := os.Stat(path); err == nil {
		t.Fatal("the lock file is left behind")
	}
	again, err := repoinit.Lock(path)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	again()
}

func TestAStaleLockFileOfADeadProcessDoesNotBlock(t *testing.T) {
	path := repoinit.LockPath(filepath.Join(t.TempDir(), "main.pass"), "main")
	if err := os.WriteFile(path, []byte("4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unlock, err := repoinit.Lock(path)
	if err != nil {
		t.Fatalf("lock over a stale file: %v", err)
	}
	unlock()
}

func TestALockInAMissingDirectoryFailsWithoutCreatingIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	_, err := repoinit.Lock(repoinit.LockPath(filepath.Join(dir, "main.pass"), "main"))
	if err == nil || errors.Is(err, repoinit.ErrLockHeld) {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(dir); statErr == nil {
		t.Fatal("the directory was created")
	}
}
