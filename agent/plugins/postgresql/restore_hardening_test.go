// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Mutation hardening of the restore of a database (R2b): the edges of the
// checks, the journal and the rollback that the scenarios do not reach.

// --- progress

func TestTheStepReportsPreparingAndRestoringWithNothingCountedAtTheStart(t *testing.T) {
	h := newPGHost(t)
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	phases := h.host.progress()
	if len(phases) < 3 || phases[0] != (rpEvent{phase: sdk.PhasePreparing}) || phases[1] != (rpEvent{phase: sdk.PhaseRestoring}) {
		t.Fatalf("events %+v", phases)
	}
	last := phases[len(phases)-1]
	if last.phase != sdk.PhaseRestoring || last.bytes != 1005 || last.bytesTotal != 0 {
		t.Errorf("the last progress %+v: the bytes of the dump written to pg_restore, no total", last)
	}
}

// --- the checks

func TestTheSnapshotOfTheGlobalsIsNotTheSnapshotOfADatabase(t *testing.T) {
	h := newPGHost(t)
	req := h.request()
	req.SnapshotID = rpGlobalsID
	_, f := h.run(req)
	hasPrefix(t, f, "RESTIC_OUTPUT_UNEXPECTED (check)")
	mentions(t, f.Error(), "is not the snapshot of a PostgreSQL database", "postgresql.part=database")
	h.untouched(t)
}

func TestASnapshotThatIsNotShapedAsThePluginWritesItIsRefused(t *testing.T) {
	tests := map[string]func(s *sdk.SnapshotInfo){
		"two paths":          func(s *sdk.SnapshotInfo) { s.Paths = []string{"/a", "/b"} },
		"no path":            func(s *sdk.SnapshotInfo) { s.Paths = nil },
		"a path not in root": func(s *sdk.SnapshotInfo) { s.Paths = []string{"/dir/app.dump"} },
		"a relative path":    func(s *sdk.SnapshotInfo) { s.Paths = []string{"app.dump"} },
		"the root itself":    func(s *sdk.SnapshotInfo) { s.Paths = []string{"/"} },
		"dot dot":            func(s *sdk.SnapshotInfo) { s.Paths = []string{"/.."} },
		"no database tag": func(s *sdk.SnapshotInfo) {
			s.Tags = slices.DeleteFunc(s.Tags, func(t string) bool { return strings.HasPrefix(t, "postgresql.database=") })
		},
		"a database tag that is no escape": func(s *sdk.SnapshotInfo) {
			s.Tags = append(slices.DeleteFunc(s.Tags, func(t string) bool { return strings.HasPrefix(t, "postgresql.database=") }), "postgresql.database=%zz")
		},
	}
	for name, spoil := range tests {
		t.Run(name, func(t *testing.T) {
			h := newPGHost(t)
			spoil(&h.repo.snapshots[0])
			_, f := h.run(h.request())
			hasPrefix(t, f, "RESTIC_OUTPUT_UNEXPECTED (check)")
			mentions(t, f.Error(), "is tagged as PostgreSQL", "nothing was restored")
			if len(h.server.ran("psql")) != 0 {
				t.Error("psql ran")
			}
		})
	}
}

// The snapshot of the global objects of the database is judged with the plan,
// whether they are asked for or not: a snapshot shaped otherwise is refused on sight.
func TestTheFileOfTheGlobalsSnapshotIsJudgedLikeTheOneOfTheDump(t *testing.T) {
	for _, globals := range []bool{true, false} {
		h := newPGHost(t)
		h.repo.snapshots[1].Paths = []string{"/a", "/b"}
		req := h.request()
		if globals {
			req = h.withGlobals()
		}
		_, f := h.run(req)
		hasPrefix(t, f, "RESTIC_OUTPUT_UNEXPECTED (check)")
	}
}

