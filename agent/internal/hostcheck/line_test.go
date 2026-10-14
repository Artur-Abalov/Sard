// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
)

// Rule "repo check проверяет репозиторий по шагам" (Ч1): the line of a check.

func TestATextLineHasTheStatusTheCheckTheSubjectAndTheMessage(t *testing.T) {
	var out bytes.Buffer
	p := hostcheck.NewPrinter(&out, false)
	p.Print(hostcheck.Line{Check: "repository.storage", Subject: "base", Status: hostcheck.OK, Message: "storage reachable"})
	p.Print(hostcheck.Line{Check: "restic.binary", Status: hostcheck.Warn, Message: "restic 0.19.0", Hint: "reinstall the agent package"})
	want := "OK repository.storage base storage reachable\nWARN restic.binary - restic 0.19.0\nhint: reinstall the agent package\n"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestTheSummaryCountsEveryStatus(t *testing.T) {
	var out bytes.Buffer
	p := hostcheck.NewPrinter(&out, false)
	for _, s := range []hostcheck.Status{hostcheck.OK, hostcheck.OK, hostcheck.Warn, hostcheck.Error, hostcheck.Skip} {
		p.Print(hostcheck.Line{Check: "x", Status: s, Message: "m"})
	}
	out.Reset()
	p.Finish()
	if out.String() != "summary: 2 ok, 1 warnings, 1 errors, 1 skipped\n" {
		t.Fatalf("got %q", out.String())
	}
}

func TestTheWorstStatusDecidesAndSkipsDoNotCount(t *testing.T) {
	for _, c := range []struct {
		lines []hostcheck.Status
		want  hostcheck.Status
	}{
		{nil, hostcheck.OK},
		{[]hostcheck.Status{hostcheck.OK, hostcheck.Skip}, hostcheck.OK},
		{[]hostcheck.Status{hostcheck.Skip, hostcheck.Warn}, hostcheck.Warn},
		{[]hostcheck.Status{hostcheck.Warn, hostcheck.Error, hostcheck.OK}, hostcheck.Error},
	} {
		p := hostcheck.NewPrinter(&bytes.Buffer{}, false)
		for _, s := range c.lines {
			p.Print(hostcheck.Line{Check: "x", Status: s})
		}
		if got := p.Worst(); got != c.want {
			t.Errorf("%v: worst %v, want %v", c.lines, got, c.want)
		}
	}
}

// Rule "--json" (Ч26): one object per line, exactly five fields.

func decode(t *testing.T, line string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestJSONLinesHaveExactlyTheFiveFieldsAndNoSummary(t *testing.T) {
	var out bytes.Buffer
	p := hostcheck.NewPrinter(&out, true)
	p.Print(hostcheck.Line{Check: "repository.locks", Subject: "base", Status: hostcheck.OK, Message: "no locks"})
	p.Finish()
	first := decode(t, strings.TrimSpace(out.String()))
	want := map[string]any{"check": "repository.locks", "subject": "base", "status": "ok", "message": "no locks", "hint": nil}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("got %v (a summary or extra line would not decode): %q", first, out.String())
	}
}

func TestInJSONAMissingSubjectAndHintAreNullAndAGivenHintIsAString(t *testing.T) {
	var out bytes.Buffer
	hostcheck.NewPrinter(&out, true).Print(hostcheck.Line{Check: "restic.binary", Status: hostcheck.Skip, Message: "m", Hint: "h"})
	got := decode(t, strings.TrimSpace(out.String()))
	if got["subject"] != nil || got["status"] != "skipped" || got["hint"] != "h" {
		t.Errorf("got %v", got)
	}
}

func TestTheStatusNamesOfJSONAreOKWarningErrorSkipped(t *testing.T) {
	var out bytes.Buffer
	p := hostcheck.NewPrinter(&out, true)
	for _, s := range []hostcheck.Status{hostcheck.OK, hostcheck.Warn, hostcheck.Error, hostcheck.Skip} {
		p.Print(hostcheck.Line{Check: "x", Status: s})
	}
	for i, want := range []string{"ok", "warning", "error", "skipped"} {
		if got := strings.Split(strings.TrimSpace(out.String()), "\n")[i]; !strings.Contains(got, `"status":"`+want+`"`) {
			t.Errorf("line %d: %s", i, got)
		}
	}
}

func TestALineIsOneLineWhateverTheMessageHolds(t *testing.T) {
	var out bytes.Buffer
	hostcheck.NewPrinter(&out, false).Print(hostcheck.Line{Check: "x", Status: hostcheck.Error, Message: "a\nb\r\nc", Hint: "h\ni"})
	if got := strings.Count(out.String(), "\n"); got != 2 {
		t.Fatalf("%d lines: %q", got, out.String())
	}
}

func TestTheHelpLinkIsTheAnchorOfTheCheckWithoutDots(t *testing.T) {
	want := "https://github.com/Artur-Abalov/sard/blob/main/docs/operator/09-troubleshooting.md#repositorypassword"
	if got := hostcheck.HelpURL("repository.password"); got != want {
		t.Fatalf("got %q", got)
	}
}
