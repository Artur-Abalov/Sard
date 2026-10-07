// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package refusal_test

import (
	"os"
	"path/filepath"
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

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
