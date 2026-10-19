// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore_test

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

func (h *pgHost) setGlobals(sql string) { h.repo.content[pgGlobalsID+"/app.globals.sql"] = sql }

// globalsRun is the psql that applied the global objects.
func (h *pgHost) globalsRun(t *testing.T) pgCall {
	t.Helper()
	for _, c := range h.server.ran("psql") {
		if c.has("--single-transaction") {
			return c
		}
	}
	t.Fatal("the global objects were not applied")
	return pgCall{}
}

// before fails the test unless the first event happened before the second.
func before(t *testing.T, events []string, first, second string) {
	t.Helper()
	if i, j := slices.Index(events, first), slices.Index(events, second); i < 0 || j < 0 || i > j {
		t.Errorf("%q is not before %q in %v", first, second, events)
	}
}

// Scenario: С выбором глобальных объектов роли появляются до базы.
func TestWithGlobalsTheRolesAppearBeforeTheDatabase(t *testing.T) {
	h := newPGHost(t)
	out, f := h.run(h.withGlobals())
	if f != nil {
		t.Fatal(f)
	}
	if out.Roles != 2 || !h.server.hasRole("app_rw") || !h.server.hasRole("app_ro") {
		t.Errorf("output %+v", out)
	}
	before(t, h.server.eventLog(), "globals", "create database")
}

func TestWithGlobalsOwnersRightsAndTablespacesAreKept(t *testing.T) {
	h := newPGHost(t)
	if _, f := h.run(h.withGlobals()); f != nil {
		t.Fatal(f)
	}
	if c := h.lastRestore(); c.has("--no-owner") || c.has("--no-acl") || c.has("--no-tablespaces") {
		t.Errorf("owners and rights are left out with global objects: %q", c.Args)
	}
}

// Scenario: Глобальные объекты выполняются одной транзакцией с остановкой на первой ошибке.
func TestTheGlobalsRunInOneTransactionThatStopsAtTheFirstError(t *testing.T) {
	h := newPGHost(t)
	if _, f := h.run(h.withGlobals()); f != nil {
		t.Fatal(f)
	}
	c := h.globalsRun(t)
	vi := slices.Index(c.Args, "-v")
	if !c.has("-X") || vi < 0 || c.Args[vi+1] != "ON_ERROR_STOP=1" || !strings.Contains(c.conninfo(), "dbname='postgres'") {
		t.Errorf("args %q", c.Args)
	}
	if c.Stdin != defaultGlobalsSQL {
		t.Errorf("script %q", c.Stdin)
	}
}

// Scenario: Ошибка в глобальных объектах откатывает их все и не создаёт базу.
func TestAnErrorInTheGlobalsCreatesNeitherRolesNorDatabase(t *testing.T) {
	h := newPGHost(t)
	h.server.globalsError = `psql:<stdin>:2: ERROR:  role "nosuch" does not exist`
	_, f := h.run(h.withGlobals())
	hasPrefix(t, f, "PG_GLOBALS_FAILED (globals)")
	mentions(t, f.Error(), `role "nosuch" does not exist`, "nothing was restored")
	if slices.Contains(h.server.eventLog(), "create database") {
		t.Error("the database was created")
	}
	h.noTrace()
}

// Scenario: Роль, которая уже есть на целевом сервере, — отказ до изменений.
func TestARoleThatExistsOnTheTargetIsRefusedBeforeAnyChange(t *testing.T) {
	h := newPGHost(t)
	h.server.roles["app_rw"] = 16400
	h.mark()
	_, f := h.run(h.withGlobals())
	hasPrefix(t, f, "ROLES_EXIST (check)")
	mentions(t, f.Error(), `"app_rw"`)
	if strings.Contains(f.Error(), `"app_ro"`) {
		t.Errorf("a role that does not exist is named: %s", f.Error())
	}
	h.untouched(t)
}

// Scenario: Операторы о начальном суперпользователе целевого кластера пропускаются.
func TestStatementsAboutTheBootstrapSuperuserAreSkipped(t *testing.T) {
	h := newPGHost(t)
	h.server.bootstrap = "pgadmin"
	h.server.roles = map[string]uint32{"pgadmin": 10, "restorer": 16390}
	h.mark()
	h.setGlobals("CREATE ROLE pgadmin;\nALTER ROLE pgadmin WITH SUPERUSER INHERIT;\nALTER ROLE pgadminx WITH NOSUPERUSER;\nCREATE ROLE app_rw;\nGRANT pgadmin TO app_rw;\n")
	out, f := h.run(h.withGlobals())
	if f != nil {
		t.Fatal(f)
	}
	script := h.globalsRun(t).Stdin
	if strings.Contains(script, "CREATE ROLE pgadmin;") || strings.Contains(script, "ALTER ROLE pgadmin WITH") {
		t.Errorf("script %q", script)
	}
	for _, kept := range []string{"ALTER ROLE pgadminx WITH NOSUPERUSER;", "CREATE ROLE app_rw;", "GRANT pgadmin TO app_rw;"} {
		if !strings.Contains(script, kept) {
			t.Errorf("script %q lost %s", script, kept)
		}
	}
	if out.Roles != 1 || !strings.Contains(h.logText(), `the statements about the role "pgadmin" are skipped`) {
		t.Errorf("roles %d, log:\n%s", out.Roles, h.logText())
	}
}

