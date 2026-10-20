// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
)

const journalHead = `{"command_id":"c1","connection":{"host":"db2","port":5432,"user":"restorer","password_ref":"pg-restore","tls_mode":"disable"}`

// leftByAKill writes the entry of a restore the agent was killed in.
func (h *pgHost) leftByAKill(tail string) {
	h.t.Helper()
	if err := os.MkdirAll(h.journal(), 0o700); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.journal(), "c1.json"), []byte(journalHead+tail+"}"), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func only(t *testing.T, found []consolerestore.Evidence) consolerestore.Evidence {
	t.Helper()
	if len(found) != 1 {
		t.Fatalf("evidence: %+v", found)
	}
	return found[0]
}

// Scenario: После перезапуска агент откатывает прерванное восстановление по журналу.
func TestAfterARestartAnInterruptedRestoreIsRolledBackByTheJournal(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[pgNewDatabase] = 20001
	h.server.roles["app_rw"] = 20002
	h.leftByAKill(`,"database":{"name":"` + pgNewDatabase + `","oid":20001},"roles":[{"name":"app_rw","oid":20002}]`)
	ev := only(t, h.engine.CleanDatabases())
	if ev.Verdict != consolerestore.NothingRestored || ev.Name != "c1" {
		t.Errorf("evidence %+v", ev)
	}
	if h.server.hasDatabase(pgNewDatabase) || h.server.hasRole("app_rw") {
		t.Error("the database or the role is still there")
	}
	h.noTrace()
	for _, c := range h.server.ran("psql") {
		if v, _ := c.env("PGPASSWORD"); v != secretValue {
			t.Errorf("the password of the secret of the entry was not used: %q", v)
		}
	}
	if !strings.Contains(h.log.String(), "rolled back an interrupted restore") {
		t.Errorf("log:\n%s", h.log.String())
	}
}

// noDrops fails the test if the server saw a DROP.
func (h *pgHost) noDrops(t *testing.T) {
	t.Helper()
	for _, e := range h.server.eventLog() {
		if strings.HasPrefix(e, "drop ") {
			t.Errorf("event %s", e)
		}
	}
}

// Scenario: Убитое после завершения pg_restore восстановление сообщает об успехе из журнала отката.
func TestACompletedRestoreIsReportedAsSuccessAndNothingIsDropped(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[pgNewDatabase] = 20001
	h.leftByAKill(`,"database":{"name":"` + pgNewDatabase + `","oid":20001},"result":{"database":"` + pgNewDatabase + `","tables_restored":2,"roles_restored":0}`)
	ev := only(t, h.engine.CleanDatabases())
	if ev.Verdict != consolerestore.Completed || ev.Database == nil || *ev.Database != (consolerestore.DatabaseOutput{Database: pgNewDatabase, Tables: 2}) {
		t.Fatalf("evidence %+v", ev)
	}
	h.noDrops(t)
	if !h.server.hasDatabase(pgNewDatabase) || len(h.journalEntries()) != 1 {
		t.Error("the database or the entry is gone before the result is journaled")
	}
	h.engine.Settle("c1")
	if len(h.journalEntries()) != 0 {
		t.Errorf("journal %v", h.journalEntries())
	}
}

// Scenario: База из намерения без записанного OID не удаляется и называется оставшейся.
func TestADatabaseOfTheIntentOnlyIsNeverDroppedByNameAndIsNamed(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[pgNewDatabase] = 20001
	h.leftByAKill(`,"database":{"name":"` + pgNewDatabase + `"}`)
	ev := only(t, h.engine.CleanDatabases())
	if ev.Verdict != consolerestore.CleanupFailed || ev.Leftover != "database "+pgNewDatabase || ev.Err == nil {
		t.Fatalf("evidence %+v", ev)
	}
	h.noDrops(t)
	if !strings.Contains(h.log.String(), "level=ERROR") {
		t.Errorf("log:\n%s", h.log.String())
	}
}

func TestRolesOfTheIntentOnlyAreNamedToo(t *testing.T) {
	h := newPGHost(t)
	h.leftByAKill(`,"roles":[{"name":"app_rw"},{"name":"app_ro","oid":0}]`)
	ev := only(t, h.engine.CleanDatabases())
	if ev.Verdict != consolerestore.CleanupFailed || ev.Leftover != "role app_rw, role app_ro" {
		t.Fatalf("evidence %+v", ev)
	}
}