func TestTheNewestSnapshotOfTheGlobalsOfTheDatabaseIsTheOneRestored(t *testing.T) {
	h := newPGHost(t)
	older := snapshotG()
	older.ID = "dddd1111dddd1111dddd1111dddd1111dddd1111dddd1111dddd1111dddd1111"
	older.Time = older.Time.Add(-time.Hour)
	older.Paths = []string{"/older.globals.sql"}
	foreign := snapshotG()
	foreign.ID = "eeee1111eeee1111eeee1111eeee1111eeee1111eeee1111eeee1111eeee1111"
	foreign.Time = foreign.Time.Add(time.Hour)
	foreign.Tags = append(slices.DeleteFunc(slices.Clone(foreign.Tags), func(t string) bool { return strings.HasPrefix(t, "postgresql.main_snapshot=") }), "postgresql.main_snapshot=ffff")
	otherSource := snapshotG()
	otherSource.ID = "aaaa2222aaaa2222aaaa2222aaaa2222aaaa2222aaaa2222aaaa2222aaaa2222"
	otherSource.Time = otherSource.Time.Add(2 * time.Hour)
	otherSource.Tags[0] = "sard.source=s7"
	h.repo.snapshots = []sdk.SnapshotInfo{snapshotP(), older, snapshotG(), foreign, otherSource}
	h.repo.content[older.ID+"/older.globals.sql"] = "CREATE ROLE older;\n"
	if _, f := h.run(h.withGlobals()); f != nil {
		t.Fatal(f)
	}
	if !h.server.hasRole("app_rw") || h.server.hasRole("older") {
		t.Errorf("roles %v", h.server.roles)
	}
}

