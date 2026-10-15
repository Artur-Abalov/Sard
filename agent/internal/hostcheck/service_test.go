// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
)

// Rule "Служба запущена и шаги в работе" (Ч21).

const showActive = "ActiveState=active\nSubState=running\nNRestarts=0\nActiveEnterTimestamp=Wed 2026-10-14 08:00:00 UTC\n"

func TestSystemctlShowIsRead(t *testing.T) {
	s, err := hostcheck.ParseShow(showActive)
	if err != nil || s.ActiveState != "active" || s.SubState != "running" || s.Restarts != 0 || !s.Since.Equal(time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("%+v, %v", s, err)
	}
}

func TestAnAnswerOfSystemctlThatIsNotOneIsRefused(t *testing.T) {
	if _, err := hostcheck.ParseShow("ActiveState=active\nNRestarts=many\n"); err == nil {
		t.Error("NRestarts=many is accepted")
	}
	if _, err := hostcheck.ParseShow("ActiveEnterTimestamp=yesterday\n"); err == nil {
		t.Error("a timestamp that is none is accepted")
	}
}

func TestAServiceThatNeverStartedHasNoStartTime(t *testing.T) {
	if s, err := hostcheck.ParseShow("ActiveState=inactive\nSubState=dead\nNRestarts=0\nActiveEnterTimestamp=\n"); err != nil || !s.Since.IsZero() {
		t.Errorf("%+v, %v", s, err)
	}
}

func TestARunningServiceNamesTheStartAndTheAge(t *testing.T) {
	s, _ := hostcheck.ParseShow(showActive)
	l := hostcheck.ServiceRunningLine(s, tlsNow)
	if l.Check != "service.running" || l.Status != hostcheck.OK || !strings.Contains(l.Message, "running since 2026-10-14 08:00:00 UTC") || !strings.Contains(l.Message, "4h") {
		t.Errorf("%+v", l)
	}
}

func TestARestartedServiceIsAWarning(t *testing.T) {
	s, _ := hostcheck.ParseShow(strings.Replace(showActive, "NRestarts=0", "NRestarts=3", 1))
	l := hostcheck.ServiceRunningLine(s, tlsNow)
	if l.Status != hostcheck.Warn || !strings.Contains(l.Message, "restarted 3 times after failures") || !strings.Contains(l.Hint, "journalctl -u sard-agent") {
		t.Errorf("%+v", l)
	}
}

func TestAServiceThatDoesNotRunIsAnErrorWithTheCommandThatStartsIt(t *testing.T) {
	for _, show := range []string{
		"ActiveState=failed\nSubState=failed\nNRestarts=0\nActiveEnterTimestamp=\n",
		"ActiveState=inactive\nSubState=dead\nNRestarts=0\nActiveEnterTimestamp=\n",
		"ActiveState=activating\nSubState=auto-restart\nNRestarts=5\nActiveEnterTimestamp=\n",
	} {
		s, _ := hostcheck.ParseShow(show)
		l := hostcheck.ServiceRunningLine(s, tlsNow)
		if l.Status != hostcheck.Error || !strings.Contains(l.Hint, "sudo systemctl start sard-agent") || !strings.Contains(l.Hint, "journalctl -u sard-agent") || !strings.Contains(l.Message, s.ActiveState) {
			t.Errorf("%q: %+v", show, l)
		}
	}
}

func TestStepsInProgress(t *testing.T) {
	for n, want := range map[int]string{0: "no steps in progress", 1: "1 steps in progress", 2: "2 steps in progress"} {
		if l := hostcheck.ServiceStepsLine(n, nil); l.Check != "service.steps" || l.Status != hostcheck.OK || l.Message != want {
			t.Errorf("%d: %+v", n, l)
		}
	}
	l := hostcheck.ServiceStepsLine(0, errors.New("open /s/journal: permission denied"))
	if l.Status != hostcheck.Warn || !strings.Contains(l.Message, "permission denied") || !strings.Contains(l.Hint, "09-troubleshooting.md#servicesteps") {
		t.Errorf("%+v", l)
	}
}
