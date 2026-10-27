// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
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

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/restorejournal"
	"github.com/Artur-Abalov/sard/agent/plugins"
	"github.com/Artur-Abalov/sard/agent/plugins/postgresql"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// The journal of the restores of databases the previous release (R2b) left in
// <executor.state_dir>/pg-restore is read for one release (ADR 0066 point 4):
// the files of testdata/r2b-journal are those the code of R2b wrote.

const (
	upgradeBin = "/usr/lib/postgresql/16/bin"
	upgradeDB  = "app_restored_20261011"
)

// cluster is the PostgreSQL server as far as the rollback of an interrupted
// restore sees it: it answers the checks by OID and drops what it is told to.
type cluster struct {
	mu          sync.Mutex
	databases   map[string]uint32
	roles       map[string]uint32
	dropDBError string
	events      []string
}

func (c *cluster) Run(_ context.Context, cmd postgresql.Cmd) (int, error) {
	script, vars := scriptOf(cmd)
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case strings.Contains(script, "from pg_database where oid"):
		answerByOID(cmd, c.databases, vars)
	case strings.Contains(script, "from pg_roles where oid"):
		answerByOID(cmd, c.roles, vars)
	case strings.Contains(script, "DROP DATABASE"):
		return c.drop(cmd, c.databases, "database", vars["name"])
	case strings.Contains(script, "DROP ROLE"):
		return c.drop(cmd, c.roles, "role", vars["name"])
	}
	return 0, nil
}

// drop drops the object, unless the test made the server refuse the DROP of a database.
func (c *cluster) drop(cmd postgresql.Cmd, objects map[string]uint32, kind, name string) (int, error) {
	if kind == "database" && c.dropDBError != "" {
		cmd.Stderr(c.dropDBError)
		return 3, nil
	}
	c.events = append(c.events, "drop "+kind+" "+name)
	delete(objects, name)
	return 0, nil
}

// scriptOf is the script psql got on its standard input and its variables.
func scriptOf(cmd postgresql.Cmd) (script string, vars map[string]string) {
	if cmd.Stdin != nil {
		data, _ := io.ReadAll(cmd.Stdin)
		script = string(data)
	}
	vars = map[string]string{}
	for i, a := range cmd.Args {
		if a == "-v" && i+1 < len(cmd.Args) {
			k, v, _ := strings.Cut(cmd.Args[i+1], "=")
			vars[k] = v
		}
	}
	return script, vars
}

// answerByOID prints "<objects with the name>|<objects with the OID>".
func answerByOID(cmd postgresql.Cmd, objects map[string]uint32, vars map[string]string) {
	same, found := 0, 0
	for name, oid := range objects {
		if strings.TrimSpace(vars["oid"]) == itoa(oid) {
			found = 1
			if name == vars["name"] {
				same = 1
			}
		}
	}
	_, _ = io.WriteString(cmd.Stdout, itoa(uint32(same))+"|"+itoa(uint32(found))+"\n")
}

func itoa(n uint32) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func (c *cluster) dropped() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.events...)
}

type toolsFS map[string]bool

func (t toolsFS) Stat(name string) (fs.FileInfo, error) {
	if !t[name] {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: syscall.ENOENT}
	}
	return toolInfo{name}, nil
}

type toolInfo struct{ name string }

func (i toolInfo) Name() string     { return i.name }
func (toolInfo) Size() int64        { return 0 }
func (toolInfo) Mode() fs.FileMode  { return 0o755 }
func (toolInfo) ModTime() time.Time { return time.Time{} }
func (toolInfo) IsDir() bool        { return false }
func (toolInfo) Sys() any           { return nil }

// spyPostgres is the postgresql plugin that notes which entries it was given.
type spyPostgres struct {
	postgresql.Plugin
	mu      sync.Mutex
	entries map[string][][]byte
}

func (s *spyPostgres) Recover(ctx context.Context, h sdk.RecoveryHost, id string, entry []byte) sdk.Recovery {
	s.mu.Lock()
	if s.entries == nil {
		s.entries = map[string][][]byte{}
	}
	s.entries[id] = append(s.entries[id], entry)
	s.mu.Unlock()
	return s.Plugin.Recover(ctx, h, id, entry)
}

