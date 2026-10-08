// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Rule "secret list показывает только имена".

func TestSecretListNamesTheSecretsAndWhereTheyAreDefined(t *testing.T) {
	h := newSetupHost(t)
	h.set("db", "SECRET-MARKER")
	code, stdout, stderr := h.sudo("secret", "list", "--config", "C")
	assertCode(t, code, exitOK)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 || strings.Fields(lines[0])[0] != "NAME" || strings.Fields(lines[0])[1] != "DEFINED_IN" {
		t.Fatalf("stdout %q", stdout)
	}
	if got := strings.Fields(lines[1]); got[0] != "db" || got[1] != h.path("agent.d/secret-db.yaml") {
		t.Fatalf("line %q", lines[1])
	}
	if got := strings.Fields(lines[2]); got[0] != "pg" || got[1] != h.cfgPath {
		t.Fatalf("line %q", lines[2])
	}
	h.assertValuesHidden(stdout, stderr)
}

func TestSecretListOpensNoValueFile(t *testing.T) {
	h := newSetupHost(t)
	ok(t, os.Remove(h.path("secrets/pg")))
	failing := func(string) ([]byte, error) { t.Error("a file was read"); return nil, os.ErrNotExist }
	h.deps.readFile = failing
	h.deps.openFile = nil
	h.deps.stat = func(path string) (info secretsInfo, err error) {
		t.Errorf("a file was looked at: %s", path)
		return info, os.ErrNotExist
	}
	code, _, _ := h.sudo("secret", "list", "--config", "C")
	assertCode(t, code, exitOK)
}

