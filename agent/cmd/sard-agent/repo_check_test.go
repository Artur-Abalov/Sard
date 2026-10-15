// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
)

// Scenarios of A8d-1 (docs/specs/agent/host-check.feature): repo check.

// The healthy host of the conventions: the clock is 2026-10-14T12:00:00Z, the
// host db1, the repository base is initialised (id REPO-A) and the service
// announced it with that id.
func checkHost(t *testing.T) *setupHost {
	t.Helper()
	h := newSetupHost(t)
	h.clock.now = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	h.base().initialized = true
	h.base().id = "REPO-A"
	h.alive = map[int]bool{}
	h.deps.hostname = func() (string, error) { return "db1", nil }
	h.deps.pidAlive = func(pid int) bool { return h.alive[pid] }
	h.announce(hostcheck.AnnouncedRepository{Name: "base", RepositoryID: "REPO-A"})
	return h
}

// announce is what the service wrote into the state dir S at its Register.
func (h *setupHost) announce(repos ...hostcheck.AnnouncedRepository) {
	h.t.Helper()
	ok(h.t, os.MkdirAll(h.stateDir(), 0o700))
	ok(h.t, hostcheck.WriteAnnounced(h.stateDir(), repos))
}

func (h *setupHost) repoCheck(extra ...string) (int, string, string) {
	return h.sudo(append([]string{"repo", "check", "--config", "C"}, extra...)...)
}

// outLine is a line of the output of a check with the hint that follows it.
type outLine struct{ status, check, subject, message, hint string }

func parseOut(t *testing.T, stdout string) (lines []outLine, summary string) {
	t.Helper()
	for _, l := range strings.Split(strings.TrimSuffix(stdout, "\n"), "\n") {
		switch {
		case l == "":
		case strings.HasPrefix(l, "hint: "):
			lines[len(lines)-1].hint = strings.TrimPrefix(l, "hint: ")
		case strings.HasPrefix(l, "summary: "):
			summary = l
		default:
			f := strings.SplitN(l, " ", 4)
			if len(f) != 4 {
				t.Fatalf("a line of four fields: %q", l)
			}
			lines = append(lines, outLine{status: f[0], check: f[1], subject: f[2], message: f[3]})
		}
	}
	return lines, summary
}

// line is the line of the check with the subject.
func line(t *testing.T, lines []outLine, check, subject string) outLine {
	t.Helper()
	for _, l := range lines {
		if l.check == check && l.subject == subject {
			return l
		}
	}
	t.Fatalf("no line %s %s in %v", check, subject, lines)
	return outLine{}
}

var wantSteps = []string{"repository.storage", "repository.credentials", "repository.initialized", "repository.password", "repository.id", "repository.locks"}

// Rule "repo check проверяет репозиторий по шагам и останавливается на первой ошибке".

func TestAHealthyRepositoryPassesAllTheSteps(t *testing.T) {
	h := checkHost(t)
	code, stdout, stderr := h.repoCheck("base")
	assertCode(t, code, exitOK)
	lines, summary := parseOut(t, stdout)
	if len(lines) != len(wantSteps) {
		t.Fatalf("lines %v, stderr %q", lines, stderr)
	}
	for i, l := range lines {
		if l.check != wantSteps[i] || l.subject != "base" || l.status != "OK" || l.hint != "" {
			t.Errorf("line %d: %+v", i, l)
		}
	}
	if summary != "summary: 6 ok, 0 warnings, 0 errors, 0 skipped" {
		t.Errorf("summary %q", summary)
	}
}

// printsOn makes the subcommand of restic fail with the line on stderr.
func printsOn(sub, line string) func(string, []string) (string, int, bool) {
	return func(s string, _ []string) (string, int, bool) { return line, 1, s == sub }
}

// useAddress gives the repository base of C the address, and the fake restic
// the repository behind it.
func (h *setupHost) useAddress(url, envFile string) {
	h.t.Helper()
	repo := h.base()
	h.cfg.Repositories[0].URL = url
	h.cfg.Repositories[0].EnvFile = envFile
	h.restic.repos[url] = repo
	if envFile != "" {
		h.write(envFile, "AWS_ACCESS_KEY_ID=KEYID\nAWS_SECRET_ACCESS_KEY=SECRET-MARKER\n", 0o600)
	}
	h.saveConfig()
}

const (
	obsS3   = "s3:https://s3.example.com/bucket-b/base"
	obsSFTP = "sftp:backup@nas.example.com:/srv/base"
)

func (h *setupHost) onS3() { h.useAddress(obsS3, h.path("secrets/restic-base.env")) }