// upgradeRig is an agent that starts over the state directory a previous release left.
type upgradeRig struct {
	t       *testing.T
	state   string
	cfg     config.Config
	cluster *cluster
	plugin  *spyPostgres
	log     *bytes.Buffer
}

func newUpgradeRig(t *testing.T) *upgradeRig {
	t.Helper()
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	password := filepath.Join(dir, "pg-restore.secret")
	writeFile(t, password, []byte("pw\n"), 0o600)
	cfg := configIn(t, dir, "server:\n  address: s:1\nexecutor:\n  state_dir: "+state+"\nsecrets:\n  pg-restore: "+password+"\n")
	bindIn(t, dir, "pg-restore", "db2:5432/*")
	c := &cluster{
		databases: map[string]uint32{upgradeDB: 20003, "postgres": 5},
		roles:     map[string]uint32{"app_rw": 20001, "app_ro": 20002, "restorer": 16390},
	}
	p := &spyPostgres{Plugin: postgresql.Plugin{
		AgentVersion: "test", Runner: c, FS: toolsFS{upgradeBin + "/pg_restore": true, upgradeBin + "/psql": true},
		Environ: func() []string { return []string{"PATH=" + upgradeBin} },
	}}
	return &upgradeRig{t: t, state: state, cfg: cfg, cluster: c, plugin: p, log: &bytes.Buffer{}}
}

// legacy puts a file of testdata into the journal of the previous release as c1.
func (u *upgradeRig) legacy(file string) string {
	u.t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "r2b-journal", file))
	if err != nil {
		u.t.Fatal(err)
	}
	dir := filepath.Join(u.state, "pg-restore")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		u.t.Fatal(err)
	}
	path := filepath.Join(dir, "c1.json")
	writeFile(u.t, path, data, 0o600)
	return path
}

// start is the start of the agent as main makes it: the journal of the state
// directory, the plugins recover their entries, the recovery of the host.
func (u *upgradeRig) start() *recovery {
	u.t.Helper()
	logger := slog.New(slog.NewTextHandler(u.log, nil))
	journal := newRestoreJournal(u.cfg, logger)
	recovered := recoverJournal(u.cfg, journal, registryOf(u.t, u.plugin), nil, logger)
	engine := consolerestore.New(consolerestore.Options{Dir: u.t.TempDir(), FS: hostsetup.OS{}, Logger: logger})
	return newRecoveryWith(engine, journal, recovered)
}

func (u *upgradeRig) newEntry(id string) string {
	return filepath.Join(u.state, "restore-journal", id+".json")
}

