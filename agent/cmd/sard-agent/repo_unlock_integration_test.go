// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build integration

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
)

// Integration tests of repo check and repo unlock with the pinned restic and
// a local repository: the scenarios tagged @integration of host-check.feature
// (A8d-1). The locks are the real ones of a restic backup that is held on
// its standard input; the host name and the clock of the agent are chosen by
// the test, the lock of another host would need another name of the machine.

// heldLock is a restic backup that holds its lock until it is killed.
type heldLock struct {
	cmd *exec.Cmd
}

func (h *setupHost) holdLock() *heldLock {
	h.t.Helper()
	cmd := exec.Command(pinnedRestic(h.t), "backup", "--no-cache", "--stdin", "--stdin-filename", "held")
	cmd.Env = append(os.Environ(), "RESTIC_REPOSITORY="+h.path("base"), "RESTIC_PASSWORD_FILE="+h.path("secrets/base.pass"))
	stdin, err := cmd.StdinPipe()
	ok(h.t, err)
	ok(h.t, cmd.Start())
	held := &heldLock{cmd: cmd}
	h.t.Cleanup(func() { _ = stdin.Close(); held.kill() })
	for range 1000 {
		if h.lockFiles() > 0 {
			return held
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatal("restic took no lock")
	return nil
}

// kill ends the process the hard way: its lock stays in the repository.
func (l *heldLock) kill() {
	_ = l.cmd.Process.Kill()
	_ = l.cmd.Wait()
}

// lockFiles counts the lock files of the repository base: the entries named
// by the id of a lock, not the temporary file restic writes first.
func (h *setupHost) lockFiles() int {
	entries, err := os.ReadDir(h.path("base/locks"))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if lockName.MatchString(e.Name()) {
			n++
		}
	}
	return n
}

var lockName = regexp.MustCompile(`^[0-9a-f]{64}$`)

// announceBase is what the service would have recorded for base.
func (h *setupHost) announceBase() {
	h.t.Helper()
	_, stdout, _ := h.sudo("repo", "show", "--config", "C", "--json", "base")
	var card struct {
		RepositoryID string `json:"repository_id"`
	}
	ok(h.t, json.Unmarshal([]byte(stdout), &card))
	h.announce(hostcheck.AnnouncedRepository{Name: "base", RepositoryID: card.RepositoryID})
}

func (h *setupHost) thirtyHoursLater() {
	h.clock.now = time.Now().Add(30 * time.Hour)
}

func TestIntegrationRealResticRemovesTheOwnStaleLockOfADeadProcess(t *testing.T) {
	h := realRestoreHost(t)
	h.holdLock().kill()
	h.thirtyHoursLater()
	if h.lockFiles() != 1 {
		t.Fatalf("%d lock files before", h.lockFiles())
	}
	code, stdout, stderr := h.sudo("repo", "unlock", "--config", "C", "base", "--remove-stale")
	assertCode(t, code, exitOK)
	if h.lockFiles() != 0 || !strings.Contains(stdout, "removed 1 stale lock") {
		t.Errorf("%d lock files, stdout %q, stderr %q", h.lockFiles(), stdout, stderr)
	}
}

func TestIntegrationRealResticKeepsTheLockOfAnotherHost(t *testing.T) {
	h := realRestoreHost(t)
	h.holdLock().kill()
	h.thirtyHoursLater()
	h.deps.hostname = func() (string, error) { return "db2-elsewhere", nil }
	code, stdout, stderr := h.sudo("repo", "unlock", "--config", "C", "base", "--remove-stale")
	assertCode(t, code, exitOK)
	// the lock was made by this machine; for the agent that is another host
	if h.lockFiles() != 1 || !strings.Contains(stdout, "no stale locks of this host") {
		t.Errorf("%d lock files, stdout %q, stderr %q", h.lockFiles(), stdout, stderr)
	}
}

func TestIntegrationALockOfARunningProcessStaysAndIsNoWarning(t *testing.T) {
	h := realRestoreHost(t)
	h.holdLock()
	h.clock.now = time.Now()
	code, stdout, _ := h.sudo("repo", "unlock", "--config", "C", "base", "--remove-stale")
	assertCode(t, code, exitOK)
	if h.lockFiles() != 1 || !strings.Contains(stdout, "no stale locks of this host") {
		t.Errorf("%d lock files, stdout %q", h.lockFiles(), stdout)
	}
	h.announceBase()
	code, stdout, _ = h.sudo("repo", "check", "--config", "C", "base")
	assertCode(t, code, exitOK)
	host, _ := os.Hostname()
	lines, _ := parseOut(t, stdout)
	if l := line(t, lines, hostcheck.CheckRepositoryLocks, "base"); l.status != "OK" || !strings.Contains(l.message, "1 lock") || !strings.Contains(l.message, host) {
		t.Errorf("locks: %+v", l)
	}
}

func TestIntegrationRealResticChecksAPartOfTheData(t *testing.T) {
	h := realRestoreHost(t)
	src := h.path("src")
	h.write(src+"/a.txt", "AAA", 0o640)
	h.backupBase(src)
	h.announceBase()
	h.clock.now = time.Now()
	code, stdout, stderr := h.sudo("repo", "check", "--config", "C", "base", "--read-data", "100")
	assertCode(t, code, exitOK)
	lines, _ := parseOut(t, stdout)
	if l := line(t, lines, hostcheck.CheckRepositoryIntegrity, "base"); l.status != "OK" {
		t.Errorf("integrity: %+v (stderr %q)", l, stderr)
	}
}

func TestIntegrationACheckOfDataIsRefusedWhileABackupHoldsTheRepository(t *testing.T) {
	h := realRestoreHost(t)
	h.holdLock()
	h.announceBase()
	h.clock.now = time.Now()
	code, stdout, _ := h.sudo("repo", "check", "--config", "C", "base", "--read-data", "100")
	assertCode(t, code, exitWarnings)
	lines, _ := parseOut(t, stdout)
	if l := line(t, lines, hostcheck.CheckRepositoryIntegrity, "base"); l.status != "WARN" || !strings.Contains(l.message, "locked by another process") {
		t.Errorf("integrity: %+v", l)
	}
}

func TestIntegrationRealResticStaleLockOfADeadProcessIsWarnedAboutOnlyAfter24Hours(t *testing.T) {
	h := realRestoreHost(t)
	h.holdLock().kill()
	h.clock.now = time.Now()
	_, stdout, _ := h.sudo("repo", "check", "--config", "C", "base")
	lines, _ := parseOut(t, stdout)
	if l := line(t, lines, hostcheck.CheckRepositoryLocks, "base"); l.status != "OK" {
		t.Errorf("fresh: %+v", l)
	}
	h.thirtyHoursLater()
	code, stdout, _ := h.sudo("repo", "check", "--config", "C", "base")
	assertCode(t, code, exitWarnings)
	lines, _ = parseOut(t, stdout)
	if l := line(t, lines, hostcheck.CheckRepositoryLocks, "base"); l.status != "WARN" || !strings.Contains(l.hint, "repo unlock base --remove-stale") {
		t.Errorf("stale: %+v", l)
	}
}