func (h *setupHost) onSFTP() { h.useAddress(obsSFTP, "") }

func TestAStepThatFailsNamesTheReasonAndTheSkippedStepsFollow(t *testing.T) {
	for _, c := range []struct {
		name    string
		arrange func(h *setupHost)
		step    string
		reason  string
		hint    string
	}{
		{"no such host", func(h *setupHost) {
			h.base().script = printsOn("cat", "Fatal: Stat: dial tcp: lookup nas: no such host")
		}, checkStorage, "BACKEND_UNAVAILABLE", "09-troubleshooting.md#repositorystorage"},
		{"connection refused", func(h *setupHost) {
			h.base().script = printsOn("cat", "Fatal: unable to open config file: dial tcp 192.0.2.1:443: connect: connection refused")
		}, checkStorage, "BACKEND_UNAVAILABLE", "09-troubleshooting.md#repositorystorage"},
		{"x509", func(h *setupHost) {
			h.base().script = printsOn("cat", "Fatal: Stat: tls: failed to verify certificate: x509 certificate signed by unknown authority")
		}, checkStorage, "BACKEND_UNAVAILABLE", "09-troubleshooting.md#repositorystorage"},
		{"invalid key id", func(h *setupHost) {
			h.onS3()
			h.base().script = printsOn("cat", "Fatal: unable to open config file: Stat: InvalidAccessKeyId: x")
		}, checkCredentials, "S3_KEY_REJECTED", "09-troubleshooting.md#repositorycredentials"},
		{"access denied", func(h *setupHost) {
			h.onS3()
			h.base().script = printsOn("cat", "Fatal: unable to open config file: Stat: Access Denied.")
		}, checkCredentials, "STORAGE_ACCESS_DENIED", "09-troubleshooting.md#repositorycredentials"},
		{"no such bucket", func(h *setupHost) {
			h.onS3()
			h.base().script = printsOn("cat", "Fatal: Stat: NoSuchBucket: x")
		}, checkCredentials, "BUCKET_NOT_FOUND", "09-troubleshooting.md#repositorycredentials"},
		{"ssh key", func(h *setupHost) {
			h.onSFTP()
			h.base().script = printsOn("cat", "subprocess ssh: Permission denied (publickey).")
		}, checkCredentials, "SSH_KEY_NOT_AUTHORIZED", "authorized_keys"},
		{"host key", func(h *setupHost) {
			h.onSFTP()
			h.base().script = printsOn("cat", "subprocess ssh: Host key verification failed.")
		}, checkCredentials, "HOST_KEY_MISMATCH", "09-troubleshooting.md#repositorycredentials"},
		{"not initialized", func(h *setupHost) { h.base().initialized = false }, checkInitialized, "REPOSITORY_NOT_INITIALIZED", "sudo sard-agent repo add base"},
		{"no password file", func(h *setupHost) { ok(h.t, os.Remove(h.path("secrets/base.pass"))) }, checkPassword, "PASSWORD_FILE_MISSING", "sudo sard-agent repo add base"},
		{"password file open to others", func(h *setupHost) { ok(h.t, os.Chmod(h.path("secrets/base.pass"), 0o644)) }, checkPassword, "SECRET_FILE_REJECTED", "sudo chmod 0600"},
		{"wrong password", func(h *setupHost) { h.base().wrongPassword = true }, checkPassword, "WRONG_PASSWORD", "09-troubleshooting.md#repositorypassword"},
	} {
		h := checkHost(t)
		c.arrange(h)
		code, stdout, _ := h.repoCheck("base")
		assertCode(t, code, exitAgentError)
		assertFailedAt(t, c.name, stdout, c.step, c.reason, c.hint)
	}
}

// assertFailedAt: the steps before the one are OK, it is an ERROR that names
// the reason and the hint, the steps after it are skipped because of it.
func assertFailedAt(t *testing.T, name, stdout, step, reason, hint string) {
	t.Helper()
	lines, summary := parseOut(t, stdout)
	if len(lines) != len(wantSteps) {
		t.Fatalf("%s: lines %v", name, lines)
	}
	failed := slices.Index(wantSteps, step)
	for i, l := range lines {
		if want := statusAt(i, failed); l.status != want {
			t.Errorf("%s: line %d is %s, want %s", name, i, l.status, want)
		}
	}
	if bad := lines[failed]; !strings.Contains(bad.message, reason) || !strings.Contains(bad.hint, hint) {
		t.Errorf("%s: message %q hint %q", name, bad.message, bad.hint)
	}
	assertSkippedBecause(t, name, lines[failed+1:], step)
	if want := fmt.Sprintf("summary: %d ok, 0 warnings, 1 errors, %d skipped", failed, len(wantSteps)-failed-1); summary != want {
		t.Errorf("%s: summary %q, want %q", name, summary, want)
	}
}

