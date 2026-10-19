// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// Mutation hardening of the restore of a database (R2b): the edges of the
// checks, the journal and the rollback that the scenarios do not reach.

// --- progress and descriptors

func TestTheStepReportsPreparingAndRestoringWithNothingCountedAtTheStart(t *testing.T) {
	h := newPGHost(t)
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	phases := progressOf(h)
	if len(phases) < 3 || phases[0] != (event{phase: agentv1.StepPhase_STEP_PHASE_PREPARING}) || phases[1] != (event{phase: agentv1.StepPhase_STEP_PHASE_RESTORING}) {
		t.Fatalf("events %+v", phases)
	}
	last := phases[len(phases)-1]
	if last.phase != agentv1.StepPhase_STEP_PHASE_RESTORING || last.bytes != 1005 || last.bytesTotal != 0 {
		t.Errorf("the last progress %+v: the bytes of the dump written to pg_restore, no total", last)
	}
}

// progressOf are the progress events of the step, without its log lines.
func progressOf(h *pgHost) []event {
	var phases []event
	for _, e := range h.rep.events {
		if !e.isLog {
			phases = append(phases, e)
		}
	}
	return phases
}

func TestTheRestoreOfADatabaseClosesEverythingItOpens(t *testing.T) {
	tests := map[string]func(h *pgHost){
		"a restore": func(h *pgHost) { _, _ = h.run(h.withGlobals()) },
		"a restore settled": func(h *pgHost) {
			_, _ = h.run(h.request())
			h.engine.Settle("c1")
		},
		"a failed restore": func(h *pgHost) {
			h.server.restoreError = "pg_restore: error: boom"
			_, _ = h.run(h.withGlobals())
		},
		"an abandoned restore": func(h *pgHost) {
			h.server.restoreError = "pg_restore: error: boom"
			h.server.dropDBError = "ERROR:  denied"
			_, _ = h.run(h.request())
			h.engine.Abandon("c1")
		},
		"the start after a kill": func(h *pgHost) {
			h.server.databases[pgNewDatabase] = 20001
			h.leftByAKill(`,"database":{"name":"` + pgNewDatabase + `","oid":20001}`)
			h.engine.CleanDatabases()
		},
		"an unreadable entry": func(h *pgHost) {
			h.leftByAKill(`,"database":`)
			h.engine.CleanDatabases()
			h.engine.Settle("c1")
		},
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			h := newPGHost(t)
			run(h)
			h.faults.noLeaks(t)
		})
	}
}

// --- the checks

func TestTheSnapshotOfTheGlobalsIsNotTheSnapshotOfADatabase(t *testing.T) {
	h := newPGHost(t)
	req := h.request()
	req.SnapshotID = pgGlobalsID
	_, f := h.run(req)
	hasPrefix(t, f, "RESTIC_OUTPUT_UNEXPECTED (check)")
	mentions(t, f.Error(), "is not the snapshot of a PostgreSQL database", "postgresql.part=database")
	h.untouched(t)
}

func TestAPgRestoreThatFailsIsMissingEvenIfItPrintedAVersion(t *testing.T) {
	h := newPGHost(t)
	h.server.versionOutput = "pg_restore (PostgreSQL) 16.4"
	h.server.versionFails = true
	_, f := h.run(h.request())
	hasPrefix(t, f, "PG_CLIENT_MISSING (check)")
	mentions(t, f.Error(), "cannot read the version")
	h.untouched(t)
}

func TestARoleThatIsASuperuserNeedsNoCreatedb(t *testing.T) {
	h := newPGHost(t)
	h.server.super, h.server.createdb = true, false
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
}

func TestARoleWithoutCreatedbIsRefusedWithItsNameQuoted(t *testing.T) {
	h := newPGHost(t)
	h.server.createdb, h.server.user = false, `we"ird`
	_, f := h.run(h.request())
	hasPrefix(t, f, "CREATEDB_REQUIRED (check)")
	mentions(t, f.Error(), `role "we\"ird"`, `ALTER ROLE "we""ird" CREATEDB`)
}

