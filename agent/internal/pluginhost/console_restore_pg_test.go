// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/postgresql"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/proto"
)

// The restore of a database (R2b, R2c-2) through the host: the postgresql
// plugin restores itself, the host gives it the repository, the journal and the
// secrets, and words what it returns. psql and pg_restore are programs of the test.

const (
	pgID  = "bbbb1111bbbb1111bbbb1111bbbb1111bbbb1111bbbb1111bbbb1111bbbb1111"
	pgGID = "cccc1111cccc1111cccc1111cccc1111cccc1111cccc1111cccc1111cccc1111"
	pgBin = "/usr/lib/postgresql/16/bin"
	pgCfg = `{"host": "db2", "user": "restorer", "tls_mode": "disable", "password_ref": "pg-restore", "database": "app_restored_20261011"}`
)

// pgProgram plays psql and pg_restore: the target answers what a healthy
// server would, unless fails says otherwise.
type pgProgram struct {
	mu    sync.Mutex
	calls []postgresql.Cmd
	// fail, if set, is the stderr and exit code of psql.
	fail     string
	failCode int
	// restoreFails makes pg_restore of the dump fail, dropFails the DROP DATABASE.
	restoreFails, dropFails bool
	// onRestore is called when pg_restore starts to restore the data.
	onRestore func()
	// super: the role is a superuser.
	super bool
}

func (p *pgProgram) Run(_ context.Context, c postgresql.Cmd) (int, error) {
	p.mu.Lock()
	p.calls = append(p.calls, c)
	fail, code := p.fail, p.failCode
	p.mu.Unlock()
	if strings.HasSuffix(c.Path, "pg_restore") {
		return p.pgRestore(c)
	}
	script := ""
	if c.Stdin != nil {
		data, _ := io.ReadAll(c.Stdin)
		script = string(data)
	}
	if fail != "" {
		c.Stderr(fail)
		return failCode(code), nil
	}
	return p.psql(c, script)
}

func (p *pgProgram) pgRestore(c postgresql.Cmd) (int, error) {
	switch c.Args[0] {
	case "--version":
		_, _ = io.WriteString(c.Stdout, "pg_restore (PostgreSQL) 16.4\n")
	case "-l":
		_, _ = io.WriteString(c.Stdout, "1; 1259 16500 TABLE public t app\n")
	default:
		if p.onRestore != nil {
			p.onRestore()
		}
		_, _ = io.Copy(io.Discard, c.Stdin)
		if p.restoreFails {
			c.Stderr("pg_restore: error: could not execute query")
			return 1, nil
		}
	}
	return 0, nil
}

// answers are what a healthy server answers to a script that contains the key.
var answers = []struct{ marker, answer string }{
	{"server_version_num", "160004|f|t|restorer"},
	{"pg_control_system", "1"},
	{"exists(", "f"},
	{"max_locks", "6400"},
	{"select oid from pg_database", "20001"},
	{"select oid from pg_roles", "20002"},
	{"from pg_database where oid", "1|1"},
	{"from pg_class", "3"},
}

func (p *pgProgram) psql(c postgresql.Cmd, script string) (int, error) {
	if p.dropFails && strings.Contains(script, "DROP DATABASE") {
		c.Stderr("ERROR:  permission denied to drop database")
		return 3, nil
	}
	for _, a := range answers {
		if strings.Contains(script, a.marker) {
			answer := a.answer
			if a.marker == "server_version_num" && p.super {
				answer = "160004|t|t|restorer"
			}
			_, _ = io.WriteString(c.Stdout, answer+"\n")
			break
		}
	}
	return 0, nil
}

func failCode(c int) int { // psql exits 2 when it cannot sign in
	if c == 0 {
		return 2
	}
	return c
}

func (p *pgProgram) psqlCalls() []postgresql.Cmd {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []postgresql.Cmd
	for _, c := range p.calls {
		if strings.HasSuffix(c.Path, "psql") {
			out = append(out, c)
		}
	}
	return out
}