func assertSkippedBecause(t *testing.T, name string, skipped []outLine, step string) {
	t.Helper()
	for _, l := range skipped {
		if l.message != "not checked: "+step+" failed" || l.hint == "" {
			t.Errorf("%s: skipped line %+v", name, l)
		}
	}
}

func statusAt(i, failed int) string {
	switch {
	case i < failed:
		return "OK"
	case i == failed:
		return "ERROR"
	}
	return "SKIP"
}

func TestAnS3FailureOfTheEnvFileNamesTheFileNotTheValuesAndNeverCallsRestic(t *testing.T) {
	h := checkHost(t)
	h.onS3()
	ok(t, os.Chmod(h.path("secrets/restic-base.env"), 0o644))
	code, stdout, stderr := h.repoCheck("base")
	assertCode(t, code, exitAgentError)
	lines, _ := parseOut(t, stdout)
	bad := line(t, lines, checkCredentials, "base")
	env := h.path("secrets/restic-base.env")
	if bad.status != "ERROR" || !strings.Contains(bad.message, "SECRET_FILE_REJECTED") || !strings.Contains(bad.message, env) {
		t.Errorf("credentials: %+v", bad)
	}
	if !strings.Contains(bad.hint, "sudo chmod 0600 "+env) {
		t.Errorf("hint %q", bad.hint)
	}
	if h.restic.backendCalls() != 0 {
		t.Errorf("restic was called: %v", h.restic.subs())
	}
	assertNoSecrets(t, stdout, stderr)
	h.assertValuesHidden(stdout, stderr)
}

// Rule "Молчащее хранилище при repo check ограничено таймаутом подключения".

func TestASilentStorageIsCutOffByTheConnectTimeout(t *testing.T) {
	h := checkHost(t)
	vc := newVirtualClock()
	h.deps.clock = vc
	h.base().hangCat = true
	done := h.runInBackground("repo", "check", "base", "--config", "C", "--connect-timeout", "2s")
	vc.waitTimers(t, 2)
	vc.advance(2 * time.Second)
	r := within(t, done)
	assertCode(t, r.code, exitAgentError)
	lines, _ := parseOut(t, r.stdout)
	bad := line(t, lines, checkStorage, "base")
	if bad.status != "ERROR" || !strings.Contains(bad.message, "BACKEND_UNAVAILABLE") || !strings.Contains(bad.message, "2s") || !h.base().terminated {
		t.Errorf("storage %+v, terminated %v", bad, h.base().terminated)
	}
}

// ЧВ9
func TestTheConnectTimeoutOfACheckIsTenSecondsByDefault(t *testing.T) {
	h := checkHost(t)
	vc := newVirtualClock()
	h.deps.clock = vc
	h.base().hangCat = true
	done := h.runInBackground("repo", "check", "base", "--config", "C")
	vc.waitTimers(t, 2)
	vc.advance(10 * time.Second)
	r := within(t, done)
	lines, _ := parseOut(t, r.stdout)
	if bad := line(t, lines, checkStorage, "base"); !strings.Contains(bad.message, "10s") {
		t.Errorf("storage %+v", bad)
	}
}

// Rule "Шаги restic идут без блокировки и без кэша".

func TestTheStepsOfResticRunWithoutLockAndCacheAndNeverCheck(t *testing.T) {
	h := checkHost(t)
	h.base().addLock(h.clock.now, "db1", 5*time.Minute, 4242)
	code, _, _ := h.repoCheck("base")
	assertCode(t, code, exitOK)
	var subs []string
	for _, c := range h.restic.calls {
		if c.sub == "version" {
			continue
		}
		subs = append(subs, c.sub)
		if !slices.Contains(c.args, "--no-lock") || !slices.Contains(c.args, "--no-cache") {
			t.Errorf("restic %q ran with a lock or a cache", c.args)
		}
	}
	if !slices.Equal(subs, []string{"cat", "list", "cat"}) {
		t.Errorf("restic ran %v", subs)
	}
}

// Rule "repository id сравнивается с объявленным службой серверу".

func TestTheRepositoryIDIsTheAnnouncedOne(t *testing.T) {
	h := checkHost(t)
	_, stdout, _ := h.repoCheck("base")
	lines, _ := parseOut(t, stdout)
	if l := line(t, lines, checkID, "base"); l.status != "OK" || !strings.Contains(l.message, "REPO-A") {
		t.Errorf("%+v", l)
	}
}

