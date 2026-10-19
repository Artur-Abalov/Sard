// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Scenario: Сбой после создания базы удаляет базу и созданные роли.
func TestAFailureAfterTheDatabaseIsCreatedDropsItAndTheRoles(t *testing.T) {
	tests := []struct {
		name    string
		globals bool
		setup   func(h *pgHost)
		reason  string
		stage   string
	}{
		{"pg_restore fails", false, func(h *pgHost) { h.server.restoreError = "pg_restore: error: could not execute query" }, "PG_RESTORE_FAILED", "restore"},
		{"pg_restore fails with globals", true, func(h *pgHost) { h.server.restoreError = "pg_restore: error: could not execute query" }, "PG_RESTORE_FAILED", "restore"},
		{"the storage stops", true, func(h *pgHost) {
			h.repo.dumpHook = func(_ context.Context, _, path string, w io.Writer) error {
				if path == "/app.dump" && len(h.repo.dumped()) > 1 {
					_, _ = io.WriteString(w, "PGDMP")
					return restic.ErrNetwork
				}
				_, _ = io.WriteString(w, "PGDMP")
				return nil
			}
			h.server.restoreError = "pg_restore: error: unexpected end of file"
		}, "BACKEND_UNAVAILABLE", "restore"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newPGHost(t)
			tt.setup(h)
			req := h.request()
			if tt.globals {
				req = h.withGlobals()
			}
			_, f := h.run(req)
			hasPrefix(t, f, tt.reason+" ("+tt.stage+")")
			mentions(t, f.Error(), "nothing was restored")
			h.noTrace()
			if strings.Contains(f.Error(), "\n") {
				t.Errorf("the message is not one line: %q", f.Error())
			}
		})
	}
}

// Scenario: Сообщение pg_restore цитируется одной строкой.
func TestTheErrorOfPgRestoreIsQuotedInOneLine(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreError = "pg_restore: error: could not execute query: ERROR:  out of memory"
	_, f := h.run(h.request())
	hasPrefix(t, f, "PG_RESTORE_FAILED (restore)")
	mentions(t, f.Error(), "out of memory", "nothing was restored")
}

// Scenario: Отмена шага останавливает pg_restore и откатывает созданное.
func TestCancellingTheStepRollsBackWhatIsCreated(t *testing.T) {
	h := newPGHost(t)
	ctx, cancel := context.WithCancel(context.Background())
	h.server.restoreHook = func(c context.Context, _ pgCall) (int, error) {
		cancel()
		<-c.Done()
		return -1, errors.New("signal: terminated")
	}
	_, f := h.runCtx(ctx, h.request())
	hasPrefix(t, f, "INTERRUPTED (restore)")
	h.noTrace()
}

func TestAStepThatTimesOutRollsBackAsWell(t *testing.T) {
	h := newPGHost(t)
	ctx, cancel := context.WithTimeoutCause(context.Background(), 0, context.DeadlineExceeded)
	defer cancel()
	_, f := h.runCtx(ctx, h.request())
	if f == nil {
		t.Fatal("no failure")
	}
	h.untouched(t)
}

// Scenario: Сбой создания базы откатывает уже созданные роли.
func TestAFailedCreateDatabaseRollsBackTheRoles(t *testing.T) {
	h := newPGHost(t)
	h.server.createError = "psql:<stdin>:1: ERROR:  permission denied to create database"
	_, f := h.run(h.withGlobals())
	hasPrefix(t, f, "PG_CREATE_DATABASE (create-database)")
	mentions(t, f.Error(), "permission denied to create database")
	h.noTrace()
	events := h.server.eventLog()
	if !contains(events, "drop role app_rw") || !contains(events, "drop role app_ro") {
		t.Errorf("events %v", events)
	}
}

func contains(events []string, want string) bool {
	for _, e := range events {
		if e == want {
			return true
		}
	}
	return false
}

// Scenario: База, созданная другим между проверкой и созданием, не трогается.
func TestADatabaseCreatedByAnotherInBetweenIsLeftAlone(t *testing.T) {
	h := newPGHost(t)
	h.server.createError = `psql:<stdin>:1: ERROR:  database "` + pgNewDatabase + `" already exists`
	_, f := h.run(h.request())
	hasPrefix(t, f, "DATABASE_EXISTS (create-database)")
	mentions(t, f.Error(), pgNewDatabase, "nothing was restored")
	for _, e := range h.server.eventLog() {
		if strings.HasPrefix(e, "drop database") || e == "pg_restore" {
			t.Errorf("event %s", e)
		}
	}
	if entries := h.journalEntries(); len(entries) != 0 {
		t.Errorf("journal %v", entries)
	}
}

