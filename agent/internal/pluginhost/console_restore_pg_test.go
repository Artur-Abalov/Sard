// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/proto"
)

const (
	pgID  = "bbbb1111bbbb1111bbbb1111bbbb1111bbbb1111bbbb1111bbbb1111bbbb1111"
	pgBin = "/usr/lib/postgresql/16/bin"
	pgCfg = `{"password_ref": "pg", "database": "app_restored_20261011"}`
)

// dbRestorer is a plugin the console can restore into a new database.
type dbRestorer struct{ plugin }

func (*dbRestorer) RestoreConfigSchema() []byte {
	return []byte(`{"type": "object", "properties": {"password_ref": {"type": "string", "format": "sard-secret"}, "database": {"type": "string"}, "globals": {"type": "boolean"}}, "required": ["password_ref", "database"], "additionalProperties": false}`)
}

func (*dbRestorer) PlanRestore(cfg sdk.Config) (sdk.RestorePlan, error) {
	return sdk.RestorePlan{Postgres: &sdk.PostgresRestore{Host: "db2", Port: 5432, User: "restorer", PasswordRef: "pg", TLSMode: "disable", Name: "app_restored_20261011"}}, nil
}

// pgProgram plays psql and pg_restore: the target answers what a healthy
// server would, unless fails says otherwise.
type pgProgram struct {
	mu    sync.Mutex
	calls []restic.Command
	// fail, if set, is the stderr and exit code of psql.
	fail     string
	failCode int
	// restoreFails makes pg_restore of the dump fail, dropFails the DROP DATABASE.
	restoreFails, dropFails bool
}

func (p *pgProgram) Run(_ context.Context, c restic.Command) (int, error) {
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
		c.Stderr([]byte(fail))
		return failCode(code), nil
	}
	return p.psql(c, script)
}