func TestAnIDThatDiffersFromTheAnnouncedOneIsToBeRestartedOrFixed(t *testing.T) {
	for _, c := range []struct {
		name     string
		announce []hostcheck.AnnouncedRepository
		status   string
		text     string
		code     int
	}{
		{"another id", []hostcheck.AnnouncedRepository{{Name: "base", RepositoryID: "REPO-B"}}, "ERROR", "REPO-B", exitAgentError},
		{"empty id", []hostcheck.AnnouncedRepository{{Name: "base"}}, "WARN", "could not read", exitWarnings},
		{"only another repository", []hostcheck.AnnouncedRepository{{Name: "other", RepositoryID: "X"}}, "WARN", "not announced to the server yet", exitWarnings},
	} {
		h := checkHost(t)
		h.announce(c.announce...)
		code, stdout, _ := h.repoCheck("base")
		assertCode(t, code, c.code)
		lines, _ := parseOut(t, stdout)
		l := line(t, lines, checkID, "base")
		if l.status != c.status || !strings.Contains(l.message, c.text) || !strings.Contains(l.hint, "sudo systemctl restart sard-agent") {
			t.Errorf("%s: %+v", c.name, l)
		}
	}
}

func TestWithoutARecordOfTheServiceTheComparisonIsSkipped(t *testing.T) {
	h := checkHost(t)
	ok(t, os.Remove(filepath.Join(h.stateDir(), hostcheck.AnnouncedFile)))
	code, stdout, _ := h.repoCheck("base")
	assertCode(t, code, exitOK)
	lines, _ := parseOut(t, stdout)
	if l := line(t, lines, checkID, "base"); l.status != "SKIP" || !strings.Contains(l.hint, "sudo systemctl start sard-agent") {
		t.Errorf("%+v", l)
	}
}

func TestAnErrorOfTheIDStopsTheLocksStep(t *testing.T) {
	h := checkHost(t)
	h.announce(hostcheck.AnnouncedRepository{Name: "base", RepositoryID: "REPO-B"})
	_, stdout, _ := h.repoCheck("base")
	lines, _ := parseOut(t, stdout)
	if l := line(t, lines, checkLocks, "base"); l.status != "SKIP" || l.message != "not checked: repository.id failed" {
		t.Errorf("%+v", l)
	}
	if len(h.restic.callsTo(h.path("base"), "list")) != 0 {
		t.Error("the locks were read")
	}
}

// Rule "repo check показывает блокировки restic с возрастом и хостом-владельцем".

func (h *setupHost) lockLine() (outLine, int) {
	h.t.Helper()
	code, stdout, _ := h.repoCheck("base")
	lines, _ := parseOut(h.t, stdout)
	return line(h.t, lines, checkLocks, "base"), code
}

func TestNoLocks(t *testing.T) {
	h := checkHost(t)
	l, _ := h.lockLine()
	if l.status != "OK" || l.message != "no locks" {
		t.Errorf("%+v", l)
	}
	calls := h.restic.callsTo(h.path("base"), "list")
	if len(calls) != 1 || !slices.Contains(calls[0].args, "--no-lock") {
		t.Errorf("list locks calls %v", calls)
	}
}

func TestFreshLocksAreNoWarning(t *testing.T) {
	h := checkHost(t)
	h.base().addLock(h.clock.now, "db1", 10*time.Minute, 4242)
	h.base().addLock(h.clock.now, "db2", 2*time.Hour, 77)
	l, code := h.lockLine()
	assertCode(t, code, exitOK)
	for _, want := range []string{"2 locks", "db1", "db2"} {
		if l.status != "OK" || !strings.Contains(l.message, want) {
			t.Errorf("%q: %+v", want, l)
		}
	}
}

func TestAnOwnStaleLockOfADeadProcessOffersTheUnlockCommand(t *testing.T) {
	h := checkHost(t)
	h.base().addLock(h.clock.now, "db1", 30*time.Hour, 4242)
	l, code := h.lockLine()
	assertCode(t, code, exitWarnings)
	if l.status != "WARN" || !strings.Contains(l.message, "db1") || !strings.Contains(l.message, "30h") || !strings.Contains(l.hint, "sudo sard-agent repo unlock base --remove-stale") {
		t.Errorf("%+v", l)
	}
}

func TestAnOwnStaleLockOfALiveProcessIsNotOfferedForRemoval(t *testing.T) {
	h := checkHost(t)
	h.base().addLock(h.clock.now, "db1", 30*time.Hour, 4242)
	h.alive[4242] = true
	l, _ := h.lockLine()
	if l.status != "WARN" || !strings.Contains(l.message, "held by running process 4242") || strings.Contains(l.hint, "repo unlock") {
		t.Errorf("%+v", l)
	}
}