func (p *pgProgram) ran() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

type onlyTools map[string]fs.FileMode

func (t onlyTools) Stat(name string) (fs.FileInfo, error) {
	if mode, ok := t[name]; ok {
		return fileInfo{name, mode}, nil
	}
	return nil, &fs.PathError{Op: "stat", Path: name, Err: syscall.ENOENT}
}

type fileInfo struct {
	name string
	mode fs.FileMode
}

func (i fileInfo) Name() string      { return i.name }
func (fileInfo) Size() int64         { return 0 }
func (i fileInfo) Mode() fs.FileMode { return i.mode }
func (fileInfo) ModTime() time.Time  { return time.Time{} }
func (i fileInfo) IsDir() bool       { return false }
func (fileInfo) Sys() any            { return nil }

func healthyTools() onlyTools {
	return onlyTools{pgBin + "/pg_restore": 0o755, pgBin + "/psql": 0o755}
}

// pgRig is the host with the plugin postgresql, as the agent builds it, and the
// programs of the test.
type pgRig struct {
	*ownRig
	programs *pgProgram
}

func newPGRig(t *testing.T, tools onlyTools) *pgRig {
	t.Helper()
	programs := &pgProgram{}
	r := newOwnRig(t, postgresql.Plugin{
		AgentVersion: "test", Runner: programs, FS: tools,
		Environ: func() []string { return []string{"PATH=" + pgBin} },
	})
	r.repo.snapshots = []restic.Snapshot{
		{ID: pgID, Time: time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC), Paths: []string{"/app.dump"}, Tags: []string{
			"sard.source=s2", "postgresql.part=database", "postgresql.database=app", "postgresql.pg_dump_version=16.4", "postgresql.server_version=16.4"}},
		{ID: pgGID, Time: time.Date(2026, 10, 11, 3, 0, 1, 0, time.UTC), Paths: []string{"/app.globals.sql"}, Tags: []string{
			"sard.source=s2", "postgresql.part=globals", "postgresql.database=app", "postgresql.main_snapshot=" + pgID}},
	}
	r.repo.dump = func(_ context.Context, _, path string, w io.Writer) error {
		content := "PGDMP"
		if strings.HasSuffix(path, ".sql") {
			content = "CREATE ROLE app_rw;\n"
		}
		_, err := io.WriteString(w, content)
		return err
	}
	return &pgRig{ownRig: r, programs: programs}
}

func pgStep(cfg string) *agentv1.RunStep {
	return &agentv1.RunStep{
		CommandId: "c1", Plugin: "postgresql", Action: restore, RepositoryName: "main", SnapshotId: pgID, ConfigJson: cfg,
		Tags: map[string]string{"sard.source": "s2", "sard.run": "r9", "sard.step": "st9"},
	}
}

// runWith runs the step the way the executor hands it over: with the secrets it
// read at its start.
func (r *pgRig) runWith(t *testing.T, st *agentv1.RunStep, secrets map[string][]byte) (*agentv1.StepResult, error) {
	t.Helper()
	handler, ok := r.handlers.Handler("postgresql")
	if !ok {
		t.Fatal("no handler")
	}
	return handler.Run(context.Background(), st, &reporter{snapshot: secrets})
}

// Scenario: Снимок базы восстанавливается в новую базу.
func TestARestoreStepOfADatabaseReportsTheDatabaseTheTablesAndTheRoles(t *testing.T) {
	r := newPGRig(t, healthyTools())
	res, err := r.run(pgStep(pgCfg))
	if err != nil {
		t.Fatal(err)
	}
	want := &agentv1.RestoreOutput{Target: "app_restored_20261011", Database: "app_restored_20261011", TablesRestored: 3}
	if !proto.Equal(res.GetRestore(), want) {
		t.Errorf("output = %v, want %v", res.GetRestore(), want)
	}
	for _, c := range r.programs.psqlCalls() {
		var pw string
		for _, kv := range c.Env {
			if v, ok := strings.CutPrefix(kv, "PGPASSWORD="); ok {
				pw = v
			}
		}
		if pw != secretP2 {
			t.Errorf("PGPASSWORD = %q: the secret of the step, without its line ending", pw)
		}
	}
}

