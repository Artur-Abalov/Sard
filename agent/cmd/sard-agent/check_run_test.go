// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

// Rules "Общий код выхода check отражает худшую строку", "json выводит по
// объекту на проверку", "offline не обращается к серверу и хранилищам",
// "check под sudo видит всё" (A8d-2).

func TestTheExitCodeFollowsTheWorstLine(t *testing.T) {
	for _, c := range []struct {
		name    string
		arrange func(h *setupHost)
		code    int
	}{
		{"a warning and an error", func(h *setupHost) {
			h.installIdentity(h.certEnding(10 * 24 * time.Hour))
			ok(h.t, os.Chmod(h.cfg.TLS.KeyFile, 0o644))
		}, exitAgentError},
		{"skipped after offline", func(h *setupHost) {}, exitOK},
		{"no PostgreSQL tools and a warning", func(h *setupHost) {
			h.withoutPostgres()
			h.installIdentity(h.certEnding(10 * 24 * time.Hour))
		}, exitWarnings},
		{"no PostgreSQL tools and an error", func(h *setupHost) {
			h.withoutPostgres()
			ok(h.t, os.Chmod(h.cfg.TLS.KeyFile, 0o644))
		}, exitAgentError},
	} {
		h := fullHost(t)
		c.arrange(h)
		code, _, _ := h.check()
		if code != c.code {
			t.Errorf("%s: exit code %d, want %d", c.name, code, c.code)
		}
	}
}

func TestAFlagErrorIsAUsageErrorWithNothingOnStdout(t *testing.T) {
	h := fullHost(t)
	code, stdout, stderr := h.check("--no-such-flag")
	assertCode(t, code, exitUsage)
	if stdout != "" || !strings.Contains(stderr, "sard-agent check") {
		t.Errorf("stdout %q stderr %q", stdout, stderr)
	}
}

func TestReadDataIsRefusedAndRepoCheckIsNamed(t *testing.T) {
	h := fullHost(t)
	code, stdout, stderr := h.check("--read-data", "5")
	assertCode(t, code, exitUsage)
	if stdout != "" || !strings.Contains(stderr, "--read-data") || !strings.Contains(stderr, "repo check") {
		t.Errorf("stdout %q stderr %q", stdout, stderr)
	}
}

func TestAnInterruptDuringTheCheckOfARepositoryEndsItWithoutASummary(t *testing.T) {
	h := fullHost(t)
	ctx, cancel := context.WithCancel(context.Background())
	h.base().hangCat = true
	h.base().script = func(sub string, _ []string) (string, int, bool) {
		cancel()
		return "", 0, false
	}
	h.deps.euid = 0
	var out, errOut bytes.Buffer
	code := runCheck(ctx, h.subst([]string{"--config", "C"}), &out, &errOut, h.deps)
	assertRefusal(t, code, errOut.String(), exitTemporary, "INTERRUPTED")
	if strings.Contains(out.String(), "summary:") || !h.base().terminated {
		t.Errorf("stdout %q, terminated %v", out.String(), h.base().terminated)
	}
}

// Rule "json выводит по объекту на проверку".

func TestEveryLineOfJSONIsAnObjectOfFiveFields(t *testing.T) {
	h := fullHost(t)
	_, text, _ := h.check()
	code, stdout, _ := h.check("--json")
	assertCode(t, code, exitOK)
	textLines, _ := parseOut(t, text)
	rows := jsonRows(t, stdout)
	if len(rows) != len(textLines) {
		t.Fatalf("%d rows, %d lines of text", len(rows), len(textLines))
	}
	for _, obj := range rows {
		assertShapeOfRow(t, obj)
	}
}

