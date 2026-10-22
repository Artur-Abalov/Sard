// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"errors"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Rules "Хост без программ PostgreSQL..." and "Каждая ошибка..." (Ч18, Ч31): the
// check of the programs of the plugin, which the plugin owns (R2c-3, В1).

var _ sdk.ProgramChecker = Plugin{}

// programHost answers `<path> --version` from versions (tool path -> version);
// a path that is not in it does not start. ran counts the runs of each path.
type programHost struct {
	path     []string
	versions map[string]string
	ran      map[string]int
}

func (h *programHost) Path() []string { return h.path }

func (h *programHost) Run(path string, args ...string) (string, error) {
	if h.ran == nil {
		h.ran = map[string]int{}
	}
	h.ran[path]++
	version, ok := h.versions[path]
	if !ok || len(args) != 1 || args[0] != "--version" {
		return "", errors.New("does not start")
	}
	if version == "" {
		return "no version here", nil
	}
	return path[strings.LastIndex(path, "/")+1:] + " (PostgreSQL) " + version, nil
}

// client puts the four tools of a PostgreSQL client of the version into dir.
func client(dir, version string) map[string]string {
	m := map[string]string{}
	for _, tool := range []string{"pg_dump", "psql", "pg_dumpall", "pg_restore"} {
		m[dir+"/"+tool] = version
	}
	return m
}

func checkPrograms(h *programHost) sdk.ProgramsCheck { return Plugin{}.CheckPrograms(h) }

func TestTheProgramsOfPostgreSQLAreNamedWithPathsAndVersions(t *testing.T) {
	l := checkPrograms(&programHost{path: []string{"/usr/bin"}, versions: client("/usr/bin", "16.4")})
	want := "pg_dump /usr/bin/pg_dump 16.4, psql /usr/bin/psql 16.4, pg_dumpall /usr/bin/pg_dumpall 16.4, pg_restore /usr/bin/pg_restore 16.4"
	if l.Status != sdk.ProgramsOK || l.Hint != "" || l.Message != want {
		t.Fatalf("%+v", l)
	}
}

func TestWithoutPgDumpTheCheckIsSkippedWithTheOwnersText(t *testing.T) {
	l := checkPrograms(&programHost{path: []string{"/usr/bin", "/bin"}})
	if l.Status != sdk.ProgramsSkip || !strings.HasPrefix(l.Message, "PostgreSQL client tools are not installed") ||
		!strings.Contains(l.Message, "PostgreSQL sources on this host will fail until pg_dump is in the PATH or pg_dump_path is set in the source") {
		t.Fatalf("%+v", l)
	}
	if !strings.Contains(l.Hint, "postgresql-client") || !strings.Contains(l.Hint, "docs/plugins/postgresql.md") {
		t.Errorf("hint %q", l.Hint)
	}
}

func TestAnIncompleteClientIsAnErrorOrAWarning(t *testing.T) {
	without := func(tool string) map[string]string {
		m := client("/usr/bin", "16.4")
		delete(m, "/usr/bin/"+tool)
		return m
	}
	another := func(tool, version string) map[string]string {
		m := client("/usr/bin", "16.4")
		m["/usr/bin/"+tool] = version
		return m
	}
	both := without("psql")
	delete(both, "/usr/bin/pg_restore")
	for _, c := range []struct {
		name     string
		versions map[string]string
		status   sdk.ProgramsStatus
		in       string
	}{
		{"no psql", without("psql"), sdk.ProgramsError, "psql is not beside pg_dump"},
		{"no pg_dumpall", without("pg_dumpall"), sdk.ProgramsError, "pg_dumpall is not beside pg_dump"},
		{"no pg_restore", without("pg_restore"), sdk.ProgramsWarn, "pg_restore is not beside pg_dump"},
		{"another major", another("pg_dumpall", "15.2"), sdk.ProgramsWarn, "the tools are of different major versions"},
		{"the same major", another("psql", "16.3"), sdk.ProgramsOK, "psql /usr/bin/psql 16.3"},
		{"no psql and no pg_restore", both, sdk.ProgramsError, "pg_restore is not beside pg_dump"},
	} {
		l := checkPrograms(&programHost{path: []string{"/usr/bin"}, versions: c.versions})
		if l.Status != c.status || !strings.Contains(l.Message, c.in) {
			t.Errorf("%s: %+v", c.name, l)
		}
		if (c.status != sdk.ProgramsOK) != strings.Contains(l.Hint, "postgresql-client") {
			t.Errorf("%s: hint %q", c.name, l.Hint)
		}
	}
}