func isThere(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Scenario: Прежняя запись разбирается плагином postgresql (a database and a role with OIDs).
func TestALegacyEntryWithObjectsIsRolledBackBeforeAnyStepIsAccepted(t *testing.T) {
	u := newUpgradeRig(t)
	u.legacy("created.json")
	r := u.start()
	if _, ok := u.cluster.databases[upgradeDB]; ok {
		t.Error("the database with the OID of the entry is still there")
	}
	if _, ok := u.cluster.roles["app_rw"]; ok {
		t.Error("the role app_rw is still there")
	}
	assertNothingRestored(t, r, "c1")
	if _, ok := u.cluster.roles["restorer"]; !ok {
		t.Error("a role the entry does not name was dropped")
	}
}

// Scenario: Прежняя запись разбирается плагином postgresql (completed).
func TestALegacyEntryOfACompletedRestoreSucceedsAndDropsNothing(t *testing.T) {
	u := newUpgradeRig(t)
	u.legacy("completed.json")
	r := u.start()
	got := r.interrupted("c1", agentv1.Action_ACTION_RESTORE)
	if got == nil || !got.Succeeded || !strings.Contains(got.Message, "agent restarted after the restore completed") ||
		got.Output.GetDatabase() != upgradeDB || got.Output.GetTablesRestored() != 2 || got.Output.GetRolesRestored() != 0 {
		t.Fatalf("Interruption = %+v", got)
	}
	if events := u.cluster.dropped(); len(events) != 0 {
		t.Errorf("dropped %v", events)
	}
}

// Scenario: Прежняя запись разбирается плагином postgresql (the intent only).
func TestALegacyEntryOfAnIntentOnlyNamesTheDatabaseAndDropsNothing(t *testing.T) {
	u := newUpgradeRig(t)
	u.legacy("intent.json")
	got := u.start().interrupted("c1", agentv1.Action_ACTION_RESTORE)
	if got == nil || got.Succeeded || !strings.HasPrefix(got.Message, "CLEANUP_FAILED") || got.Output.GetLeftover() != "database "+upgradeDB {
		t.Fatalf("Interruption = %+v", got)
	}
	if events := u.cluster.dropped(); len(events) != 0 {
		t.Errorf("dropped %v", events)
	}
}

// Scenario: Прежняя запись удаляется после записи результата и не переносится в новый журнал.
func TestALegacyEntryGoesWithItsResultAndIsNotCarriedToTheNewJournal(t *testing.T) {
	u := newUpgradeRig(t)
	path := u.legacy("created.json")
	r := u.start()
	r.interrupted("c1", agentv1.Action_ACTION_RESTORE)
	if !isThere(path) {
		t.Fatal("the entry went before the result was journaled")
	}
	r.journaled(&agentv1.StepResult{CommandId: "c1", Status: agentv1.StepStatus_STEP_STATUS_FAILED})
	if isThere(path) || isThere(u.newEntry("c1")) {
		t.Errorf("the legacy entry %v, the new one %v", isThere(path), isThere(u.newEntry("c1")))
	}
}

// Scenario: Прежняя запись, которую плагин просил сохранить, остаётся в прежнем журнале.
func TestALegacyEntryThatThePluginAskedToKeepStaysInTheOldJournal(t *testing.T) {
	u := newUpgradeRig(t)
	path := u.legacy("created.json")
	u.cluster.dropDBError = "ERROR:  permission denied to drop database"
	r := u.start()
	got := r.interrupted("c1", agentv1.Action_ACTION_RESTORE)
	if got == nil || got.Succeeded || !strings.HasPrefix(got.Message, "CLEANUP_FAILED") || !strings.Contains(got.Output.GetLeftover(), "database "+upgradeDB) {
		t.Fatalf("Interruption = %+v", got)
	}
	r.journaled(&agentv1.StepResult{CommandId: "c1", Status: agentv1.StepStatus_STEP_STATUS_FAILED, Output: &agentv1.StepResult_Restore{Restore: got.Output}})
	if !isThere(path) {
		t.Error("the entry went: the next start cannot drop the database")
	}
}

// Scenario: Испорченная прежняя запись называется и остаётся.
func TestACorruptedLegacyEntryIsNamedAndStays(t *testing.T) {
	u := newUpgradeRig(t)
	path := u.legacy("corrupted.json")
	r := u.start()
	got := r.interrupted("c1", agentv1.Action_ACTION_RESTORE)
	if got == nil || got.Succeeded || !strings.HasPrefix(got.Message, "CLEANUP_FAILED") || got.Output.GetLeftover() != path {
		t.Fatalf("Interruption = %+v", got)
	}
	r.journaled(&agentv1.StepResult{CommandId: "c1", Status: agentv1.StepStatus_STEP_STATUS_FAILED, Output: &agentv1.StepResult_Restore{Restore: got.Output}})
	r.executorStarted() // the agent accepts steps
	if !isThere(path) {
		t.Error("the corrupted entry went")
	}
}

// Scenario: Без прежнего журнала агент стартует как обычно.
func TestWithoutALegacyJournalTheAgentStartsAsUsual(t *testing.T) {
	u := newUpgradeRig(t)
	r := u.start()
	r.executorStarted()
	if strings.Contains(u.log.String(), "level=ERROR") || strings.Contains(u.log.String(), "restore journal") {
		t.Errorf("log:\n%s", u.log.String())
	}
	if isThere(filepath.Join(u.state, "pg-restore")) {
		t.Error("the directory of the previous journal was made")
	}
}

// Scenario: Запись одной команды в двух журналах разбирается по новому.
func TestACommandInBothJournalsIsRecoveredOnceFromTheNewOne(t *testing.T) {
	u := newUpgradeRig(t)
	legacy := u.legacy("created.json")
	completed, err := os.ReadFile(filepath.Join("testdata", "r2b-journal", "completed.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := newJournalStore(u.cfg).Save("postgresql", "c1", completed); err != nil {
		t.Fatal(err)
	}
	r := u.start()
	if got := u.plugin.entries["c1"]; len(got) != 1 || !bytes.Equal(got[0], completed) {
		t.Fatalf("the plugin was given %q", got)
	}
	if events := u.cluster.dropped(); len(events) != 0 {
		t.Errorf("the legacy entry was taken: dropped %v", events)
	}
	r.interrupted("c1", agentv1.Action_ACTION_RESTORE)
	r.journaled(&agentv1.StepResult{CommandId: "c1", Status: agentv1.StepStatus_STEP_STATUS_SUCCEEDED})
	if isThere(legacy) || isThere(u.newEntry("c1")) {
		t.Errorf("the legacy entry %v, the new one %v", isThere(legacy), isThere(u.newEntry("c1")))
	}
}

// Scenario: Доля таблицы блокировок из конфига агента доходит до плагина.
func TestTheLockShareOfTheConfigOfTheAgentReachesThePlugin(t *testing.T) {
	tests := []struct {
		name, yaml, mode, share string
	}{
		{"without the setting", "", "single transaction", "0.5"},
		{"with the setting in agent.d", "restore:\n  postgresql_lock_share: 0.25\n", "single transaction", "0.25"},
		{"with zero", "restore:\n  postgresql_lock_share: 0\n", "separate transactions", "0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := mustConfig(t, "server:\n  address: s:1\n"+tt.yaml)
			pg := builtinPostgresql(t, cfg)
			server := &healthyServer{}
			pg.Runner, pg.FS = server, toolsFS{upgradeBin + "/pg_restore": true, upgradeBin + "/psql": true}
			pg.Environ = func() []string { return []string{"PATH=" + upgradeBin} }
			rep := &stepReporter{}
			res, err := restoreStep(t, pg, rep)
			if err != nil || res.GetRestore().GetDatabase() != "app_new" {
				t.Fatalf("res %v, err %v", res, err)
			}
			want := "pg_restore mode: " + tt.mode + " (10 objects, lock capacity 6400, share " + tt.share + ")"
			if !rep.logged(agentv1.LogLevel_LOG_LEVEL_INFO, want) {
				t.Errorf("no INFO %q in\n%s", want, rep.text())
			}
		})
	}
}

// builtinPostgresql is the postgresql plugin the agent builds for the config.
func builtinPostgresql(t *testing.T, cfg config.Config) postgresql.Plugin {
	t.Helper()
	for _, p := range plugins.Builtin("test", pluginOptions(cfg)...) {
		if pg, ok := p.(postgresql.Plugin); ok {
			return pg
		}
	}
	t.Fatal("no postgresql plugin in the build")
	return postgresql.Plugin{}
}

// newJournalStore is the store of the restore journal of the config, for a test
// that puts an entry of its own into it.
func newJournalStore(cfg config.Config) *restorejournal.Store {
	return restorejournal.New(restorejournal.Options{
		Dir: restoreJournalDir(cfg), FS: hostsetup.OS{}, Owner: hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid()},
	})
}