func TestTheClusterThatCannotBeComparedIsToldInTheLog(t *testing.T) {
	tests := map[string]struct {
		set  func(s *pgServer)
		want string
	}{
		"the role cannot read it": {func(s *pgServer) { s.systemIDError = "ERROR:  permission denied for function pg_control_system" },
			"the role cannot read pg_control_system(): psql failed (exit code 3): ERROR:  permission denied for function pg_control_system"},
		"an empty answer":        {func(s *pgServer) { s.answers["pg_control_system"] = "" }, `unexpected answer of psql: ""`},
		"an answer not a number": {func(s *pgServer) { s.answers["pg_control_system"] = "12ab" }, `unexpected answer of psql: "12ab"`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			h := newPGHost(t)
			tt.set(h.server)
			if _, f := h.run(h.request()); f != nil {
				t.Fatal(f)
			}
			mentions(t, h.logText(), "the target cluster was not compared with the one of the snapshot: "+tt.want)
		})
	}
}

func TestACancelDuringTheChecksStopsAtOnceWithoutAskingMore(t *testing.T) {
	for _, tt := range []struct{ event, next string }{{"probe", "system identifier"}, {"system identifier", "database exists?"}} {
		t.Run(tt.event, func(t *testing.T) {
			h := newPGHost(t)
			ctx := h.cancelAt(tt.event)
			_, f := h.runCtx(ctx, h.request())
			hasPrefix(t, f, "INTERRUPTED (check)")
			if events := h.server.eventLog(); events[len(events)-1] != tt.event {
				t.Errorf("the step went on after %s: %v", tt.event, events)
			}
		})
	}
}

func TestTheSizeOfTheGlobalsFileIsNamedInTheRefusal(t *testing.T) {
	h := newPGHost(t)
	h.repo.dumpHook = func(_ context.Context, _, _ string, w io.Writer) error {
		_, err := w.Write(make([]byte, 65<<20))
		return err
	}
	_, f := h.run(h.withGlobals())
	hasPrefix(t, f, "PG_GLOBALS_FAILED (check)")
	mentions(t, f.Detail, "the file of the global objects is larger than 64 MiB")
	h.untouched(t)
}

func TestAGlobalsFileOfExactlyTheLimitIsRead(t *testing.T) {
	h := newPGHost(t)
	h.repo.dumpHook = func(_ context.Context, _, _ string, w io.Writer) error {
		_, err := io.WriteString(w, defaultGlobalsSQL+strings.Repeat("-", 64<<20-len(defaultGlobalsSQL)-1)+"\n")
		return err
	}
	if _, f := h.run(h.withGlobals()); f != nil {
		t.Fatal(f)
	}
}

func TestACancelWhileTheGlobalsAreTooLargeIsStillACancel(t *testing.T) {
	h := newPGHost(t)
	ctx, cancel := context.WithCancel(context.Background())
	h.repo.dumpHook = func(_ context.Context, _, _ string, w io.Writer) error {
		_, err := w.Write(make([]byte, 65<<20))
		cancel()
		return err
	}
	_, f := h.runCtx(ctx, h.withGlobals())
	hasPrefix(t, f, "INTERRUPTED (check)")
}

// --- the mode of pg_restore

func TestTheModeIsLoggedWithItsNumbers(t *testing.T) {
	h := newPGHost(t)
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	mentions(t, h.logText(), "pg_restore mode: single transaction (10 objects, lock capacity 6400, share 0.5)")
	h = newPGHost(t)
	h.server.toc = 3200
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	mentions(t, h.logText(), "pg_restore mode: separate transactions (3200 objects, lock capacity 6400, share 0.5)")
}

func TestOnlyTheEntriesOfTheContentsAreCounted(t *testing.T) {
	h := newPGHost(t)
	h.server.tocBlank = true
	h.server.toc = 3199
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	mentions(t, h.logText(), "single transaction (3199 objects")
}