// assertShapeOfRow: the five fields, one of the four statuses, no hint for OK,
// the subject of a repository and none for the host.
func assertShapeOfRow(t *testing.T, obj map[string]any) {
	t.Helper()
	keys := make([]string, 0, 5)
	for k := range obj {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"check", "hint", "message", "status", "subject"}) {
		t.Errorf("keys %v", keys)
	}
	if !slices.Contains([]string{"ok", "warning", "error", "skipped"}, obj["status"].(string)) {
		t.Errorf("status %v", obj["status"])
	}
	if obj["status"] == "ok" && obj["hint"] != nil {
		t.Errorf("hint of %v", obj)
	}
	assertSubjectOfRow(t, obj)
}

func assertSubjectOfRow(t *testing.T, obj map[string]any) {
	t.Helper()
	check := obj["check"].(string)
	switch {
	case strings.HasPrefix(check, "repository."):
		if obj["subject"] != "base" {
			t.Errorf("subject of %v", obj)
		}
	case check == "config.load" && obj["subject"] != nil:
		t.Errorf("subject of %v", obj)
	}
}

func TestAnErrorInJSONCarriesTheHint(t *testing.T) {
	h := fullHost(t)
	h.base().wrongPassword = true
	code, stdout, _ := h.check("--json")
	assertCode(t, code, exitAgentError)
	found := false
	for _, row := range strings.Split(strings.TrimSpace(stdout), "\n") {
		var obj map[string]any
		ok(t, json.Unmarshal([]byte(row), &obj))
		if obj["check"] == "repository.password" {
			found = true
			if obj["status"] != "error" || !strings.Contains(obj["hint"].(string), "09-troubleshooting.md#repositorypassword") {
				t.Errorf("%s", row)
			}
		}
	}
	if !found {
		t.Error("no line")
	}
}

// pinnedIDs is the list of Ч10: a renamed identifier fails this test.
var pinnedIDs = []string{
	"service.user", "config.load", "config.names", "config.references", "files.permissions", "restic.binary",
	"tls.identity", "tls.chain", "tls.expiry", "tls.role", "server.reachable", "server.tls", "server.accepted", "clock.skew",
	"repository.storage", "repository.credentials", "repository.initialized", "repository.password", "repository.id", "repository.keys", "repository.locks",
	"secret.readable", "secret.bound", "plugin.declared", "plugin.programs", "restore.locations", "selinux.labels", "selinux.denials", "service.running", "service.steps",
}

func TestTheIdentifiersAreTheOnesOfTheList(t *testing.T) {
	h := fullHost(t)
	h.onSELinux()
	_, stdout, _ := h.check("--json")
	seen := map[string]bool{}
	for _, row := range strings.Split(strings.TrimSpace(stdout), "\n") {
		var obj struct{ Check string }
		ok(t, json.Unmarshal([]byte(row), &obj))
		seen[obj.Check] = true
	}
	var got []string
	for id := range seen {
		got = append(got, id)
	}
	want := slices.Clone(pinnedIDs)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) || len(pinnedIDs) != 30 {
		t.Errorf("identifiers\n%v\nwant\n%v", got, want)
	}
}