// healthyServer plays psql and pg_restore against a server that has what a
// restore into a new database needs: 64 locks for each of 100 connections, a
// dump of 10 objects.
type healthyServer struct{}

func (healthyServer) Run(_ context.Context, c postgresql.Cmd) (int, error) {
	if strings.HasSuffix(c.Path, "pg_restore") {
		switch c.Args[0] {
		case "--version":
			_, _ = io.WriteString(c.Stdout, "pg_restore (PostgreSQL) 16.4\n")
		case "-l":
			_, _ = io.WriteString(c.Stdout, strings.Repeat("1; 1259 16500 TABLE public t app\n", 10))
		default:
			_, _ = io.Copy(io.Discard, c.Stdin)
		}
		return 0, nil
	}
	data, _ := io.ReadAll(c.Stdin)
	script := string(data)
	for _, a := range []struct{ marker, answer string }{
		{"server_version_num", "160004|f|t|restorer"},
		{"pg_control_system", "1"},
		{"exists(", "f"},
		{"max_locks", "6400"},
		{"select oid from pg_database", "20001"},
		{"from pg_database where oid", "1|1"},
		{"from pg_class", "3"},
	} {
		if strings.Contains(script, a.marker) {
			_, _ = io.WriteString(c.Stdout, a.answer+"\n")
			break
		}
	}
	return 0, nil
}