// Scenario: Отказ подключения к целевому PostgreSQL без подсказки check.
func TestAConnectionRefusedByTheTargetGetsNoCheckHint(t *testing.T) {
	r := newPGRig(t, healthyTools())
	r.programs.fail = `psql: error: connection to server at "db2" failed: FATAL:  password authentication failed for user "restorer"`
	res, err := r.run(pgStep(pgCfg))
	if err == nil || !strings.HasPrefix(err.Error(), "PG_CONNECT (check)") || strings.Contains(err.Error(), "sard-agent check") {
		t.Fatalf("err = %v", err)
	}
	if res != nil {
		t.Errorf("result %v", res)
	}
}

// Scenario: Нет клиента PostgreSQL — отказ с подсказкой check.
func TestAMissingPgRestoreFailsTheStepWithTheCheckHint(t *testing.T) {
	r := newPGRig(t, onlyTools{})
	_, err := r.run(pgStep(pgCfg))
	if err == nil || !strings.HasPrefix(err.Error(), "PG_CLIENT_MISSING (check)") || !strings.HasSuffix(err.Error(), "\n"+checkHint) {
		t.Fatalf("err = %v", err)
	}
	var f *consolerestore.Failure
	if !errors.As(err, &f) {
		t.Errorf("the cause is lost: %v", err)
	}
	if len(r.programs.psqlCalls()) != 0 {
		t.Error("psql ran")
	}
}

// A secret that cannot be read is told as it is, not as an empty one.
func TestASecretThatCannotBeReadFailsTheStepWithTheReadError(t *testing.T) {
	r := newPGRig(t, healthyTools())
	cfg := strings.Replace(pgCfg, "pg-restore", "gone", 1)
	_, err := r.runWith(t, pgStep(cfg), nil)
	if err == nil || !strings.HasPrefix(err.Error(), "SECRET_INVALID (check)") || !strings.Contains(err.Error(), `secret "gone"`) || strings.Contains(err.Error(), "is empty") {
		t.Fatalf("err = %v", err)
	}
	if r.programs.ran() != 0 {
		t.Error("a program ran")
	}
}

// A secret whose file holds nothing cannot sign in.
func TestASecretWithoutContentFailsTheStep(t *testing.T) {
	r := newPGRig(t, healthyTools())
	_, err := r.runWith(t, pgStep(pgCfg), map[string][]byte{"pg-restore": []byte("\r\n")})
	if err == nil || !strings.HasPrefix(err.Error(), "SECRET_INVALID (check)") || !strings.Contains(err.Error(), `"pg-restore"`) {
		t.Fatalf("err = %v", err)
	}
	if r.programs.ran() != 0 {
		t.Error("a program ran")
	}
}

func TestASecretWithANULByteFailsTheStep(t *testing.T) {
	r := newPGRig(t, healthyTools())
	_, err := r.runWith(t, pgStep(pgCfg), map[string][]byte{"pg-restore": []byte("a\x00b")})
	if err == nil || !strings.HasPrefix(err.Error(), "SECRET_INVALID (check)") {
		t.Fatalf("err = %v", err)
	}
}

// Scenario: Недопустимый конфиг восстановления postgresql отклоняется по схеме.
func TestAnInvalidConfigOfADatabaseRestoreIsRejectedBeforeAnyProgramRuns(t *testing.T) {
	r := newPGRig(t, healthyTools())
	_, err := r.run(pgStep(`{"password_ref": "nope", "database": "x"}`))
	if !errors.Is(err, executor.ErrRejected) || !strings.Contains(err.Error(), "password_ref") {
		t.Fatalf("err = %v", err)
	}
	if r.programs.ran() != 0 {
		t.Error("a program ran")
	}
}