func TestAForeignStaleLockNamesTheHostAndIsNotRemovedHere(t *testing.T) {
	h := checkHost(t)
	h.base().addLock(h.clock.now, "db2", 48*time.Hour, 77)
	l, _ := h.lockLine()
	if l.status != "WARN" || !strings.Contains(l.message, "db2") || !strings.Contains(l.message, "48h") || !strings.Contains(l.hint, "on db2") {
		t.Errorf("%+v", l)
	}
}

func TestALockExactlyAtTheThresholdIsNotYetStale(t *testing.T) {
	h := checkHost(t)
	h.base().addLock(h.clock.now, "db1", 24*time.Hour, 4242)
	if l, _ := h.lockLine(); l.status != "OK" {
		t.Errorf("%+v", l)
	}
}

func TestALockThatCannotBeReadDoesNotFailTheStep(t *testing.T) {
	h := checkHost(t)
	h.base().addLock(h.clock.now, "db1", 5*time.Minute, 4242)
	h.base().checkState.gone = map[string]bool{"L1": true}
	l, code := h.lockLine()
	assertCode(t, code, exitOK)
	if l.status != "OK" || !strings.Contains(l.message, "1 lock disappeared while reading") {
		t.Errorf("%+v", l)
	}
}

func TestWithoutTheNameOfThisHostTheLocksStepFails(t *testing.T) {
	h := checkHost(t)
	h.deps.hostname = func() (string, error) { return "", errors.New("no name") }
	l, code := h.lockLine()
	assertCode(t, code, exitAgentError)
	if l.status != "ERROR" || !strings.Contains(l.message, "no name") {
		t.Errorf("%+v", l)
	}
}

func TestTheLocksOfAnUnreachableStorageAreAnErrorOfTheStep(t *testing.T) {
	h := checkHost(t)
	h.base().checkState.unlockCode = 0
	h.base().script = printsOn("list", "Fatal: dial tcp 192.0.2.1:443: connect: connection refused")
	l, code := h.lockLine()
	assertCode(t, code, exitAgentError)
	if l.status != "ERROR" || !strings.Contains(l.message, "BACKEND_UNAVAILABLE") {
		t.Errorf("%+v", l)
	}
}

// Rule "read-data запускает лёгкую проверку целостности".

func TestReadDataRunsResticCheckOfThatPercentAsTheServiceUser(t *testing.T) {
	h := checkHost(t)
	code, _, _ := h.repoCheck("base", "--read-data", "5")
	assertCode(t, code, exitOK)
	calls := h.restic.callsTo(h.path("base"), "check")
	if len(calls) != 1 || !slices.Contains(calls[0].args, "--read-data-subset=5%") {
		t.Fatalf("check calls %+v", calls)
	}
	if calls[0].runAs == nil || calls[0].runAs.UID != serviceUID {
		t.Errorf("restic check ran as %+v", calls[0].runAs)
	}
}

func TestTheIntegrityLineComesLastAfterTheLocks(t *testing.T) {
	h := checkHost(t)
	_, stdout, _ := h.repoCheck("base", "--read-data", "5")
	lines, summary := parseOut(t, stdout)
	last := lines[len(lines)-1]
	if last.check != checkIntegrity || last.status != "OK" || lines[len(lines)-2].check != checkLocks {
		t.Errorf("lines %v", lines)
	}
	if summary != "summary: 7 ok, 0 warnings, 0 errors, 0 skipped" {
		t.Errorf("summary %q", summary)
	}
}

func TestErrorsOfTheIntegrityAreAnErrorWithTheCountOfRestic(t *testing.T) {
	h := checkHost(t)
	h.base().checkState.checkCode = 1
	h.base().checkState.checkStderr = []string{"Fatal: pack abc contains 1 errors"}
	code, stdout, _ := h.repoCheck("base", "--read-data", "5")
	assertCode(t, code, exitAgentError)
	lines, _ := parseOut(t, stdout)
	l := line(t, lines, checkIntegrity, "base")
	if l.status != "ERROR" || !strings.Contains(l.message, "contains 1 errors") || !strings.Contains(l.hint, "09-troubleshooting.md#repositoryintegrity") {
		t.Errorf("%+v", l)
	}
}

func TestAPercentThatIsNotAWholeNumberFrom1To100IsAUsageErrorBeforeTheStorage(t *testing.T) {
	for _, value := range []string{"0", "101", "-5", "5.5", "abc"} {
		h := checkHost(t)
		code, stdout, stderr := h.repoCheck("base", "--read-data", value)
		assertCode(t, code, exitUsage)
		if !strings.Contains(stderr, "--read-data") || stdout != "" || h.restic.backendCalls() != 0 || len(h.restic.subs()) != 0 {
			t.Errorf("%s: stdout %q stderr %q calls %v", value, stdout, stderr, h.restic.subs())
		}
	}
}