func TestTheFaultsOfTheClientFollowTheToolsInTheMessage(t *testing.T) {
	m := client("/usr/bin", "16.4")
	delete(m, "/usr/bin/psql")
	m["/usr/bin/pg_dumpall"] = "15.2"
	l := checkPrograms(&programHost{path: []string{"/usr/bin"}, versions: m})
	want := "pg_dump /usr/bin/pg_dump 16.4, pg_dumpall /usr/bin/pg_dumpall 15.2, pg_restore /usr/bin/pg_restore 16.4" +
		": psql is not beside pg_dump; the tools are of different major versions"
	if l.Status != sdk.ProgramsError || l.Message != want || l.Hint != programsHint {
		t.Errorf("%+v, want message %q", l, want)
	}
}

func TestAToolWithoutAVersionIsNamedByItsPathAndDoesNotChangeTheMajor(t *testing.T) {
	m := client("/usr/bin", "16.4")
	m["/usr/bin/psql"] = ""
	l := checkPrograms(&programHost{path: []string{"/usr/bin"}, versions: m})
	want := "pg_dump /usr/bin/pg_dump 16.4, psql /usr/bin/psql, pg_dumpall /usr/bin/pg_dumpall 16.4, pg_restore /usr/bin/pg_restore 16.4"
	if l.Status != sdk.ProgramsOK || l.Message != want {
		t.Errorf("%+v, want message %q", l, want)
	}
}

func TestAMissingToolDoesNotTakePartInTheMajorComparison(t *testing.T) {
	m := client("/usr/bin", "16.4")
	delete(m, "/usr/bin/psql")
	m["/other/psql"] = "15.2"
	l := checkPrograms(&programHost{path: []string{"/usr/bin", "/other"}, versions: m})
	if l.Status != sdk.ProgramsError || strings.Contains(l.Message, "different major versions") {
		t.Errorf("%+v", l)
	}
}

func TestOnlyTheFirstDirectoryWithPgDumpIsLookedAtForTheTools(t *testing.T) {
	m := client("/usr/bin", "16.4")
	for k, v := range client("/opt/pg15/bin", "15.2") {
		m[k] = v
	}
	h := &programHost{path: []string{"/usr/bin", "/opt/pg15/bin"}, versions: m}
	l := checkPrograms(h)
	if l.Status != sdk.ProgramsOK || strings.Contains(l.Message, "/opt/pg15") {
		t.Errorf("%+v", l)
	}
	for path, n := range h.ran {
		if strings.HasPrefix(path, "/opt/pg15/") && n != 0 {
			t.Errorf("%s was run", path)
		}
	}
}

func TestPgDumpIsRunOnce(t *testing.T) {
	h := &programHost{path: []string{"/usr/bin"}, versions: client("/usr/bin", "16.4")}
	checkPrograms(h)
	if h.ran["/usr/bin/pg_dump"] != 1 {
		t.Errorf("pg_dump was run %d times", h.ran["/usr/bin/pg_dump"])
	}
}

func TestADirectoryAfterTheFirstWithPgDumpIsNotLookedAtWhenItHasNone(t *testing.T) {
	m := client("/opt/bin", "16.4")
	l := checkPrograms(&programHost{path: []string{"/usr/bin", "/opt/bin"}, versions: m})
	if l.Status != sdk.ProgramsOK || !strings.Contains(l.Message, "/opt/bin/pg_dump 16.4") {
		t.Errorf("%+v", l)
	}
}
