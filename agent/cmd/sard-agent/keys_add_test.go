// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// docs/specs/agent/restic-keys.feature, rule "key add выпускает новый ключ с обязательным назначением".

func TestAKeyForTheVerifierIsIssuedWithThePurposeInTheUserFieldOfTheKey(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	code, stdout, stderr := h.keyCmd("add", "base", "--for", "verifier")
	assertCode(t, code, exitOK)
	added := h.added()
	if added.user != "sard:verifier" || added.host != "src1" {
		t.Errorf("restic key add --user %q --host %q", added.user, added.host)
	}
	h.assertPasswordHidden(added.password, stdout, stderr)
}

func TestThePurposeAndTheLabelAreWrittenToTheKey(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		flags []string
		field string
	}{
		{[]string{"--for", "recovery"}, "sard:recovery"},
		{[]string{"--for", "verifier", "--label", "vrf-1"}, "sard:verifier:vrf-1"},
		{[]string{"--for", "recovery", "--label", "safe-box"}, "sard:recovery:safe-box"},
		{[]string{"--for", "other", "--label", "auditor"}, "sard:other:auditor"},
	} {
		h := newIssueHost(t)
		code, _, stderr := h.keyCmd(append([]string{"add", "base"}, c.flags...)...)
		if code != exitOK || h.added().user != c.field {
			t.Errorf("%v: code %d, user %q, stderr %q, want %q", c.flags, code, h.added().user, stderr, c.field)
		}
	}
}

func TestThePasswordOfANewKeyIs43CharactersOfBase64urlFromTheGenerator(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	raw, want := known32()
	h.deps.random = bytes.NewReader(raw)
	code, _, _ := h.keyCmd("add", "base", "--for", "recovery")
	assertCode(t, code, exitOK)
	if got := h.added().password; got != want || len(got) != 43 {
		t.Errorf("password %q, want %q", got, want)
	}
}

func TestAKeyIsAddedWithThePasswordOfThisHostAsTheServiceUser(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	code, _, _ := h.keyCmd("add", "base", "--for", "recovery")
	assertCode(t, code, exitOK)
	var calls []fakeCall
	for _, c := range h.restic.callsTo(h.path("base"), "key") {
		if c.args[1] == "add" {
			calls = append(calls, c)
		}
	}
	if len(calls) != 1 || calls[0].runAs == nil || calls[0].runAs.UID != serviceUID || calls[0].runAs.GID != serviceUID {
		t.Fatalf("calls %d", len(calls))
	}
	if calls[0].passwordFile != h.path("secrets/base.pass") {
		t.Errorf("restic opened the repository with %q", calls[0].passwordFile)
	}
}

func TestWithoutAPurposeNoKeyIsIssued(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	code, _, stderr := h.keyCmd("add", "base")
	assertRefusal(t, code, stderr, exitUsage, "KEY_PURPOSE_MISSING")
	assertReason(t, stderr, "--for verifier, --for recovery or --for other")
	h.assertNoResticCalls()
}

func TestAnInvalidPurposeOrLabelIsRefusedBeforeRestic(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		flags  []string
		reason string
	}{
		{[]string{"--for", "admin"}, "KEY_PURPOSE_INVALID"},
		{[]string{"--for", "other"}, "KEY_LABEL_REQUIRED"},
		{[]string{"--for", "other", "--label", ""}, "LABEL_INVALID"},
		{[]string{"--for", "verifier", "--label", "-vrf"}, "LABEL_INVALID"},
		{[]string{"--for", "verifier", "--label", "vrf.1"}, "LABEL_INVALID"},
		{[]string{"--for", "verifier", "--label", "vrf 1"}, "LABEL_INVALID"},
		{[]string{"--for", "verifier", "--label", strings.Repeat("a", 65)}, "LABEL_INVALID"},
	} {
		h := newIssueHost(t)
		code, _, stderr := h.keyCmd(append([]string{"add", "base"}, c.flags...)...)
		assertRefusal(t, code, stderr, exitUsage, c.reason)
		h.assertNoResticCalls()
	}
}

func TestALabelOf64CharactersIsAccepted(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	code, _, _ := h.keyCmd("add", "base", "--for", "other", "--label", strings.Repeat("a", 64))
	assertCode(t, code, exitOK)
}

func TestAnUnknownRepositoryNamesTheConnectedOnes(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	code, _, stderr := h.keyCmd("add", "nope", "--for", "recovery")
	assertRefusal(t, code, stderr, exitUsage, "REPOSITORY_UNKNOWN")
	assertReason(t, stderr, "base")
	h.assertNoResticCalls()
}