// Scenario: База удаляется по OID, а не только по имени.
func TestADatabaseThatWasRenamedIsNotDroppedByItsOldName(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreHook = func(context.Context, pgCall) (int, error) {
		h.server.mu.Lock()
		defer h.server.mu.Unlock()
		h.server.databases["renamed"] = h.server.databases[pgNewDatabase]
		delete(h.server.databases, pgNewDatabase)
		h.server.databases[pgNewDatabase] = 5555 // someone else created a database under the name
		return 1, nil
	}
	_, f := h.run(h.request())
	hasPrefix(t, f, "CLEANUP_FAILED (rollback)")
	mentions(t, f.Error(), "database "+pgNewDatabase+" (oid ", "PG_RESTORE_FAILED (restore)")
	for _, e := range h.server.eventLog() {
		if strings.HasPrefix(e, "drop database") {
			t.Errorf("event %s", e)
		}
	}
	if !h.server.hasDatabase(pgNewDatabase) || !h.server.hasDatabase("renamed") {
		t.Error("a database that is not the step's was touched")
	}
}

// Scenario: Неудачное удаление базы — отдельный исход, а не тишина.
func TestAFailedDropOfTheDatabaseIsNamedNotSilent(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreError = "pg_restore: error: could not execute query: ERROR:  out of memory"
	h.server.dropDBError = "ERROR:  permission denied to drop database"
	_, f := h.run(h.request())
	hasPrefix(t, f, "CLEANUP_FAILED (rollback)")
	mentions(t, f.Error(), "out of memory", "permission denied to drop database", "left behind: database "+pgNewDatabase)
	if len(f.Leftover) != 1 || f.Leftover[0] != "database "+pgNewDatabase {
		t.Errorf("leftover %v", f.Leftover)
	}
	if !strings.Contains(h.log.String(), "level=ERROR") || !strings.Contains(h.log.String(), pgNewDatabase) {
		t.Errorf("agent log:\n%s", h.log.String())
	}
	if entries := h.journalEntries(); len(entries) != 1 {
		t.Errorf("the journal keeps the entry for the next start: %v", entries)
	}
}

// Scenario: Неудачное удаление роли перечисляется в оставшемся.
func TestAFailedDropOfARoleIsListedAsLeft(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreError = "pg_restore: error: boom"
	h.server.dropRoleError["app_ro"] = "ERROR:  role \"app_ro\" cannot be dropped because some objects depend on it"
	_, f := h.run(h.withGlobals())
	hasPrefix(t, f, "CLEANUP_FAILED (rollback)")
	if len(f.Leftover) != 1 || f.Leftover[0] != "role app_ro" || h.server.hasRole("app_rw") || h.server.hasDatabase(pgNewDatabase) {
		t.Errorf("leftover %v", f.Leftover)
	}
}

// A role whose OID cannot be told is named, not dropped by its name.
func TestRolesWhoseOIDsCannotBeReadAreLeftAndNamed(t *testing.T) {
	h := newPGHost(t)
	h.server.roleOIDError = "ERROR:  boom"
	_, f := h.run(h.withGlobals())
	hasPrefix(t, f, "CLEANUP_FAILED (rollback)")
	if h.server.hasDatabase(pgNewDatabase) {
		t.Error("the database was created")
	}
	mentions(t, f.Error(), "role app_rw", "role app_ro", "PG_GLOBALS_FAILED (globals)")
}

// The OID of the new database is needed to drop it: a CREATE whose outcome is
// not known leaves the intent, which is named and never dropped by name.
func TestACreateWhoseOutcomeIsUnknownIsLeftAndNamed(t *testing.T) {
	h := newPGHost(t)
	h.server.killOnCreate = true
	_, f := h.run(h.request())
	hasPrefix(t, f, "CLEANUP_FAILED (rollback)")
	mentions(t, f.Error(), "left behind: database "+pgNewDatabase)
	for _, e := range h.server.eventLog() {
		if strings.HasPrefix(e, "drop database") {
			t.Errorf("event %s", e)
		}
	}
}

func TestADatabaseWhoseOIDCannotBeReadAfterTheCreateIsLeftAndNamed(t *testing.T) {
	h := newPGHost(t)
	h.server.createHook = func(name string, _ uint32) { delete(h.server.databases, name) } // the OID query finds nothing
	_, f := h.run(h.request())
	hasPrefix(t, f, "CLEANUP_FAILED (rollback)")
	mentions(t, f.Error(), "PG_CREATE_DATABASE (create-database)", "database "+pgNewDatabase)
}