func TestAPgRestoreThatFailsIsMissingEvenIfItPrintedAVersion(t *testing.T) {
	h := newPGHost(t)
	h.server.versionOutput = "pg_restore (PostgreSQL) 16.4"
	h.server.versionFails = true
	_, f := h.run(h.request())
	hasPrefix(t, f, "PG_CLIENT_MISSING (check)")
	mentions(t, f.Error(), "cannot read the version")
	if !errors.Is(f, sdk.ErrProgramMissing) {
		t.Errorf("the cause of %v is lost", f)
	}
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
		set  func(s *rpServer)
		want string
	}{
		"the role cannot read it": {func(s *rpServer) { s.systemIDError = "ERROR:  permission denied for function pg_control_system" },
			"the role cannot read pg_control_system(): psql failed (exit code 3): ERROR:  permission denied for function pg_control_system"},
		"an empty answer":        {func(s *rpServer) { s.answers["pg_control_system"] = "" }, `unexpected answer of psql: ""`},
		"an answer not a number": {func(s *rpServer) { s.answers["pg_control_system"] = "12ab" }, `unexpected answer of psql: "12ab"`},
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
		_, err := io.WriteString(w, rpGlobalsSQL+strings.Repeat("-", 64<<20-len(rpGlobalsSQL)-1)+"\n")
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

// --- the rollback

// withinFiveMinutes fails the test for a call whose context had not about five minutes left.
func withinFiveMinutes(t *testing.T, c rpCall) {
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

func TestADatabaseThatIsGoneIsNotKeptInTheEntryWhenARoleStays(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreError = "pg_restore: error: boom"
	h.server.dropRoleError["app_ro"] = "ERROR:  cannot be dropped"
	h.server.restoreHook = func(context.Context, rpCall) (int, error) {
		h.server.mu.Lock()
		defer h.server.mu.Unlock()
		delete(h.server.databases, rpNewDatabase)
		return 1, nil
	}
	_, f := h.run(h.withGlobals())
	hasPrefix(t, f, "CLEANUP_FAILED (rollback)")
	data := h.host.journal.current()
	if strings.Contains(data, rpNewDatabase) || !strings.Contains(data, `"name":"app_ro"`) || strings.Contains(data, `"name":"app_rw"`) {
		t.Errorf("the entry keeps %s", data)
	}
	if !f.Retain {
		t.Error("a role with an OID is left: the entry stays")
	}
}

func TestACheckOfTheCatalogThatFailedIsNotTrustedEvenIfItPrintedACount(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreError = "pg_restore: error: boom"
	h.server.failOut["from pg_database where oid"] = [2]string{"0|0", "ERROR:  catalog is down"}
	_, f := h.run(h.request())
	hasPrefix(t, f, "CLEANUP_FAILED (rollback)")
	mentions(t, f.Error(), "catalog is down", "database "+rpNewDatabase)
	if !h.server.hasDatabase(rpNewDatabase) {
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
	mentions(t, h.logText(), "dropped the database "+rpNewDatabase)
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
	if !slices.Contains(h.server.eventLog(), "drop database "+rpNewDatabase) {
		t.Errorf("events %v", h.server.eventLog())
	}
}

func TestARefusedCreateDatabaseForgetsTheIntentInTheJournalBeforeTheRollback(t *testing.T) {
	h := newPGHost(t)
	h.server.createError = "psql:<stdin>:1: ERROR:  permission denied to create database"
	_, f := h.run(h.withGlobals())
	hasPrefix(t, f, "PG_CREATE_DATABASE (create-database)")
	at := h.server.journalAt["drop role app_rw"]
	if at == "" || strings.Contains(at, rpNewDatabase) {
		t.Errorf("the journal when the roles were dropped: %s", at)
	}
}

// The step waits for the dump it stopped: nothing of it runs on after the table of contents is read.
func TestTheDumpStoppedAfterTheContentsAreReadHasEndedBeforeTheStepGoesOn(t *testing.T) {
	h := newPGHost(t)
	var ended atomic.Bool
	h.repo.dumpHook = func(ctx context.Context, id, path string, w io.Writer) error {
		if path != "/app.dump" {
			return nil
		}
		if _, err := io.WriteString(w, "PGDMP"); err != nil {
			return err
		}
		<-ctx.Done()
		time.Sleep(150 * time.Millisecond)
		ended.Store(true)
		return ctx.Err()
	}
	h.server.hooks["lock capacity"] = func() {
		if !ended.Load() {
			t.Error("the step went on while the dump it stopped was still running")
		}
	}
	h.server.restoreHook = func(context.Context, rpCall) (int, error) { return 0, nil }
	_, _ = h.run(h.request())
}

func TestAPgRestoreThatFailsStopsTheDumpThatIsStillRunning(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreHook = func(context.Context, rpCall) (int, error) { return 1, nil }
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

func TestAPgRestoreThatClosesItsInputFailsWithItsOwnError(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreHook = func(_ context.Context, c rpCall) (int, error) {
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
	h.server.restoreHook = func(context.Context, rpCall) (int, error) { return 0, nil }
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
	h.server.restoreHook = func(context.Context, rpCall) (int, error) { return 0, nil }
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
	for name, set := range map[string]func(s *rpServer){
		"exit code 1": func(s *rpServer) { s.globalsExit = 1 },
		"exit code 2": func(s *rpServer) { s.globalsExit = 2 },
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
	h.server.createError = `psql:<stdin>:1: ERROR:  database "` + rpNewDatabase + `" already exists`
	h.server.createExit = 2
	_, f := h.run(h.request())
	hasPrefix(t, f, "CLEANUP_FAILED (rollback)")
	mentions(t, f.Error(), "PG_CREATE_DATABASE (create-database)", "database "+rpNewDatabase)
	if strings.Contains(f.Error(), "DATABASE_EXISTS") {
		t.Errorf("%s", f.Error())
	}
}

func TestAPathOfTheAgentWithAnEmptyEntryNeverMeansTheCurrentDirectory(t *testing.T) {
	h := newPGHost(t)
	h.tools.nodes["pg_restore"] = 0o755
	h.path = ":" + rpBin
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	for _, c := range h.server.ran("pg_restore") {
		if c.Path != rpBin+"/pg_restore" {
			t.Errorf("pg_restore started as %s", c.Path)
		}
	}
}

func TestTheShareOfTheLockTableIsLoggedWithItsDigits(t *testing.T) {
	h := newPGHost(t)
	h.share = 0.3333
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
	mentions(t, f.Error(), "left behind: database "+rpNewDatabase)
}

// A journal that stops working while the rollback runs does not hide what is left.
func TestAJournalThatFailsWhileTheRollbackKeepsTheLeftoverAnyway(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreError = "pg_restore: error: boom"
	h.server.dropDBError = "ERROR:  denied"
	h.host.journal.failFrom = 3 // after the first entry, the OID and the intent
	_, f := h.run(h.request())
	hasPrefix(t, f, "CLEANUP_FAILED (rollback)")
	mentions(t, f.Error(), "database "+rpNewDatabase)
}