func TestIssuingAKeyNeedsSudo(t *testing.T) {
	t.Parallel()
	for name, run := range map[string]func(*keysHost) (int, string, string){
		"the service user": func(h *keysHost) (int, string, string) {
			return h.asService("repo", "key", "add", "base", "--for", "recovery")
		},
		"an outsider": func(h *keysHost) (int, string, string) {
			return h.asOther("repo", "key", "add", "base", "--for", "recovery")
		},
	} {
		h := newIssueHost(t)
		code, _, stderr := run(h)
		assertRefusal(t, code, stderr, exitUsage, "PRIVILEGES_REQUIRED")
		assertReason(t, stderr, "sudo sard-agent repo key add base --for recovery")
		if h.restic.backendCalls() != 0 {
			t.Errorf("%s: restic was called", name)
		}
	}
}

func TestIssuingAVerifierKeyDoesNotChangeTheConfigOrTouchTheService(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	before := h.hostTree()
	code, _, _ := h.keyCmd("add", "base", "--for", "verifier")
	assertCode(t, code, exitOK)
	h.assertConfigUntouched(before)
	if h.sd.touched() {
		t.Errorf("systemctl was called: %v", h.sd.calls)
	}
}

// В1, K19.
func TestIssuingARecoveryKeyAppliesTheChangeSoTheServerLearnsTheFlag(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	before := h.hostTree()
	code, stdout, _ := h.keyCmd("add", "base", "--for", "recovery", "--label", "box-2")
	assertCode(t, code, exitOK)
	h.assertApplied(stdout)
	h.assertConfigUntouched(before)
}

func TestIssuingARecoveryKeyWithNoRestartPrintsTheRestartCommand(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	code, stdout, _ := h.keyCmd("add", "base", "--for", "recovery", "--label", "box-2", "--no-restart")
	assertCode(t, code, exitOK)
	if h.sd.touched() || !strings.Contains(stdout, "sudo systemctl restart sard-agent") {
		t.Errorf("systemctl %v, stdout:\n%s", h.sd.calls, stdout)
	}
}

func TestIssuingARecoveryKeyWithEntriesInTheJournalDoesNotRestartTheService(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	journal := h.path("state/journal")
	ok(t, os.MkdirAll(journal, 0o700))
	h.write(journal+"/a.json", "{}", 0o600)
	h.write(journal+"/b.json", "{}", 0o600)
	code, stdout, _ := h.keyCmd("add", "base", "--for", "recovery", "--label", "box-2")
	assertCode(t, code, exitOK)
	if h.sd.restarted() || !strings.Contains(stdout, "2") || !strings.Contains(stdout, "sudo systemctl restart sard-agent") {
		t.Errorf("restarted %v, stdout:\n%s", h.sd.restarted(), stdout)
	}
}

func TestAFailedRestartAfterARecoveryKeyLeavesTheKey(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	h.sd.restartErr = errFakeFailure
	code, stdout, stderr := h.keyCmd("add", "base", "--for", "recovery", "--label", "box-2")
	assertRefusal(t, code, stderr, exitAgentError, "SERVICE_RESTART_FAILED")
	if n := strings.Count(stdout, h.added().password); n != 1 {
		t.Errorf("the password was in stdout %d times", n)
	}
}

// K10, K19.
func TestARecoveryKeyWithIfMissingAndAKeyThereDoesNotTouchTheService(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	code, _, _ := h.keyCmd("add", "base", "--for", "recovery", "--if-missing")
	assertCode(t, code, exitOK)
	if h.sd.touched() {
		t.Errorf("systemctl was called: %v", h.sd.calls)
	}
}

func TestIssuingAKeyWritesAnAuditLineWithoutThePassword(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	code, _, _ := h.keyCmd("add", "base", "--for", "verifier", "--label", "vrf-1")
	assertCode(t, code, exitOK)
	if len(h.log.lines) != 1 {
		t.Fatalf("audit lines: %q", h.log.lines)
	}
	line := h.log.lines[0]
	for _, want := range []string{"repository key added", "base", h.base().keyring.keys[len(h.base().keyring.keys)-1].id, "verifier", "vrf-1", "alice"} {
		if !strings.Contains(line, want) {
			t.Errorf("the audit line lacks %q: %s", want, line)
		}
	}
	if strings.Contains(line, h.added().password) {
		t.Errorf("the password is in the audit line: %s", line)
	}
}