// V0 is not merged: a stub.
func TestCredentialsThatCannotWriteGetAnExplanationInsteadOfTheTextOfRestic(t *testing.T) {
	h := checkHost(t)
	h.base().checkState.checkCode = 1
	h.base().checkState.checkStderr = []string{"Fatal: unable to create lock in backend: client.PutObject: Access Denied."}
	_, stdout, _ := h.repoCheck("base", "--read-data", "5")
	lines, _ := parseOut(t, stdout)
	l := line(t, lines, checkIntegrity, "base")
	for _, want := range []string{"restic check needs to create a lock in the repository and these credentials cannot write", "checking with read-only credentials is not supported yet", "restic said: Access Denied"} {
		if l.status != "ERROR" || !strings.Contains(l.message, want) {
			t.Errorf("message lacks %q: %+v", want, l)
		}
	}
	if !strings.Contains(l.hint, "09-troubleshooting.md#repositoryintegrity") || strings.Contains(stdout, "unable to create lock") {
		t.Errorf("hint %q, stdout %q", l.hint, stdout)
	}
}

func TestAnErrorBeforeTheIntegrityStepSkipsIt(t *testing.T) {
	h := checkHost(t)
	h.base().wrongPassword = true
	_, stdout, _ := h.repoCheck("base", "--read-data", "5")
	lines, _ := parseOut(t, stdout)
	if l := line(t, lines, checkIntegrity, "base"); l.status != "SKIP" {
		t.Errorf("%+v", l)
	}
	if len(h.restic.callsTo(h.path("base"), "check")) != 0 {
		t.Error("restic check was called")
	}
}

func TestACheckOfAnUnreachableStorageInTheIntegrityStepIsToldAsTheStorageIs(t *testing.T) {
	h := checkHost(t)
	h.base().checkState.checkCode = 1
	h.base().checkState.checkStderr = []string{"Fatal: dial tcp 192.0.2.1:443: connect: connection refused"}
	_, stdout, _ := h.repoCheck("base", "--read-data", "5")
	lines, _ := parseOut(t, stdout)
	if l := line(t, lines, checkIntegrity, "base"); l.status != "ERROR" || !strings.Contains(l.message, "BACKEND_UNAVAILABLE") {
		t.Errorf("%+v", l)
	}
}

func TestWithReadDataTheCheckHasNoTimeLimitUnlessGiven(t *testing.T) {
	h := checkHost(t)
	vc := newVirtualClock()
	h.deps.clock = vc
	code, _, _ := h.sudo("repo", "check", "base", "--config", "C", "--read-data", "5")
	assertCode(t, code, exitOK)
	if vc.hasTimer(2*time.Minute) || vc.hasTimer(time.Hour) {
		t.Errorf("timers %v: the whole check is limited", vc.waits)
	}
	h = checkHost(t)
	vc = newVirtualClock()
	h.deps.clock = vc
	code, _, _ = h.sudo("repo", "check", "base", "--config", "C", "--read-data", "5", "--timeout", "1h")
	if !vc.hasTimer(time.Hour) || vc.hasTimer(2*time.Minute) || code != exitOK {
		t.Errorf("code %d, timers %v: --timeout is not honoured", code, vc.waits)
	}
}

func TestWithoutReadDataTheWholeCheckIsLimitedToTwoMinutes(t *testing.T) {
	h := checkHost(t)
	vc := newVirtualClock()
	h.deps.clock = vc
	h.sudo("repo", "check", "base", "--config", "C")
	if !vc.hasTimer(2 * time.Minute) {
		t.Error("no timer of 2m")
	}
}

// Rule "repo check без имени проверяет все репозитории хоста по очереди".

// fragmentRepo is a repository of D/agent.d, initialised, announced.
func (h *setupHost) fragmentRepo(name string) *fakeRepo {
	h.t.Helper()
	url := h.path(name)
	h.write(h.path("secrets/"+name+".pass"), "pw\n", 0o600)
	h.write(filepath.Join(h.agentD(), "repo-"+name+".yaml"),
		"repositories:\n  - {name: "+name+", url: "+url+", password_file: "+h.path("secrets/"+name+".pass")+"}\n", 0o640)
	repo := h.restic.repo(url)
	repo.initialized = true
	repo.id = "REPO-" + strings.ToUpper(name)
	return repo
}

