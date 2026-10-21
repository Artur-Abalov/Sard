// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os"
	"strings"
	"testing"
)

// docs/specs/agent/restic-keys.feature, rule "repo init нового репозитория тоже выпускает ключ восстановления".

// newInitHost is a host whose repository base is not initialized and has no password file.
func newInitHost(t *testing.T) *keysHost {
	t.Helper()
	h := newKeysHost(t)
	base := h.base()
	base.initialized, base.password = false, ""
	base.keyring.keys = nil
	ok(t, os.Remove(h.path("secrets/base.pass")))
	return h
}

func (h *keysHost) initBase(flags ...string) (int, string, string) {
	return h.sudo(append([]string{"repo", "init", "base", "--config", "C", "--generate-password"}, flags...)...)
}

func TestRepoInitInTheTerminalIssuesARecoveryKeyRightAfterInit(t *testing.T) {
	t.Parallel()
	h := newInitHost(t)
	code, stdout, stderr := h.initBase()
	assertCode(t, code, exitOK)
	added := h.base().lastAdded()
	if added.user != "sard:recovery" || len(h.restic.callsTo(h.path("base"), "init")) != 1 {
		t.Errorf("key added as %q", added.user)
	}
	if strings.Count(stdout, added.password) != 1 || !strings.Contains(stdout, "this password is stored nowhere else") {
		t.Errorf("stdout:\n%s", stdout)
	}
	h.assertPasswordHidden(added.password, stdout, stderr)
}

func TestRepoInitWritesTheRecoveryKeyToAFileOfRoot(t *testing.T) {
	t.Parallel()
	h := newInitHost(t)
	h.deps.stdoutTerminal = false
	ok(t, os.MkdirAll(h.path("out"), 0o755))
	code, stdout, _ := h.initBase("--recovery-key-file", h.path("out/rk"))
	assertCode(t, code, exitOK)
	password := h.base().lastAdded().password
	if h.fileContent(h.path("out/rk")) != password+"\n" || strings.Contains(stdout, password) {
		t.Errorf("file %q, stdout:\n%s", h.fileContent(h.path("out/rk")), stdout)
	}
	h.assertOwner(h.path("out/rk"), 0)
	h.assertMode(h.path("out/rk"), 0o600)
}

func TestRepoInitByTheServiceUserWritesTheRecoveryKeyFileToTheServiceUser(t *testing.T) {
	t.Parallel()
	h := newInitHost(t)
	h.deps.stdoutTerminal = false
	ok(t, os.MkdirAll(h.path("out"), 0o755))
	code, _, _ := h.asService("repo", "init", "base", "--config", "C", "--generate-password", "--recovery-key-file", h.path("out/rk"))
	assertCode(t, code, exitOK)
	h.assertOwner(h.path("out/rk"), serviceUID)
	h.assertMode(h.path("out/rk"), 0o600)
}

func TestRepoInitWithoutATerminalAndWithoutFlagsDoesNotInitialize(t *testing.T) {
	t.Parallel()
	h := newInitHost(t)
	h.deps.stdoutTerminal = false
	code, _, stderr := h.initBase()
	assertRefusal(t, code, stderr, exitUsage, "RECOVERY_KEY_OUTPUT_MISSING")
	for _, want := range []string{"--recovery-key-file", "--no-recovery-key"} {
		assertReason(t, stderr, want)
	}
	if n := len(h.restic.callsTo(h.path("base"), "init")); n != 0 {
		t.Errorf("restic init was called %d times", n)
	}
	h.assertAbsent(h.path("secrets/base.pass"))
}

func TestRepoInitRefusingTheRecoveryKeyWarnsAndIsRecordedInTheAudit(t *testing.T) {
	t.Parallel()
	h := newInitHost(t)
	h.deps.stdoutTerminal = false
	code, _, stderr := h.initBase("--no-recovery-key")
	assertCode(t, code, exitOK)
	h.assertNoKeyAddOf(h.base())
	assertReason(t, stderr, "without a recovery key, losing this host means losing the backups")
	if len(h.log.lines) != 1 || !strings.Contains(h.log.lines[0], "repository recovery key skipped") || !strings.Contains(h.log.lines[0], "base") {
		t.Errorf("audit lines %q", h.log.lines)
	}
}

func TestRepoInitOfAnInitializedRepositoryIssuesNoRecoveryKey(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	h.deps.stdoutTerminal = false
	ok(t, os.MkdirAll(h.path("out"), 0o755))
	code, _, _ := h.sudo("repo", "init", "base", "--config", "C", "--recovery-key-file", h.path("out/rk"))
	assertCode(t, code, 4)
	h.assertNoKeyAdd()
	h.assertAbsent(h.path("out/rk"))
}

func TestAFailedRecoveryKeyAfterRepoInitLeavesThePasswordFile(t *testing.T) {
	t.Parallel()
	h := newInitHost(t)
	h.base().keyring.fail = map[string]string{"add": "Fatal: something went wrong"}
	code, _, stderr := h.initBase()
	assertRefusal(t, code, stderr, exitAgentError, "RECOVERY_KEY_FAILED")
	assertReason(t, stderr, "sudo sard-agent repo key add base --for recovery")
	if _, err := os.Stat(h.path("secrets/base.pass")); err != nil {
		t.Errorf("the password file is gone: %v", err)
	}
}