func TestAnUnavailableSyslogDoesNotStopIssuingAKey(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	h.log.down = true
	code, _, stderr := h.keyCmd("add", "base", "--for", "recovery")
	assertCode(t, code, exitOK)
	assertReason(t, stderr, "not recorded in the system log")
}

func TestAStorageRefusalWhileIssuingAKeyDoesNotShowThePassword(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		line   string
		wrong  bool
		code   int
		reason string
	}{
		{line: "Fatal: unable to open config file: Stat: Get \"https://s3.example.com/b/config\": dial tcp: lookup s3.example.com: no such host", code: exitTemporary, reason: "BACKEND_UNAVAILABLE"},
		{wrong: true, code: exitUsage, reason: "WRONG_PASSWORD"},
		{line: "Fatal: unexpected response 418", code: exitAgentError, reason: "BACKEND_REFUSED"},
	} {
		h := newIssueHost(t)
		h.base().wrongPassword = c.wrong
		if c.line != "" {
			h.base().keyring.fail = map[string]string{"add": c.line}
		}
		code, stdout, stderr := h.keyCmd("add", "base", "--for", "recovery")
		assertRefusal(t, code, stderr, c.code, c.reason)
		if strings.Contains(stdout, "this password") || len(h.base().keyring.adds) > 0 && strings.Contains(stdout, h.added().password) {
			t.Errorf("%s: stdout shows a password:\n%s", c.reason, stdout)
		}
		h.assertPasswordHidden("", stdout, stderr)
	}
}

func TestAStorageAccessDeniedWhileIssuingAKeyIsToldAsUsage(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	h.cfg.Repositories[0].URL = s3Address
	h.cfg.Repositories[0].EnvFile = h.path("secrets/s3.env")
	h.write(h.path("secrets/s3.env"), "AWS_ACCESS_KEY_ID=KEY\nAWS_SECRET_ACCESS_KEY=S3-MARKER\n", 0o600)
	h.saveConfig()
	repo := h.s3Repo()
	repo.initialized, repo.password = true, passMarker
	repo.keyring.keys = []fakeKey{keyH()}
	repo.keyring.fail = map[string]string{"add": "Fatal: client.PutObject: Forbidden: Access Denied."}
	code, stdout, stderr := h.keyCmd("add", "base", "--for", "recovery")
	assertRefusal(t, code, stderr, exitUsage, "STORAGE_ACCESS_DENIED")
	if strings.Contains(stdout, "this password") {
		t.Errorf("stdout shows a password:\n%s", stdout)
	}
}

// hangingAdd runs repo key add against a restic key add that never ends and
// ends it by the --timeout (the timer of 1s fires) or by SIGINT (the context).
func hangingAdd(t *testing.T, timeout bool) (*keysHost, cmdResult) {
	t.Helper()
	h := newIssueHost(t)
	clock := newSplitClock()
	h.deps.clock = clock
	ok(t, os.MkdirAll(h.path("out"), 0o755))
	h.base().keyring.hang = "add"
	h.base().keyring.entered = make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := h.startKeyCmd(ctx, "add", "base", "--for", "recovery", "--to-file", h.path("out/rk"), "--timeout", "1s")
	select {
	case <-h.base().keyring.entered:
	case r := <-done:
		t.Fatalf("the command ended before restic hung: %+v", r)
	case <-time.After(3 * time.Second):
		t.Fatal("restic key add was not reached")
	}
	if timeout {
		clock.fireTimer(time.Second)
	} else {
		cancel()
	}
	return h, within(t, done)
}

func TestATimeoutOrAnInterruptWhileIssuingAKeySaysTheKeyMayHaveBeenCreated(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		timeout bool
		reason  string
	}{{true, "TIMEOUT"}, {false, "INTERRUPTED"}} {
		h, r := hangingAdd(t, c.timeout)
		for _, want := range []string{c.reason, "repo key list base", "repo key remove"} {
			assertReason(t, r.stderr, want)
		}
		if strings.Contains(r.stdout, "this password") {
			t.Errorf("%s: stdout shows a password:\n%s", c.reason, r.stdout)
		}
		h.assertAbsent(h.path("out/rk"))
	}
}

func TestASecondSimultaneousIssueOnTheHostDoesNotWait(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	unlock := h.holdConfigLock()
	defer unlock()
	code, _, stderr := h.keyCmd("add", "base", "--for", "recovery")
	assertRefusal(t, code, stderr, exitTemporary, "CONFIG_LOCKED")
	h.assertNoResticCalls()
}

var _ = slices.Contains[[]string]
