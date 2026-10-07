// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

type fakeSyslog struct {
	lines []string
	err   error
}

func (f *fakeSyslog) Write(line string) error {
	if f.err != nil {
		return f.err
	}
	f.lines = append(f.lines, line)
	return nil
}

func env(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }

func TestActorIsTheSudoUserWhenThereIsOne(t *testing.T) {
	a := hostsetup.ActorFrom(env(map[string]string{"SUDO_USER": "alice", "SUDO_UID": "1000"}), "root", 0)
	if a.Name != "alice" || a.UID != "1000" {
		t.Fatalf("actor %+v", a)
	}
}

func TestActorIsTheProcessUserWithoutSudo(t *testing.T) {
	for name, kv := range map[string]map[string]string{
		"nothing":      {},
		"only a name":  {"SUDO_USER": "alice"},
		"only a uid":   {"SUDO_UID": "1000"},
		"empty values": {"SUDO_USER": "", "SUDO_UID": ""},
	} {
		a := hostsetup.ActorFrom(env(kv), "root", 0)
		if a.Name != "root" || a.UID != "0" {
			t.Errorf("%s: actor %+v", name, a)
		}
	}
}

func TestAuditLineHasActionKindNameAndWho(t *testing.T) {
	sl := &fakeSyslog{}
	actor := hostsetup.Actor{Name: "alice", UID: "1000"}
	if err := hostsetup.Audit(sl, actor, "secret", "db", "added"); err != nil {
		t.Fatal(err)
	}
	if len(sl.lines) != 1 {
		t.Fatalf("lines %q", sl.lines)
	}
	for _, want := range []string{"secret db added", "alice", "1000"} {
		if !strings.Contains(sl.lines[0], want) {
			t.Errorf("line %q lacks %q", sl.lines[0], want)
		}
	}
}

func TestPasswordRevealAuditLineNamesThePassword(t *testing.T) {
	sl := &fakeSyslog{}
	if err := hostsetup.Audit(sl, hostsetup.Actor{Name: "a", UID: "1"}, "repository", "base", "password revealed"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sl.lines[0], "repository base password revealed") {
		t.Fatalf("line %q", sl.lines[0])
	}
}

func TestAnUnavailableSyslogOnlyWarns(t *testing.T) {
	var warn bytes.Buffer
	open := func() (hostsetup.Auditor, error) { return nil, errors.New("no /dev/log") }
	hostsetup.Record(&warn, open, hostsetup.Actor{Name: "a", UID: "1"}, "secret", "db", "added")
	if !strings.Contains(warn.String(), "not recorded in the system log") || !strings.Contains(warn.String(), "no /dev/log") {
		t.Fatalf("warning %q", warn.String())
	}
	warn.Reset()
	sl := &fakeSyslog{err: errors.New("broken pipe")}
	hostsetup.Record(&warn, func() (hostsetup.Auditor, error) { return sl, nil }, hostsetup.Actor{Name: "a", UID: "1"}, "secret", "db", "added")
	if !strings.Contains(warn.String(), "not recorded in the system log") {
		t.Fatalf("warning %q", warn.String())
	}
}

func TestRecordWritesOneLineAndStaysQuiet(t *testing.T) {
	var warn bytes.Buffer
	sl := &fakeSyslog{}
	hostsetup.Record(&warn, func() (hostsetup.Auditor, error) { return sl, nil }, hostsetup.Actor{Name: "a", UID: "1"}, "secret", "db", "added")
	if warn.Len() != 0 || len(sl.lines) != 1 {
		t.Fatalf("warning %q, lines %q", warn.String(), sl.lines)
	}
}

func TestAnAuditNameWithALineBreakCannotMakeASecondLine(t *testing.T) {
	sl := &fakeSyslog{}
	ok(t, hostsetup.Audit(sl, hostsetup.Actor{Name: "a", UID: "1"}, "secret", "db\nsecret x added by root (uid 0)", "added"))
	if len(sl.lines) != 1 || strings.Contains(sl.lines[0], "\n") {
		t.Fatalf("lines %q", sl.lines)
	}
	if !strings.Contains(sl.lines[0], `"db\nsecret x added by root (uid 0)"`) {
		t.Fatalf("line %q", sl.lines[0])
	}
}
