// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package refusal_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// The directory of a lock file belongs to the service user, the command
// that takes the lock may be root: whatever the service user plants there,
// root must not write through it.
func TestLockNeverWritesThroughALink(t *testing.T) {
	cases := map[string]func(t *testing.T, dir, lock, victim string){
		"a symbolic link to a file": func(t *testing.T, _, lock, victim string) { ok(t, os.Symlink(victim, lock)) },
		"a dangling symbolic link": func(t *testing.T, dir, lock, _ string) {
			ok(t, os.Symlink(filepath.Join(dir, "created-by-root"), lock))
		},
		"a hard link": func(t *testing.T, _, lock, victim string) { ok(t, os.Link(victim, lock)) },
	}
	for name, plant := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			victim, lock := filepath.Join(dir, "victim"), filepath.Join(dir, ".sard.lock")
			ok(t, os.WriteFile(victim, []byte("root:x:0:0\n"), 0o600))
			plant(t, dir, lock, victim)
			unlock, err := refusal.Lock(os.OpenFile, lock)
			if err == nil {
				unlock()
				t.Fatal("the lock was taken through the link")
			}
			if data, _ := os.ReadFile(victim); string(data) != "root:x:0:0\n" {
				t.Fatalf("the victim was written: %q", data)
			}
			if _, err := os.Lstat(filepath.Join(dir, "created-by-root")); err == nil {
				t.Fatal("the target of the dangling link was created")
			}
		})
	}
}

func TestLockOfAPlainFileStillWorks(t *testing.T) {
	lock := filepath.Join(t.TempDir(), ".sard.lock")
	ok(t, os.WriteFile(lock, []byte("123\n"), 0o600))
	unlock, err := refusal.Lock(os.OpenFile, lock)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}

func openFDs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	ok(t, err)
	return len(entries)
}

func TestLockCreatesTheFileAndNamesItsHolder(t *testing.T) {
	lock := filepath.Join(t.TempDir(), ".sard.lock")
	unlock, err := refusal.Lock(os.OpenFile, lock)
	ok(t, err)
	defer unlock()
	data, err := os.ReadFile(lock)
	ok(t, err)
	if string(data) != strconv.Itoa(os.Getpid())+"\n" {
		t.Fatalf("lock file = %q", data)
	}
	if info, err := os.Stat(lock); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("lock mode = %v, %v", info, err)
	}
}

func TestLockHeldByAnotherIsRefusedAndLeavesTheHoldersFile(t *testing.T) {
	lock := filepath.Join(t.TempDir(), ".sard.lock")
	unlock, err := refusal.Lock(os.OpenFile, lock)
	ok(t, err)
	defer unlock()
	before := openFDs(t)
	second, err := refusal.Lock(os.OpenFile, lock)
	if second != nil || !errors.Is(err, refusal.ErrLockHeld) {
		t.Fatalf("second lock taken = %v, %v", second != nil, err)
	}
	if openFDs(t) != before {
		t.Fatal("the refused lock left its file open")
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("the holder's lock file is gone: %v", err)
	}
}

func TestUnlockRemovesTheFileClosesItAndLetsTheNextHolderIn(t *testing.T) {
	lock := filepath.Join(t.TempDir(), ".sard.lock")
	before := openFDs(t)
	unlock, err := refusal.Lock(os.OpenFile, lock)
	ok(t, err)
	unlock()
	if _, err := os.Lstat(lock); !os.IsNotExist(err) {
		t.Fatalf("the lock file stays after unlock: %v", err)
	}
	if openFDs(t) != before {
		t.Fatal("unlock left the file open")
	}
	again, err := refusal.Lock(os.OpenFile, lock)
	ok(t, err)
	again()
}

func TestLockOpenErrorIsReturnedAsIs(t *testing.T) {
	boom := errors.New("no write access")
	open := func(string, int, os.FileMode) (*os.File, error) { return nil, boom }
	if unlock, err := refusal.Lock(open, "x"); unlock != nil || !errors.Is(err, boom) {
		t.Fatalf("Lock taken = %v, %v", unlock != nil, err)
	}
	if f, err := refusal.OpenLockFile(open, "x"); f != nil || !errors.Is(err, boom) {
		t.Fatalf("OpenLockFile = %v, %v", f, err)
	}
}

// A holder that released between our open and our flock removed the file
// we hold: the lock on it means nothing.
func TestLockOnAFileRemovedUnderneathIsRefused(t *testing.T) {
	lock := filepath.Join(t.TempDir(), ".sard.lock")
	open := func(name string, flag int, perm os.FileMode) (*os.File, error) {
		f, err := os.OpenFile(name, flag, perm)
		ok(t, err)
		ok(t, os.Remove(name))
		return f, nil
	}
	before := openFDs(t)
	unlock, err := refusal.Lock(open, lock)
	if unlock != nil || !errors.Is(err, refusal.ErrLockHeld) {
		t.Fatalf("Lock taken = %v, %v", unlock != nil, err)
	}
	if openFDs(t) != before {
		t.Fatal("the refused lock left its file open")
	}
}

func TestOpenLockFileRefusesAFileThatIsNotRegularAndClosesIt(t *testing.T) {
	open := func(string, int, os.FileMode) (*os.File, error) { return os.OpenFile(os.DevNull, os.O_RDWR, 0) }
	before := openFDs(t)
	f, err := refusal.OpenLockFile(open, "x")
	if f != nil || !errors.Is(err, refusal.ErrUnsafeLockFile) {
		t.Fatalf("OpenLockFile = %v, %v", f, err)
	}
	if openFDs(t) != before {
		t.Fatal("the refused file was left open")
	}
}

func TestCheckLockFileOfAClosedFileIsTheStatError(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "x"))
	ok(t, err)
	ok(t, f.Close())
	if err := refusal.CheckLockFile(f); err == nil || errors.Is(err, refusal.ErrUnsafeLockFile) {
		t.Fatalf("err = %v", err)
	}
	if refusal.SameFileAtPath(f, f.Name()) {
		t.Fatal("a closed file is the file at its path")
	}
}

func TestSameFileAtPathIsTheFileItselfNotAReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x")
	f, err := os.Create(path)
	ok(t, err)
	defer func() { _ = f.Close() }()
	if !refusal.SameFileAtPath(f, path) {
		t.Fatal("the file is not itself")
	}
	ok(t, os.Remove(path))
	if refusal.SameFileAtPath(f, path) {
		t.Fatal("a removed file is still at its path")
	}
	ok(t, os.WriteFile(path, nil, 0o600))
	if refusal.SameFileAtPath(f, path) {
		t.Fatal("a replacement is the held file")
	}
}

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