func (p *pgProgram) pgRestore(c restic.Command) (int, error) {
	switch {
	case len(c.Args) == 1:
		c.Stdout([]byte("pg_restore (PostgreSQL) 16.4"))
	case c.Args[0] == "-l":
		c.Stdout([]byte("1; 1259 16500 TABLE public t app"))
	default:
		_, _ = io.Copy(io.Discard, c.Stdin)
		if p.restoreFails {
			c.Stderr([]byte("pg_restore: error: could not execute query"))
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
	{"from pg_database where oid", "1|1"},
	{"from pg_class", "3"},
}

func (p *pgProgram) psql(c restic.Command, script string) (int, error) {
	if p.dropFails && strings.Contains(script, "DROP DATABASE") {
		c.Stderr([]byte("ERROR:  permission denied to drop database"))
		return 3, nil
	}
	for _, a := range answers {
		if strings.Contains(script, a.marker) {
			c.Stdout([]byte(a.answer))
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

func (p *pgProgram) psqlCalls() []restic.Command {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []restic.Command
	for _, c := range p.calls {
		if strings.HasSuffix(c.Path, "psql") {
			out = append(out, c)
		}
	}
	return out
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

type pgConsoleHost struct {
	*handlersFixture
	programs *pgProgram
	state    string
	engine   *consolerestore.Engine
}

func newPGConsoleHost(t *testing.T, tools onlyTools) *pgConsoleHost {
	t.Helper()
	return newPGConsoleHostWith(t, tools, map[string]string{"/etc/sard/pg": "s3cret\n"})
}

// newPGConsoleHostWith is a host whose secret files are data.
func newPGConsoleHostWith(t *testing.T, tools onlyTools, data map[string]string) *pgConsoleHost {
	t.Helper()
	reg, err := sdk.NewRegistry(&dbRestorer{})
	if err != nil {
		t.Fatal(err)
	}
	f := &handlersFixture{repo: &repo{}, restoreDir: filepath.Join(t.TempDir(), "verify")}
	f.repo.snapshots = []restic.Snapshot{{ID: pgID, Time: time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC), Paths: []string{"/app.dump"}, Tags: []string{
		"sard.source=s2", "postgresql.part=database", "postgresql.database=app", "postgresql.pg_dump_version=16.4", "postgresql.server_version=16.4"}}}
	f.repo.dump = func(_ context.Context, _, _ string, w io.Writer) error {
		_, err := io.WriteString(w, "PGDMP")
		return err
	}
	programs := &pgProgram{}
	state := t.TempDir()
	engine := consolerestore.New(consolerestore.Options{
		Dir: filepath.Join(t.TempDir(), "restore"), FS: hostsetup.OS{}, Owner: hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid(), Mode: 0o700}, Logger: slog.New(slog.DiscardHandler),
		Postgres: consolerestore.PostgresOptions{Exec: programs, Tools: tools, StateDir: state, LockShare: 0.5,
			Environ: func() []string { return []string{"PATH=" + pgBin} }},
	})
	secrets := pluginhost.NewSecrets(map[string]string{"pg": "/etc/sard/pg"}, (&files{data: data}).read)
	repos := func(name, commandID string, stderr io.Writer) (restic.Repository, bool) {
		return f.repo, name == "main"
	}
	f.handlers, err = pluginhost.NewHandlers(reg, secrets, repos, f.restoreDir, pluginhost.WithConsoleRestore(engine))
	if err != nil {
		t.Fatal(err)
	}
	return &pgConsoleHost{handlersFixture: f, programs: programs, state: state, engine: engine}
}

func healthyTools() onlyTools {
	return onlyTools{pgBin + "/pg_restore": 0o755, pgBin + "/psql": 0o755}
}

func pgStep(cfg string) *agentv1.RunStep {
	st := step(restore, cfg)
	st.SnapshotId = pgID
	st.Tags = map[string]string{"sard.source": "s2", "sard.run": "r9", "sard.step": "st9"}
	return st
}

// run runs the step with the secret the executor read at its start, the way
// the executor hands it over.
func (h *pgConsoleHost) run(t *testing.T, st *agentv1.RunStep) (*agentv1.StepResult, error) {
	t.Helper()
	handler, ok := h.handlers.Handler("fake")
	if !ok {
		t.Fatal("no handler")
	}
	return handler.Run(context.Background(), st, &reporter{snapshot: map[string][]byte{"pg": []byte("s3cret\n")}})
}

// Scenario: Снимок базы восстанавливается в новую базу.
func TestARestoreStepOfADatabaseReportsTheDatabaseTheTablesAndTheRoles(t *testing.T) {
	h := newPGConsoleHost(t, healthyTools())
	res, err := h.run(t, pgStep(pgCfg))
	if err != nil {
		t.Fatal(err)
	}
	want := &agentv1.RestoreOutput{Target: "app_restored_20261011", Database: "app_restored_20261011", TablesRestored: 3}
	if !proto.Equal(res.GetRestore(), want) {
		t.Errorf("output = %v, want %v", res.GetRestore(), want)
	}
	for _, c := range h.programs.psqlCalls() {
		var pw string
		for _, kv := range c.Env {
			if v, ok := strings.CutPrefix(kv, "PGPASSWORD="); ok {
				pw = v
			}
		}
		if pw != "s3cret" {
			t.Errorf("PGPASSWORD = %q: the secret of the step, without its line ending", pw)
		}
	}
}

// Scenario: Отказ подключения к целевому PostgreSQL без подсказки check.
func TestAConnectionRefusedByTheTargetGetsNoCheckHint(t *testing.T) {
	h := newPGConsoleHost(t, healthyTools())
	h.programs.fail = `psql: error: connection to server at "db2" failed: FATAL:  password authentication failed for user "restorer"`
	res, err := h.run(t, pgStep(pgCfg))
	if err == nil || !strings.HasPrefix(err.Error(), "PG_CONNECT (check)") || strings.Contains(err.Error(), "sard-agent check") {
		t.Fatalf("err = %v", err)
	}
	if res != nil {
		t.Errorf("result %v", res)
	}
}

// Scenario: Нет клиента PostgreSQL — отказ с подсказкой check.
func TestAMissingPgRestoreFailsTheStepWithTheCheckHint(t *testing.T) {
	h := newPGConsoleHost(t, onlyTools{})
	_, err := h.run(t, pgStep(pgCfg))
	if err == nil || !strings.HasPrefix(err.Error(), "PG_CLIENT_MISSING (check)") || !strings.HasSuffix(err.Error(), "\n"+checkHint) {
		t.Fatalf("err = %v", err)
	}
	var f *consolerestore.Failure
	if !errors.As(err, &f) {
		t.Errorf("the cause is lost: %v", err)
	}
	if len(h.programs.psqlCalls()) != 0 {
		t.Error("psql ran")
	}
}

// A secret that cannot be read is told as it is, not as an empty one.
func TestASecretThatCannotBeReadFailsTheStepWithTheReadError(t *testing.T) {
	h := newPGConsoleHostWith(t, healthyTools(), map[string]string{})
	handler, _ := h.handlers.Handler("fake")
	_, err := handler.Run(context.Background(), pgStep(pgCfg), &reporter{})
	if err == nil || !strings.HasPrefix(err.Error(), "SECRET_INVALID (check)") || !strings.Contains(err.Error(), `secret "pg"`) || strings.Contains(err.Error(), "is empty") {
		t.Fatalf("err = %v", err)
	}
	if len(h.programs.psqlCalls()) != 0 {
		t.Error("psql ran")
	}
}

// A secret whose file holds nothing cannot sign in.
func TestASecretWithoutContentFailsTheStep(t *testing.T) {
	h := newPGConsoleHost(t, healthyTools())
	handler, _ := h.handlers.Handler("fake")
	_, err := handler.Run(context.Background(), pgStep(pgCfg), &reporter{snapshot: map[string][]byte{"pg": []byte("\r\n")}})
	if err == nil || !strings.HasPrefix(err.Error(), "SECRET_INVALID (check)") || !strings.Contains(err.Error(), `"pg"`) {
		t.Fatalf("err = %v", err)
	}
	if len(h.programs.psqlCalls()) != 0 {
		t.Error("psql ran")
	}
}

func TestASecretWithANULByteFailsTheStep(t *testing.T) {
	h := newPGConsoleHost(t, healthyTools())
	handler, _ := h.handlers.Handler("fake")
	_, err := handler.Run(context.Background(), pgStep(pgCfg), &reporter{snapshot: map[string][]byte{"pg": []byte("a\x00b")}})
	if err == nil || !strings.HasPrefix(err.Error(), "SECRET_INVALID (check)") {
		t.Fatalf("err = %v", err)
	}
}

// Scenario: Недопустимый конфиг восстановления postgresql отклоняется по схеме.
func TestAnInvalidConfigOfADatabaseRestoreIsRejectedBeforeAnyProgramRuns(t *testing.T) {
	h := newPGConsoleHost(t, healthyTools())
	_, err := h.run(t, pgStep(`{"password_ref": "nope", "database": "x"}`))
	if !errors.Is(err, executor.ErrRejected) || !strings.Contains(err.Error(), "password_ref") {
		t.Fatalf("err = %v", err)
	}
	if len(h.programs.calls) != 0 {
		t.Error("a program ran")
	}
}

// Failures that leave something carry only the leftover in the output.
func TestAFailedRollbackOfADatabaseCarriesTheLeftoverInTheOutput(t *testing.T) {
	h := newPGConsoleHost(t, healthyTools())
	h.programs.restoreFails, h.programs.dropFails = true, true
	res, err := h.run(t, pgStep(pgCfg))
	if err == nil || !strings.HasPrefix(err.Error(), "CLEANUP_FAILED (rollback)") {
		t.Fatalf("err = %v", err)
	}
	want := &agentv1.RestoreOutput{Leftover: "database app_restored_20261011"}
	if res == nil || !proto.Equal(res.GetRestore(), want) {
		t.Errorf("result %v, want only %v", res, want)
	}
}

func TestAFailedRestoreThatRolledBackCleanlyCarriesNoOutput(t *testing.T) {
	h := newPGConsoleHost(t, healthyTools())
	h.programs.restoreFails = true
	res, err := h.run(t, pgStep(pgCfg))
	if err == nil || !strings.HasPrefix(err.Error(), "PG_RESTORE_FAILED (restore)") || !strings.HasSuffix(err.Error(), "nothing was restored") {
		t.Fatalf("err = %v", err)
	}
	if res != nil {
		t.Errorf("result %v", res)
	}
}

// Scenario: Settle after the result is journaled removes the entry of the restore.
func TestTheJournalEntryOfADatabaseRestoreGoesWhenTheResultIsJournaled(t *testing.T) {
	h := newPGConsoleHost(t, healthyTools())
	if _, err := h.run(t, pgStep(pgCfg)); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(h.state, "pg-restore"))
	if len(entries) != 1 {
		t.Fatalf("journal %v", entries)
	}
	h.engine.Settle("cmd-1")
	if entries, _ := os.ReadDir(filepath.Join(h.state, "pg-restore")); len(entries) != 0 {
		t.Errorf("journal %v", entries)
	}
}

// Scenario: Убитое после завершения pg_restore восстановление сообщает об успехе из журнала отката.
func TestAnInterruptedRestoreOfADatabaseThatCompletedSucceedsWithTheTotalsOfTheJournal(t *testing.T) {
	interrupted := pluginhost.Interrupted([]consolerestore.Evidence{
		{Name: "c1", Path: "/s/pg-restore/c1.json", Verdict: consolerestore.Completed, Database: &consolerestore.DatabaseOutput{Database: "app_restored_20261011", Tables: 2, Roles: 1}},
	})
	got := interrupted("c1", restore)
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
	interrupted := pluginhost.Interrupted([]consolerestore.Evidence{
		{Name: "c1", Path: "/s/pg-restore/c1.json", Verdict: consolerestore.CleanupFailed, Leftover: "database app_restored_20261011, role app_rw", Err: errors.New("not dropped by name")},
	})
	got := interrupted("c1", restore)
	want := "CLEANUP_FAILED (rollback): interrupted: agent restarted before the step finished; " +
		"left behind: database app_restored_20261011, role app_rw (not dropped by name); remove it by hand"
	if got == nil || got.Message != want || got.Output.GetLeftover() != "database app_restored_20261011, role app_rw" {
		t.Fatalf("Interruption = %+v", got)
	}
}
