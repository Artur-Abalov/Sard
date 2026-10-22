// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

// The edges of the key commands: what happens when a step of them fails.

// brokenRandom is a generator that fails.
type brokenRandom struct{}

func (brokenRandom) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

func TestEveryKeyCommandNeedsTheNameOfARepository(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	for _, args := range [][]string{{"add", "--for", "recovery"}, {"list"}, {"remove", "deadbeef"}} {
		code, _, _ := h.keyCmd(args...)
		assertCode(t, code, exitUsage)
	}
	h.assertNoResticCalls()
}

func TestKeyCommandsOfAnUnknownRepositoryNameTheConnectedOnes(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	for _, args := range [][]string{{"list", "nope"}, {"remove", "nope", "deadbeef", "--yes"}} {
		code, _, stderr := h.keyCmd(args...)
		assertRefusal(t, code, stderr, exitUsage, "REPOSITORY_UNKNOWN")
	}
}

func TestAGeneratorThatFailsIssuesNoKey(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	h.deps.random = brokenRandom{}
	code, _, stderr := h.keyCmd("add", "base", "--for", "recovery")
	assertCode(t, code, exitAgentError)
	assertReason(t, stderr, "no entropy")
	h.assertNoKeyAdd()
}

func TestAHostWithoutANameIssuesNoKey(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	h.deps.hostname = func() (string, error) { return "", errors.New("no hostname") }
	code, _, stderr := h.keyCmd("add", "base", "--for", "recovery")
	assertCode(t, code, exitAgentError)
	assertReason(t, stderr, "the name of this host is unknown")
	h.assertNoKeyAdd()
}

func TestAPasswordFileThatCannotBeWrittenAfterTheKeyWasMadeSaysHowToRecover(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	ok(t, os.MkdirAll(h.path("out"), 0o755))
	h.fsys.failOn, h.fsys.failPath = "link", "vk"
	code, stdout, stderr := h.keyCmd("add", "base", "--for", "verifier", "--to-file", h.path("out/vk"))
	assertRefusal(t, code, stderr, exitWrite, "KEY_FILE_WRITE")
	assertReason(t, stderr, "its password is lost")
	assertReason(t, stderr, "repo key remove base")
	if strings.Contains(stdout, h.added().password) {
		t.Errorf("the password is on the screen:\n%s", stdout)
	}
	h.assertAbsent(h.path("out/vk"))
}

func TestADirectoryThatCannotBeOpenedIsAWriteFailureBeforeRestic(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	ok(t, os.MkdirAll(h.path("out"), 0o755))
	h.fsys.failOn, h.fsys.failPath = "opendir", "out"
	code, _, stderr := h.keyCmd("add", "base", "--for", "verifier", "--to-file", h.path("out/vk"))
	assertRefusal(t, code, stderr, exitWrite, "KEY_FILE_WRITE")
	h.assertNoResticCalls()
}

func TestAKeyWhoseIDCannotBeReadAfterwardsIsStillIssued(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	kr := &h.base().keyring
	kr.onAdd = func() { kr.fail = map[string]string{"list": "Fatal: boom"} }
	code, stdout, stderr := h.keyCmd("add", "base", "--for", "recovery")
	assertCode(t, code, exitOK)
	assertReason(t, stderr, "the id of the new key is unknown")
	if strings.Count(stdout, h.added().password) != 1 {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestAKeyListThatFailsStopsTheRemoval(t *testing.T) {
	t.Parallel()
	h := newRemoveHost(t)
	h.base().keyring.fail = map[string]string{"list": "Fatal: unexpected response 418"}
	code, _, stderr := h.keyCmd("remove", "base", idOf(keyV()), "--yes")
	assertRefusal(t, code, stderr, exitAgentError, "BACKEND_REFUSED")
	h.assertNoKeyRemove()
}

func TestAFailedRestartAfterRemovingTheRecoveryKeyLeavesTheKeyRemoved(t *testing.T) {
	t.Parallel()
	h := newRemoveHost(t)
	h.sd.restartErr = errFakeFailure
	code, stdout, stderr := h.keyCmd("remove", "base", idOf(keyR()), "--yes")
	assertRefusal(t, code, stderr, exitAgentError, "SERVICE_RESTART_FAILED")
	if !strings.Contains(stdout, "removed") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestRepoAddAndRepoInitTakeARelativeRecoveryKeyFileAsAFlagError(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	code, _, stderr := h.addFresh("--recovery-key-file", "rk")
	assertCode(t, code, exitUsage)
	assertReason(t, stderr, "--recovery-key-file")
	code, _, stderr = h.sudo("repo", "init", "base", "--recovery-key-file", "rk")
	assertCode(t, code, exitUsage)
	assertReason(t, stderr, "--recovery-key-file")
	h.assertNoResticCalls()
}

func TestRepoInitTakesNoRecoveryKeyFileAndRefusalTogether(t *testing.T) {
	t.Parallel()
	h := newInitHost(t)
	code, _, _ := h.initBase("--recovery-key-file", h.path("out/rk"), "--no-recovery-key")
	assertCode(t, code, exitUsage)
	h.assertNoResticCalls()
}

func TestARecoveryKeyFileThatCannotBeWrittenAfterInitIsARecoveryKeyFailure(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	h.deps.stdoutTerminal = false
	ok(t, os.MkdirAll(h.path("out"), 0o755))
	h.fsys.failOn, h.fsys.failPath = "link", "rk"
	code, _, stderr := h.addFresh("--recovery-key-file", h.path("out/rk"))
	assertRefusal(t, code, stderr, exitAgentError, "RECOVERY_KEY_FAILED")
	assertReason(t, stderr, "its password is lost")
	if _, err := os.Stat(h.path("agent.d/repo-fresh.yaml")); err != nil {
		t.Errorf("the fragment is not written: %v", err)
	}
}

func TestAHostWithoutANameIssuesNoRecoveryKeyOfANewRepository(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	h.deps.hostname = func() (string, error) { return "", errors.New("no hostname") }
	code, _, stderr := h.addFresh()
	assertRefusal(t, code, stderr, exitAgentError, "RECOVERY_KEY_FAILED")
	assertReason(t, stderr, "the name of this host is unknown")
}

func TestARecoveryKeyOfANewRepositoryWhoseIDCannotBeReadIsStillIssued(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	kr := &h.fresh().keyring
	kr.onAdd = func() { kr.fail = map[string]string{"list": "Fatal: boom"} }
	code, stdout, _ := h.addFresh()
	assertCode(t, code, exitOK)
	if strings.Count(stdout, h.fresh().lastAdded().password) != 1 {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestARepositoryThatIsNotOpenHasNoRecoveryKeyState(t *testing.T) {
	t.Parallel()
	read := recoveryKeyReader(resticRepositories{}, os.ReadFile, 0)
	if _, err := read(t.Context(), config.Repository{Name: "ghost"}); err == nil {
		t.Fatal("a repository that is not open is read")
	}
}
