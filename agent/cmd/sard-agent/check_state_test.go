// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Rules "Служба запущена и шаги в работе", "Нет репозиториев" and "SELinux
// проверяется, только если он enforcing".

func TestARunningServiceAndTheStepsInProgress(t *testing.T) {
	h := fullHost(t)
	journal := filepath.Join(h.stateDir(), "journal")
	h.write(filepath.Join(journal, "aa.json"), "{}", 0o600)
	h.write(filepath.Join(journal, "bb.json"), "{}", 0o600)
	h.write(filepath.Join(journal, ".tmp-123"), "{}", 0o600)
	lines, _, _ := h.checkLines()
	if l := line(t, lines, "service.running", "-"); l.status != "OK" || !strings.Contains(l.message, "2026-10-14 08:00:00 UTC") {
		t.Errorf("%+v", l)
	}
	if l := line(t, lines, "service.steps", "-"); l.status != "OK" || l.message != "2 steps in progress" {
		t.Errorf("%+v", l)
	}
}

func TestWithoutSystemdTheServiceIsNotChecked(t *testing.T) {
	h := fullHost(t)
	h.sd.present = false
	lines, _, code := h.checkLines()
	assertCode(t, code, exitOK)
	if l := line(t, lines, "service.running", "-"); l.status != "SKIP" || !strings.Contains(l.message, "systemd") || l.hint == "" {
		t.Errorf("%+v", l)
	}
	if l := line(t, lines, "service.steps", "-"); l.status != "OK" {
		t.Errorf("%+v", l)
	}
	if h.progs.ran("systemctl") != 0 {
		t.Errorf("systemctl was run: %v", h.progs.keys())
	}
}

func TestOnlySystemctlShowIsEverAskedOfSystemd(t *testing.T) {
	h := fullHost(t)
	h.check()
	if h.sd.touched() {
		t.Errorf("systemd was asked: %v", h.sd.calls)
	}
	for _, key := range h.progs.keys() {
		if strings.HasPrefix(key, "systemctl") && !strings.HasPrefix(key, "systemctl show ") {
			t.Errorf("%s", key)
		}
	}
}

// Decision of the owner, A8d-2: no repositories is not a warning.
func TestWithoutRepositoriesTheCheckIsOKWithANote(t *testing.T) {
	h := fullHost(t)
	h.cfg.Repositories = nil
	h.saveHostConfig()
	lines, _, code := h.checkLines()
	assertCode(t, code, exitOK)
	l := line(t, lines, "repository.storage", "-")
	if l.status != "OK" || l.message != noRepositoriesNote || l.hint != "" {
		t.Errorf("%+v", l)
	}
	if h.restic.backendCalls() != 0 {
		t.Errorf("restic %v", h.restic.subs())
	}
}

const enforcing = "Enforcing\n"

// A getenforce that failed has not answered, whatever it wrote before.
func TestAGetenforceThatFailedIsTakenAsSELinuxNotEnforcing(t *testing.T) {
	h := fullHost(t)
	h.onSELinux()
	h.progs.answers["getenforce"] = progAnswer{out: enforcing, err: errors.New("killed")}
	lines, _, _ := h.checkLines()
	for _, id := range []string{"selinux.labels", "selinux.denials"} {
		if l := line(t, lines, id, "-"); l.status != "OK" || l.message != "SELinux is not enforcing" {
			t.Errorf("%+v", l)
		}
	}
	if h.progs.ran("matchpathcon") != 0 || h.progs.ran("ausearch") != 0 {
		t.Errorf("programs %v", h.progs.keys())
	}
}

func TestWithoutEnforcingSELinuxIsNotAsked(t *testing.T) {
	h := fullHost(t)
	lines, _, _ := h.checkLines()
	for _, id := range []string{"selinux.labels", "selinux.denials"} {
		if l := line(t, lines, id, "-"); l.status != "OK" || l.message != "SELinux is not enforcing" {
			t.Errorf("%+v", l)
		}
	}
	if h.progs.ran("matchpathcon") != 0 || h.progs.ran("ausearch") != 0 {
		t.Errorf("programs %v", h.progs.keys())
	}
}

