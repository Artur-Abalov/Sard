// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
)

// The edges of the restore of a database: answers the agent cannot read,
// queries that fail, a context that ends at each stage, a journal that cannot
// be written.

// Scenario: Отказ проверки PREPARING называет причину и ничего не создаёт (an unreadable answer).
func TestAnswersTheAgentCannotReadAreFailuresOfTheCheck(t *testing.T) {
	tests := map[string]struct{ marker, answer, reason string }{
		"probe without columns":    {"server_version_num", "garbage", "PG_CONNECT (check)"},
		"probe with a bad version": {"server_version_num", "x|f|t|r", "PG_CONNECT (check)"},
		"roles without a bar":      {"oid = 10", "garbage", "PG_CONNECT (check)"},
		"lock table not a number":  {"max_locks_per_transaction", "many", "PG_CONNECT (check)"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			h := newPGHost(t)
			h.server.answers[tt.marker] = tt.answer
			_, f := h.run(h.withGlobals())
			hasPrefix(t, f, tt.reason)
			mentions(t, f.Error(), "unexpected answer of psql")
			h.untouched(t)
		})
	}
}

func TestQueriesThatFailAreFailuresOfTheCheck(t *testing.T) {
	for name, marker := range map[string]string{"roles": "oid = 10", "exists": "exists(select", "lock table": "max_locks_per_transaction"} {
		t.Run(name, func(t *testing.T) {
			h := newPGHost(t)
			h.server.failOn[marker] = "ERROR:  boom"
			_, f := h.run(h.withGlobals())
			hasPrefix(t, f, "PG_CONNECT (check)")
			mentions(t, f.Error(), "boom")
			h.untouched(t)
		})
	}
}

// cancelAt makes the step's context end when the event begins.
func (h *pgHost) cancelAt(event string) context.Context {
	ctx, cancel := context.WithCancelCause(context.Background())
	h.server.hooks[event] = func() { cancel(errors.New("cancelled")) }
	return ctx
}

// Scenario: Отмена шага останавливает восстановление на любой стадии.
func TestACancelAtAnyStageEndsTheStepAndRollsBack(t *testing.T) {
	for _, tt := range []struct{ event, stage string }{
		{"probe", "check"}, {"system identifier", "check"}, {"existing roles", "check"}, {"database exists?", "check"},
		{"lock capacity", "globals"}, {"globals", "globals"}, {"role oid app_rw", "globals"},
	} {
		t.Run(tt.event, func(t *testing.T) {
			h := newPGHost(t)
			ctx := h.cancelAt(tt.event)
			_, f := h.runCtx(ctx, h.withGlobals())
			hasPrefix(t, f, "INTERRUPTED ("+tt.stage+")")
			h.noTrace()
		})
	}
}

func TestACancelWhileTheVersionOfPgRestoreIsReadEndsTheStep(t *testing.T) {
	h := newPGHost(t)
	ctx, cancel := context.WithCancel(context.Background())
	h.server.versionHook = cancel
	_, f := h.runCtx(ctx, h.request())
	hasPrefix(t, f, "INTERRUPTED (check)")
}

func TestACancelWhileTheGlobalsAreReadEndsTheStep(t *testing.T) {
	h := newPGHost(t)
	ctx, cancel := context.WithCancel(context.Background())
	h.repo.dumpHook = func(context.Context, string, string, io.Writer) error {
		cancel()
		return errors.New("signal: terminated")
	}
	_, f := h.runCtx(ctx, h.withGlobals())
	hasPrefix(t, f, "INTERRUPTED (check)")
}

func TestACancelWhileTheDatabaseIsCreatedLeavesItNamed(t *testing.T) {
	h := newPGHost(t)
	ctx := h.cancelAt("create database")
	h.server.killOnCreate = true
	_, f := h.runCtx(ctx, h.request())
	hasPrefix(t, f, "CLEANUP_FAILED (rollback)")
	mentions(t, f.Error(), "INTERRUPTED (create-database)", "database "+pgNewDatabase)
}