func TestErrorsOfTheCommandInJSONModeAreTextOnStderr(t *testing.T) {
	h := fullHost(t)
	code, stdout, stderr := h.asOther("check", "--json", "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "PRIVILEGES_REQUIRED")
	if stdout != "" || !strings.Contains(stderr, "sard-agent check") {
		t.Errorf("stdout %q stderr %q", stdout, stderr)
	}
}

// Rule "offline не обращается к серверу и хранилищам".

func TestOfflineMakesNoCallToStorageAndSkipsWhatNeedsIt(t *testing.T) {
	h := fullHost(t)
	lines, _, code := h.checkLines("--offline")
	assertCode(t, code, exitOK)
	assertLines(t, lines, "SKIP", "-", "server.reachable", "server.tls", "server.accepted", "clock.skew")
	assertLines(t, lines, "SKIP", "base", wantSteps...)
	assertLines(t, lines, "OK", "-", "config.load", "files.permissions", "restic.binary", "tls.identity", "tls.chain", "tls.expiry", "service.running")
	assertLines(t, lines, "OK", "pg", "secret.readable")
	if h.restic.backendCalls() != 0 {
		t.Errorf("restic %v", h.restic.subs())
	}
}

// assertLines: the lines of the checks have the status; a SKIP line says "offline".
func assertLines(t *testing.T, lines []outLine, status, subject string, checks ...string) {
	t.Helper()
	for _, id := range checks {
		l := line(t, lines, id, subject)
		if l.status != status || (status == "SKIP" && l.message != "offline") {
			t.Errorf("%+v", l)
		}
	}
}

func TestOfflineDoesNotTurnNetworkErrorsIntoWarnings(t *testing.T) {
	h := fullHost(t)
	h.cfg.Server.Address = "no-such-host.invalid:9443"
	h.saveHostConfig()
	_, _, code := h.checkLines("--offline")
	assertCode(t, code, exitOK)
}

// Decision of the owner, A8d-2 (Ч32): repository.id offline.
func TestOfflineTheIdOfTheRepositoryIsSkippedAndTheRecordOfTheServiceIsNotRead(t *testing.T) {
	for _, c := range []struct {
		name    string
		arrange func(h *setupHost)
	}{
		{"with a record", func(h *setupHost) {}},
		{"without a record", func(h *setupHost) { ok(h.t, os.Remove(filepath.Join(h.stateDir(), "announced-repositories.json"))) }},
	} {
		h := fullHost(t)
		c.arrange(h)
		lines, _, _ := h.checkLines("--offline")
		l := line(t, lines, "repository.id", "base")
		if l.status != "SKIP" || l.message != "offline" || !strings.Contains(l.hint, "without --offline") {
			t.Errorf("%s: %+v", c.name, l)
		}
		for _, p := range h.plainReads {
			if strings.Contains(p, "announced-repositories") {
				t.Errorf("%s: the record was read", c.name)
			}
		}
	}
}

func TestOnlineWithoutARecordTheIdIsSkippedAsBefore(t *testing.T) {
	h := fullHost(t)
	ok(t, os.Remove(filepath.Join(h.stateDir(), "announced-repositories.json")))
	lines, _, code := h.checkLines()
	assertCode(t, code, exitOK)
	if l := line(t, lines, "repository.id", "base"); l.status != "SKIP" || !strings.Contains(l.hint, "sudo systemctl start sard-agent") {
		t.Errorf("%+v", l)
	}
}

// Rule "check под sudo видит всё, от пользователя службы — что может, посторонним — отказ".

func TestTheServiceUserSeesWhatItCanAndTheRestNeedsRoot(t *testing.T) {
	h := fullHost(t)
	h.onSELinux()
	code, stdout, stderr := h.asService("check", "--config", "C")
	assertCode(t, code, exitOK)
	lines, summary := parseOut(t, strings.ReplaceAll(stdout, "some checks need root: run sudo sard-agent check\n", ""))
	if l := line(t, lines, "selinux.denials", "-"); l.status != "SKIP" || !strings.Contains(l.message, "needs root") {
		t.Errorf("%+v", l)
	}
	if !strings.Contains(stdout[strings.Index(stdout, summary):], "some checks need root: run sudo sard-agent check") {
		t.Errorf("stdout %q stderr %q", stdout, stderr)
	}
	for _, c := range h.restic.calls {
		if c.runAs != nil {
			t.Errorf("restic %v ran as %+v", c.args, c.runAs)
		}
	}
	if h.progs.ran("ausearch") != 0 {
		t.Error("ausearch was run")
	}
}

func TestWhenNothingNeedsRootThereIsNoLineAboutIt(t *testing.T) {
	h := fullHost(t)
	_, stdout, _ := h.asService("check", "--config", "C")
	if strings.Contains(stdout, "need root") {
		t.Errorf("stdout %q", stdout)
	}
}

func TestAFileTheServiceUserIsRefusedIsAMatterOfRoot(t *testing.T) {
	h := fullHost(t)
	plain := h.deps.readFile
	h.deps.readFile = func(p string) ([]byte, error) {
		if p == h.cfg.TLS.KeyFile {
			return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrPermission}
		}
		return plain(p)
	}
	_, stdout, _ := h.asService("check", "--config", "C")
	lines, _ := parseOut(t, strings.ReplaceAll(stdout, "some checks need root: run sudo sard-agent check\n", ""))
	if l := line(t, lines, "tls.identity", "-"); l.status != "SKIP" || !strings.Contains(l.message, "needs root") || !strings.Contains(l.message, h.cfg.TLS.KeyFile) {
		t.Errorf("%+v", l)
	}
	if !strings.Contains(stdout, "some checks need root") {
		t.Errorf("stdout %q", stdout)
	}
}