// onSELinux makes SELinux enforce with every file labeled right and no denial.
func (h *setupHost) onSELinux() {
	h.progs.answer("getenforce", enforcing)
	h.progs.prefix("matchpathcon -V", "")
	h.progs.prefix("ausearch -m avc -ts yesterday", "<no matches>\n")
}

func TestAFileWithTheWrongLabelIsAWarningWithRestorecon(t *testing.T) {
	h := fullHost(t)
	h.onSELinux()
	pg := h.path("secrets/pg")
	h.progs.prefix("matchpathcon -V", pg+" has context unconfined_u:object_r:tmp_t:s0, should be system_u:object_r:etc_t:s0\n")
	lines, _, code := h.checkLines()
	assertCode(t, code, exitWarnings)
	l := line(t, lines, "selinux.labels", "-")
	if l.status != "WARN" || !strings.Contains(l.message, pg) || !strings.Contains(l.message, "tmp_t") || !strings.Contains(l.message, "etc_t") || !strings.Contains(l.hint, "sudo restorecon -Rv "+pg) {
		t.Errorf("%+v", l)
	}
	if l := line(t, lines, "selinux.denials", "-"); l.status != "OK" {
		t.Errorf("%+v", l)
	}
}

func TestMatchpathconIsAskedAboutTheFilesOfTheAgent(t *testing.T) {
	h := fullHost(t)
	h.onSELinux()
	h.check()
	var key string
	for _, k := range h.progs.keys() {
		if strings.HasPrefix(k, "matchpathcon") {
			key = k
		}
	}
	for _, want := range []string{h.cfgPath, h.secretsDir(), h.path("secrets/pg"), h.tlsDir(), h.cfg.TLS.KeyFile, h.stateDir(), h.cfg.Restic.CacheDir, h.cfg.Restic.Path} {
		if !strings.Contains(key+" ", want+" ") {
			t.Errorf("%q lacks %s", key, want)
		}
	}
}

func TestSevenDenialsOfTheLast24HoursAreAWarningWithTheFirstFive(t *testing.T) {
	h := fullHost(t)
	h.onSELinux()
	var out strings.Builder
	for i := 1; i <= 7; i++ {
		out.WriteString(avcAt("restic", i, h.clock.now.Add(-time.Hour).Unix()) + "\n----\n")
	}
	out.WriteString(avcAt("restic", 8, h.clock.now.Add(-30*time.Hour).Unix()) + "\n")
	h.progs.prefix("ausearch -m avc -ts yesterday", out.String())
	lines, _, code := h.checkLines()
	assertCode(t, code, exitWarnings)
	l := line(t, lines, "selinux.denials", "-")
	if l.status != "WARN" || !strings.Contains(l.message, "7 denials") || !strings.Contains(l.message, "pg5") || strings.Contains(l.message, "pg6") || !strings.Contains(l.hint, "ausearch") {
		t.Errorf("%+v", l)
	}
}

func avcAt(comm string, n int, at int64) string {
	return fmt.Sprintf(`type=AVC msg=audit(%d.123:%d): avc:  denied  { read } for  pid=1 comm=%q name="pg%d" scontext=a:b:c:s0 tcontext=a:b:d:s0 tclass=file permissive=0`, at, n, comm, n)
}

func TestWithoutAusearchTheDenialsAreSkippedAndThePackageIsNamed(t *testing.T) {
	h := fullHost(t)
	h.onSELinux()
	h.progs.removePrefix("ausearch")
	lines, _, code := h.checkLines()
	assertCode(t, code, exitOK)
	if l := line(t, lines, "selinux.denials", "-"); l.status != "SKIP" || !strings.Contains(l.hint, "audit") {
		t.Errorf("%+v", l)
	}
}