// Scenario: Сбой записи журнала на любом шаге откатывает созданное.
func TestAJournalThatFailsAtAnySaveRollsBackWhatIsCreated(t *testing.T) {
	for save := 0; save < 6; save++ {
		t.Run(fmt.Sprint(save), func(t *testing.T) {
			h := newPGHost(t)
			tmp := filepath.Join(h.journal(), "c1.json.tmp")
			h.faults.createErr = map[string]error{tmp: syscall.EIO}
			h.faults.createSkip = map[string]int{tmp: save}
			_, f := h.run(h.withGlobals())
			hasPrefix(t, f, "TARGET_WRITE")
			mentions(t, f.Error(), "rollback journal", "input/output error")
			h.noTrace()
		})
	}
}

func TestAJournalThatCannotBeSyncedOrRenamedIsAFailure(t *testing.T) {
	for name, set := range map[string]func(h *pgHost){
		"sync of the file": func(h *pgHost) {
			h.faults.fileErr = map[string]error{filepath.Join(h.journal(), "c1.json.tmp"): syscall.EIO}
		},
		"rename":          func(h *pgHost) { h.faults.renameErr = syscall.EIO },
		"sync of the dir": func(h *pgHost) { h.faults.syncErr = map[string]error{h.journal(): syscall.EIO} },
	} {
		t.Run(name, func(t *testing.T) {
			h := newPGHost(t)
			set(h)
			_, f := h.run(h.request())
			hasPrefix(t, f, "TARGET_WRITE (check)")
			h.untouched(t)
		})
	}
}

func TestAJournalEntryThatCannotBeRemovedAfterARollbackIsLogged(t *testing.T) {
	h := newPGHost(t)
	h.faults.removeErr = map[string]error{filepath.Join(h.journal(), "c1.json"): syscall.EIO}
	h.server.restoreError = "pg_restore: error: boom"
	_, f := h.run(h.request())
	hasPrefix(t, f, "PG_RESTORE_FAILED (restore)")
	if !strings.Contains(h.log.String(), "could not be removed") {
		t.Errorf("log:\n%s", h.log.String())
	}
}

func TestAnEntryThatCannotBeRemovedWhenItsResultIsJournaledIsLogged(t *testing.T) {
	h := newPGHost(t)
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	h.faults.removeErr = map[string]error{filepath.Join(h.journal(), "c1.json"): syscall.EIO}
	h.engine.Settle("c1")
	if !strings.Contains(h.log.String(), "cannot remove the rollback journal entry") || len(h.journalEntries()) != 1 {
		t.Errorf("log:\n%s", h.log.String())
	}
}

func TestAJournalDirectoryThatCannotBeListedIsReportedAtTheStart(t *testing.T) {
	h := newPGHost(t)
	h.leftByAKill("")
	h.faults.namesErr = map[string]error{h.journal(): syscall.EIO}
	ev := only(t, h.engine.CleanDatabases())
	if ev.Verdict != consolerestore.CleanupFailed || ev.Err == nil {
		t.Errorf("evidence %+v", ev)
	}
}

func TestAJournalThatCannotBeOpenedWhenSettlingIsLogged(t *testing.T) {
	h := newPGHost(t)
	h.faults.openErr = map[string]error{h.journal(): syscall.EACCES}
	h.engine.Settle("c1")
	if !strings.Contains(h.log.String(), "cannot open the rollback journal") {
		t.Errorf("log:\n%s", h.log.String())
	}
}

func TestWithoutSecretsAtTheStartTheRollbackNamesEverything(t *testing.T) {
	h := newPGHost(t)
	h.secrets = nil
	h.rebuildWithoutSecrets()
	h.leftByAKill(`,"database":{"name":"` + pgNewDatabase + `","oid":20001}`)
	ev := only(t, h.engine.CleanDatabases())
	if ev.Verdict != consolerestore.CleanupFailed || !strings.Contains(ev.Err.Error(), "no secrets") {
		t.Errorf("evidence %+v", ev)
	}
}

// A database that someone dropped meanwhile is gone: nothing is left.
func TestADatabaseThatIsGoneWhenTheRollbackLooksIsNotLeft(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreHook = func(context.Context, pgCall) (int, error) {
		h.server.mu.Lock()
		defer h.server.mu.Unlock()
		delete(h.server.databases, pgNewDatabase)
		return 1, nil
	}
	_, f := h.run(h.request())
	hasPrefix(t, f, "PG_RESTORE_FAILED (restore)")
	h.noTrace()
}

