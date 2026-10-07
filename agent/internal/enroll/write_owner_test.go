// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/enroll"
)

// chownLog records the owner changes of an enrollment instead of making them.
type chownLog struct {
	mu      sync.Mutex
	changed map[string][2]int
	calls   int
	failAt  int // the owner change with this number (1 is the key, 2 the certificate, 3 the CA bundle) fails
}

func (c *chownLog) chown(path string, uid, gid int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.calls == c.failAt {
		return errors.New("operation not permitted")
	}
	if c.changed == nil {
		c.changed = map[string][2]int{}
	}
	c.changed[path] = [2]int{uid, gid}
	return nil
}

func (c *chownLog) owner() *enroll.Owner {
	return &enroll.Owner{UID: 990, GID: 991, Chown: c.chown}
}

func filesIn(dir string) enroll.Files {
	return enroll.Files{KeyFile: filepath.Join(dir, "agent.key"), CertFile: filepath.Join(dir, "agent.pem"), CAFile: filepath.Join(dir, "ca.pem")}
}

// Р25: the temporary file is the service user's before it is renamed, so
// no target path ever belongs to root.
func TestEachTemporaryFileIsOwnedBeforeItIsRenamed(t *testing.T) {
	dir := t.TempDir()
	log := &chownLog{}
	var renamed []string
	restore := enroll.SetRenameForTest(func(oldpath, newpath string) error {
		log.mu.Lock()
		got, ok := log.changed[oldpath]
		log.mu.Unlock()
		if !ok || got != [2]int{990, 991} {
			t.Errorf("%s was renamed to %s before its owner was changed", oldpath, newpath)
		}
		renamed = append(renamed, newpath)
		return os.Rename(oldpath, newpath)
	})
	defer restore()
	files := filesIn(dir)
	if err := enroll.WriteIdentityAs(files, []byte("k"), []byte("c"), []byte("a"), log.owner()); err != nil {
		t.Fatal(err)
	}
	if len(renamed) != 3 {
		t.Fatalf("renames %v", renamed)
	}
}

func TestOwnedIdentityHasItsModesWhateverTheUmask(t *testing.T) {
	dir := t.TempDir()
	files := filesIn(dir)
	if err := enroll.WriteIdentityAs(files, []byte("k"), []byte("c"), []byte("a"), (&chownLog{}).owner()); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{files.KeyFile: 0o600, files.CertFile: 0o644, files.CAFile: 0o644} {
		if info, _ := os.Stat(path); info.Mode().Perm() != want {
			t.Errorf("%s: mode %v, want %v", path, info.Mode().Perm(), want)
		}
	}
}

func TestAFailedOwnerChangeLeavesNoFileAndNoTemporaryFile(t *testing.T) {
	for failAt, victim := range []string{"agent.key", "agent.pem", "ca.pem"} {
		t.Run(victim, func(t *testing.T) {
			dir := t.TempDir()
			log := &chownLog{failAt: failAt + 1}
			err := enroll.WriteIdentityAs(filesIn(dir), []byte("k"), []byte("c"), []byte("a"), log.owner())
			var eerr *enroll.Error
			if !errors.As(err, &eerr) || eerr.Class != enroll.ClassWrite {
				t.Fatalf("err = %v", err)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Fatalf("left behind: %v", entries)
			}
		})
	}
}

func TestAFailedOwnerChangeWithForceKeepsThePreviousIdentity(t *testing.T) {
	dir := t.TempDir()
	files := filesIn(dir)
	old := map[string]string{files.KeyFile: "old-k", files.CertFile: "old-c", files.CAFile: "old-a"}
	for path, content := range old {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	err := enroll.WriteIdentityAs(files, []byte("k"), []byte("c"), []byte("a"), (&chownLog{failAt: 1}).owner())
	if err == nil {
		t.Fatal("no error")
	}
	for path, content := range old {
		if got, _ := os.ReadFile(path); string(got) != content {
			t.Errorf("%s changed to %q", path, got)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 3 {
		t.Fatalf("directory holds %v", entries)
	}
}

func TestWithoutAnOwnerNothingIsChanged(t *testing.T) {
	dir := t.TempDir()
	if err := enroll.WriteIdentityAs(filesIn(dir), []byte("k"), []byte("c"), []byte("a"), nil); err != nil {
		t.Fatal(err)
	}
}
