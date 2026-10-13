// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build integration

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// Integration tests of repo restore with the pinned restic and a local
// repository: the scenarios tagged @integration of repo-restore.feature.

// restoreExecutor runs the real restic as the test process: a restore is
// asked to run as root (no user to change to), every other call as the
// service user, which a test cannot become.
type restoreExecutor struct {
	restic.ProcessExecutor
	t *testing.T
}

func (e restoreExecutor) Run(ctx context.Context, cmd restic.Command) (int, error) {
	asService := cmd.RunAs != nil && *cmd.RunAs == restic.RunAs{UID: serviceUID, GID: serviceUID}
	if isRestore := cmd.Args[0] == "restore"; isRestore == asService {
		e.t.Errorf("restic %v ran as %+v", cmd.Args, cmd.RunAs)
	}
	cmd.RunAs = nil
	return e.ProcessExecutor.Run(ctx, cmd)
}

// realRestoreHost is the host of repo restore with the real restic and the
// initialised repository base.
func realRestoreHost(t *testing.T) *setupHost {
	t.Helper()
	h := newSetupHost(t)
	h.cfg.Restic.Path = pinnedRestic(t)
	h.saveConfig()
	h.deps.exec = restoreExecutor{t: t}
	h.deps.pathEnv = os.Getenv("PATH")
	assertCode(t, first(h.sudo("repo", "init", "--config", "C", "base")), exitOK)
	return h
}

// backupBase makes a snapshot of dir in the repository base with the real restic.
func (h *setupHost) backupBase(dir string, tags ...string) {
	h.t.Helper()
	args := []string{"backup", "--no-cache", dir}
	for _, tag := range tags {
		args = append(args, "--tag", tag)
	}
	cmd := exec.Command(pinnedRestic(h.t), args...)
	cmd.Env = append(os.Environ(), "RESTIC_REPOSITORY="+h.path("base"), "RESTIC_PASSWORD_FILE="+h.path("secrets/base.pass"))
	if out, err := cmd.CombinedOutput(); err != nil {
		h.t.Fatalf("restic backup: %v\n%s", err, out)
	}
}

// fullID is the id of the only snapshot, read through the command.
func (h *setupHost) onlySnapshotID() string {
	h.t.Helper()
	_, stdout, _ := h.sudo("repo", "snapshots", "--config", "C", "--json", "base")
	id := hexID.FindString(stdout)
	if id == "" {
		h.t.Fatalf("no snapshot in %q", stdout)
	}
	return id
}

func sameFile(t *testing.T, want, got string) {
	t.Helper()
	wantData, err := os.ReadFile(want)
	ok(t, err)
	gotData, err := os.ReadFile(got)
	if err != nil || string(gotData) != string(wantData) {
		t.Fatalf("%s = %q (%v), want %q", got, gotData, err, wantData)
	}
	wantInfo, _ := os.Stat(want)
	gotInfo, _ := os.Stat(got)
	if gotInfo.Mode() != wantInfo.Mode() || !gotInfo.ModTime().Equal(wantInfo.ModTime()) {
		t.Errorf("%s: mode %v and time %v, want %v and %v", got, gotInfo.Mode(), gotInfo.ModTime(), wantInfo.Mode(), wantInfo.ModTime())
	}
}

// Файлы снимка ложатся под исходным путём внутри цели байт в байт
func TestIntegrationTheFilesOfASnapshotLieUnderTheirOriginalPathsInTheTarget(t *testing.T) {
	h := realRestoreHost(t)
	src := h.path("src")
	h.write(src+"/a.txt", "AAA", 0o640)
	h.write(src+"/sub/b.txt", "BBB", 0o600)
	ok(t, os.Chtimes(src+"/a.txt", time.Unix(1_700_000_000, 0), time.Unix(1_700_000_000, 0)))
	h.backupBase(src)
	code, stdout, stderr := h.restore(h.onlySnapshotID())
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Errorf("stderr %q", stderr)
	}
	sameFile(t, src+"/a.txt", filepath.Join(h.target(), src, "a.txt"))
	sameFile(t, src+"/sub/b.txt", filepath.Join(h.target(), src, "sub/b.txt"))
	if !hexID.MatchString(stdout) {
		t.Errorf("stdout %q", stdout)
	}
}

// Несколько path восстанавливают только эти пути
func TestIntegrationSeveralPathsRestoreOnlyThosePaths(t *testing.T) {
	h := realRestoreHost(t)
	src := h.path("src")
	h.write(src+"/a.txt", "A", 0o644)
	h.write(src+"/b.txt", "B", 0o644)
	h.write(src+"/sub/c.txt", "C", 0o644)
	h.backupBase(src)
	code, _, stderr := h.restore(h.onlySnapshotID(), "--path", src+"/a.txt", "--path", src+"/sub")
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Errorf("stderr %q", stderr)
	}
	sameFile(t, src+"/a.txt", filepath.Join(h.target(), src, "a.txt"))
	sameFile(t, src+"/sub/c.txt", filepath.Join(h.target(), src, "sub/c.txt"))
	h.assertAbsent(filepath.Join(h.target(), src, "b.txt"))
}

// A path that is not in the snapshot is found by the real restic before anything is written.
func TestIntegrationAPathOfNoFileIsRefusedBeforeTheTargetIsMade(t *testing.T) {
	h := realRestoreHost(t)
	src := h.path("src")
	h.write(src+"/a.txt", "A", 0o644)
	h.backupBase(src)
	code, _, stderr := h.restore(h.onlySnapshotID(), "--path", src+"/nothing")
	assertRefusal(t, code, stderr, exitUsage, "PATH_NOT_IN_SNAPSHOT")
	h.assertAbsent(h.target())
}

// На новом хосте репозиторий, подключённый с паролем из копии, восстанавливается
func TestIntegrationARepositoryAttachedWithThePasswordOfACopyIsRestoredWithoutRegistration(t *testing.T) {
	h := realRestoreHost(t)
	src := h.path("src")
	h.write(src+"/a.txt", "AAA", 0o644)
	h.backupBase(src, "sard.run=r1", "sard.source=s1")
	copyOfPassword := h.path("outside/copy.pass")
	h.write(copyOfPassword, passMarker+"\n", 0o600)
	// the new host: no repositories, no tls files, a server that is not there
	h.cfg.Repositories = nil
	h.cfg.Server.Address = "127.0.0.1:1"
	h.saveConfig()
	code, _, stderr := h.sudo("repo", "add", "main", h.path("base"), "--password-from-file", copyOfPassword, "--no-restart", "--config", "C")
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Errorf("stderr %q", stderr)
	}
	code, _, stderr = h.sudo("repo", "restore", "main", "latest", "--source", "s1", "--target", h.target(), "--config", "C")
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Errorf("stderr %q", stderr)
	}
	sameFile(t, src+"/a.txt", filepath.Join(h.target(), src, "a.txt"))
}
