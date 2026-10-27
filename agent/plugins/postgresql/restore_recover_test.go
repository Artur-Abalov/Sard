// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/plugins/postgresql"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

var _ sdk.Recoverer = postgresql.Plugin{}

const journalHead = `{"command_id":"c1","connection":{"host":"db2","port":5432,"user":"restorer","password_ref":"pg-restore","tls_mode":"disable"}`

// leftByAKill is the entry of a restore the agent was killed in.
func leftByAKill(tail string) string { return journalHead + tail + "}" }

// recover gives the entry to the plugin the way the agent does at its start.
func (h *rpHostRig) recover(entry string) sdk.Recovery {
	h.t.Helper()
	return h.recoverCtx(context.Background(), entry)
}

func (h *rpHostRig) recoverCtx(ctx context.Context, entry string) sdk.Recovery {
	h.t.Helper()
	return h.plugin().Recover(ctx, h.host, "c1", []byte(entry))
}

// noDrops fails the test if the server saw a DROP.
func (h *rpHostRig) noDrops(t *testing.T) {
	t.Helper()
	for _, e := range h.server.eventLog() {
		if strings.HasPrefix(e, "drop ") {
			t.Errorf("event %s", e)
		}
	}
}

// nothingRestored: the recovery undid everything and asks to keep nothing.
func nothingRestored(t *testing.T, got sdk.Recovery) {
	t.Helper()
	if got.Kind != sdk.RecoveryNothingRestored || got.Retain {
		t.Errorf("recovery %+v", got)
	}
}

// leftBehind judges a recovery that could not undo everything.
func leftBehind(t *testing.T, got sdk.Recovery, leftover []string, retain bool) {
	t.Helper()
	if got.Kind != sdk.RecoveryLeftBehind || !slices.Equal(got.Leftover, leftover) || got.Retain != retain || got.Cleanup == "" {
		t.Fatalf("recovery %+v, want leftover %q, retain %v", got, leftover, retain)
	}
}

// Scenario: После перезапуска агент откатывает прерванное восстановление по журналу.
func TestAfterARestartAnInterruptedRestoreIsRolledBackByTheJournal(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[rpNewDatabase] = 20001
	h.server.roles["app_rw"] = 20002
	got := h.recover(leftByAKill(`,"database":{"name":"` + rpNewDatabase + `","oid":20001},"roles":[{"name":"app_rw","oid":20002}]`))
	nothingRestored(t, got)
	if h.server.hasDatabase(rpNewDatabase) || h.server.hasRole("app_rw") {
		t.Error("the database or the role is still there")
	}
	h.noTrace()
	for _, c := range h.server.ran("psql") {
		if v, _ := c.env("PGPASSWORD"); v != rpSecret {
			t.Errorf("the password of the secret of the entry was not used: %q", v)
		}
	}
	if !strings.Contains(h.logText(), "dropped the database "+rpNewDatabase) || !strings.Contains(h.logText(), "dropped the role app_rw") {
		t.Errorf("log:\n%s", h.logText())
	}
}

// Scenario: Убитое после завершения pg_restore восстановление сообщает об успехе из журнала отката.
func TestACompletedRestoreIsReportedAsSuccessAndNothingIsDropped(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[rpNewDatabase] = 20001
	got := h.recover(leftByAKill(`,"database":{"name":"` + rpNewDatabase + `","oid":20001},"result":{"database":"` + rpNewDatabase + `","tables_restored":2,"roles_restored":1}`))
	want := sdk.Restored(sdk.RestoreResult{Target: rpNewDatabase, Database: rpNewDatabase, Tables: 2, Roles: 1})
	if got.Kind != sdk.RecoveryCompleted || got.Result != want.Result || got.Retain {
		t.Fatalf("recovery %+v", got)
	}
	h.noDrops(t)
	if !h.server.hasDatabase(rpNewDatabase) || len(h.server.ran("psql")) != 0 {
		t.Error("the database is gone or the server was asked")
	}
}

// Scenario: База из намерения без записанного OID не удаляется и называется оставшейся.
func TestADatabaseOfTheIntentOnlyIsNeverDroppedByNameAndIsNamed(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[rpNewDatabase] = 20001
	got := h.recover(leftByAKill(`,"database":{"name":"` + rpNewDatabase + `"}`))
	leftBehind(t, got, []string{"database " + rpNewDatabase}, false)
	h.noDrops(t)
}

func TestRolesOfTheIntentOnlyAreNamedToo(t *testing.T) {
	h := newPGHost(t)
	got := h.recover(leftByAKill(`,"roles":[{"name":"app_rw"},{"name":"app_ro","oid":0}]`))
	leftBehind(t, got, []string{"role app_rw", "role app_ro"}, false)
}