func TestFailuresOfTheCatalogQueriesOfTheRollbackAreLeftover(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreError = "pg_restore: error: boom"
	h.server.failOn["from pg_database where oid"] = "ERROR:  catalog is down"
	_, f := h.run(h.request())
	hasPrefix(t, f, "CLEANUP_FAILED (rollback)")
	mentions(t, f.Error(), "catalog is down", "database "+pgNewDatabase)
}

func TestACountOfTablesThatFailsIsAFailureOfTheRestore(t *testing.T) {
	h := newPGHost(t)
	h.server.failOn["from pg_class"] = "ERROR:  boom"
	_, f := h.run(h.request())
	hasPrefix(t, f, "PG_RESTORE_FAILED (restore)")
	mentions(t, f.Error(), "cannot count the tables")
	h.noTrace()
}

// A pg_restore that exits without reading the dump: its failure comes first,
// not the broken pipe of restic dump.
func TestAPgRestoreThatStopsReadingFailsWithItsOwnError(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreHook = func(context.Context, pgCall) (int, error) { return 1, nil }
	h.repo.dumpHook = func(_ context.Context, _, path string, w io.Writer) error {
		if path != "/app.dump" {
			return nil
		}
		if len(h.server.eventLog()) > 0 && h.server.eventLog()[len(h.server.eventLog())-1] != "pg_restore -l" {
			// the dump for the restore: it has much to write
			_, err := io.WriteString(w, strings.Repeat("x", 1<<20))
			return err
		}
		_, err := io.WriteString(w, "PGDMP")
		return err
	}
	_, f := h.run(h.request())
	hasPrefix(t, f, "PG_RESTORE_FAILED (restore)")
	h.noTrace()
}

func TestTheOutputOfTheProgramsIsLoggedByLevel(t *testing.T) {
	h := newPGHost(t)
	h.server.stderr = []string{"", "pg_restore: warning: errors ignored on restore: 1", "pg_restore: connecting to database", strings.Repeat("e", 600) + " error: long"}
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	var warn, info bool
	for _, l := range h.rep.logs() {
		warn = warn || strings.Contains(l.text, "warning: errors ignored")
		info = info || l.text == "pg_restore: connecting to database"
		if l.text == "" {
			t.Error("an empty line was logged")
		}
	}
	if !warn || !info {
		t.Errorf("log:\n%s", h.logText())
	}
}

func TestALongErrorOfAProgramIsCutInTheMessage(t *testing.T) {
	h := newPGHost(t)
	h.server.restoreError = "pg_restore: error: " + strings.Repeat("é", 400)
	_, f := h.run(h.request())
	hasPrefix(t, f, "PG_RESTORE_FAILED (restore)")
	if len(f.Error()) > 800 || !strings.Contains(f.Error(), "...") {
		t.Errorf("message of %d bytes: %s", len(f.Error()), f.Error())
	}
}

func TestASocketHostConnectsWithoutTLSAndATCPHostRequiresIt(t *testing.T) {
	h := newPGHost(t)
	req := h.request()
	req.Plan.Host, req.Plan.TLSMode = "/var/run/postgresql", ""
	if _, f := h.run(req); f != nil {
		t.Fatal(f)
	}
	if got := h.server.ran("psql")[0].conninfo(); !strings.Contains(got, "sslmode='disable'") || !strings.Contains(got, "host='/var/run/postgresql'") {
		t.Errorf("socket: %s", got)
	}
	h = newPGHost(t)
	req = h.request()
	req.Plan.TLSMode, req.Plan.TLSRootCert, req.Plan.Name = "", "/etc/ca.pem", "it's"
	if _, f := h.run(req); f != nil {
		t.Fatal(f)
	}
	if got := h.server.ran("psql")[0].conninfo(); !strings.Contains(got, "sslmode='require'") || !strings.Contains(got, "sslrootcert='/etc/ca.pem'") {
		t.Errorf("tcp: %s", got)
	}
	if got := h.lastRestore().conninfo(); !strings.Contains(got, `dbname='it\'s'`) {
		t.Errorf("the name is not quoted: %s", got)
	}
}

