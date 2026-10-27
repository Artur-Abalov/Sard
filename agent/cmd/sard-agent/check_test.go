// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/secrets"
)

// Scenarios of A8d-2 (docs/specs/agent/host-check.feature): sard-agent check.
// The check of the server (A8d-3) is not there yet: its four lines are SKIP.

// Rule "sard-agent check печатает по строке на каждую проверку хоста".

func checkIDsOf(lines []outLine) []string {
	var ids []string
	for _, l := range lines {
		ids = append(ids, l.check)
	}
	return ids
}

var notYet = []string{"tls.role", "restore.locations"}

func TestAHealthyHostHasALineForEveryCheckInTheOrderOfTheList(t *testing.T) {
	h := fullHost(t)
	lines, summary, code := h.checkLines()
	assertCode(t, code, exitOK)
	if got := checkIDsOf(lines); !slices.Equal(got, wantIDs) {
		t.Fatalf("checks\n%v\nwant\n%v", got, wantIDs)
	}
	for _, l := range lines {
		assertHealthyLine(t, l)
	}
	if summary != "summary: 29 ok, 0 warnings, 0 errors, 2 skipped" {
		t.Errorf("summary %q", summary)
	}
}

// assertHealthyLine: a line of a healthy host is OK without a hint, or one of
// the checks that are not made yet, SKIP with the reason in the hint.
func assertHealthyLine(t *testing.T, l outLine) {
	t.Helper()
	wantSkip := slices.Contains(notYet, l.check)
	if (l.status == "SKIP") != wantSkip || (!wantSkip && l.status != "OK") {
		t.Errorf("%+v", l)
	}
	if (l.status == "OK") != (l.hint == "") {
		t.Errorf("hint of %+v", l)
	}
}

func TestTheConditionalChecksAreSkippedWithTheirReason(t *testing.T) {
	h := fullHost(t)
	lines, _, code := h.checkLines()
	assertCode(t, code, exitOK)
	if l := line(t, lines, "tls.role", "-"); l.status != "SKIP" || l.message != "agent role is checked once V1 is available" {
		t.Errorf("%+v", l)
	}
	if l := line(t, lines, "restore.locations", "-"); l.status != "SKIP" || l.message != "restore locations are checked once R2 is available" {
		t.Errorf("%+v", l)
	}
}

func TestACheckNeverRunsResticCheckAndHasNoIntegrityLine(t *testing.T) {
	h := fullHost(t)
	lines, _, _ := h.checkLines()
	if slices.Contains(checkIDsOf(lines), "repository.integrity") {
		t.Error("repository.integrity is there")
	}
	for _, sub := range h.restic.subs() {
		if sub == "check" {
			t.Errorf("restic %v", h.restic.subs())
		}
	}
}

// Rule "Каждая ошибка и предупреждение check сопровождаются подсказкой".

// breakAndCheck arranges the host, runs check and returns the line of the check.
func breakAndCheck(t *testing.T, arrange func(h *setupHost), check, subject string) (outLine, int) {
	t.Helper()
	h := fullHost(t)
	arrange(h)
	lines, _, code := h.checkLines()
	return line(t, lines, check, subject), code
}

