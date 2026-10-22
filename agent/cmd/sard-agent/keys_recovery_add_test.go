// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// docs/specs/agent/restic-keys.feature, rule "repo add нового репозитория сразу выпускает ключ восстановления".

// newDir is D/new, the address of the repository the scenarios create.
func (h *keysHost) newDir() string { return h.path("new") }

// addFresh is "repo add fresh D/new" plus flags.
func (h *keysHost) addFresh(flags ...string) (int, string, string) {
	return h.sudo(append([]string{"repo", "add", "fresh", h.newDir(), "--config", "C"}, flags...)...)
}

func (h *keysHost) fresh() *fakeRepo { return h.restic.repo(h.newDir()) }

func TestANewRepositoryInTheTerminalGetsARecoveryKeyRightAfterInit(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	var fragmentThen, initsThen = true, 0
	h.fresh().keyring.onAdd = func() {
		_, err := os.Stat(h.path("agent.d/repo-fresh.yaml"))
		fragmentThen = err == nil
		initsThen = len(h.restic.callsTo(h.newDir(), "init"))
	}
	code, stdout, stderr := h.addFresh()
	assertCode(t, code, exitOK)
	added := h.fresh().lastAdded()
	if added.user != "sard:recovery" || fragmentThen || initsThen != 1 {
		t.Errorf("key added as %q; fragment written at that time %v; inits before it %d", added.user, fragmentThen, initsThen)
	}
	if strings.Count(stdout, added.password) != 1 || !strings.Contains(stdout, "this password is stored nowhere else") {
		t.Errorf("stdout:\n%s", stdout)
	}
	h.assertPasswordHidden(added.password, stdout, stderr)
}

func TestTheRecoveryKeyOfANewRepositoryIsWrittenToAFile(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	h.deps.stdoutTerminal = false
	ok(t, os.MkdirAll(h.path("out"), 0o755))
	code, stdout, _ := h.addFresh("--recovery-key-file", h.path("out/rk"))
	assertCode(t, code, exitOK)
	password := h.fresh().lastAdded().password
	if h.fileContent(h.path("out/rk")) != password+"\n" || strings.Contains(stdout, password) {
		t.Errorf("file %q, stdout:\n%s", h.fileContent(h.path("out/rk")), stdout)
	}
	h.assertOwner(h.path("out/rk"), 0)
	h.assertMode(h.path("out/rk"), 0o600)
}

func TestWithoutATerminalAndWithoutFlagsANewRepositoryIsNotCreated(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	h.deps.stdoutTerminal = false
	before := h.hostTree()
	code, _, stderr := h.addFresh()
	assertRefusal(t, code, stderr, exitUsage, "RECOVERY_KEY_OUTPUT_MISSING")
	for _, want := range []string{"--recovery-key-file", "--no-recovery-key"} {
		assertReason(t, stderr, want)
	}
	if n := len(h.restic.callsTo(h.newDir(), "init")); n != 0 {
		t.Errorf("restic init was called %d times", n)
	}
	h.assertHostUnchanged(before)
}

func TestRefusingTheRecoveryKeyWarnsAndIsRecordedInTheAudit(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	h.deps.stdoutTerminal = false
	code, _, stderr := h.addFresh("--no-recovery-key")
	assertCode(t, code, exitOK)
	h.assertNoKeyAddOf(h.fresh())
	assertReason(t, stderr, "without a recovery key, losing this host means losing the backups")
	found := false
	for _, line := range h.log.lines {
		found = found || strings.Contains(line, "repository recovery key skipped") && strings.Contains(line, "fresh") && strings.Contains(line, "alice") && strings.Contains(line, "1000")
	}
	if !found {
		t.Errorf("audit lines %q", h.log.lines)
	}
}

func TestTheRecoveryKeyFileAndTheRefusalTogetherAreAFlagError(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	code, _, _ := h.addFresh("--recovery-key-file", h.path("out/rk"), "--no-recovery-key")
	assertCode(t, code, exitUsage)
	h.assertNoResticCalls()
}