func TestAConfigTheServiceUserIsRefusedIsAMatterOfRoot(t *testing.T) {
	h := fullHost(t)
	h.deps.loadConfig = func(string) (config.Config, error) {
		return config.Config{}, &fs.PathError{Op: "open", Path: h.agentD(), Err: fs.ErrPermission}
	}
	_, stdout, _ := h.asService("check", "--config", "C")
	lines, _ := parseOut(t, strings.ReplaceAll(stdout, "some checks need root: run sudo sard-agent check\n", ""))
	if l := line(t, lines, "config.load", "-"); l.status != "SKIP" || !strings.Contains(l.message, "needs root") || !strings.Contains(l.message, h.agentD()) {
		t.Errorf("%+v", l)
	}
}

func TestAStrangerIsRefusedWithTheCommandToRun(t *testing.T) {
	h := fullHost(t)
	code, stdout, stderr := h.asOther("check", "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "PRIVILEGES_REQUIRED")
	if !strings.Contains(stderr, "sudo sard-agent check") || stdout != "" || len(h.restic.subs()) != 0 {
		t.Errorf("stdout %q stderr %q restic %v", stdout, stderr, h.restic.subs())
	}
}

func TestRootReadsTheConfigAsTheServiceDoes(t *testing.T) {
	h := fullHost(t)
	ok(t, os.Chmod(h.agentD(), 0o700))
	h.fsys.setOwner(h.agentD(), 0, 0)
	lines, _, code := h.checkLines()
	assertCode(t, code, exitAgentError)
	l := line(t, lines, "config.load", "-")
	if l.status != "ERROR" || !strings.Contains(l.message, h.agentD()) || !strings.Contains(l.message, "cannot read") {
		t.Errorf("%+v", l)
	}
}

// Rule "check и repo check ничего не меняют на хосте".

func TestACheckOfAHealthyHostChangesNothing(t *testing.T) {
	h := fullHost(t)
	before := h.hostTree()
	h.check()
	h.assertHostUnchanged(before)
	h.assertAbsent(filepath.Join(h.agentD(), ".sard-config.lock"))
}

func TestACheckOfABrokenHostChangesNothingEither(t *testing.T) {
	h := fullHost(t)
	ok(t, os.Chmod(h.cfg.TLS.KeyFile, 0o644))
	h.progs.answer(showKey, "ActiveState=failed\nSubState=failed\nNRestarts=0\nActiveEnterTimestamp=\n")
	h.base().addLock(h.clock.now, "db1", 30*time.Hour, 4242)
	before := h.hostTree()
	h.check()
	h.assertHostUnchanged(before)
	for _, sub := range h.restic.subs() {
		if sub == "unlock" {
			t.Error("restic unlock was run")
		}
	}
	for _, key := range h.progs.keys() {
		if strings.Contains(key, " start ") || strings.Contains(key, " restart ") {
			t.Errorf("%s", key)
		}
	}
}

func TestRepoCheckWithReadDataWritesNothingOnTheHost(t *testing.T) {
	h := fullHost(t)
	before := h.hostTree()
	code, _, _ := h.sudo("repo", "check", "base", "--config", "C", "--read-data", "5")
	assertCode(t, code, exitOK)
	h.assertHostUnchanged(before)
}

func TestTwoChecksAtOnceDoNotGetInEachOthersWay(t *testing.T) {
	h := fullHost(t)
	h.deps.readFile = os.ReadFile
	h.deps.readOwned = func(p string, _ uint32) ([]byte, error) { return os.ReadFile(p) }
	h.base().addLock(h.clock.now, "db1", time.Minute, 4242)
	h.alive[4242] = true
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out, errOut bytes.Buffer
			codes[i] = runCheck(context.Background(), h.subst([]string{"--config", "C"}), &out, &errOut, h.deps)
		}()
	}
	wg.Wait()
	if codes[0] != exitOK || codes[1] != exitOK {
		t.Errorf("codes %v", codes)
	}
	for _, c := range h.restic.calls {
		if c.sub != "version" && !slices.Contains(c.args, "--no-lock") {
			t.Errorf("restic %q ran with a lock", c.args)
		}
	}
}