func TestHostErrorsHaveAHint(t *testing.T) {
	other := func(h *setupHost) testPKI { return h.certHolding(200 * 24 * time.Hour) }
	for _, c := range []struct {
		name    string
		arrange func(h *setupHost)
		check   string
		hint    string
	}{
		{"no service user", func(h *setupHost) { h.people = []hostsetup.User{{Name: "root"}} }, "service.user", "09-troubleshooting.md#serviceuser"},
		{"broken yaml", func(h *setupHost) { h.write(h.cfgPath, "server: [", 0o640) }, "config.load", "09-troubleshooting.md#configload"},
		{"a fragment with a server key", func(h *setupHost) {
			h.writeFragment("x.yaml", "server:\n  address: x:1\n", 0o640)
		}, "config.load", "09-troubleshooting.md#configload"},
		{"a repository in two places", func(h *setupHost) {
			h.writeFragment("repo-base.yaml", "repositories:\n  - {name: base, url: /x, password_file: /y}\n", 0o640)
		}, "config.names", "sudo sard-agent repo remove base"},
		{"a secret that points nowhere", func(h *setupHost) {
			h.cfg.Secrets["pg"] = h.path("secrets/none")
			h.saveHostConfig()
		}, "config.references", "sudo sard-agent secret set pg"},
		{"a key open to others", func(h *setupHost) { ok(h.t, os.Chmod(h.cfg.TLS.KeyFile, 0o644)) }, "files.permissions", "sudo chmod 0600"},
		{"a secret of root", func(h *setupHost) { h.fsys.setOwner(h.path("secrets/pg"), 0, 0) }, "files.permissions", "sudo chown sard-agent"},
		{"a secrets directory open to others", func(h *setupHost) { ok(h.t, os.Chmod(h.secretsDir(), 0o755)) }, "files.permissions", "sudo chmod 0700"},
		{"no restic", func(h *setupHost) {
			h.cfg.Restic.Path = h.path("missing-restic")
			h.saveHostConfig()
		}, "restic.binary", "09-troubleshooting.md#resticbinary"},
		{"an old restic", func(h *setupHost) { h.restic.version = "0.18.0" }, "restic.binary", "09-troubleshooting.md#resticbinary"},
		{"the key of another certificate", func(h *setupHost) { h.write(h.cfg.TLS.KeyFile, string(other(h).key), 0o600) }, "tls.identity", "sudo sard-agent enroll --force"},
		{"no certificate in the config", func(h *setupHost) {
			h.cfg.TLS.CertFile = ""
			h.saveHostConfig()
		}, "tls.identity", "sudo sard-agent enroll --server"},
		{"a certificate of another CA", func(h *setupHost) { h.write(h.cfg.TLS.CAFile, string(other(h).ca), 0o644) }, "tls.chain", "sudo sard-agent enroll --force"},
		{"a certificate that expired yesterday", func(h *setupHost) {
			h.installIdentity(newTestPKI(h.t, h.clock.now.Add(-90*24*time.Hour), h.clock.now.Add(-24*time.Hour)))
		}, "tls.expiry", "sudo sard-agent enroll --force"},
		{"a certificate valid from tomorrow", func(h *setupHost) {
			h.installIdentity(newTestPKI(h.t, h.clock.now.Add(24*time.Hour), h.clock.now.Add(100*24*time.Hour)))
		}, "tls.expiry", "09-troubleshooting.md#tlsexpiry"},
		{"a secret that is a directory", func(h *setupHost) {
			ok(h.t, os.Remove(h.path("secrets/pg")))
			ok(h.t, os.Mkdir(h.path("secrets/pg"), 0o700))
			h.deps.readOwned = readAsTester
		}, "secret.readable", "sudo sard-agent secret set pg"},
		{"a secret that is a link", func(h *setupHost) {
			ok(h.t, os.Remove(h.path("secrets/pg")))
			h.write(h.path("secrets/real"), "value", 0o600)
			ok(h.t, os.Symlink(h.path("secrets/real"), h.path("secrets/pg")))
			h.deps.readOwned = readAsTester
		}, "secret.readable", "sudo sard-agent secret set pg"},
		{"psql beside pg_dump is missing", func(h *setupHost) { h.progs.remove(pgBin + "/psql --version") }, "plugin.programs", "postgresql-client"},
		{"pg_dumpall beside pg_dump is missing", func(h *setupHost) { h.progs.remove(pgBin + "/pg_dumpall --version") }, "plugin.programs", "postgresql-client"},
		{"the service failed", func(h *setupHost) {
			h.progs.answer(showKey, "ActiveState=failed\nSubState=failed\nNRestarts=0\nActiveEnterTimestamp=\n")
		}, "service.running", "sudo systemctl start sard-agent"},
	} {
		subject := "-"
		switch c.check {
		case "secret.readable":
			subject = "pg"
		case "plugin.programs":
			subject = "postgresql"
		}
		l, code := breakAndCheck(t, c.arrange, c.check, subject)
		if l.status != "ERROR" || !strings.Contains(l.hint, c.hint) {
			t.Errorf("%s: %+v", c.name, l)
		}
		if code != exitAgentError {
			t.Errorf("%s: exit code %d", c.name, code)
		}
	}
}

// readAsTester is secrets.ReadOwned for the files the test wrote as its own user.
func readAsTester(path string, _ uint32) ([]byte, error) {
	return secrets.ReadOwned(path, uint32(os.Getuid()))
}

