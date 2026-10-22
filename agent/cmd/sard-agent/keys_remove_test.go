// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"strings"
	"testing"
)

// docs/specs/agent/restic-keys.feature, rule "key remove удаляет ключ после подтверждения".

func newRemoveHost(t *testing.T) *keysHost {
	t.Helper()
	h := newKeysHost(t)
	h.keys(keyH(), keyR(), keyV())
	return h
}

func TestAKeyIsRemovedAfterYesInTheTerminal(t *testing.T) {
	t.Parallel()
	h := newRemoveHost(t)
	h.terminalIs("yes")
	code, _, _ := h.keyCmd("remove", "base", idOf(keyV()))
	assertCode(t, code, exitOK)
	if calls := h.base().keyring.removes; len(calls) != 1 || calls[0] != keyV().id {
		t.Fatalf("removed %v, want the full id of V", calls)
	}
}

func TestAKeyIsRemovedAsTheServiceUser(t *testing.T) {
	t.Parallel()
	h := newRemoveHost(t)
	code, _, _ := h.keyCmd("remove", "base", idOf(keyV()), "--yes")
	assertCode(t, code, exitOK)
	var removes []fakeCall
	for _, c := range h.restic.callsTo(h.path("base"), "key") {
		if c.args[1] == "remove" {
			removes = append(removes, c)
		}
	}
	if len(removes) != 1 || removes[0].runAs == nil || removes[0].runAs.UID != serviceUID {
		t.Errorf("restic key remove did not run as the service user: %+v", removes)
	}
}

func TestTheRemovalOfAKeyIsAudited(t *testing.T) {
	t.Parallel()
	h := newRemoveHost(t)
	code, _, _ := h.keyCmd("remove", "base", idOf(keyV()), "--yes")
	assertCode(t, code, exitOK)
	if len(h.log.lines) != 1 {
		t.Fatalf("audit lines %q", h.log.lines)
	}
	for _, want := range []string{"repository key removed", "base", keyV().id, "verifier", "vrf-1"} {
		if !strings.Contains(h.log.lines[0], want) {
			t.Errorf("the audit line lacks %q: %s", want, h.log.lines[0])
		}
	}
}

func TestTheConfirmationQuestionNamesTheKey(t *testing.T) {
	t.Parallel()
	h := newRemoveHost(t)
	term := h.terminalIs("no")
	h.keyCmd("remove", "base", idOf(keyV()))
	if len(term.prompts) != 1 {
		t.Fatalf("prompts %q", term.prompts)
	}
	for _, want := range []string{keyV().id, "verifier", "vrf-1", "src1", "2026-10-20T10:00:00Z"} {
		if !strings.Contains(term.prompts[0], want) {
			t.Errorf("the question lacks %q: %s", want, term.prompts[0])
		}
	}
}

func TestAnyAnswerButYesCancelsTheRemoval(t *testing.T) {
	t.Parallel()
	for name, answers := range map[string][]string{"no": {"no"}, "y": {"y"}, "YES": {"YES"}, "empty": {""}, "end of input": nil} {
		h := newRemoveHost(t)
		h.terminalIs(answers...)
		code, _, stderr := h.keyCmd("remove", "base", idOf(keyV()))
		if code != exitUsage || !strings.Contains(stderr, "CONFIRMATION_REJECTED") {
			t.Errorf("%s: code %d, stderr %q", name, code, stderr)
		}
		h.assertNoKeyRemove()
	}
}

func TestTheYesFlagRemovesWithoutATerminal(t *testing.T) {
	t.Parallel()
	h := newRemoveHost(t)
	h.deps.stdoutTerminal = false
	code, _, _ := h.keyCmd("remove", "base", idOf(keyV()), "--yes")
	assertCode(t, code, exitOK)
	if got := h.base().keyring.removes; len(got) != 1 || got[0] != keyV().id {
		t.Errorf("removed %v", got)
	}
}