// Rule "Секреты не попадают в вывод check".

func TestNoSecretReachesTheOutputTheProcessesOrTheLog(t *testing.T) {
	h := fullHost(t)
	h.write(h.path("secrets/pg"), "SECRET-MARKER-1", 0o600)
	h.write(h.path("secrets/base.pass"), "PASS-MARKER-1\n", 0o600)
	url := "rest:http://qa:URL-MARKER@127.0.0.1:9/obj"
	h.write(h.path("secrets/obj.pass"), "obj-pass\n", 0o600)
	h.write(h.path("secrets/obj.env"), "AWS_SECRET_ACCESS_KEY=SECRET-MARKER-2\n", 0o600)
	h.writeFragment("repo-obj.yaml", "repositories:\n  - {name: obj, url: \""+url+"\", password_file: "+h.path("secrets/obj.pass")+", env_file: "+h.path("secrets/obj.env")+"}\n", 0o640)
	obj := h.restic.repo(url)
	obj.initialized = true
	obj.script = printsOn("cat", "Fatal: unable to open config file: Stat: Get \""+url+"\": Access Denied. SECRET-MARKER-2")
	code, text, errText := h.check()
	assertCode(t, code, exitAgentError)
	_, asJSON, errJSON := h.check("--json")
	assertNoMarkers(t, append([]string{text, errText, asJSON, errJSON}, h.log.lines...))
	args := h.progs.keys()
	for _, c := range h.restic.calls {
		args = append(args, c.args...)
	}
	assertNoMarkers(t, args)
	if !strings.Contains(text, "***") {
		t.Errorf("the address of obj is not hidden by ***:\n%s", text)
	}
}

func assertNoMarkers(t *testing.T, texts []string) {
	t.Helper()
	for _, out := range texts {
		for _, marker := range []string{"SECRET-MARKER", "PASS-MARKER", "URL-MARKER"} {
			if strings.Contains(out, marker) {
				t.Errorf("%s is in %q", marker, out)
			}
		}
	}
}

// Rule "Полная проверка без read-data укладывается в секунды".

func TestACheckWithRespondingDependenciesIsFast(t *testing.T) {
	h := fullHost(t)
	h.fragmentRepo("obj")
	h.fragmentRepo("arch")
	start := time.Now()
	code, _, _ := h.check()
	if elapsed := time.Since(start); elapsed > 5*time.Second || code == exitUsage {
		t.Errorf("%v, code %d", elapsed, code)
	}
}

func TestStoragesThatDoNotAnswerAreCutOffByTheConnectTimeoutOneAfterTheOther(t *testing.T) {
	h := fullHost(t)
	h.fragmentRepo("obj").hangCat = true
	h.fragmentRepo("arch").hangCat = true
	h.base().hangCat = true
	vc := newVirtualClock()
	h.deps.clock = vc
	done := h.runInBackground("check", "--config", "C", "--connect-timeout", "1s")
	for i := 1; i <= 3; i++ {
		vc.waitTimers(t, 2*i)
		vc.advance(time.Second)
	}
	r := within(t, done)
	assertCode(t, r.code, exitAgentError)
	if !vc.hasTimer(2 * time.Minute) {
		t.Errorf("timers %v: a repository is not limited to 2m", vc.waits)
	}
	lines, _ := parseOut(t, r.stdout)
	for _, name := range []string{"base", "obj", "arch"} {
		if l := line(t, lines, "repository.storage", name); l.status != "ERROR" || !strings.Contains(l.message, "BACKEND_UNAVAILABLE") || !strings.Contains(l.message, "1s") {
			t.Errorf("%s: %+v", name, l)
		}
	}
}