// Scenario: Недоступный при перезапуске PostgreSQL даёт неудачный откат с тем, что осталось.
func TestAnUnreachableServerAtTheStartLeavesTheDatabaseNamed(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[rpNewDatabase] = 20001
	h.server.connectError = "psql: error: connection to server failed: Connection refused"
	got := h.recover(leftByAKill(`,"database":{"name":"` + rpNewDatabase + `","oid":20001}`))
	leftBehind(t, got, []string{"database " + rpNewDatabase}, true)
	if !strings.Contains(got.Cleanup, "Connection refused") {
		t.Errorf("cleanup %q", got.Cleanup)
	}
}

// The server is back at the next start: the retry drops what is left.
func TestTheNextStartRetriesTheRollbackThatFailed(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[rpNewDatabase] = 20001
	entry := leftByAKill(`,"database":{"name":"` + rpNewDatabase + `","oid":20001}`)
	h.server.connectError = "psql: error: connection to server failed: Connection refused"
	h.recover(entry)
	h.server.connectError = ""
	nothingRestored(t, h.recover(entry))
	if h.server.hasDatabase(rpNewDatabase) {
		t.Error("the retry left something")
	}
}

func TestOnlyWhatCouldNotBeDroppedStaysInTheEntry(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[rpNewDatabase] = 20001
	h.server.roles["app_rw"] = 20002
	h.server.roles["app_ro"] = 20003
	h.server.dropRoleError["app_ro"] = "ERROR:  role cannot be dropped"
	got := h.recover(leftByAKill(`,"database":{"name":"` + rpNewDatabase + `","oid":20001},"roles":[{"name":"app_rw","oid":20002},{"name":"app_ro","oid":20003}]`))
	leftBehind(t, got, []string{"role app_ro"}, true)
	data := h.host.journal.current()
	if strings.Contains(data, "app_rw") || strings.Contains(data, rpNewDatabase) || !strings.Contains(data, "app_ro") {
		t.Errorf("entry: %s", data)
	}
}

func TestAnEntryWhereNothingCouldBeDroppedIsNotWrittenAgain(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[rpNewDatabase] = 20001
	h.server.connectError = "psql: error: connection to server failed: Connection refused"
	h.recover(leftByAKill(`,"database":{"name":"` + rpNewDatabase + `","oid":20001}`))
	if n := h.host.journal.count(); n != 0 {
		t.Errorf("the entry was written %d times: the host holds it as it is", n)
	}
}

func TestWithoutTheSecretOfTheEntryTheRollbackNamesEverything(t *testing.T) {
	h := newPGHost(t)
	delete(h.host.secrets, rpPasswordRef)
	got := h.recover(leftByAKill(`,"database":{"name":"` + rpNewDatabase + `","oid":20001},"roles":[{"name":"app_rw","oid":20002}]`))
	leftBehind(t, got, []string{"database " + rpNewDatabase, "role app_rw"}, true)
	if !strings.Contains(got.Cleanup, rpPasswordRef) {
		t.Errorf("cleanup %q", got.Cleanup)
	}
	if len(h.server.ran("psql")) != 0 {
		t.Error("psql ran without a password")
	}
}

func TestWithoutPsqlTheRollbackNamesEverything(t *testing.T) {
	h := newPGHost(t)
	h.tools.nodes = nil
	got := h.recover(leftByAKill(`,"database":{"name":"` + rpNewDatabase + `","oid":20001}`))
	leftBehind(t, got, []string{"database " + rpNewDatabase}, true)
	if !strings.Contains(got.Cleanup, "PG_CLIENT_MISSING") {
		t.Errorf("cleanup %q", got.Cleanup)
	}
}

func TestAnEntryWithNothingCreatedIsJustRolledBack(t *testing.T) {
	h := newPGHost(t)
	nothingRestored(t, h.recover(leftByAKill("")))
	if len(h.server.ran("psql")) != 0 {
		t.Error("psql ran")
	}
}

// Scenario: Испорченная прежняя запись называется и остаётся (the plugin asks to keep what it cannot read).
func TestAnUnreadableEntryIsNamedAndKept(t *testing.T) {
	h := newPGHost(t)
	for _, entry := range []string{"{broken", "", "[]"} {
		got := h.recover(entry)
		leftBehind(t, got, nil, true)
		if !strings.Contains(got.Cleanup, "unreadable") {
			t.Errorf("%q: cleanup %q", entry, got.Cleanup)
		}
	}
	if len(h.server.ran("psql")) != 0 {
		t.Error("psql ran")
	}
}