// Scenario: Табличные пространства в глобальных объектах — отказ до изменений.
func TestTablespacesInTheGlobalsAreRefusedBeforeAnyChange(t *testing.T) {
	h := newPGHost(t)
	h.setGlobals("CREATE ROLE app_rw;\nCREATE TABLESPACE fast OWNER app_rw LOCATION '/mnt/fast';\n")
	_, f := h.run(h.withGlobals())
	hasPrefix(t, f, "GLOBALS_TABLESPACES (check)")
	mentions(t, f.Error(), `"fast"`, "without restore_globals")
	h.untouched(t)
}

func TestACommandOfPsqlInTheGlobalsIsRefused(t *testing.T) {
	h := newPGHost(t)
	h.setGlobals("CREATE ROLE app_rw;\n\\! touch /tmp/pwned\n")
	_, f := h.run(h.withGlobals())
	hasPrefix(t, f, "PG_GLOBALS_FAILED (check)")
	mentions(t, f.Error(), "line 2", "command of psql")
	h.untouched(t)
}

func TestTheGuardsOfPgDumpAreNotCommandsToRefuse(t *testing.T) {
	h := newPGHost(t)
	h.setGlobals("\\restrict abc123\nCREATE ROLE app_rw;\n\\unrestrict abc123\n")
	if _, f := h.run(h.withGlobals()); f != nil {
		t.Fatal(f)
	}
	if !strings.Contains(h.globalsRun(t).Stdin, `\restrict abc123`) {
		t.Error("the guard was cut")
	}
}

func TestRoleNamesAreReadAsPgDumpallWritesThem(t *testing.T) {
	h := newPGHost(t)
	h.setGlobals("CREATE ROLE \"App \"\"Rw\"\"\";\nCREATE ROLE plain WITH LOGIN;\nALTER ROLE plain WITH LOGIN;\n")
	out, f := h.run(h.withGlobals())
	if f != nil {
		t.Fatal(f)
	}
	if out.Roles != 2 || !h.server.hasRole(`App "Rw"`) {
		t.Errorf("roles %d", out.Roles)
	}
}

func TestAFileOfGlobalsThatCannotBeReadIsAFailureOfTheCheck(t *testing.T) {
	h := newPGHost(t)
	h.repo.dumpHook = func(_ context.Context, _, path string, _ io.Writer) error {
		if strings.HasSuffix(path, ".sql") {
			return restic.ErrNetwork
		}
		return nil
	}
	_, f := h.run(h.withGlobals())
	hasPrefix(t, f, "BACKEND_UNAVAILABLE (check)")
	h.untouched(t)
}

func TestAFileOfGlobalsThatIsTooLargeIsRefused(t *testing.T) {
	h := newPGHost(t)
	h.repo.dumpHook = func(_ context.Context, _, path string, w io.Writer) error {
		chunk := []byte(strings.Repeat("-", 1<<20))
		for i := 0; i < 65; i++ {
			if _, err := w.Write(chunk); err != nil {
				return err
			}
		}
		return nil
	}
	_, f := h.run(h.withGlobals())
	hasPrefix(t, f, "PG_GLOBALS_FAILED (check)")
	mentions(t, f.Error(), "64 MiB")
}

// Scenario: psql убит после COMMIT глобальных объектов: роли не скрываются.
func TestAPsqlThatDiesAfterTheGlobalsCommittedNeverHidesTheRoles(t *testing.T) {
	h := newPGHost(t)
	h.server.killAfterGlobals = true
	_, f := h.run(h.withGlobals())
	if f == nil {
		t.Fatal("restored")
	}
	if !h.server.hasRole("app_rw") {
		t.Fatal("rig: the globals did not create app_rw")
	}
	mentions(t, f.Error(), "CLEANUP_FAILED", "app_rw")
	if strings.Contains(f.Error(), "nothing was restored") {
		t.Errorf("claims nothing was restored while role app_rw exists: %s", f.Error())
	}
}