// Rule "Справка называет флаги, права и коды" (check).

func TestTheHelpOfCheckNamesFlagsRightsAndCodes(t *testing.T) {
	h := newSetupHost(t)
	code, stdout, stderr := h.sudo("check", "--help")
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Fatalf("stderr %q", stderr)
	}
	for _, want := range []string{"Usage: sard-agent check", "--json", "--offline", "--connect-timeout", "--config", "sudo", "only reads", "09-troubleshooting.md", "WARN", "service.user", "service.steps"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help lacks %q:\n%s", want, stdout)
		}
	}
	assertDescribesCodes(t, stdout, []int{0, 1, 2, 6, 8}, []int{3, 4, 5, 7})
}

func TestTheCommandLineOfTheAgentListsCheck(t *testing.T) {
	t.Parallel()
	_, _, stderr := runAgent()
	if !strings.Contains(stderr, "sard-agent check") {
		t.Errorf("stderr %q", stderr)
	}
}

func TestCheckIsDispatchedFromTheAgentCommandLine(t *testing.T) {
	t.Parallel()
	code, stdout, _ := runAgent("check", "--help")
	assertCode(t, code, exitOK)
	if !strings.Contains(stdout, "Usage: sard-agent check") {
		t.Errorf("stdout %q", stdout)
	}
}

// Ч10: what a failed check takes with it.

func TestWhatAFailedCheckTakesWithIt(t *testing.T) {
	for _, c := range []struct {
		name    string
		arrange func(h *setupHost)
		failed  string
		skipped []string
	}{
		{"no service user", func(h *setupHost) { h.people = []hostsetup.User{{Name: "root"}} }, "service.user",
			[]string{"repository.storage", "repository.credentials", "repository.initialized", "repository.password", "repository.id", "repository.keys", "repository.locks", "secret.readable"}},
		{"no usable restic", func(h *setupHost) { h.restic.version = "0.18.0" }, "restic.binary",
			[]string{"repository.storage", "repository.credentials", "repository.initialized", "repository.password", "repository.id", "repository.keys", "repository.locks"}},
		{"a key that is not of the certificate", func(h *setupHost) { h.write(h.cfg.TLS.KeyFile, string(h.certHolding(time.Hour).key), 0o600) }, "tls.identity",
			[]string{"server.reachable", "server.tls", "server.accepted", "clock.skew"}},
	} {
		h := fullHost(t)
		c.arrange(h)
		lines, _, _ := h.checkLines()
		for _, id := range c.skipped {
			i := slices.IndexFunc(lines, func(l outLine) bool { return l.check == id })
			if l := lines[i]; l.status != "SKIP" || l.message != "not checked: "+c.failed+" failed" {
				t.Errorf("%s: %+v", c.name, l)
			}
		}
		if h.restic.backendCalls() != 0 && c.failed != "tls.identity" {
			t.Errorf("%s: restic %v", c.name, h.restic.subs())
		}
	}
}

func TestTheConnectTimeoutOfCheckIsTenSecondsByDefault(t *testing.T) {
	h := fullHost(t)
	h.base().hangCat = true
	vc := newVirtualClock()
	h.deps.clock = vc
	done := h.runInBackground("check", "--config", "C")
	vc.waitTimers(t, 2)
	vc.advance(10 * time.Second)
	r := within(t, done)
	lines, _ := parseOut(t, r.stdout)
	if l := line(t, lines, "repository.storage", "base"); !strings.Contains(l.message, "10s") {
		t.Errorf("%+v", l)
	}
}
