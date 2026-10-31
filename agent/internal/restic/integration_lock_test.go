// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build integration

package restic_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// OQ-156, scenario "Бэкап ждёт исключительную блокировку вместо мгновенного
// провала" (docs/specs/agent/retention-forget.feature): a restic check that
// holds the exclusive lock is stopped, and released after a few seconds.
func TestIntegrationABackupWaitsForAnExclusiveLockInsteadOfFailingAtOnce(t *testing.T) {
	ctx := context.Background()
	r := newRealRepo(t)
	if _, err := r.cli.Init(ctx); err != nil {
		t.Fatal(err)
	}
	writeData(t, r.data, 16)
	if _, err := r.cli.Backup(ctx, restic.BackupRequest{Paths: []string{r.data}}, nil); err != nil {
		t.Fatal(err)
	}
	check := exec.Command(pinnedBinary(t), "check", "--read-data", "--limit-download", "4096", "-q")
	env := []string{"RESTIC_REPOSITORY=" + r.dir, "RESTIC_PASSWORD_FILE=" + filepath.Join(filepath.Dir(r.dir), "password"), "RESTIC_CACHE_DIR=" + filepath.Join(filepath.Dir(r.dir), "cache"), "PATH=" + os.Getenv("PATH")}
	check.Env = env
	if err := check.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = check.Process.Kill(); _ = check.Wait() })
	// Wait until restic lists the lock: the file alone may be half written.
	for deadline := time.Now().Add(time.Minute); ; {
		list := exec.Command(pinnedBinary(t), "list", "locks", "--no-lock")
		list.Env = env
		out, err := list.Output()
		if err == nil && len(strings.Fields(string(out))) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no lock appeared")
		}
	}
	if err := check.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	released := time.AfterFunc(6*time.Second, func() { _ = check.Process.Signal(syscall.SIGCONT) })
	defer released.Stop()
	start := time.Now()
	if _, err := r.cli.Backup(ctx, restic.BackupRequest{Paths: []string{r.data}}, nil); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if took := time.Since(start); took < 5*time.Second {
		t.Errorf("the backup took %v: it did not wait for the lock", took)
	}
}