func TestSecretListInJSON(t *testing.T) {
	h := newSetupHost(t)
	h.set("db", "SECRET-MARKER")
	code, stdout, _ := h.sudo("secret", "list", "--json", "--config", "C")
	assertCode(t, code, exitOK)
	var got struct {
		Secrets []struct{ Name, Defined_In string } `json:"secrets"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || len(got.Secrets) != 2 || got.Secrets[0].Name != "db" {
		t.Fatalf("stdout %q err %v", stdout, err)
	}
	if !strings.Contains(stdout, `"defined_in":"`+h.cfgPath+`"`) {
		t.Fatalf("stdout %q", stdout)
	}
}

func TestSecretListWithoutSecretsSaysSo(t *testing.T) {
	h := newSetupHost(t)
	h.cfg.Secrets = nil
	h.saveConfig()
	code, stdout, _ := h.sudo("secret", "list", "--config", "C")
	assertCode(t, code, exitOK)
	if !strings.Contains(stdout, "no secrets are configured") {
		t.Fatalf("stdout %q", stdout)
	}
	code, stdout, _ = h.sudo("secret", "list", "--json", "--config", "C")
	assertCode(t, code, exitOK)
	if strings.TrimSpace(stdout) != `{"secrets":[]}` {
		t.Fatalf("stdout %q", stdout)
	}
}

func TestTheServiceUserMayListSecrets(t *testing.T) {
	h := newSetupHost(t)
	code, _, stderr := h.asService("secret", "list", "--config", "C")
	assertCode(t, code, exitOK)
	if strings.Contains(stderr, "PRIVILEGES_REQUIRED") {
		t.Fatalf("stderr %q", stderr)
	}
}

func TestOneSecretNameInTwoFragmentsIsADuplicate(t *testing.T) {
	h := newSetupHost(t)
	a := h.path("agent.d/secret-a.yaml")
	b := h.path("agent.d/secret-b.yaml")
	h.write(a, "secrets:\n  db: /x/a\n", 0o640)
	h.write(b, "secrets:\n  db: /x/b\n", 0o640)
	code, _, stderr := h.sudo("secret", "list", "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "DUPLICATE_NAME")
	for _, want := range []string{"db", a, b} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %s", want, stderr)
		}
	}
}

// Rule "secret remove убирает секрет из настройки хоста".

func TestRemovingASecretRemovesTheFragmentAndTheValueFile(t *testing.T) {
	h := newSetupHost(t)
	h.set("db", "SECRET-MARKER")
	h.sd.calls, h.log.lines = nil, nil
	code, stdout, _ := h.sudo("secret", "remove", "db", "--config", "C")
	assertCode(t, code, exitOK)
	h.assertAbsent(h.path("agent.d/secret-db.yaml"), h.path("secrets/db"))
	h.assertApplied(stdout)
	if len(h.log.lines) != 1 || !strings.Contains(h.log.lines[0], "secret db removed") {
		t.Fatalf("audit %q", h.log.lines)
	}
}

func TestRemovingAnUnsetSecretDoesNothing(t *testing.T) {
	h := newSetupHost(t)
	before := h.hostTree()
	code, stdout, _ := h.sudo("secret", "remove", "db", "--config", "C")
	assertCode(t, code, exitOK)
	if !strings.Contains(stdout, "nothing to remove") {
		t.Fatalf("stdout %q", stdout)
	}
	h.assertHostUnchanged(before)
}

func TestASecretOfTheMainConfigIsNotRemoved(t *testing.T) {
	h := newSetupHost(t)
	before := h.hostTree()
	code, _, stderr := h.sudo("secret", "remove", "pg", "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "DEFINED_IN_CONFIG")
	if !strings.Contains(stderr, h.cfgPath) {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertHostUnchanged(before)
}

func TestRemovingKeepsAValueFileSomethingElseUses(t *testing.T) {
	h := newSetupHost(t)
	h.set("db", "SECRET-MARKER")
	h.cfg.Scripts = map[string]string{"s": h.path("secrets/db")}
	h.saveConfig()
	code, _, _ := h.sudo("secret", "remove", "db", "--config", "C")
	assertCode(t, code, exitOK)
	if got := h.fileContent(h.path("secrets/db")); got != "SECRET-MARKER" {
		t.Fatalf("the value file of another key was removed: %q", got)
	}
}

func TestAFragmentThatCannotBeRemovedIsAWriteErrorNamingIt(t *testing.T) {
	h := newSetupHost(t)
	h.set("db", "SECRET-MARKER")
	h.sd.calls, h.log.lines = nil, nil
	h.fsys.failOn, h.fsys.failPath = "remove", "secret-db.yaml"
	code, _, stderr := h.sudo("secret", "remove", "db", "--config", "C")
	assertRefusal(t, code, stderr, exitWrite, "CONFIG_WRITE")
	if !strings.Contains(stderr, h.path("agent.d/secret-db.yaml")) {
		t.Fatalf("stderr %q", stderr)
	}
	if h.sd.touched() || len(h.log.lines) != 0 {
		t.Fatalf("calls %v, audit %q", h.sd.calls, h.log.lines)
	}
}

// Rule "Изменение применяется перезапуском службы, только если шаги не идут".

func (h *setupHost) journal(names ...string) {
	h.t.Helper()
	ok(h.t, os.MkdirAll(filepath.Join(h.stateDir(), "journal"), 0o700))
	for _, n := range names {
		h.write(filepath.Join(h.stateDir(), "journal", n), "{}", 0o600)
	}
}

func TestWithoutRunningStepsTheServiceIsRestarted(t *testing.T) {
	h := newSetupHost(t)
	code, stdout, _ := h.set("db", "SECRET-MARKER")
	assertCode(t, code, exitOK)
	h.assertApplied(stdout)
}

func TestARunningStepPostponesTheRestart(t *testing.T) {
	h := newSetupHost(t)
	h.journal("a.json", "b.json")
	code, stdout, _ := h.set("db", "SECRET-MARKER")
	assertCode(t, code, exitOK)
	if h.sd.restarted() {
		t.Fatalf("restarted: %v", h.sd.calls)
	}
	for _, want := range []string{"2 steps are running", "not restarted", "sudo systemctl restart sard-agent"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q: %s", want, stdout)
		}
	}
}

func TestTemporaryJournalFilesAreNotSteps(t *testing.T) {
	h := newSetupHost(t)
	h.journal(".tmp-123")
	h.set("db", "SECRET-MARKER")
	if !h.sd.restarted() {
		t.Fatalf("calls %v", h.sd.calls)
	}
}

func TestNoRestartNeverRestarts(t *testing.T) {
	h := newSetupHost(t)
	code, stdout, _ := h.set("db", "SECRET-MARKER", "--no-restart")
	assertCode(t, code, exitOK)
	if h.sd.touched() || !strings.Contains(stdout, "sudo systemctl restart sard-agent") {
		t.Fatalf("calls %v, stdout %q", h.sd.calls, stdout)
	}
}

func TestAStoppedServiceIsNotStarted(t *testing.T) {
	h := newSetupHost(t)
	h.sd.active = false
	code, stdout, _ := h.set("db", "SECRET-MARKER")
	assertCode(t, code, exitOK)
	if h.sd.restarted() || !strings.Contains(stdout, "takes effect when the service starts") {
		t.Fatalf("calls %v, stdout %q", h.sd.calls, stdout)
	}
}

func TestWithoutSystemdTheOperatorIsToldToRestartTheAgent(t *testing.T) {
	h := newSetupHost(t)
	h.sd.present = false
	code, stdout, _ := h.set("db", "SECRET-MARKER")
	assertCode(t, code, exitOK)
	if h.sd.touched() || !strings.Contains(stdout, "restart the agent yourself") {
		t.Fatalf("calls %v, stdout %q", h.sd.calls, stdout)
	}
}

func TestAnUnreadableJournalPostponesTheRestart(t *testing.T) {
	h := newSetupHost(t)
	h.journal()
	h.fsys.failOn, h.fsys.failPath = "readdir", "journal"
	code, stdout, _ := h.set("db", "SECRET-MARKER")
	assertCode(t, code, exitOK)
	if h.sd.restarted() || !strings.Contains(stdout, filepath.Join(h.stateDir(), "journal")) || !strings.Contains(stdout, "sudo systemctl restart sard-agent") {
		t.Fatalf("calls %v, stdout %q", h.sd.calls, stdout)
	}
}

func TestAMissingStateDirectoryMeansNoSteps(t *testing.T) {
	h := newSetupHost(t)
	code, _, _ := h.set("db", "SECRET-MARKER")
	assertCode(t, code, exitOK)
	if !h.sd.restarted() {
		t.Fatalf("calls %v", h.sd.calls)
	}
}

func TestAFailedRestartIsAnAgentErrorAndTheChangeStays(t *testing.T) {
	h := newSetupHost(t)
	h.sd.restartErr = os.ErrInvalid
	code, _, stderr := h.set("db", "SECRET-MARKER")
	assertRefusal(t, code, stderr, exitAgentError, "SERVICE_RESTART_FAILED")
	if !strings.Contains(stderr, "journalctl -u sard-agent") {
		t.Fatalf("stderr %q", stderr)
	}
	if _, err := os.Stat(h.path("agent.d/secret-db.yaml")); err != nil {
		t.Fatal("the change was not kept")
	}
}

func TestCommandsThatChangeNothingLeaveTheServiceAlone(t *testing.T) {
	h := newSetupHost(t)
	h.set("db", "SECRET-MARKER")
	h.sd.calls = nil
	steps := [][]string{{"secret", "remove", "nope"}, {"repo", "password", "base", "--reveal"}, {"repo", "list"}}
	h.stdinIs("SECRET-MARKER")
	h.sudo("secret", "set", "db", "--stdin", "--config", "C")
	for _, s := range steps {
		h.sudo(append(s, "--config", "C")...)
	}
	if h.sd.touched() {
		t.Fatalf("systemctl: %v", h.sd.calls)
	}
}

// Rule "Каждое изменение оставляет строку аудита без значений".

func TestAChangeWritesOneAuditLineNamingWhoMadeIt(t *testing.T) {
	h := newSetupHost(t)
	h.set("db", "SECRET-MARKER")
	if len(h.log.lines) != 1 {
		t.Fatalf("lines %q", h.log.lines)
	}
	for _, want := range []string{"secret db added", "alice", "1000"} {
		if !strings.Contains(h.log.lines[0], want) {
			t.Errorf("line %q lacks %q", h.log.lines[0], want)
		}
	}
	h.assertValuesHidden()
}

func TestWithoutSudoTheAuditLineNamesTheProcessUser(t *testing.T) {
	h := newSetupHost(t)
	h.env = map[string]string{}
	h.set("db", "SECRET-MARKER")
	if !regexp.MustCompile(`root.*\b0\b`).MatchString(h.log.lines[0]) {
		t.Fatalf("line %q", h.log.lines[0])
	}
}

func TestAnUnavailableSystemLogOnlyWarns(t *testing.T) {
	h := newSetupHost(t)
	h.log.down = true
	code, _, stderr := h.set("db", "SECRET-MARKER")
	assertCode(t, code, exitOK)
	if _, err := os.Stat(h.path("agent.d/secret-db.yaml")); err != nil {
		t.Fatal("the change was cancelled")
	}
	if !strings.Contains(stderr, "not recorded in the system log") {
		t.Fatalf("stderr %q", stderr)
	}
}

func TestReadCommandsWriteNoAuditLines(t *testing.T) {
	h := newSetupHost(t)
	for _, s := range [][]string{{"secret", "list"}, {"repo", "list"}} {
		h.sudo(append(s, "--config", "C")...)
	}
	if len(h.log.lines) != 0 {
		t.Fatalf("lines %q", h.log.lines)
	}
}

// Rule "Одновременные изменения хоста не смешиваются".

func TestASecondChangeWhileTheFirstHoldsTheLockIsRefused(t *testing.T) {
	h := newSetupHost(t)
	h.set("first", "SECRET-MARKER")
	unlock := h.holdConfigLock()
	defer unlock()
	code, _, stderr := h.set("other", "SECRET-MARKER")
	assertRefusal(t, code, stderr, exitTemporary, "CONFIG_LOCKED")
	h.assertAbsent(h.path("agent.d/secret-other.yaml"))
}

func TestALeftoverConfigLockFileDoesNotBlock(t *testing.T) {
	h := newSetupHost(t)
	h.write(h.path("agent.d/.sard-config.lock"), "123\n", 0o600)
	code, _, _ := h.set("db", "SECRET-MARKER")
	assertCode(t, code, exitOK)
}