func TestAnExistingRecoveryKeyFileIsFoundBeforeInit(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	h.write(h.path("out/rk"), "mine", 0o600)
	before := h.hostTree()
	code, _, stderr := h.addFresh("--recovery-key-file", h.path("out/rk"))
	assertRefusal(t, code, stderr, exitUsage, "KEY_FILE_EXISTS")
	if n := len(h.restic.callsTo(h.newDir(), "init")); n != 0 {
		t.Errorf("restic init was called %d times", n)
	}
	h.assertHostUnchanged(before)
}

func TestAFailedRecoveryKeyAfterInitLeavesTheRepositoryConnected(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	h.fresh().keyring.fail = map[string]string{"add": "Fatal: something went wrong"}
	code, stdout, stderr := h.addFresh()
	assertRefusal(t, code, stderr, exitAgentError, "RECOVERY_KEY_FAILED")
	assertReason(t, stderr, "sudo sard-agent repo key add fresh --for recovery")
	if _, err := os.Stat(h.path("agent.d/repo-fresh.yaml")); err != nil {
		t.Errorf("the fragment is not written: %v", err)
	}
	if pw := h.fresh().lastAdded().password; pw == "" || strings.Contains(stdout, pw) {
		t.Errorf("password %q, stdout:\n%s", pw, stdout)
	}
}

func TestConnectingAnExistingRepositoryIssuesNoRecoveryKey(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	h.deps.stdoutTerminal = false
	old := h.path("old")
	ok(t, os.MkdirAll(old, 0o755))
	r := h.restic.repo(old)
	r.initialized, r.password = true, "old-password"
	h.write(h.path("P"), "old-password\n", 0o600)
	ok(t, os.MkdirAll(h.path("out"), 0o755))
	code, stdout, _ := h.sudo("repo", "add", "old", old, "--config", "C", "--password-from-file", h.path("P"), "--recovery-key-file", h.path("out/rk"))
	assertCode(t, code, exitOK)
	if len(r.keyring.adds) != 0 {
		t.Error("restic key add was called")
	}
	h.assertAbsent(h.path("out/rk"))
	if !strings.Contains(stdout, "recovery key was not issued") || !strings.Contains(stdout, "repo key add old --for recovery") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestRepeatingRepoAddIssuesNoRecoveryKey(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	code, _, _ := h.addFresh()
	assertCode(t, code, exitOK)
	h.log.lines = nil
	adds := len(h.fresh().keyring.adds)
	code, stdout, _ := h.addFresh("--no-recovery-key")
	assertCode(t, code, exitOK)
	if !strings.Contains(stdout, "unchanged") || len(h.fresh().keyring.adds) != adds || len(h.log.lines) != 0 {
		t.Errorf("stdout %q, adds %d, audit %q", stdout, len(h.fresh().keyring.adds), h.log.lines)
	}
}

// assertNoKeyAddOf is "restic key add не вызывался" for a repository.
func (h *keysHost) assertNoKeyAddOf(r *fakeRepo) {
	h.t.Helper()
	if n := len(r.keyring.adds); n != 0 {
		h.t.Fatalf("restic key add was called %d times", n)
	}
}

// The recovery key of a new repository is one access to the storage like any
// other: --connect-timeout bounds it, and a call that ended with the time may
// have made the key (K20).
func TestARecoveryKeyThatHangsEndsWithTheConnectTimeoutAndSaysTheKeyMayExist(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	clock := newSplitClock()
	h.deps.clock = clock
	h.deps.euid = 0
	h.fresh().keyring.hang = "add"
	h.fresh().keyring.entered = make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan cmdResult, 1)
	go func() {
		var out, errOut bytes.Buffer
		args := h.subst([]string{"add", "fresh", h.newDir(), "--config", "C", "--connect-timeout", "2s"})
		code := runRepoWithDeps(ctx, args, &out, &errOut, h.deps)
		done <- cmdResult{code, out.String(), errOut.String()}
	}()
	select {
	case <-h.fresh().keyring.entered:
	case r := <-done:
		t.Fatalf("the command ended before restic hung: %+v", r)
	case <-time.After(3 * time.Second):
		t.Fatal("restic key add was not reached")
	}
	clock.fireTimer(2 * time.Second)
	r := within(t, done)
	for _, want := range []string{"RECOVERY_KEY_FAILED", "the key may have been created", "repo key list fresh", "repo key remove fresh"} {
		assertReason(t, r.stderr, want)
	}
}
