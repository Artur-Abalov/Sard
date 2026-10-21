// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
)

// The contract of the restore state machine the other tests of the rig take for
// granted: when a step stops, what the journal holds, what the plugin leaves
// alone (R2c-2, ВК22, ВК27).

// Scenario: Шаг, у которого кончился контекст, больше не запускает программ.
func TestAStepWhoseContextEndedStartsNoMoreProgramsAndIsInterrupted(t *testing.T) {
	points := map[string]func(h *rpHostRig, cancel func()){
		"the version of pg_restore": func(h *rpHostRig, cancel func()) { h.server.versionHook = cancel },
		"the probe of the server":   func(h *rpHostRig, cancel func()) { h.server.hooks["probe"] = cancel },
		"the system identifier":     func(h *rpHostRig, cancel func()) { h.server.hooks["system identifier"] = cancel },
		"the table of contents":     func(h *rpHostRig, cancel func()) { h.server.hooks["pg_restore -l"] = cancel },
	}
	for name, set := range points {
		t.Run(name, func(t *testing.T) {
			h := newPGHost(t)
			ctx, cancel := context.WithCancel(context.Background())
			var atEnd atomic.Int64
			set(h, func() {
				atEnd.Store(h.started.Load())
				cancel()
			})
			_, f := h.runCtx(ctx, h.request())
			hasPrefix(t, f, "INTERRUPTED (check)")
			if started := h.started.Load(); started != atEnd.Load() {
				t.Errorf("%d programs were started, %d when the context ended", started, atEnd.Load())
			}
			h.untouched(t)
		})
	}
}

// A blank line in the answer about the roles is no role and no end of the answer.
func TestBlankLinesInTheAnswerAboutTheRolesAreSkipped(t *testing.T) {
	h := newPGHost(t)
	h.server.answers["oid = 10"] = "\nf|app_rw\n  \nt|postgres"
	_, f := h.run(h.withGlobals())
	hasPrefix(t, f, "ROLES_EXIST (check)")
	mentions(t, f.Error(), `"app_rw"`)
	h.untouched(t)
}

// Scenario: Отказанный скрипт глобальных объектов снимает роли с записи журнала.
func TestAGlobalsScriptThatWasRefusedLeavesNoRolesInTheJournal(t *testing.T) {
	h := newPGHost(t)
	h.server.globalsError = "psql:<stdin>:3: ERROR:  syntax error"
	_, f := h.run(h.withGlobals())
	hasPrefix(t, f, "PG_GLOBALS_FAILED (globals)")
	if got := h.host.journal.current(); strings.Contains(got, "app_rw") || strings.Contains(got, "app_ro") {
		t.Errorf("the entry still announces the roles that were not created: %s", got)
	}
	h.noTrace()
}

// Scenario: Отказанный CREATE DATABASE снимает базу с записи журнала.
func TestACreateDatabaseThatWasRefusedLeavesNoDatabaseInTheJournal(t *testing.T) {
	h := newPGHost(t)
	h.server.createError = "psql:<stdin>:1: ERROR:  permission denied to create database"
	_, f := h.run(h.request())
	hasPrefix(t, f, "PG_CREATE_DATABASE (create-database)")
	if got := h.host.journal.current(); strings.Contains(got, rpNewDatabase) {
		t.Errorf("the entry still announces the database that was not created: %s", got)
	}
	h.noTrace()
}

// A rollback that undid everything leaves the entry to the agent to remove:
// the plugin writes nothing more to the journal.
func TestARollbackThatUndidEverythingWritesNothingMoreToTheJournal(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreError = "pg_restore: error: could not execute query"
	var atRollback atomic.Int64
	atRollback.Store(-1)
	h.server.hooks["drop database "+rpNewDatabase] = func() { atRollback.Store(int64(h.host.journal.count())) }
	_, f := h.run(h.request())
	hasPrefix(t, f, "PG_RESTORE_FAILED (restore)")
	if atRollback.Load() < 0 {
		t.Fatal("the rollback did not drop the database")
	}
	if got := int64(h.host.journal.count()); got != atRollback.Load() {
		t.Errorf("%d entries saved, %d when the rollback began", got, atRollback.Load())
	}
	if len(f.Leftover) != 0 || f.Cleanup != "" || f.Retain {
		t.Errorf("failure %+v", f.RestoreError)
	}
	h.noTrace()
}

// Scenario: Повторный откат записывает в журнал только то, что изменилось.
func TestARecoveryThatDroppedSomeRolesKeepsTheOthersInTheJournal(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[rpNewDatabase] = 20001
	h.server.roles["app_rw"], h.server.roles["app_ro"] = 20002, 20003
	h.server.dropDBError = "ERROR:  database is being accessed by other users"
	h.server.dropRoleError["app_ro"] = "ERROR:  role cannot be dropped"
	got := h.recover(leftByAKill(`,"database":{"name":"` + rpNewDatabase + `","oid":20001},"roles":[{"name":"app_rw","oid":20002},{"name":"app_ro","oid":20003}]`))
	leftBehind(t, got, []string{"database " + rpNewDatabase, "role app_ro"}, true)
	if entry := h.host.journal.current(); !strings.Contains(entry, "app_ro") || strings.Contains(entry, "app_rw") || !strings.Contains(entry, rpNewDatabase) {
		t.Errorf("the entry kept for the next start: %q", entry)
	}
}