// Scenario: Журнал отката пишется до первого изменения целевого сервера.
func TestTheJournalHoldsTheConnectionAndTheSecretNameBeforeTheFirstChange(t *testing.T) {
	h := newPGHost(t)
	if _, f := h.run(h.withGlobals()); f != nil {
		t.Fatal(f)
	}
	first := h.server.journalAt["globals"]
	for _, want := range []string{`"password_ref":"pg-restore"`, `"host":"db2"`, `"user":"restorer"`, `"port":5432`} {
		if !strings.Contains(first, want) {
			t.Errorf("the journal at the first change lacks %s: %s", want, first)
		}
	}
	if strings.Contains(first, secretValue) {
		t.Errorf("the password is in the journal: %s", first)
	}
}

func TestTheJournalIsPrivateToTheService(t *testing.T) {
	h := newPGHost(t)
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	for path, mode := range map[string]os.FileMode{filepath.Join(h.journal(), "c1.json"): 0o600, h.journal(): 0o700} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != mode {
			t.Errorf("%s: %v, %v", path, info, err)
		}
	}
}

// has fails the test if the journal text lacks one of the parts, or holds one of the others.
func has(t *testing.T, when, text string, present, absent []string) {
	t.Helper()
	for _, p := range present {
		if !strings.Contains(text, p) {
			t.Errorf("%s: the journal lacks %s: %s", when, p, text)
		}
	}
	for _, a := range absent {
		if strings.Contains(text, a) {
			t.Errorf("%s: the journal holds %s: %s", when, a, text)
		}
	}
}

// Scenario: Состояния журнала отката пишутся до соответствующих действий.
func TestTheStatesOfTheJournalAreWrittenBeforeTheActionsTheyAnnounce(t *testing.T) {
	h := newPGHost(t)
	if _, f := h.run(h.withGlobals()); f != nil {
		t.Fatal(f)
	}
	at := h.server.journalAt
	has(t, "before the globals", at["globals"], []string{`{"name":"app_rw"}`, `{"name":"app_ro"}`}, []string{`"oid"`, `"database":{"name":"app`})
	has(t, "before CREATE DATABASE", at["create database"],
		[]string{`"name":"app_rw","oid":`, `"name":"app_ro","oid":`, `"database":{"name":"` + pgNewDatabase + `"}`}, nil)
	has(t, "before pg_restore", at["pg_restore"], []string{`"database":{"name":"` + pgNewDatabase + `","oid":`}, []string{`"result"`})
	data, err := os.ReadFile(filepath.Join(h.journal(), "c1.json"))
	if err != nil {
		t.Fatal(err)
	}
	has(t, "at the end", string(data), []string{`"result":{"database":"` + pgNewDatabase + `","tables_restored":2,"roles_restored":2}`}, nil)
}

// Scenario: Снимок базы восстанавливается в новую базу (журнала отката нет после записи результата).
func TestTheJournalEntryOfAFinishedRestoreGoesWhenItsResultIsJournaled(t *testing.T) {
	h := newPGHost(t)
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	if entries := h.journalEntries(); len(entries) != 1 {
		t.Fatalf("journal %v", entries)
	}
	h.engine.Settle("c1")
	if entries := h.journalEntries(); len(entries) != 0 {
		t.Errorf("journal %v", entries)
	}
}

func TestAJournalThatCannotBeWrittenStopsTheRestoreBeforeAnyChange(t *testing.T) {
	h := newPGHost(t)
	if err := os.WriteFile(filepath.Join(h.stateDir, "pg-restore"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, f := h.run(h.request())
	hasPrefix(t, f, "TARGET_WRITE (check)")
	mentions(t, f.Error(), "rollback journal")
	h.untouched(t)
}

func TestAPlanKeepsItsConnectionInTheJournalWithoutThePassword(t *testing.T) {
	h := newPGHost(t)
	req := h.request()
	req.Plan = sdk.PostgresRestore{Host: "/var/run/postgresql", Port: 5433, User: "u", PasswordRef: "ref", TLSMode: "verify-full", TLSRootCert: "/etc/ca.pem", Name: "x", ClientPath: pgBin + "/pg_restore"}
	h.secrets["ref"] = []byte("p")
	req.Password = []byte("p")
	if _, f := h.run(req); f != nil {
		t.Fatal(f)
	}
	data, _ := os.ReadFile(filepath.Join(h.journal(), "c1.json"))
	for _, want := range []string{`"tls_mode":"verify-full"`, `"tls_root_cert":"/etc/ca.pem"`, `"client_path":"` + pgBin + `/pg_restore"`, `"host":"/var/run/postgresql"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("journal lacks %s: %s", want, data)
		}
	}
}