// Scenario: Недоступный при перезапуске PostgreSQL даёт неудачный откат с тем, что осталось.
func TestAnUnreachableServerAtTheStartLeavesTheDatabaseNamed(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[pgNewDatabase] = 20001
	h.server.connectError = "psql: error: connection to server failed: Connection refused"
	h.leftByAKill(`,"database":{"name":"` + pgNewDatabase + `","oid":20001}`)
	ev := only(t, h.engine.CleanDatabases())
	if ev.Verdict != consolerestore.CleanupFailed || ev.Leftover != "database "+pgNewDatabase || !strings.Contains(ev.Err.Error(), "Connection refused") {
		t.Fatalf("evidence %+v", ev)
	}
	if !strings.Contains(h.log.String(), "level=ERROR") || len(h.journalEntries()) != 1 {
		t.Errorf("the entry must stay for the next start; log:\n%s", h.log.String())
	}
}

// The server is back at the next start: the retry drops what is left.
func TestTheNextStartRetriesTheRollbackThatFailed(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[pgNewDatabase] = 20001
	h.leftByAKill(`,"database":{"name":"` + pgNewDatabase + `","oid":20001}`)
	h.server.connectError = "psql: error: connection to server failed: Connection refused"
	h.engine.CleanDatabases()
	h.server.connectError = ""
	if ev := only(t, h.engine.CleanDatabases()); ev.Verdict != consolerestore.NothingRestored {
		t.Errorf("retry: %+v", ev)
	}
	if h.server.hasDatabase(pgNewDatabase) || len(h.journalEntries()) != 0 {
		t.Error("the retry left something")
	}
}

func TestOnlyWhatCouldNotBeDroppedStaysInTheEntry(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[pgNewDatabase] = 20001
	h.server.roles["app_rw"] = 20002
	h.server.roles["app_ro"] = 20003
	h.server.dropRoleError["app_ro"] = "ERROR:  role cannot be dropped"
	h.leftByAKill(`,"database":{"name":"` + pgNewDatabase + `","oid":20001},"roles":[{"name":"app_rw","oid":20002},{"name":"app_ro","oid":20003}]`)
	ev := only(t, h.engine.CleanDatabases())
	if ev.Leftover != "role app_ro" || ev.Verdict != consolerestore.CleanupFailed {
		t.Fatalf("evidence %+v", ev)
	}
	data, _ := os.ReadFile(filepath.Join(h.journal(), "c1.json"))
	if strings.Contains(string(data), "app_rw") || strings.Contains(string(data), pgNewDatabase) || !strings.Contains(string(data), "app_ro") {
		t.Errorf("entry: %s", data)
	}
}

func TestWithoutTheSecretOfTheEntryTheRollbackNamesEverything(t *testing.T) {
	h := newPGHost(t)
	delete(h.secrets, passwordSecret)
	h.leftByAKill(`,"database":{"name":"` + pgNewDatabase + `","oid":20001},"roles":[{"name":"app_rw","oid":20002}]`)
	ev := only(t, h.engine.CleanDatabases())
	if ev.Verdict != consolerestore.CleanupFailed || ev.Leftover != "database "+pgNewDatabase+", role app_rw" || !strings.Contains(ev.Err.Error(), passwordSecret) {
		t.Fatalf("evidence %+v", ev)
	}
	if len(h.server.ran("psql")) != 0 {
		t.Error("psql ran without a password")
	}
}

func TestWithoutPsqlTheRollbackNamesEverything(t *testing.T) {
	h := newPGHost(t)
	h.tools.nodes = nil
	h.rebuild()
	h.leftByAKill(`,"database":{"name":"` + pgNewDatabase + `","oid":20001}`)
	ev := only(t, h.engine.CleanDatabases())
	if ev.Verdict != consolerestore.CleanupFailed || !strings.Contains(ev.Err.Error(), "PG_CLIENT_MISSING") {
		t.Fatalf("evidence %+v", ev)
	}
}