func TestHostWarningsHaveAHint(t *testing.T) {
	for _, c := range []struct {
		name    string
		arrange func(h *setupHost)
		check   string
		subject string
		hint    string
	}{
		{"a config the group can write", func(h *setupHost) { ok(h.t, os.Chmod(h.cfgPath, 0o660)) }, "files.permissions", "-", "sudo chmod g-w"},
		{"a fragment open to all", func(h *setupHost) {
			h.writeFragment("repo-x.yaml", "repositories: []\n", 0o666)
			ok(h.t, os.Chmod(h.path("agent.d/repo-x.yaml"), 0o666))
		}, "files.permissions", "-", "sudo chmod 0640"},
		{"a restic older than the shipped one", func(h *setupHost) { h.restic.version = "0.19.0" }, "restic.binary", "-", "restic.path"},
		{"a certificate with 10 days of a year left", func(h *setupHost) { h.installIdentity(h.certEnding(10 * 24 * time.Hour)) }, "tls.expiry", "-", "sudo sard-agent status"},
		{"no pg_restore", func(h *setupHost) { h.progs.remove(pgBin + "/pg_restore --version") }, "plugin.programs", "postgresql", "postgresql-client"},
		{"pg_dumpall of another major", func(h *setupHost) { h.progs.answer(pgBin+"/pg_dumpall --version", pgVersionLine("pg_dumpall", "15.2")) }, "plugin.programs", "postgresql", "postgresql-client"},
		{"a service that restarted", func(h *setupHost) {
			h.progs.answer(showKey, strings.Replace(showActive, "NRestarts=0", "NRestarts=3", 1))
		}, "service.running", "-", "journalctl -u sard-agent"},
		{"a journal that cannot be read", func(h *setupHost) { h.fsys.failOn, h.fsys.failPath = "readdir", "journal" }, "service.steps", "-", "09-troubleshooting.md#servicesteps"},
	} {
		l, code := breakAndCheck(t, c.arrange, c.check, c.subject)
		if l.status != "WARN" || !strings.Contains(l.hint, c.hint) {
			t.Errorf("%s: %+v", c.name, l)
		}
		if code != exitWarnings {
			t.Errorf("%s: exit code %d", c.name, code)
		}
	}
}

func TestAConfigErrorSkipsTheChecksThatDependOnIt(t *testing.T) {
	h := fullHost(t)
	h.write(h.cfgPath, "server: [", 0o640)
	lines, _, code := h.checkLines()
	assertCode(t, code, exitAgentError)
	skipped := []string{"config.names", "config.references", "files.permissions", "tls.identity", "tls.chain", "tls.expiry",
		"server.reachable", "server.tls", "server.accepted", "clock.skew", "secret.readable", "repository.storage"}
	for _, id := range skipped {
		l := line(t, lines, id, lines[slices.IndexFunc(lines, func(l outLine) bool { return l.check == id })].subject)
		if l.status != "SKIP" || l.message != "not checked: config.load failed" || l.hint == "" {
			t.Errorf("%+v", l)
		}
	}
	for _, id := range []string{"restic.binary", "plugin.declared", "service.running", "service.steps"} {
		if l := line(t, lines, id, "-"); l.status == "SKIP" {
			t.Errorf("%+v", l)
		}
	}
	if n := h.restic.backendCalls(); n != 0 {
		t.Errorf("restic %v", h.restic.subs())
	}
}

func TestAReferenceToAMissingFileNamesTheKeyAndThePath(t *testing.T) {
	l, _ := breakAndCheck(t, func(h *setupHost) {
		h.cfg.Repositories[0].PasswordFile = h.path("secrets/none.pass")
		h.saveHostConfig()
	}, "config.references", "-")
	if l.status != "ERROR" || !strings.Contains(l.message, "repositories.base.password_file") || !strings.Contains(l.message, "secrets/none.pass") {
		t.Errorf("%+v", l)
	}
}

func TestOneNameInTwoPlacesNamesBothFiles(t *testing.T) {
	l, _ := breakAndCheck(t, func(h *setupHost) {
		h.writeFragment("secret-pg.yaml", "secrets:\n  pg: "+h.path("secrets/pg")+"\n", 0o640)
	}, "config.names", "-")
	if l.status != "ERROR" || !strings.Contains(l.message, "DUPLICATE_NAME") || !strings.Contains(l.message, "agent.yaml") || !strings.Contains(l.message, "agent.d/secret-pg.yaml") {
		t.Errorf("%+v", l)
	}
}