// A failed result that names what the rollback left: an entry that holds only
// intents has nothing to retry and goes once the result is journaled; one that
// holds an OID stays, so that the next start tries again.
func TestTheEntryOfAFailedRestoreIsKeptOnlyIfSomethingWithAnOIDIsLeftToDrop(t *testing.T) {
	tests := []struct {
		name   string
		tail   string
		retain bool
	}{
		{"intents only", `,"database":{"name":"` + rpNewDatabase + `"},"roles":[{"name":"app_rw"}]`, false},
		{"a database with an OID", `,"database":{"name":"` + rpNewDatabase + `","oid":20001}`, true},
		{"a role with an OID", `,"roles":[{"name":"app_rw","oid":20002}]`, true},
		{"a role with an OID and a database of the intent", `,"roles":[{"name":"app_rw","oid":20002}],"database":{"name":"x"}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newPGHost(t)
			h.server.connectError = "psql: error: connection to server failed: Connection refused"
			got := h.recover(leftByAKill(tt.tail))
			if got.Kind != sdk.RecoveryLeftBehind || got.Retain != tt.retain {
				t.Errorf("recovery %+v, want retain %v", got, tt.retain)
			}
		})
	}
}

// Scenario: Пустой секрет записи журнала не используется для отката.
func TestAnEmptySecretOfAnInterruptedRestoreIsNotUsedToRollBack(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[rpNewDatabase] = 20001
	h.host.secrets["pg-restore"] = []byte("\n")
	got := h.recover(leftByAKill(`,"database":{"name":"` + rpNewDatabase + `","oid":20001}`))
	leftBehind(t, got, []string{"database " + rpNewDatabase}, true)
	if !strings.Contains(got.Cleanup, "empty") {
		t.Errorf("cleanup %q", got.Cleanup)
	}
	if !h.server.hasDatabase(rpNewDatabase) {
		t.Error("the database was dropped with an empty password")
	}
}

func TestASecretWithANulByteOfAnInterruptedRestoreIsNotUsedToRollBack(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[rpNewDatabase] = 20001
	h.host.secrets["pg-restore"] = []byte("pa\x00ss")
	got := h.recover(leftByAKill(`,"database":{"name":"` + rpNewDatabase + `","oid":20001}`))
	leftBehind(t, got, []string{"database " + rpNewDatabase}, true)
	if !strings.Contains(got.Cleanup, "NUL") {
		t.Errorf("cleanup %q", got.Cleanup)
	}
}

func TestAnEntryWhoseSecretCannotBeReadSaysWhy(t *testing.T) {
	h := newPGHost(t)
	h.host.secrets = nil
	got := h.recover(leftByAKill(`,"database":{"name":"` + rpNewDatabase + `","oid":20001}`))
	if !strings.Contains(got.Cleanup, `the secret pg-restore of the interrupted restore cannot be used: unknown secret "pg-restore"`) {
		t.Errorf("cleanup %q", got.Cleanup)
	}
}

// The rollback at the start has the time the host gives it, and a recovery that
// is cancelled is not believed to have dropped anything it did not.
func TestTheRollbackAtTheStartUsesTheContextOfTheHost(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[rpNewDatabase] = 20001
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	h.recoverCtx(ctx, leftByAKill(`,"database":{"name":"`+rpNewDatabase+`","oid":20001}`))
	calls := h.server.ran("psql")
	if len(calls) == 0 {
		t.Fatal("no call")
	}
	for _, c := range calls {
		if c.Deadline < 3*time.Minute+50*time.Second || c.Deadline > 4*time.Minute {
			t.Errorf("the call had %v", c.Deadline)
		}
	}
}

func TestACompletedRestoreIsLoggedWhenTheAgentStartsByTheHostNotThePlugin(t *testing.T) {
	h := newPGHost(t)
	h.recover(leftByAKill(`,"database":{"name":"` + rpNewDatabase + `","oid":20001},"result":{"database":"` + rpNewDatabase + `","tables_restored":2,"roles_restored":0}`))
	if len(h.host.logs()) != 0 {
		t.Errorf("the plugin logged:\n%s", h.logText())
	}
}

func TestWhatTheRollbackLogsIsLoggedByLevelAtTheStart(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[rpNewDatabase] = 20001
	h.server.roles["app_rw"] = 20002
	h.server.dropRoleError["app_rw"] = "ERROR:  denied"
	h.recover(leftByAKill(`,"database":{"name":"` + rpNewDatabase + `","oid":20001},"roles":[{"name":"app_rw","oid":20002}]`))
	infos, warns := h.lines(sdk.LevelInfo), h.lines(sdk.LevelWarn)
	if !slices.Contains(infos, "dropped the database "+rpNewDatabase) || !slices.Contains(warns, "ERROR:  denied") {
		t.Errorf("info %q, warn %q", infos, warns)
	}
}

// What the rollback could not drop is named even when the journal cannot keep the rest.
func TestAJournalThatCannotKeepWhatIsLeftDoesNotHideIt(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[rpNewDatabase] = 20001
	h.server.roles["app_rw"] = 20002
	h.server.dropRoleError["app_rw"] = "ERROR:  role cannot be dropped"
	h.host.journal.failFrom = 1
	h.host.journal.saves = []string{"the entry as the host holds it"}
	got := h.recover(leftByAKill(`,"database":{"name":"` + rpNewDatabase + `","oid":20001},"roles":[{"name":"app_rw","oid":20002}]`))
	leftBehind(t, got, []string{"role app_rw"}, true)
	if errs := h.lines(sdk.LevelError); len(errs) != 1 || !strings.Contains(errs[0], "input/output error") {
		t.Errorf("errors logged: %q", errs)
	}
}