func TestAnAnswerOfTheLockTableFromAFailedPsqlIsNotUsed(t *testing.T) {
	h := newPGHost(t)
	h.server.failOut["max_locks_per_transaction"] = [2]string{"6400", "ERROR:  boom"}
	_, f := h.run(h.request())
	hasPrefix(t, f, "PG_CONNECT (check)")
	mentions(t, f.Error(), "cannot read the size of the lock table", "boom")
	h.untouched(t)
}

func TestTheLockTableMayBeAsLargeAsAnUnsigned64BitNumber(t *testing.T) {
	h := newPGHost(t)
	h.server.answers["max_locks_per_transaction"] = "9223372036854775808"
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	mentions(t, h.logText(), "lock capacity 9223372036854775808")
}

// --- the answers of the catalog

func TestTheCountOfTablesMayBeLargeAndIsReadInBaseTen(t *testing.T) {
	h := newPGHost(t)
	h.server.answers["from pg_class"] = "9223372036854775808"
	out, f := h.run(h.request())
	if f != nil || out.Tables != 9223372036854775808 {
		t.Errorf("%+v, %v", out, f)
	}
	h = newPGHost(t)
	h.server.answers["from pg_class"] = "10"
	if out, f := h.run(h.request()); f != nil || out.Tables != 10 {
		t.Errorf("%+v, %v", out, f)
	}
}

func TestAnAnswerOfTheTablesFromAFailedPsqlIsNotUsed(t *testing.T) {
	h := newPGHost(t)
	h.server.failOut["from pg_class"] = [2]string{"7", "ERROR:  boom"}
	_, f := h.run(h.request())
	hasPrefix(t, f, "PG_RESTORE_FAILED (restore)")
	mentions(t, f.Error(), "cannot count the tables")
	h.noTrace()
}

func TestTheOIDsAreThoseOfAnUnsigned32BitNumberThatIsNotZero(t *testing.T) {
	for _, tt := range []struct {
		name, marker, oid string
		ok                bool
	}{
		{"database 1", "select oid from pg_database", "1", true},
		{"database above 31 bits", "select oid from pg_database", "3000000000", true},
		{"database the largest", "select oid from pg_database", "4294967295", true},
		{"database over 32 bits", "select oid from pg_database", "4294967296", false},
		{"database 0", "select oid from pg_database", "0", false},
		{"role 1", "select oid from pg_roles", "1", true},
		{"role above 31 bits", "select oid from pg_roles", "3000000000", true},
		{"role over 32 bits", "select oid from pg_roles", "4294967296", false},
		{"role 0", "select oid from pg_roles", "0", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newPGHost(t)
			h.server.answers[tt.marker] = tt.oid
			_, f := h.run(h.withGlobals())
			if tt.ok != (f == nil) || (f != nil && !strings.Contains(f.Error(), "OID cannot be read")) {
				t.Errorf("the OID %s: %v", tt.oid, f)
			}
		})
	}
}

func TestAnOIDFromAFailedPsqlIsNotUsed(t *testing.T) {
	for marker, what := range map[string]string{"select oid from pg_database": "PG_CREATE_DATABASE (create-database)", "select oid from pg_roles": "PG_GLOBALS_FAILED (globals)"} {
		t.Run(marker, func(t *testing.T) {
			h := newPGHost(t)
			h.server.failOut[marker] = [2]string{"20999", "ERROR:  boom"}
			_, f := h.run(h.withGlobals())
			hasPrefix(t, f, "CLEANUP_FAILED (rollback)")
			mentions(t, f.Error(), what, "boom")
		})
	}
}

// --- the journal