func TestEveryViolationOfPermissionsIsInOneLine(t *testing.T) {
	h := fullHost(t)
	ok(t, os.Chmod(h.cfg.TLS.KeyFile, 0o644))
	ok(t, os.Chmod(h.path("secrets/pg"), 0o640))
	lines, _, _ := h.checkLines()
	n := 0
	for _, l := range lines {
		if l.check != "files.permissions" {
			continue
		}
		n++
		for _, p := range []string{h.cfg.TLS.KeyFile, h.path("secrets/pg")} {
			if l.status != "ERROR" || !strings.Contains(l.message, p) || !strings.Contains(l.hint, "sudo chmod 0600 "+p) {
				t.Errorf("%+v lacks %s", l, p)
			}
		}
	}
	if n != 1 {
		t.Errorf("%d lines of files.permissions", n)
	}
}

func TestAnInconvenientButAllowedModeIsNoViolation(t *testing.T) {
	l, code := breakAndCheck(t, func(h *setupHost) {
		h.writeFragment("repo-x.yaml", "repositories: []\n", 0o644)
	}, "files.permissions", "-")
	if l.status != "OK" || code != exitOK {
		t.Errorf("%+v, code %d", l, code)
	}
}

func TestACertificateWith31DaysOfAYearLeftIsStillFine(t *testing.T) {
	l, _ := breakAndCheck(t, func(h *setupHost) { h.installIdentity(h.certEnding(31 * 24 * time.Hour)) }, "tls.expiry", "-")
	if l.status != "OK" || !strings.Contains(l.message, "2026-11-14") {
		t.Errorf("%+v", l)
	}
}

func TestACertificateWith30DaysOfAYearLeftIsWarned(t *testing.T) {
	l, _ := breakAndCheck(t, func(h *setupHost) { h.installIdentity(h.certEnding(30 * 24 * time.Hour)) }, "tls.expiry", "-")
	if l.status != "WARN" {
		t.Errorf("%+v", l)
	}
}

func TestTheWarningAboutTheExpiryPointsAtTheStatusNotAtANewRegistration(t *testing.T) {
	l, _ := breakAndCheck(t, func(h *setupHost) { h.installIdentity(h.certEnding(10 * 24 * time.Hour)) }, "tls.expiry", "-")
	if !strings.Contains(l.message, "expires in 10 days") || !strings.Contains(l.message, "has not been renewed") ||
		strings.Contains(l.message, "automatic renewal is not available yet") {
		t.Errorf("%+v", l)
	}
}

func TestThePluginsTheAgentDeclaresAreNamedWithTheirVersions(t *testing.T) {
	h := fullHost(t)
	lines, _, _ := h.checkLines()
	l := line(t, lines, "plugin.declared", "-")
	if l.status != "OK" || !strings.Contains(l.message, "files "+version) || !strings.Contains(l.message, "postgresql "+version) {
		t.Errorf("%+v", l)
	}
}

func TestTheToolsOfPostgreSQLAreNamedWithPathsAndVersion(t *testing.T) {
	h := fullHost(t)
	lines, _, _ := h.checkLines()
	l := line(t, lines, "plugin.programs", "postgresql")
	for _, tool := range []string{"pg_dump", "psql", "pg_dumpall", "pg_restore"} {
		if !strings.Contains(l.message, pgBin+"/"+tool+" 16.4") {
			t.Errorf("message lacks %s: %s", tool, l.message)
		}
	}
}

func TestASecretIsCheckedWithoutShowingItsValue(t *testing.T) {
	h := fullHost(t)
	h.write(h.path("secrets/pg"), "SECRET-MARKER-1", 0o600)
	code, stdout, stderr := h.check()
	assertCode(t, code, exitOK)
	assertNoSecrets(t, stdout, stderr)
	h.assertValuesHidden(stdout, stderr)
	if strings.Contains(stdout, "SECRET-MARKER-1") {
		t.Errorf("stdout %q", stdout)
	}
	lines, _ := parseOut(t, stdout)
	if l := line(t, lines, "secret.readable", "pg"); l.status != "OK" {
		t.Errorf("%+v", l)
	}
}

func TestAnEmptySecretFileIsAnError(t *testing.T) {
	l, _ := breakAndCheck(t, func(h *setupHost) { h.write(h.path("secrets/pg"), "", 0o600) }, "secret.readable", "pg")
	if l.status != "ERROR" || !strings.Contains(l.message, "empty") {
		t.Errorf("%+v", l)
	}
}

func TestSecretsAreReadAsTheServiceUser(t *testing.T) {
	h := fullHost(t)
	h.check()
	var asked []ownedRead
	for _, r := range h.readOwned {
		if r.path == h.path("secrets/pg") {
			asked = append(asked, r)
		}
	}
	if len(asked) != 1 || asked[0].uid != serviceUID {
		t.Errorf("reads %v", h.readOwned)
	}
}