// Failures that leave something carry only the leftover in the output.
func TestAFailedRollbackOfADatabaseCarriesTheLeftoverInTheOutput(t *testing.T) {
	r := newPGRig(t, healthyTools())
	r.programs.restoreFails, r.programs.dropFails = true, true
	res, err := r.run(pgStep(pgCfg))
	if err == nil || !strings.HasPrefix(err.Error(), "CLEANUP_FAILED (rollback)") {
		t.Fatalf("err = %v", err)
	}
	want := &agentv1.RestoreOutput{Leftover: "database app_restored_20261011"}
	if res == nil || !proto.Equal(res.GetRestore(), want) {
		t.Errorf("result %v, want only %v", res, want)
	}
}

func TestAFailedRestoreThatRolledBackCleanlyCarriesNoOutput(t *testing.T) {
	r := newPGRig(t, healthyTools())
	r.programs.restoreFails = true
	res, err := r.run(pgStep(pgCfg))
	if err == nil || !strings.HasPrefix(err.Error(), "PG_RESTORE_FAILED (restore)") || !strings.HasSuffix(err.Error(), "nothing was restored") {
		t.Fatalf("err = %v", err)
	}
	if res != nil {
		t.Errorf("result %v", res)
	}
}

// Scenario: Журнал отката PostgreSQL лежит в журнале восстановления под именем плагина.
func TestTheRollbackEntryOfADatabaseLiesInTheRestoreJournalUnderTheNameOfThePlugin(t *testing.T) {
	r := newPGRig(t, healthyTools())
	r.programs.super = true
	var plugin, commandID, entry string
	r.programs.onRestore = func() { // after CREATE DATABASE, before the data
		p, c, e, found := r.envelopeOf("c1")
		if !found {
			t.Error("no entry of c1 in the restore journal")
		}
		plugin, commandID, entry = p, c, string(e)
	}
	cfg := strings.Replace(pgCfg, `"database"`, `"restore_globals": true, "database"`, 1)
	if _, err := r.run(pgStep(cfg)); err != nil {
		t.Fatal(err)
	}
	if plugin != "postgresql" || commandID != "c1" {
		t.Errorf("envelope of %q, command %q", plugin, commandID)
	}
	for _, want := range []string{`"database":{"name":"app_restored_20261011","oid":20001}`, `"password_ref":"pg-restore"`, `"roles":[{"name":"app_rw","oid":20002}]`} {
		if !strings.Contains(entry, want) {
			t.Errorf("the entry lacks %s: %s", want, entry)
		}
	}
	if strings.Contains(entry, secretP2) {
		t.Errorf("the entry holds the password: %s", entry)
	}
	assertNoLegacyEntries(t, r.state)
}

// Scenario: Новый агент не пишет в прежний журнал.
func TestTheNewAgentNeverWritesToTheJournalOfTheOldOne(t *testing.T) {
	r := newPGRig(t, healthyTools())
	if _, err := r.run(pgStep(pgCfg)); err != nil {
		t.Fatal(err)
	}
	assertNoLegacyEntries(t, r.state)
}

func assertNoLegacyEntries(t *testing.T, state string) {
	t.Helper()
	if entries, err := os.ReadDir(filepath.Join(state, "pg-restore")); err == nil && len(entries) != 0 {
		t.Errorf("the journal of the previous release holds %v", entries)
	}
}

// Scenario: Запись удаляет хост после записи результата.
func TestTheJournalEntryOfADatabaseRestoreGoesWhenTheResultIsJournaled(t *testing.T) {
	r := newPGRig(t, healthyTools())
	res, err := r.run(pgStep(pgCfg))
	if err != nil {
		t.Fatal(err)
	}
	if !r.hasEntry("c1") {
		t.Fatal("no entry before the result is journaled")
	}
	res.CommandId, res.Status = "c1", agentv1.StepStatus_STEP_STATUS_SUCCEEDED
	r.journal.Journaled(res)
	if r.hasEntry("c1") {
		t.Error("the entry stays after its result was journaled")
	}
}