func TestAStaleTemporaryFileOfTheJournalDoesNotStopTheRestore(t *testing.T) {
	h := newPGHost(t)
	if err := os.MkdirAll(h.journal(), 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(h.journal(), "c1.json.tmp")
	if err := os.WriteFile(stale, []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("the stale file: %v", err)
	}
}

func TestAJournalThatFailsToRenameLeavesNoTemporaryFile(t *testing.T) {
	h := newPGHost(t)
	h.faults.renameErr = syscall.EIO
	_, f := h.run(h.request())
	hasPrefix(t, f, "TARGET_WRITE (check)")
	if entries := h.journalEntries(); len(entries) != 0 {
		t.Errorf("the journal holds %v", entries)
	}
}

func TestSettlingRemovesTheTemporaryFileOfTheEntryToo(t *testing.T) {
	h := newPGHost(t)
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	if err := os.WriteFile(filepath.Join(h.journal(), "c1.json.tmp"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.engine.Settle("c1")
	if entries := h.journalEntries(); len(entries) != 0 {
		t.Errorf("the journal holds %v", entries)
	}
	mentions(t, h.log.String(), "level=INFO", "removed the rollback journal entry of a restore of a database", "command_id=c1")
	if strings.Contains(h.log.String(), "level=ERROR") {
		t.Errorf("log:\n%s", h.log.String())
	}
}

func TestAnEntryThatIsGoneBeforeTheRollbackRemovesItIsNoTrouble(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreError = "pg_restore: error: boom"
	h.server.hooks["pg_restore"] = func() { _ = os.Remove(filepath.Join(h.journal(), "c1.json")) }
	_, f := h.run(h.request())
	hasPrefix(t, f, "PG_RESTORE_FAILED (restore)")
	if strings.Contains(h.log.String(), "could not be removed") {
		t.Errorf("log:\n%s", h.log.String())
	}
}

func TestAnEntryThatCannotBeOpenedAtTheStartIsNamedWithItsError(t *testing.T) {
	h := newPGHost(t)
	h.leftByAKill("")
	c1 := filepath.Join(h.journal(), "c1.json")
	if err := os.Remove(c1); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("elsewhere", c1); err != nil {
		t.Fatal(err)
	}
	ev := only(t, h.engine.CleanDatabases())
	if ev.Verdict != consolerestore.CleanupFailed || ev.Err == nil || !errors.Is(ev.Err, syscall.ELOOP) {
		t.Errorf("evidence %+v", ev)
	}
	mentions(t, h.log.String(), "level=ERROR", "cannot read an entry of the rollback journal")
}

func TestAnUnusableJournalIsLoggedAsAnError(t *testing.T) {
	h := newPGHost(t)
	h.leftByAKill("")
	h.faults.namesErr = map[string]error{h.journal(): syscall.EIO}
	h.engine.CleanDatabases()
	mentions(t, h.log.String(), "level=ERROR", "cannot read the rollback journal of restores of databases")
}

func TestOnlyANameThatCanBeAFileOfTheJournalSettlesAnEntry(t *testing.T) {
	h := newPGHost(t)
	outside := filepath.Join(h.stateDir, "x.json")
	if err := os.WriteFile(outside, []byte(journalHead+"}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(h.journal(), 0o700); err != nil {
		t.Fatal(err)
	}
	h.engine.Abandon("../x")
	h.engine.Settle("../x")
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("a file outside the journal was removed: %v", err)
	}
}

func TestAHostWithoutAStateDirectoryKeepsNoJournalAndLogsNothingOfIt(t *testing.T) {
	h := newPGHost(t)
	h.noState = true
	h.rebuild()
	h.engine.Abandon("c1")
	h.engine.Settle("c1")
	if h.engine.CleanDatabases() != nil || h.log.Len() != 0 {
		t.Errorf("log:\n%s", h.log.String())
	}
}

func TestAnEntryWithOnlyARoleWithAnOIDIsNotAbandoned(t *testing.T) {
	h := newPGHost(t)
	h.leftByAKill(`,"roles":[{"name":"app_rw","oid":20002}]`)
	h.engine.Abandon("c1")
	if entries := h.journalEntries(); !slices.Equal(entries, []string{"c1.json"}) {
		t.Errorf("journal %v", entries)
	}
	h.leftByAKill(`,"roles":[{"name":"app_rw"}],"database":{"name":"x"}`)
	h.engine.Abandon("c1")
	if entries := h.journalEntries(); len(entries) != 0 {
		t.Errorf("an entry of intents only stays: %v", entries)
	}
}

// --- the start after a kill and the rollback

func TestWhatTheRollbackLogsIsLoggedByLevelAtTheStart(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[pgNewDatabase] = 20001
	h.server.roles["app_rw"] = 20002
	h.server.dropRoleError["app_rw"] = "ERROR:  denied"
	h.leftByAKill(`,"database":{"name":"` + pgNewDatabase + `","oid":20001},"roles":[{"name":"app_rw","oid":20002}]`)
	ev := only(t, h.engine.CleanDatabases())
	if ev.Verdict != consolerestore.CleanupFailed {
		t.Fatalf("evidence %+v", ev)
	}
	log := h.log.String()
	mentions(t, log, "level=INFO", "dropped the database "+pgNewDatabase, "level=WARN", "ERROR:  denied")
}

// withinFiveMinutes fails the test for a call whose context had not about five minutes left.
func withinFiveMinutes(t *testing.T, c pgCall) {
	t.Helper()
	if c.Deadline < 4*time.Minute+50*time.Second || c.Deadline > 5*time.Minute {
		t.Errorf("the rollback had %v", c.Deadline)
	}
}

func TestTheRollbackOfAStepHasFiveMinutes(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreError = "pg_restore: error: boom"
	_, _ = h.run(h.request())
	checked := 0
	for _, c := range h.server.ran("psql") {
		if strings.Contains(c.Stdin, "DROP DATABASE") || strings.Contains(c.Stdin, "from pg_database where oid") {
			checked++
			withinFiveMinutes(t, c)
		}
	}
	if checked != 2 {
		t.Errorf("%d rollback calls", checked)
	}
}

func TestTheRollbackAtTheStartHasFiveMinutes(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[pgNewDatabase] = 20001
	h.leftByAKill(`,"database":{"name":"` + pgNewDatabase + `","oid":20001}`)
	h.engine.CleanDatabases()
	calls := h.server.ran("psql")
	if len(calls) == 0 {
		t.Fatal("no call")
	}
	for _, c := range calls {
		withinFiveMinutes(t, c)
	}
}

func TestADatabaseThatIsGoneIsNotKeptInTheEntryWhenARoleStays(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreError = "pg_restore: error: boom"
	h.server.dropRoleError["app_ro"] = "ERROR:  cannot be dropped"
	h.server.restoreHook = func(context.Context, pgCall) (int, error) {
		h.server.mu.Lock()
		defer h.server.mu.Unlock()
		delete(h.server.databases, pgNewDatabase)
		return 1, nil
	}
	_, f := h.run(h.withGlobals())
	hasPrefix(t, f, "CLEANUP_FAILED (rollback)")
	data, _ := os.ReadFile(filepath.Join(h.journal(), "c1.json"))
	if strings.Contains(string(data), pgNewDatabase) || !strings.Contains(string(data), `"name":"app_ro"`) || strings.Contains(string(data), `"name":"app_rw"`) {
		t.Errorf("the entry keeps %s", data)
	}
}

func TestACheckOfTheCatalogThatFailedIsNotTrustedEvenIfItPrintedACount(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreError = "pg_restore: error: boom"
	h.server.failOut["from pg_database where oid"] = [2]string{"0|0", "ERROR:  catalog is down"}
	_, f := h.run(h.request())
	hasPrefix(t, f, "CLEANUP_FAILED (rollback)")
	mentions(t, f.Error(), "catalog is down", "database "+pgNewDatabase)
	if !h.server.hasDatabase(pgNewDatabase) {
		t.Error("the database was taken for gone")
	}
}

func TestAFailedDropIsToldWithTheWordsOfTheServer(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreError = "pg_restore: error: boom"
	h.server.dropDBError = "ERROR:  permission denied to drop database"
	_, f := h.run(h.request())
	if f.Cleanup != "DROP DATABASE: ERROR:  permission denied to drop database" {
		t.Errorf("cleanup %q", f.Cleanup)
	}
}

func TestARestoreThatIsRolledBackReportsTheDropToTheStep(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreError = "pg_restore: error: boom"
	_, _ = h.run(h.request())
	mentions(t, h.logText(), "dropped the database "+pgNewDatabase)
}

func TestAJournalThatFailsToBeWrittenForTheGlobalsNeverRunsThem(t *testing.T) {
	h := newPGHost(t)
	tmp := filepath.Join(h.journal(), "c1.json.tmp")
	h.faults.createErr = map[string]error{tmp: syscall.EIO}
	h.faults.createSkip = map[string]int{tmp: 1}
	_, f := h.run(h.withGlobals())
	hasPrefix(t, f, "TARGET_WRITE (globals)")
	if slices.Contains(h.server.eventLog(), "globals") {
		t.Errorf("the global objects were applied without being announced: %v", h.server.eventLog())
	}
	h.noTrace()
}

func TestWithoutGlobalsNoScriptOfGlobalsRuns(t *testing.T) {
	h := newPGHost(t)
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	for _, c := range h.server.ran("psql") {
		if c.has("--single-transaction") {
			t.Errorf("a script of globals ran: %v", c.Args)
		}
	}
}

func TestTheOIDOfARoleIsAskedOnceEvenWhenTheRollbackLooksAgain(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreError = "pg_restore: error: boom"
	_, _ = h.run(h.withGlobals())
	asked := 0
	for _, e := range h.server.eventLog() {
		if e == "role oid app_rw" {
			asked++
		}
	}
	if asked != 1 {
		t.Errorf("the OID of app_rw was asked %d times", asked)
	}
}

func TestADatabaseCreatedAndThenCancelledIsDroppedByTheOIDTheRollbackFinds(t *testing.T) {
	h := newPGHost(t)
	ctx := h.cancelAt("create database")
	_, f := h.runCtx(ctx, h.request())
	hasPrefix(t, f, "INTERRUPTED (create-database)")
	h.noTrace()
	if !slices.Contains(h.server.eventLog(), "drop database "+pgNewDatabase) {
		t.Errorf("events %v", h.server.eventLog())
	}
}

func TestARefusedCreateDatabaseForgetsTheIntentInTheJournalBeforeTheRollback(t *testing.T) {
	h := newPGHost(t)
	h.server.createError = "psql:<stdin>:1: ERROR:  permission denied to create database"
	_, f := h.run(h.withGlobals())
	hasPrefix(t, f, "PG_CREATE_DATABASE (create-database)")
	at := h.server.journalAt["drop role app_rw"]
	if at == "" || strings.Contains(at, pgNewDatabase) {
		t.Errorf("the journal when the roles were dropped: %s", at)
	}
}

func TestTheOutputOfPgRestoreIsFedWhileTheDumpIsStoppedAtTheEnd(t *testing.T) {
	h := newPGHost(t)
	var ended bool
	h.repo.dumpHook = func(ctx context.Context, id, path string, w io.Writer) error {
		if path != "/app.dump" {
			return nil
		}
		if _, err := io.WriteString(w, "PGDMP"); err != nil {
			return err
		}
		<-ctx.Done()
		time.Sleep(30 * time.Millisecond)
		ended = true
		return ctx.Err()
	}
	h.server.restoreHook = func(ctx context.Context, c pgCall) (int, error) { return 0, nil }
	_, _ = h.run(h.request())
	if !ended {
		t.Error("the step ended before the dump did")
	}
}

func TestAPgRestoreThatFailsStopsTheDumpThatIsStillRunning(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreHook = func(context.Context, pgCall) (int, error) { return 1, nil }
	calls := 0
	h.repo.dumpHook = func(ctx context.Context, id, path string, w io.Writer) error {
		calls++
		if path != "/app.dump" || calls == 1 {
			_, err := io.WriteString(w, "PGDMP")
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, f := h.run(h.request())
		hasPrefix(t, f, "PG_RESTORE_FAILED (restore)")
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the step waits for a dump nobody reads")
	}
}

// --- the second round

func TestAnEntryThatCannotBeReadIsNamedWithTheErrorOfTheRead(t *testing.T) {
	h := newPGHost(t)
	h.leftByAKill("")
	h.faults.readErr = map[string]error{filepath.Join(h.journal(), "c1.json"): syscall.EIO}
	ev := only(t, h.engine.CleanDatabases())
	if ev.Verdict != consolerestore.CleanupFailed || !errors.Is(ev.Err, syscall.EIO) {
		t.Errorf("evidence %+v", ev)
	}
}

func TestAnEntryThatIsUnreadableIsNeitherSettledNorAbandoned(t *testing.T) {
	h := newPGHost(t)
	h.leftByAKill(`,"database":`)
	h.engine.Settle("c1")
	h.engine.Abandon("c1")
	if entries := h.journalEntries(); !slices.Equal(entries, []string{"c1.json"}) {
		t.Errorf("journal %v", entries)
	}
}

func TestAMissingJournalIsNoErrorForSettlingOrAbandoning(t *testing.T) {
	h := newPGHost(t)
	h.engine.Settle("c1")
	h.engine.Abandon("c1")
	if h.log.Len() != 0 {
		t.Errorf("log:\n%s", h.log.String())
	}
}

func TestAFailedSaveOfTheJournalRemovesItsOwnTemporaryFile(t *testing.T) {
	h := newPGHost(t)
	h.faults.renameErr = syscall.EIO
	_, _ = h.run(h.request())
	tmp := "remove " + filepath.Join(h.journal(), "c1.json.tmp")
	removed := 0
	for _, op := range h.faults.ops {
		if op == tmp {
			removed++
		}
	}
	// before the file is made, after the failed rename, and once more by the removal of the entry
	if removed != 3 {
		t.Errorf("the temporary file was removed %d times: %v", removed, h.faults.ops)
	}
}

func TestAPgRestoreThatClosesItsInputFailsWithItsOwnError(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreHook = func(_ context.Context, c pgCall) (int, error) {
		if closer, ok := c.StdinReader.(io.Closer); ok {
			_ = closer.Close()
		}
		time.Sleep(50 * time.Millisecond) // restic dump meets the closed pipe first
		return 1, nil
	}
	h.repo.dumpHook = func(_ context.Context, _, path string, w io.Writer) error {
		for i := 0; i < 1000; i++ {
			if _, err := io.WriteString(w, "PGDMP"+strings.Repeat("x", 4096)); err != nil {
				return err
			}
			if path == "/app.dump" && i == 0 && len(h.server.ran("pg_restore")) == 0 {
				return nil // the contents ask for the head only
			}
		}
		return nil
	}
	_, f := h.run(h.request())
	hasPrefix(t, f, "PG_RESTORE_FAILED (restore)")
}

func TestADumpThatFailsAfterAPgRestoreThatEndedWellIsAFailureOfTheStorage(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreHook = func(context.Context, pgCall) (int, error) { return 0, nil }
	calls := 0
	h.repo.dumpHook = func(ctx context.Context, _, path string, w io.Writer) error {
		calls++
		if calls == 1 || path != "/app.dump" {
			_, err := io.WriteString(w, "PGDMP")
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
			return errors.New("the storage failed")
		}
	}
	_, f := h.run(h.request())
	if f == nil || !strings.Contains(f.Error(), "the storage failed") {
		t.Errorf("failure %v", f)
	}
}

func TestAPgRestoreThatEndsWellIsNotCutShortOverADumpThatStillRuns(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreHook = func(context.Context, pgCall) (int, error) { return 0, nil }
	calls := 0
	h.repo.dumpHook = func(ctx context.Context, _, path string, w io.Writer) error {
		calls++
		if calls == 1 || path != "/app.dump" {
			_, err := io.WriteString(w, "PGDMP")
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
			return nil
		}
	}
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
}

func TestACompletedRestoreIsLoggedWhenTheAgentStarts(t *testing.T) {
	h := newPGHost(t)
	h.leftByAKill(`,"database":{"name":"` + pgNewDatabase + `","oid":20001},"result":{"database":"` + pgNewDatabase + `","tables_restored":2,"roles_restored":0}`)
	h.engine.CleanDatabases()
	mentions(t, h.log.String(), "level=INFO", "the restore of a database completed before the agent was restarted", "database="+pgNewDatabase)
}

func TestAScriptThatPsqlRefusedLeavesNoQuestionToTheServerAboutTheRoles(t *testing.T) {
	h := newPGHost(t)
	h.server.globalsError = "psql:<stdin>:3: ERROR:  syntax error"
	_, f := h.run(h.withGlobals())
	hasPrefix(t, f, "PG_GLOBALS_FAILED (globals)")
	asked := 0
	for _, e := range h.server.eventLog() {
		if e == "existing roles" {
			asked++
		}
	}
	if asked != 1 {
		t.Errorf("the roles were asked %d times", asked)
	}
	h.noTrace()
}

func TestAPsqlThatEndsOtherwiseThanByARefusalLeavesTheRolesNamed(t *testing.T) {
	for name, set := range map[string]func(s *pgServer){
		"exit code 1": func(s *pgServer) { s.globalsExit = 1 },
		"exit code 2": func(s *pgServer) { s.globalsExit = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			h := newPGHost(t)
			set(h.server)
			_, f := h.run(h.withGlobals())
			hasPrefix(t, f, "CLEANUP_FAILED (rollback)")
			mentions(t, f.Error(), "PG_GLOBALS_FAILED (globals)")
			if len(f.Leftover) != 2 {
				t.Errorf("leftover %v: the roles exist and are the step's", f.Leftover)
			}
		})
	}
}

func TestACreateThatEndsOtherwiseThanByARefusalLeavesTheDatabaseNamedWhateverItSays(t *testing.T) {
	h := newPGHost(t)
	h.server.createError = `psql:<stdin>:1: ERROR:  database "` + pgNewDatabase + `" already exists`
	h.server.createExit = 2
	_, f := h.run(h.request())
	hasPrefix(t, f, "CLEANUP_FAILED (rollback)")
	mentions(t, f.Error(), "PG_CREATE_DATABASE (create-database)", "database "+pgNewDatabase)
	if strings.Contains(f.Error(), "DATABASE_EXISTS") {
		t.Errorf("%s", f.Error())
	}
}

func TestAPathOfTheAgentWithAnEmptyEntryNeverMeansTheCurrentDirectory(t *testing.T) {
	h := newPGHost(t)
	h.tools.nodes["pg_restore"] = 0o755
	h.path = ":" + pgBin
	h.rebuild()
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	for _, c := range h.server.ran("pg_restore") {
		if c.Path != pgBin+"/pg_restore" {
			t.Errorf("pg_restore started as %s", c.Path)
		}
	}
}

func TestTheShareOfTheLockTableIsLoggedWithItsDigits(t *testing.T) {
	h := newPGHost(t)
	h.share = 0.3333
	h.rebuild()
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	mentions(t, h.logText(), "share 0.3333)")
}

func TestAPsqlKilledByASignalAfterTheScriptCommittedLeavesTheRolesNamedWhateverItsCode(t *testing.T) {
	h := newPGHost(t)
	h.server.globalsExit = 3
	h.server.exitErr = true
	_, f := h.run(h.withGlobals())
	hasPrefix(t, f, "CLEANUP_FAILED (rollback)")
	if len(f.Leftover) != 2 {
		t.Errorf("leftover %v: the roles exist and are the step's", f.Leftover)
	}
}

func TestACreateKilledByASignalLeavesTheDatabaseNamedWhateverItsCode(t *testing.T) {
	h := newPGHost(t)
	h.server.createError = "psql:<stdin>:1: ERROR:  permission denied to create database"
	h.server.exitErr = true
	_, f := h.run(h.request())
	hasPrefix(t, f, "CLEANUP_FAILED (rollback)")
	mentions(t, f.Error(), "left behind: database "+pgNewDatabase)
}
