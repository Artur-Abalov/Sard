// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package secrets_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/secrets"
)

// plainFile makes a file with the mode in a temporary directory.
func plainFile(t *testing.T, dir, name, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadOwnedReadsAPlainFileOfTheOwner(t *testing.T) {
	plain := plainFile(t, t.TempDir(), "plain", "KEY=VALUE\n", 0o600)
	if got, err := secrets.ReadOwned(plain, uint32(os.Getuid())); err != nil || string(got) != "KEY=VALUE\n" {
		t.Fatalf("plain file: %q, %v", got, err)
	}
}

func TestReadOwnedRefusesAFileOfAnotherOwnerOrWithGroupBits(t *testing.T) {
	dir, self := t.TempDir(), uint32(os.Getuid())
	if _, err := secrets.ReadOwned(plainFile(t, dir, "a", "x", 0o600), self+1); err == nil {
		t.Error("a file of another owner was read")
	}
	if _, err := secrets.ReadOwned(plainFile(t, dir, "b", "x", 0o640), self); err == nil {
		t.Error("a file with group bits was read")
	}
}

func TestReadOwnedNeverFollowsALinkAndReportsAMissingFile(t *testing.T) {
	dir, self := t.TempDir(), uint32(os.Getuid())
	target := plainFile(t, dir, "target", "x", 0o600)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.ReadOwned(link, self); err == nil {
		t.Error("a symbolic link was followed")
	}
	if _, err := secrets.ReadOwned(filepath.Join(dir, "absent"), self); !os.IsNotExist(err) {
		t.Errorf("a missing file: %v", err)
	}
}

func TestReadOwnedDoesNotHangOnAFIFO(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := secrets.ReadOwned(fifo, uint32(os.Getuid()))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a FIFO was read")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the read hangs on a FIFO")
	}
}

func TestReadOwnedReadsAtMostTheLimitPlusOne(t *testing.T) {
	big := filepath.Join(t.TempDir(), "big")
	if err := os.WriteFile(big, make([]byte, secrets.MaxOwnedSize*2), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.ReadOwned(big, uint32(os.Getuid())); err == nil {
		t.Fatal("a file over the limit was read")
	}
}
