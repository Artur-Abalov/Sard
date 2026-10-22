// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"encoding/json"
	"strings"
	"syscall"
	"testing"
)

// Rule "Хост без программ PostgreSQL предупреждён строкой пропуска и не
// получает ненулевой код" (Ч31, ЧВ11).

// withoutPostgres leaves the host with no tool of PostgreSQL.
func (h *setupHost) withoutPostgres() {
	for _, tool := range []string{"pg_dump", "psql", "pg_dumpall", "pg_restore"} {
		h.progs.remove(pgBin + "/" + tool + " --version")
	}
}

const noClient = "PostgreSQL client tools are not installed"

func TestWithoutPostgreSQLToolsTheLineIsSkippedWithAMessageAndAHint(t *testing.T) {
	h := fullHost(t)
	h.withoutPostgres()
	lines, summary, code := h.checkLines()
	assertCode(t, code, exitOK)
	l := line(t, lines, "plugin.programs", "postgresql")
	if l.status != "SKIP" || !strings.HasPrefix(l.message, noClient) ||
		!strings.Contains(l.message, "PostgreSQL sources on this host will fail") ||
		!strings.Contains(l.hint, "postgresql-client") || !strings.Contains(l.hint, "docs/plugins/postgresql.md") {
		t.Errorf("%+v", l)
	}
	if l := line(t, lines, "plugin.programs", "files"); l.status != "OK" {
		t.Errorf("%+v", l)
	}
	if summary != "summary: 27 ok, 0 warnings, 0 errors, 3 skipped" {
		t.Errorf("summary %q", summary)
	}
}

func TestInJSONTheMissingToolsAreAStatusOfSkippedWithoutNewFields(t *testing.T) {
	h := fullHost(t)
	h.withoutPostgres()
	code, stdout, _ := h.check("--json")
	assertCode(t, code, exitOK)
	found := false
	for _, obj := range jsonRows(t, stdout) {
		if obj["status"] == "warning" || obj["status"] == "error" {
			t.Errorf("%v", obj)
		}
		if obj["check"] == "plugin.programs" && obj["subject"] == "postgresql" {
			found = true
			assertSkippedPostgres(t, obj)
		}
	}
	if !found {
		t.Error("no line of postgresql")
	}
}

func assertSkippedPostgres(t *testing.T, obj map[string]any) {
	t.Helper()
	if len(obj) != 5 || obj["status"] != "skipped" || !strings.HasPrefix(obj["message"].(string), noClient) || !strings.Contains(obj["hint"].(string), "postgresql-client") {
		t.Errorf("%v", obj)
	}
}

// jsonRows are the objects of the lines of a JSON output.
func jsonRows(t *testing.T, stdout string) []map[string]any {
	t.Helper()
	var rows []map[string]any
	for _, row := range strings.Split(strings.TrimSpace(stdout), "\n") {
		var obj map[string]any
		if err := json.Unmarshal([]byte(row), &obj); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, obj)
	}
	return rows
}

func TestTheSkipOfPostgreSQLHasOneReasonInEveryMode(t *testing.T) {
	for _, c := range []struct {
		name string
		run  func(h *setupHost) (int, string, string)
	}{
		{"root", func(h *setupHost) (int, string, string) { return h.check() }},
		{"service user", func(h *setupHost) (int, string, string) {
			return h.asService("check", "--config", "C")
		}},
		{"offline", func(h *setupHost) (int, string, string) { return h.check("--offline") }},
	} {
		h := fullHost(t)
		h.withoutPostgres()
		_, stdout, _ := c.run(h)
		lines, _ := parseOut(t, strings.ReplaceAll(stdout, "some checks need root: run sudo sard-agent check\n", ""))
		l := line(t, lines, "plugin.programs", "postgresql")
		if l.status != "SKIP" || !strings.HasPrefix(l.message, noClient) || strings.Contains(l.message, "needs root") || strings.Contains(l.message, "offline") {
			t.Errorf("%s: %+v", c.name, l)
		}
	}
}

func TestWithTheToolsInstalledTheLineIsNeverSkipped(t *testing.T) {
	h := fullHost(t)
	_, stdout, _ := h.asService("check", "--offline", "--config", "C")
	lines, _ := parseOut(t, strings.ReplaceAll(stdout, "some checks need root: run sudo sard-agent check\n", ""))
	if l := line(t, lines, "plugin.programs", "postgresql"); l.status != "OK" {
		t.Errorf("%+v", l)
	}
}

func TestADirectoryTheServiceCannotEnterHasNoPgDump(t *testing.T) {
	h := fullHost(t)
	h.withoutPostgres()
	h.deps.pathEnv = "/root/pg:" + pgBin
	h.progs.mu.Lock()
	h.progs.answers["/root/pg/pg_dump --version"] = progAnswer{err: &fsPathError{path: "/root/pg/pg_dump", err: syscall.EACCES}}
	h.progs.mu.Unlock()
	lines, _, code := h.checkLines()
	assertCode(t, code, exitOK)
	if l := line(t, lines, "plugin.programs", "postgresql"); l.status != "SKIP" || !strings.HasPrefix(l.message, noClient) {
		t.Errorf("%+v", l)
	}
}

func TestAnIncompleteClientIsStillAnError(t *testing.T) {
	h := fullHost(t)
	h.progs.remove(pgBin + "/pg_dumpall --version")
	lines, _, code := h.checkLines()
	assertCode(t, code, exitAgentError)
	if l := line(t, lines, "plugin.programs", "postgresql"); l.status != "ERROR" {
		t.Errorf("%+v", l)
	}
}

func TestTheToolsAreRunAsTheServiceUser(t *testing.T) {
	h := fullHost(t)
	h.check()
	h.progs.mu.Lock()
	defer h.progs.mu.Unlock()
	for _, c := range h.progs.calls {
		if strings.HasPrefix(c.key, pgBin+"/pg_dump") && (c.runAs == nil || c.runAs.UID != serviceUID) {
			t.Errorf("%s ran as %+v", c.key, c.runAs)
		}
	}
}

func TestThePathOfTheAgentIsWalkedInOrder(t *testing.T) {
	h := fullHost(t)
	h.deps.pathEnv = "::/opt/pg16/bin:" + pgBin
	h.installPostgres("/opt/pg16/bin", "16.4")
	h.withoutPostgres()
	lines, _, _ := h.checkLines()
	if l := line(t, lines, "plugin.programs", "postgresql"); !strings.Contains(l.message, "/opt/pg16/bin/pg_dump 16.4") || strings.Contains(l.message, pgBin+"/") {
		t.Errorf("%+v", l)
	}
}
