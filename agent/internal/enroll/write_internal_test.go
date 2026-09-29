// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll

// White-box test of syncParent: called directly so a real os.Open failure
// (a nonexistent directory) can be told apart from the generic
// "invalid argument" a nil *os.File's own methods return — both are
// non-nil errors, so only the message distinguishes a correctly-propagated
// os.Open failure from one silently replaced by nil-receiver noise.

import (
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestSyncParentPropagatesTheOriginalOpenError(t *testing.T) {
	err := syncParent(filepath.Join(t.TempDir(), "does-not-exist", "file"))
	if err == nil {
		t.Fatal("syncParent: want an error for a missing parent directory")
	}
	if strings.Contains(err.Error(), "invalid argument") {
		t.Fatalf("error = %q, want the real os.Open failure, not a nil-receiver's generic error", err.Error())
	}
	if !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("error = %q, want it to say the directory does not exist", err.Error())
	}
}

// withFileSizeLimit lowers RLIMIT_FSIZE to 1 byte for the duration of the
// calling test, so a real os.File.Write failure is forced deterministically
// instead of depending on disk space or permissions. SIGXFSZ (which the
// kernel would otherwise raise, killing the process, when a write exceeds
// the limit) is ignored so the write instead just returns EFBIG. Both the
// rlimit and the signal disposition are restored on cleanup.
func withFileSizeLimit(t *testing.T) {
	t.Helper()
	var old syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &old); err != nil {
		t.Fatalf("Getrlimit: %v", err)
	}
	lim := syscall.Rlimit{Cur: 1, Max: old.Max}
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &lim); err != nil {
		t.Fatalf("Setrlimit: %v", err)
	}
	signal.Ignore(syscall.SIGXFSZ)
	t.Cleanup(func() {
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &old); err != nil {
			t.Fatalf("restoring RLIMIT_FSIZE: %v", err)
		}
		signal.Reset(syscall.SIGXFSZ)
	})
}

// stage must remove its temporary file when writing the data fails, not
// leave it behind next to the directory it staged into.
func TestStageRemovesItsTemporaryFileWhenWritingTheDataFails(t *testing.T) {
	withFileSizeLimit(t)
	dir := t.TempDir()
	data := []byte(strings.Repeat("x", 4096)) // well past the 1-byte limit

	name, err := stage(filepath.Join(dir, "tls.key"), data, keyMode)
	if err == nil {
		t.Fatal("stage: want an error when the write exceeds RLIMIT_FSIZE")
	}
	if name != "" {
		t.Fatalf("name = %q, want empty on failure", name)
	}
	entries, rerr := os.ReadDir(dir)
	if rerr != nil {
		t.Fatalf("ReadDir: %v", rerr)
	}
	if len(entries) != 0 {
		t.Fatalf("leftover temporary files after a failed stage: %v", entries)
	}
}