func TestEveryRepositoryIsCheckedInTheOrderOfTheConfig(t *testing.T) {
	h := checkHost(t)
	h.fragmentRepo("obj")
	h.fragmentRepo("arch")
	h.announce(hostcheck.AnnouncedRepository{Name: "base", RepositoryID: "REPO-A"}, hostcheck.AnnouncedRepository{Name: "obj", RepositoryID: "REPO-OBJ"}, hostcheck.AnnouncedRepository{Name: "arch", RepositoryID: "REPO-ARCH"})
	code, stdout, _ := h.repoCheck()
	assertCode(t, code, exitOK)
	lines, summary := parseOut(t, stdout)
	var subjects []string
	for _, l := range lines {
		subjects = append(subjects, l.subject)
	}
	// the fragments follow the main config in the order of their file names
	want := strings.Repeat("base ", 6) + strings.Repeat("arch ", 6) + strings.Repeat("obj ", 6)
	if got := strings.Join(subjects, " ") + " "; got != want {
		t.Errorf("subjects %q", got)
	}
	if summary != "summary: 18 ok, 0 warnings, 0 errors, 0 skipped" {
		t.Errorf("summary %q", summary)
	}
}

func TestTheErrorOfOneRepositoryDoesNotStopTheOthers(t *testing.T) {
	h := checkHost(t)
	h.fragmentRepo("obj").wrongPassword = true
	h.announce(hostcheck.AnnouncedRepository{Name: "base", RepositoryID: "REPO-A"})
	code, stdout, _ := h.repoCheck()
	assertCode(t, code, exitAgentError)
	lines, _ := parseOut(t, stdout)
	if l := line(t, lines, checkPassword, "obj"); l.status != "ERROR" {
		t.Errorf("%+v", l)
	}
	for _, l := range lines {
		if l.subject == "base" && l.status != "OK" {
			t.Errorf("%+v", l)
		}
	}
}

func TestTheWorstResultDecidesTheExitCode(t *testing.T) {
	h := checkHost(t)
	obj := h.fragmentRepo("obj")
	obj.addLock(h.clock.now, "db1", 30*time.Hour, 4242)
	h.announce(hostcheck.AnnouncedRepository{Name: "base", RepositoryID: "REPO-A"}, hostcheck.AnnouncedRepository{Name: "obj", RepositoryID: "REPO-OBJ"})
	code, _, _ := h.repoCheck()
	assertCode(t, code, exitWarnings)
}

// noRepositoriesNote is the informational note of the OK line (owner decision
// of A8d-2: no repositories is not a warning).
const noRepositoriesNote = "no repositories configured; add one with `sudo sard-agent repo add <name> <address>`"

func TestWithoutRepositoriesTheCheckIsOneOKLine(t *testing.T) {
	h := checkHost(t)
	h.cfg.Repositories = nil
	h.saveConfig()
	code, stdout, _ := h.repoCheck()
	assertCode(t, code, exitOK)
	lines, summary := parseOut(t, stdout)
	if len(lines) != 1 || lines[0].status != "OK" || lines[0].message != noRepositoriesNote || lines[0].hint != "" || summary != "summary: 1 ok, 0 warnings, 0 errors, 0 skipped" {
		t.Errorf("lines %v, summary %q", lines, summary)
	}
	if h.restic.backendCalls() != 0 {
		t.Errorf("restic %v", h.restic.subs())
	}
}

func TestAnUnknownNameNamesTheConnectedRepositoriesAndPrintsNothingToStdout(t *testing.T) {
	h := checkHost(t)
	code, stdout, stderr := h.repoCheck("nope")
	assertRefusal(t, code, stderr, exitUsage, "REPOSITORY_UNKNOWN")
	if stdout != "" || !strings.Contains(stderr, "base") {
		t.Errorf("stdout %q stderr %q", stdout, stderr)
	}
}

func TestAnInterruptEndsTheCheckWithoutASummary(t *testing.T) {
	h := checkHost(t)
	ctx, cancel := context.WithCancel(context.Background())
	h.base().script = func(sub string, _ []string) (string, int, bool) {
		cancel()
		return "", 0, false
	}
	h.deps.euid = 0
	var out, errOut bytes.Buffer
	code := runRepoWithDeps(ctx, h.subst([]string{"check", "base", "--config", "C"}), &out, &errOut, h.deps)
	assertRefusal(t, code, errOut.String(), exitTemporary, "INTERRUPTED")
	if strings.Contains(out.String(), "summary:") {
		t.Errorf("stdout %q", out.String())
	}
}

// Rule "Права repo check".

