// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck_test

import (
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
)

// Rules "Хост без программ PostgreSQL..." and "Каждая ошибка..." (Ч18, Ч31):
// plugin.programs of postgresql.

func tool(name, version string) hostcheck.Tool {
	return hostcheck.Tool{Name: name, Path: "/usr/bin/" + name, Version: version}
}

func missing(name string) hostcheck.Tool { return hostcheck.Tool{Name: name} }

func TestTheProgramsOfPostgreSQLAreNamedWithPathsAndVersions(t *testing.T) {
	l := hostcheck.PostgresLine(tool("pg_dump", "16.4"), tool("psql", "16.4"), tool("pg_dumpall", "16.4"), tool("pg_restore", "16.4"))
	if l.Check != "plugin.programs" || l.Subject != "postgresql" || l.Status != hostcheck.OK || l.Hint != "" {
		t.Fatalf("%+v", l)
	}
	for _, name := range []string{"pg_dump", "psql", "pg_dumpall", "pg_restore"} {
		if !strings.Contains(l.Message, "/usr/bin/"+name+" 16.4") {
			t.Errorf("message lacks %s: %s", name, l.Message)
		}
	}
}

func TestWithoutPgDumpTheLineIsSkippedWithTheOwnersText(t *testing.T) {
	l := hostcheck.PostgresLine(missing("pg_dump"), missing("psql"), missing("pg_dumpall"), missing("pg_restore"))
	if l.Status != hostcheck.Skip || !strings.HasPrefix(l.Message, "PostgreSQL client tools are not installed") ||
		!strings.Contains(l.Message, "PostgreSQL sources on this host will fail until pg_dump is in the PATH or pg_dump_path is set in the source") {
		t.Fatalf("%+v", l)
	}
	if !strings.Contains(l.Hint, "postgresql-client") || !strings.Contains(l.Hint, "docs/plugins/postgresql.md") {
		t.Errorf("hint %q", l.Hint)
	}
}

func TestAnIncompleteClientIsAnErrorOrAWarning(t *testing.T) {
	for _, c := range []struct {
		name                         string
		dump, psql, dumpall, restore hostcheck.Tool
		status                       hostcheck.Status
		in                           string
	}{
		{"no psql", tool("pg_dump", "16.4"), missing("psql"), tool("pg_dumpall", "16.4"), tool("pg_restore", "16.4"), hostcheck.Error, "psql"},
		{"no pg_dumpall", tool("pg_dump", "16.4"), tool("psql", "16.4"), missing("pg_dumpall"), tool("pg_restore", "16.4"), hostcheck.Error, "pg_dumpall"},
		{"no pg_restore", tool("pg_dump", "16.4"), tool("psql", "16.4"), tool("pg_dumpall", "16.4"), missing("pg_restore"), hostcheck.Warn, "pg_restore"},
		{"another major", tool("pg_dump", "16.4"), tool("psql", "16.4"), tool("pg_dumpall", "15.2"), tool("pg_restore", "16.4"), hostcheck.Warn, "15.2"},
		{"the same major", tool("pg_dump", "16.4"), tool("psql", "16.3"), tool("pg_dumpall", "16.4"), tool("pg_restore", "16.1"), hostcheck.OK, "16.3"},
		{"no psql and no pg_restore", tool("pg_dump", "16.4"), missing("psql"), tool("pg_dumpall", "16.4"), missing("pg_restore"), hostcheck.Error, "pg_restore"},
	} {
		l := hostcheck.PostgresLine(c.dump, c.psql, c.dumpall, c.restore)
		if l.Status != c.status || !strings.Contains(l.Message, c.in) {
			t.Errorf("%s: %+v", c.name, l)
		}
		if c.status != hostcheck.OK && !strings.Contains(l.Hint, "postgresql-client") {
			t.Errorf("%s: hint %q", c.name, l.Hint)
		}
	}
}

func TestTheVersionIsReadFromTheAnswerOfTheTool(t *testing.T) {
	for in, want := range map[string]string{
		"pg_dump (PostgreSQL) 16.4":                      "16.4",
		"pg_dump (PostgreSQL) 16.4 (Debian 16.4-1.pgdg)": "16.4",
		"psql (PostgreSQL) 18beta1":                      "18beta1",
		"something else":                                 "",
	} {
		if got := hostcheck.PostgresVersion(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