func TestWithoutATerminalAndWithoutYesTheRemovalIsRefusedWithoutReadingInput(t *testing.T) {
	t.Parallel()
	h := newRemoveHost(t)
	hung := &hungInput{}
	h.deps.stdin = hung
	code, _, stderr := h.keyCmd("remove", "base", idOf(keyV()))
	assertRefusal(t, code, stderr, exitUsage, "CONFIRMATION_REQUIRED")
	assertReason(t, stderr, "--yes")
	if hung.reads != 0 {
		t.Error("the command read standard input")
	}
	h.assertNoKeyRemove()
}

func TestEveryRemovalSaysTheMasterKeyDoesNotChange(t *testing.T) {
	t.Parallel()
	for name, how := range map[string]func(*keysHost) []string{
		"yes in the terminal": func(h *keysHost) []string { h.terminalIs("yes"); return nil },
		"--yes":               func(*keysHost) []string { return []string{"--yes"} },
	} {
		h := newRemoveHost(t)
		args := append([]string{"remove", "base", idOf(keyV())}, how(h)...)
		_, stdout, _ := h.keyCmd(args...)
		for _, want := range []string{"removing a key does not change the repository master key", "to fully revoke access create a new repository"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("%s: stdout lacks %q:\n%s", name, want, stdout)
			}
		}
	}
}

func TestTheKeyOfThisHostIsNotRemoved(t *testing.T) {
	t.Parallel()
	h := newRemoveHost(t)
	code, _, stderr := h.keyCmd("remove", "base", idOf(keyH()), "--yes")
	assertRefusal(t, code, stderr, exitUsage, "KEY_IN_USE")
	assertReason(t, stderr, "this host opens the repository with this key")
	h.assertNoKeyRemove()
}

func TestTheOnlyKeyOfTheRepositoryIsNotRemoved(t *testing.T) {
	t.Parallel()
	h := newRemoveHost(t)
	h.keys(keyH())
	code, _, stderr := h.keyCmd("remove", "base", idOf(keyH()), "--yes")
	assertRefusal(t, code, stderr, exitUsage, "LAST_KEY")
	assertReason(t, stderr, "without keys the repository cannot be opened")
	h.assertNoKeyRemove()
}

func TestAMissingKeyHasNothingToRemove(t *testing.T) {
	t.Parallel()
	h := newRemoveHost(t)
	code, stdout, _ := h.keyCmd("remove", "base", "deadbeef", "--yes")
	assertCode(t, code, exitOK)
	if !strings.Contains(stdout, "nothing to remove") || len(h.log.lines) != 0 {
		t.Errorf("stdout %q, audit %q", stdout, h.log.lines)
	}
	h.assertNoKeyRemove()
}

func TestAPrefixOfTwoKeysIsRefusedWithTheList(t *testing.T) {
	t.Parallel()
	h := newRemoveHost(t)
	r, v := keyR(), keyV()
	r.id, v.id = "abcd1234"+r.id[8:], "abcd1234"+v.id[8:]
	h.keys(keyH(), r, v)
	code, _, stderr := h.keyCmd("remove", "base", "abcd1234", "--yes")
	assertRefusal(t, code, stderr, exitUsage, "KEY_ID_AMBIGUOUS")
	assertReason(t, stderr, r.id)
	assertReason(t, stderr, v.id)
	h.assertNoKeyRemove()
}

func TestAnInvalidIDIsRefusedBeforeRestic(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"abc", "xyz12345", strings.Repeat("a", 65)} {
		h := newRemoveHost(t)
		code, _, _ := h.keyCmd("remove", "base", id, "--yes")
		assertCode(t, code, exitUsage)
		h.assertNoResticCalls()
	}
}