// Scenario: Нет клиента PostgreSQL (the ways a program can fail to run).
func TestTheWaysAToolCanFailToRun(t *testing.T) {
	tests := map[string]struct {
		set   func(h *pgHost)
		words string
	}{
		"not executable":     {func(h *pgHost) { h.tools.nodes["/opt/pg_restore"] = 0o644 }, "permission denied"},
		"a directory":        {func(h *pgHost) { h.tools.nodes["/opt/pg_restore"] = fs.ModeDir | 0o755 }, "is a directory"},
		"version unreadable": {func(h *pgHost) { h.server.versionOutput = "pg_restore: something else" }, "cannot read the version"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			h := newPGHost(t)
			tt.set(h)
			req := h.request()
			if strings.HasPrefix(name, "not") || name == "a directory" {
				req.Plan.ClientPath = "/opt/pg_restore"
			}
			_, f := h.run(req)
			hasPrefix(t, f, "PG_CLIENT_MISSING (check)")
			mentions(t, f.Error(), tt.words)
		})
	}
}

func TestAPgRestoreThatFailsToPrintItsVersionIsMissing(t *testing.T) {
	h := newPGHost(t)
	h.server.versionFails = true
	_, f := h.run(h.request())
	hasPrefix(t, f, "PG_CLIENT_MISSING (check)")
}

func TestPgRestorePathIsUsedWhenTheConfigNamesIt(t *testing.T) {
	h := newPGHost(t)
	h.tools.nodes["/opt/pg/bin/pg_restore"] = 0o755
	h.tools.nodes["/opt/pg/bin/psql"] = 0o755
	req := h.request()
	req.Plan.ClientPath = "/opt/pg/bin/pg_restore"
	if _, f := h.run(req); f != nil {
		t.Fatal(f)
	}
	if got := h.server.ran("pg_restore")[0]; got.Path != "/opt/pg/bin/pg_restore" {
		t.Errorf("pg_restore %+v", got)
	}
	req.Plan.ClientPath = "/opt/none/pg_restore"
	_, f := h.run(req)
	hasPrefix(t, f, "PG_CLIENT_MISSING (check)")
	mentions(t, f.Error(), "pg_restore_path", "/opt/none/pg_restore", "no such file or directory")
}

func TestThePathOfTheAgentIsSearchedSkippingEmptyEntries(t *testing.T) {
	h := newPGHost(t)
	h.path = ":/nonexistent:" + pgBin
	h.rebuild()
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	h = newPGHost(t)
	h.path = ""
	h.rebuild()
	_, f := h.run(h.request())
	hasPrefix(t, f, "PG_CLIENT_MISSING (check)")
}

func TestAStatThatFailsOtherwiseIsToldWithoutThePath(t *testing.T) {
	h := newPGHost(t)
	h.tools.errs = map[string]error{pgBin + "/pg_restore": &fs.PathError{Op: "stat", Path: pgBin + "/pg_restore", Err: syscall.EACCES}}
	h.rebuild()
	req := h.request()
	req.Plan.ClientPath = pgBin + "/pg_restore"
	_, f := h.run(req)
	hasPrefix(t, f, "PG_CLIENT_MISSING (check)")
	mentions(t, f.Error(), "permission denied")
	if strings.Contains(f.Error(), "stat") {
		t.Errorf("the operation of the system is in the message: %s", f.Error())
	}
	h.tools.errs[pgBin+"/pg_restore"] = errors.New("boom")
	_, f = h.run(req)
	mentions(t, f.Error(), "boom")
}

func TestThePathOfThePasswordSecretIsNeverNeededByTheEngine(t *testing.T) {
	h := newPGHost(t)
	req := h.request()
	req.Password = nil
	if _, f := h.run(req); f != nil {
		t.Fatal(f)
	}
	if _, ok := h.server.ran("psql")[0].env("PGPASSWORD"); ok {
		t.Error("a password was invented")
	}
}