// snapshotRepo is the repository of the step: the snapshot of a database of the source s2.
type snapshotRepo struct{ restic.Repository }

const snapshotOfApp = "bbbb1111bbbb1111bbbb1111bbbb1111bbbb1111bbbb1111bbbb1111bbbb1111"

func (snapshotRepo) Snapshots(context.Context, []string) ([]restic.Snapshot, error) {
	return []restic.Snapshot{{ID: snapshotOfApp, Paths: []string{"/app.dump"}, Tags: []string{
		"sard.source=s2", "postgresql.part=database", "postgresql.database=app", "postgresql.pg_dump_version=16.4", "postgresql.server_version=16.4"}}}, nil
}

func (snapshotRepo) Dump(_ context.Context, _, _ string, w io.Writer) error {
	_, err := io.WriteString(w, "PGDMP")
	return err
}

// stepReporter is the report of a step: its log lines.
type stepReporter struct {
	mu    sync.Mutex
	lines []struct {
		level agentv1.LogLevel
		text  string
	}
}

func (*stepReporter) Progress(agentv1.StepPhase, uint64, uint64)                      {}
func (*stepReporter) ProgressFiles(agentv1.StepPhase, uint64, uint64, uint64, uint64) {}
func (*stepReporter) Secret(name string) ([]byte, bool) {
	if name == "pg" {
		return []byte("pw\n"), true
	}
	return nil, false
}
func (*stepReporter) Output() io.Writer { return io.Discard }

func (*stepReporter) Stage(*agentv1.VerifyStage) {}

func (r *stepReporter) Log(level agentv1.LogLevel, text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, struct {
		level agentv1.LogLevel
		text  string
	}{level, text})
}

func (r *stepReporter) logged(level agentv1.LogLevel, text string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.lines {
		if l.level == level && l.text == text {
			return true
		}
	}
	return false
}

func (r *stepReporter) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for _, l := range r.lines {
		b.WriteString(l.text + "\n")
	}
	return b.String()
}

// restoreStep runs the step of a restore into a new database with the plugin,
// through the handlers of the agent.
func restoreStep(t *testing.T, pg postgresql.Plugin, rep executor.Reporter) (*agentv1.StepResult, error) {
	t.Helper()
	dir := t.TempDir()
	password := filepath.Join(dir, "pg.secret")
	writeFile(t, password, []byte("pw\n"), 0o600)
	cfg := mustConfig(t, "server:\n  address: s:1\nexecutor:\n  state_dir: "+filepath.Join(dir, "state")+"\n")
	if err := os.MkdirAll(filepath.Join(dir, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	handlers, err := pluginhost.NewHandlers(registryOf(t, pg), boundSecrets(t, map[string]string{"pg": password}, "db:5432/app_new", "db:5432/postgres"),
		func(string, string, io.Writer) (restic.Repository, bool) { return snapshotRepo{}, true }, filepath.Join(dir, "restore"),
		pluginhost.WithRestoreJournal(newRestoreJournal(cfg, slog.New(slog.DiscardHandler))))
	if err != nil {
		t.Fatal(err)
	}
	handler, _ := handlers.Handler("postgresql")
	return handler.Run(context.Background(), &agentv1.RunStep{
		CommandId: "c1", Plugin: "postgresql", Action: agentv1.Action_ACTION_RESTORE, RepositoryName: "main", SnapshotId: snapshotOfApp,
		Tags:       map[string]string{"sard.source": "s2"},
		ConfigJson: `{"host": "db", "user": "restorer", "tls_mode": "disable", "password_ref": "pg", "database": "app_new"}`,
	}, rep)
}