func TestRemovingAKeyNeedsSudo(t *testing.T) {
	t.Parallel()
	for name, run := range map[string]func(*keysHost) (int, string, string){
		"the service user": func(h *keysHost) (int, string, string) {
			return h.asService("repo", "key", "remove", "base", idOf(keyV()), "--yes")
		},
		"an outsider": func(h *keysHost) (int, string, string) {
			return h.asOther("repo", "key", "remove", "base", idOf(keyV()), "--yes")
		},
	} {
		h := newRemoveHost(t)
		code, _, stderr := run(h)
		assertRefusal(t, code, stderr, exitUsage, "PRIVILEGES_REQUIRED")
		if h.restic.backendCalls() != 0 {
			t.Errorf("%s: restic was called", name)
		}
	}
}

func TestAStorageRefusalWhileRemovingKeepsTheKeyAndWritesNoAudit(t *testing.T) {
	t.Parallel()
	h := newRemoveHost(t)
	h.cfg.Repositories[0].URL = s3Address
	h.cfg.Repositories[0].EnvFile = h.path("secrets/s3.env")
	h.write(h.path("secrets/s3.env"), "AWS_ACCESS_KEY_ID=KEY\nAWS_SECRET_ACCESS_KEY=S3-MARKER\n", 0o600)
	h.saveConfig()
	repo := h.s3Repo()
	repo.initialized, repo.password = true, passMarker
	repo.keyring.keys = []fakeKey{keyH(), keyR(), keyV()}
	repo.keyring.fail = map[string]string{"remove": "Fatal: client.DeleteObject: Forbidden: Access Denied."}
	code, _, stderr := h.keyCmd("remove", "base", idOf(keyV()), "--yes")
	assertRefusal(t, code, stderr, exitUsage, "STORAGE_ACCESS_DENIED")
	if len(h.log.lines) != 0 {
		t.Errorf("audit lines %q", h.log.lines)
	}
}

func TestRemovingAVerifierKeyDoesNotTouchTheService(t *testing.T) {
	t.Parallel()
	h := newRemoveHost(t)
	before := h.hostTree()
	code, _, _ := h.keyCmd("remove", "base", idOf(keyV()), "--yes")
	assertCode(t, code, exitOK)
	if h.sd.touched() {
		t.Errorf("systemctl was called: %v", h.sd.calls)
	}
	h.assertConfigUntouched(before)
}

// В1, K19.
func TestRemovingARecoveryKeyAppliesTheChange(t *testing.T) {
	t.Parallel()
	h := newRemoveHost(t)
	code, stdout, _ := h.keyCmd("remove", "base", idOf(keyR()), "--yes")
	assertCode(t, code, exitOK)
	h.assertApplied(stdout)
}

func TestRemovingARecoveryKeyWithNoRestartPrintsTheRestartCommand(t *testing.T) {
	t.Parallel()
	h := newRemoveHost(t)
	code, stdout, _ := h.keyCmd("remove", "base", idOf(keyR()), "--yes", "--no-restart")
	assertCode(t, code, exitOK)
	if h.sd.touched() || !strings.Contains(stdout, "sudo systemctl restart sard-agent") {
		t.Errorf("systemctl %v, stdout:\n%s", h.sd.calls, stdout)
	}
}

func TestASecondSimultaneousRemovalDoesNotWait(t *testing.T) {
	t.Parallel()
	h := newRemoveHost(t)
	unlock := h.holdConfigLock()
	defer unlock()
	code, _, stderr := h.keyCmd("remove", "base", idOf(keyV()), "--yes")
	assertRefusal(t, code, stderr, exitTemporary, "CONFIG_LOCKED")
	h.assertNoResticCalls()
}

func TestAVerifierDoesNotRemoveKeys(t *testing.T) {
	t.Parallel()
	h := newRemoveHost(t)
	h.deps.hostRole = verifierRole
	code, _, stderr := h.keyCmd("remove", "base", "deadbeef", "--yes")
	assertRefusal(t, code, stderr, exitUsage, "VERIFIER_READ_ONLY")
	h.assertNoResticCalls()
}