func TestRootRunsResticAsTheServiceUserAndTheServiceUserAsItself(t *testing.T) {
	h := checkHost(t)
	code, _, _ := h.repoCheck("base")
	assertCode(t, code, exitOK)
	for _, c := range h.restic.calls {
		if c.runAs == nil || c.runAs.UID != serviceUID || c.runAs.GID != serviceUID {
			t.Errorf("restic %v ran as %+v", c.args, c.runAs)
		}
	}
	h = checkHost(t)
	code, _, _ = h.asService("repo", "check", "base", "--config", "C")
	assertCode(t, code, exitOK)
	for _, c := range h.restic.calls {
		if c.runAs != nil {
			t.Errorf("restic %v ran as %+v", c.args, c.runAs)
		}
	}
}

func TestAnotherUserMayNotRunRepoCheck(t *testing.T) {
	h := checkHost(t)
	code, stdout, stderr := h.asOther("repo", "check", "base", "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "PRIVILEGES_REQUIRED")
	if !strings.Contains(stderr, "sudo sard-agent repo check") || stdout != "" || len(h.restic.subs()) != 0 {
		t.Errorf("stdout %q stderr %q calls %v", stdout, stderr, h.restic.subs())
	}
}

func TestRootWithoutTheServiceUserGetsNoCheck(t *testing.T) {
	h := checkHost(t)
	h.people = nil
	code, _, stderr := h.repoCheck("base")
	assertRefusal(t, code, stderr, exitUsage, "SERVICE_USER_UNKNOWN")
	if len(h.restic.subs()) != 0 {
		t.Errorf("restic %v", h.restic.subs())
	}
}

func TestACheckWithoutAUsableResticIsAnAgentError(t *testing.T) {
	h := checkHost(t)
	h.cfg.Restic.Path = h.path("missing-restic")
	h.saveConfig()
	code, stdout, stderr := h.repoCheck("base")
	assertRefusal(t, code, stderr, exitAgentError, "RESTIC_NOT_FOUND")
	if stdout != "" {
		t.Errorf("stdout %q", stdout)
	}
}

func TestACheckReadsNothingBackIntoTheHost(t *testing.T) {
	h := checkHost(t)
	h.base().addLock(h.clock.now, "db1", 30*time.Hour, 4242)
	before := h.hostTree()
	h.repoCheck()
	h.assertHostUnchanged(before)
}

func TestTheJSONOfACheckIsOneObjectPerLineWithoutASummary(t *testing.T) {
	h := checkHost(t)
	code, stdout, _ := h.repoCheck("base", "--json")
	assertCode(t, code, exitOK)
	rows := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(rows) != 6 || strings.Contains(stdout, "summary") || !strings.HasPrefix(rows[0], `{"check":"repository.storage","subject":"base","status":"ok"`) {
		t.Errorf("stdout %q", stdout)
	}
}

func TestARepositoryHeldByAnotherProcessExplainsWhyResticCheckCouldNotRun(t *testing.T) {
	h := checkHost(t)
	h.base().checkState.checkCode = 11
	code, stdout, _ := h.repoCheck("base", "--read-data", "5")
	assertCode(t, code, exitWarnings)
	lines, _ := parseOut(t, stdout)
	l := line(t, lines, checkIntegrity, "base")
	if l.status != "WARN" || !strings.Contains(l.message, "locked by another process") || !strings.Contains(l.message, "repeat") || strings.Contains(l.message, "BACKEND_REFUSED") {
		t.Errorf("%+v", l)
	}
	if !strings.Contains(l.hint, "repo check base --read-data 5") {
		t.Errorf("hint %q", l.hint)
	}
}

func TestAnOtherFailureOfResticCheckIsToldAsTheStorageTellsIt(t *testing.T) {
	h := checkHost(t)
	h.base().checkState.checkCode = 12
	_, stdout, _ := h.repoCheck("base", "--read-data", "5")
	lines, _ := parseOut(t, stdout)
	if l := line(t, lines, checkIntegrity, "base"); l.status != "ERROR" || !strings.Contains(l.message, "WRONG_PASSWORD") {
		t.Errorf("%+v", l)
	}
}

func TestAnUnreadableLockInsideALockReadIsCountedAndTheNextOnesAreRead(t *testing.T) {
	h := checkHost(t)
	h.base().addLock(h.clock.now, "db1", 5*time.Minute, 1)
	h.base().addLock(h.clock.now, "db2", 6*time.Minute, 2)
	h.base().checkState.gone = map[string]bool{"L1": true}
	l, _ := h.lockLine()
	if !strings.Contains(l.message, "1 lock (db2 6m)") || !strings.Contains(l.message, "1 lock disappeared") {
		t.Errorf("%+v", l)
	}
}