// The entry of a restore whose rollback left a database with an OID stays
// after a failed result: the next start tries again.
func TestTheEntryOfAFailedRollbackStaysAfterTheFailedResultIsJournaled(t *testing.T) {
	r := newPGRig(t, healthyTools())
	r.programs.restoreFails, r.programs.dropFails = true, true
	if _, err := r.run(pgStep(pgCfg)); err == nil {
		t.Fatal("no error")
	}
	r.journal.Journaled(&agentv1.StepResult{CommandId: "c1", Status: agentv1.StepStatus_STEP_STATUS_FAILED})
	if !r.hasEntry("c1") {
		t.Error("the entry went: the next start cannot drop the database")
	}
}

// A clean rollback leaves nothing to retry: the entry goes with the failed result.
func TestTheEntryOfARolledBackRestoreGoesWithTheFailedResult(t *testing.T) {
	r := newPGRig(t, healthyTools())
	r.programs.restoreFails = true
	if _, err := r.run(pgStep(pgCfg)); err == nil {
		t.Fatal("no error")
	}
	r.journal.Journaled(&agentv1.StepResult{CommandId: "c1", Status: agentv1.StepStatus_STEP_STATUS_FAILED})
	if r.hasEntry("c1") {
		t.Error("the entry stays")
	}
}

// Scenario: Убитое после завершения pg_restore восстановление сообщает об успехе из журнала отката.
func TestAnInterruptedRestoreOfADatabaseThatCompletedSucceedsWithTheTotalsOfTheJournal(t *testing.T) {
	r := newPGRig(t, healthyTools())
	entry := `{"command_id":"c1","connection":{"host":"db2","port":5432,"user":"restorer","password_ref":"pg-restore"},` +
		`"database":{"name":"app_restored_20261011","oid":20001},"result":{"database":"app_restored_20261011","tables_restored":2,"roles_restored":1}}`
	if err := r.store.Save("postgresql", "c1", []byte(entry)); err != nil {
		t.Fatal(err)
	}
	got := r.recoverAll(time.Minute).Interrupted("c1", restore)
	if got == nil || !got.Succeeded || !strings.Contains(got.Message, "agent restarted after the restore completed") {
		t.Fatalf("Interruption = %+v", got)
	}
	want := &agentv1.RestoreOutput{Target: "app_restored_20261011", Database: "app_restored_20261011", TablesRestored: 2, RolesRestored: 1}
	if !proto.Equal(got.Output, want) {
		t.Errorf("output = %v, want %v", got.Output, want)
	}
}

// Scenario: База из намерения без записанного OID не удаляется и называется оставшейся.
func TestAnInterruptedRestoreOfADatabaseWhoseRollbackLeftSomethingNamesIt(t *testing.T) {
	r := newPGRig(t, healthyTools())
	entry := `{"command_id":"c1","connection":{"host":"db2","port":5432,"user":"restorer","password_ref":"pg-restore"},` +
		`"database":{"name":"app_restored_20261011"},"roles":[{"name":"app_rw"}]}`
	if err := r.store.Save("postgresql", "c1", []byte(entry)); err != nil {
		t.Fatal(err)
	}
	got := r.recoverAll(time.Minute).Interrupted("c1", restore)
	if got == nil || !strings.HasPrefix(got.Message, "CLEANUP_FAILED (rollback): interrupted: agent restarted before the step finished; "+
		"left behind: database app_restored_20261011, role app_rw (") || !strings.HasSuffix(got.Message, "); remove it by hand") ||
		got.Output.GetLeftover() != "database app_restored_20261011, role app_rw" {
		t.Fatalf("Interruption = %+v", got)
	}
	if r.programs.ran() != 0 {
		t.Error("something was dropped by its name")
	}
}