func TestAnEntryWithNothingCreatedIsJustRemoved(t *testing.T) {
	h := newPGHost(t)
	h.leftByAKill("")
	ev := only(t, h.engine.CleanDatabases())
	if ev.Verdict != consolerestore.NothingRestored || len(h.journalEntries()) != 0 || len(h.server.ran("psql")) != 0 {
		t.Errorf("evidence %+v, journal %v", ev, h.journalEntries())
	}
}

func TestAnUnreadableEntryIsNamedAndKept(t *testing.T) {
	h := newPGHost(t)
	if err := os.MkdirAll(h.journal(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.journal(), "c1.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	ev := only(t, h.engine.CleanDatabases())
	if ev.Verdict != consolerestore.CleanupFailed || ev.Err == nil || len(h.journalEntries()) != 1 {
		t.Errorf("evidence %+v", ev)
	}
}

func TestWithoutAJournalThereIsNothingToRollBack(t *testing.T) {
	h := newPGHost(t)
	if found := h.engine.CleanDatabases(); len(found) != 0 {
		t.Errorf("evidence %+v", found)
	}
	h2 := newPGHost(t)
	h2.stateDir = ""
	h2.rebuild()
	if found := h2.engine.CleanDatabases(); len(found) != 0 {
		t.Errorf("evidence %+v", found)
	}
}

func TestAJournalThatCannotBeOpenedIsReported(t *testing.T) {
	h := newPGHost(t)
	if err := os.WriteFile(filepath.Join(h.stateDir, "pg-restore"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ev := only(t, h.engine.CleanDatabases())
	if ev.Verdict != consolerestore.CleanupFailed || ev.Err == nil {
		t.Errorf("evidence %+v", ev)
	}
}

func TestSettlingACommandWithoutAJournalEntryDoesNothing(t *testing.T) {
	h := newPGHost(t)
	h.engine.Settle("c9")
	h.leftByAKill("")
	h.engine.Settle("c9")
	if len(h.journalEntries()) != 1 {
		t.Errorf("journal %v", h.journalEntries())
	}
}

// A failed result that names what the rollback left: an entry that holds only
// intents has nothing to retry and goes once the result is journaled; one that
// holds an OID stays, so that the next start tries again.
func TestTheEntryOfAFailedRestoreGoesOnlyIfNothingWithAnOIDIsLeftToDrop(t *testing.T) {
	h := newPGHost(t)
	h.leftByAKill(`,"database":{"name":"` + pgNewDatabase + `"},"roles":[{"name":"app_rw"}]`)
	h.engine.Abandon("c1")
	if len(h.journalEntries()) != 0 {
		t.Errorf("journal %v", h.journalEntries())
	}
	h.leftByAKill(`,"database":{"name":"` + pgNewDatabase + `","oid":20001}`)
	h.engine.Abandon("c1")
	if len(h.journalEntries()) != 1 {
		t.Errorf("an entry with an OID to drop went: %v", h.journalEntries())
	}
	h.engine.Abandon("c9") // no entry
	h.engine.Abandon("../x")
}

// Scenario: Пустой секрет записи журнала не используется для отката.
func TestAnEmptySecretOfAnInterruptedRestoreIsNotUsedToRollBack(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[pgNewDatabase] = 20001
	h.secrets["pg-restore"] = []byte("\n")
	h.leftByAKill(`,"database":{"name":"` + pgNewDatabase + `","oid":20001}`)
	ev := only(t, h.engine.CleanDatabases())
	if ev.Verdict != consolerestore.CleanupFailed || ev.Err == nil || !strings.Contains(ev.Err.Error(), "empty") {
		t.Errorf("evidence %+v", ev)
	}
	if !h.server.hasDatabase(pgNewDatabase) {
		t.Error("the database was dropped with an empty password")
	}
}

func TestASecretWithANulByteOfAnInterruptedRestoreIsNotUsedToRollBack(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[pgNewDatabase] = 20001
	h.secrets["pg-restore"] = []byte("pa\x00ss")
	h.leftByAKill(`,"database":{"name":"` + pgNewDatabase + `","oid":20001}`)
	ev := only(t, h.engine.CleanDatabases())
	if ev.Verdict != consolerestore.CleanupFailed || ev.Err == nil || !strings.Contains(ev.Err.Error(), "NUL") {
		t.Errorf("evidence %+v", ev)
	}
}
