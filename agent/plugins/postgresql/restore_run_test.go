// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/plugins/postgresql"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

var _ sdk.RestoreRunner = postgresql.Plugin{}

// phasesOf are the phases the step reported, in order, each once.
func phasesOf(h *rpHostRig) []sdk.Phase {
	var phases []sdk.Phase
	for _, e := range h.host.progress() {
		if len(phases) == 0 || phases[len(phases)-1] != e.phase {
			phases = append(phases, e.phase)
		}
	}
	return phases
}

// Scenario: Снимок базы восстанавливается в новую базу с теми же таблицами и строками.
func TestASnapshotOfADatabaseIsRestoredIntoANewDatabase(t *testing.T) {
	h := newPGHost(t)
	out, f := h.run(h.request())
	if f != nil {
		t.Fatal(f)
	}
	if out != (sdk.RestoreResult{Target: rpNewDatabase, Database: rpNewDatabase, Tables: 2, Roles: 0}) {
		t.Errorf("output %+v", out)
	}
	if !h.server.hasDatabase(rpNewDatabase) {
		t.Error("the database is not there")
	}
	if want := []sdk.Phase{sdk.PhasePreparing, sdk.PhaseRestoring}; !slices.Equal(phasesOf(h), want) {
		t.Errorf("phases %v, want %v", phasesOf(h), want)
	}
}

// Scenario: Новая база создаётся из template0.
func TestTheNewDatabaseIsCreatedFromTemplate0AsAName(t *testing.T) {
	h := newPGHost(t)
	req := h.request()
	req.Plan.Name = `x"; DROP DATABASE app; --`
	if _, f := h.run(req); f != nil {
		t.Fatal(f)
	}
	create := h.scriptsWith("CREATE DATABASE")
	if len(create) != 1 || !strings.Contains(create[0].Stdin, `CREATE DATABASE :"dbname" TEMPLATE template0`) || create[0].Vars["dbname"] != req.Plan.Name {
		t.Fatalf("create: %+v", create)
	}
	if strings.Contains(create[0].Stdin, "DROP") || !h.server.hasDatabase("app") {
		t.Error("the name was taken as SQL")
	}
}

// Scenario: Без глобальных объектов владельцы, права и табличные пространства не восстанавливаются.
func TestWithoutGlobalsTheRestoreSkipsOwnersRightsAndTablespaces(t *testing.T) {
	h := newPGHost(t)
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	last := h.lastRestore()
	for _, a := range []string{"--exit-on-error", "--no-password", "--no-owner", "--no-acl", "--no-tablespaces"} {
		if !last.has(a) {
			t.Errorf("pg_restore args %q lack %s", last.Args, a)
		}
	}
	if !strings.Contains(last.conninfo(), "dbname='"+rpNewDatabase+"'") {
		t.Errorf("conninfo %q", last.conninfo())
	}
	if globals := h.scriptsWith("CREATE ROLE"); len(globals) != 0 {
		t.Errorf("psql for global objects ran: %v", globals)
	}
}

// Scenario: Дамп идёт потоком и не пишется на диск целевого хоста.
func TestTheDumpIsStreamedIntoPgRestore(t *testing.T) {
	h := newPGHost(t)
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	if got := h.lastRestore().Stdin; got != h.repo.content[rpSnapshotID+"/app.dump"] {
		t.Errorf("pg_restore read %d bytes", len(got))
	}
	for _, d := range h.repo.dumped() {
		if d != [2]string{rpSnapshotID, "/app.dump"} {
			t.Errorf("restic dump of %v", d)
		}
	}
}

// changing are the calls that sign in with the password: not --version and -l.
func (h *rpHostRig) changing() []rpCall {
	var out []rpCall
	for _, tool := range []string{"psql", "pg_restore"} {
		for _, c := range h.server.ran(tool) {
			if !slices.Equal(c.Args, []string{"--version"}) && !c.has("-l") {
				out = append(out, c)
			}
		}
	}
	return out
}

// checkEnvironment judges the arguments and the environment of a call.
func checkEnvironment(t *testing.T, c rpCall) {
	t.Helper()
	if v, ok := c.env("PGPASSWORD"); !ok || v != rpSecret {
		t.Errorf("%s %q: PGPASSWORD = %q", c.Tool, c.Args, v)
	}
	if strings.Contains(strings.Join(c.Args, " "), rpSecret) || strings.Contains(c.conninfo(), "password") {
		t.Errorf("%s: the password is in the arguments: %q", c.Tool, c.Args)
	}
	checkLocale(t, c)
}

// checkLocale: the environment of the agent does not leak into the program,
// and its messages are in English.
func checkLocale(t *testing.T, c rpCall) {
	t.Helper()
	for _, kv := range c.Env {
		if strings.HasPrefix(kv, "PGHOST=") || kv == "LC_ALL=ru_RU.UTF-8" {
			t.Errorf("%s: the environment of the agent leaks %s", c.Tool, kv)
		}
	}
	if _, ok := c.env("LC_MESSAGES"); !ok {
		t.Errorf("%s: messages are not in English", c.Tool)
	}
}

// Scenario: Пароль базы передаётся только переменной окружения.
func TestThePasswordGoesInPGPASSWORDOnly(t *testing.T) {
	h := newPGHost(t)
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	for _, c := range h.changing() {
		checkEnvironment(t, c)
	}
	for _, e := range h.host.logs() {
		if strings.Contains(e.text, rpSecret) {
			t.Errorf("log: %s", e.text)
		}
	}
}

// Scenario: Отказ проверки PREPARING называет причину и ничего не создаёт.
func TestACheckThatFailsNamesTheReasonAndCreatesNothing(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(h *rpHostRig)
		globs  bool
		reason string
		words  []string
	}{
		{"no pg_restore", func(h *rpHostRig) { h.tools.nodes = map[string]fs.FileMode{} }, false, "PG_CLIENT_MISSING (check)", []string{"postgresql-client", "pg_restore_path"}},
		{"no psql", func(h *rpHostRig) { delete(h.tools.nodes, rpBin+"/psql") }, false, "PG_CLIENT_MISSING (check)", []string{"psql", "beside pg_restore"}},
		{"old pg_restore", func(h *rpHostRig) { h.server.toolVersion = "14.11" }, false, "PG_CLIENT_TOO_OLD (check)", []string{"14.11", "16.4"}},
		{"old server", func(h *rpHostRig) { h.server.version = 140011 }, false, "PG_SERVER_TOO_OLD (check)", []string{"14.11", "16.4"}},
		{"unsupported server", func(h *rpHostRig) {
			h.server.version = 130009
			h.repo.snapshots = []sdk.SnapshotInfo{snapshotWithoutVersions()}
		}, false, "PG_SERVER_TOO_OLD (check)", []string{"13.9", "14"}},
		{"no CREATEDB", func(h *rpHostRig) { h.server.createdb = false }, false, "CREATEDB_REQUIRED (check)", []string{`"restorer"`}},
		{"no superuser for globals", func(h *rpHostRig) { h.server.super = false }, true, "SUPERUSER_REQUIRED (check)", []string{`"restorer"`, "restore_globals"}},
		{"password refused", func(h *rpHostRig) {
			h.server.connectError = `psql: error: connection to server at "db2" (10.0.0.2), port 5432 failed: FATAL:  password authentication failed for user "restorer"`
		}, false, "PG_CONNECT (check)", []string{"password authentication failed"}},
		{"database exists", func(h *rpHostRig) { h.server.databases[rpNewDatabase] = 77 }, false, "DATABASE_EXISTS (check)", []string{rpNewDatabase}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newPGHost(t)
			tt.setup(h)
			h.mark()
			req := h.request()
			if tt.globs {
				req = h.withGlobals()
				h.server.super = false
			}
			_, f := h.run(req)
			hasPrefix(t, f, tt.reason)
			mentions(t, f.Error(), append(tt.words, "nothing was restored")...)
			if strings.Contains(f.Error(), rpSecret) {
				t.Errorf("the password is in the message: %s", f.Error())
			}
			for _, d := range h.repo.dumped() {
				if tt.name == "database exists" && d[1] == "/app.dump" {
					t.Errorf("restic dump of the dump ran: %v", d)
				}
			}
			h.untouched(t)
		})
	}
}

func snapshotWithoutVersions() sdk.SnapshotInfo {
	s := snapshotP()
	s.Tags = slices.DeleteFunc(s.Tags, func(t string) bool {
		return strings.HasPrefix(t, "postgresql.server_version=") || strings.HasPrefix(t, "postgresql.pg_dump_version=")
	})
	return s
}

// Scenario: Тот же старший номер сервера с более старой минорной версией допустим.
func TestAnOlderMinorVersionOfTheSameMajorServerIsFine(t *testing.T) {
	h := newPGHost(t)
	h.server.version = 160002
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
}

// Scenario: Снимок без метки версии восстанавливается с предупреждением.
func TestASnapshotWithoutVersionTagsIsRestoredWithAWarning(t *testing.T) {
	h := newPGHost(t)
	h.repo.snapshots = []sdk.SnapshotInfo{snapshotWithoutVersions()}
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	warns := 0
	for _, l := range h.host.logs() {
		if l.level == sdk.LevelWarn && strings.Contains(l.text, "postgresql.") {
			warns++
		}
	}
	if warns != 2 {
		t.Errorf("%d warnings about the versions:\n%s", warns, h.logText())
	}
}

// Scenario: Снимок не базы PostgreSQL отклоняется до подключения.
func TestASnapshotThatIsNotADatabaseIsRefusedBeforeConnecting(t *testing.T) {
	h := newPGHost(t)
	files := sdk.SnapshotInfo{ID: rpSnapshotID, Time: time.Now(), Paths: []string{"/srv/data"}, Tags: []string{"sard.source=s2"}}
	h.repo.snapshots = []sdk.SnapshotInfo{files}
	_, f := h.run(h.request())
	hasPrefix(t, f, "RESTIC_OUTPUT_UNEXPECTED (check)")
	if n := len(h.server.ran("psql")); n != 0 {
		t.Errorf("psql ran %d times", n)
	}
}

// Scenario: Глобальные объекты без их снимка — отказ до подключения.
func TestGlobalsWithoutTheirSnapshotAreRefusedBeforeConnecting(t *testing.T) {
	h := newPGHost(t)
	h.repo.snapshots = []sdk.SnapshotInfo{snapshotP()}
	_, f := h.run(h.withGlobals())
	hasPrefix(t, f, "GLOBALS_SNAPSHOT_MISSING (check)")
	if n := len(h.server.ran("psql")); n != 0 {
		t.Errorf("psql ran %d times", n)
	}
}

func TestAnUnknownSnapshotOrOneOfAnotherSourceIsRefusedBeforeConnecting(t *testing.T) {
	h := newPGHost(t)
	h.repo.snapshots = nil
	_, f := h.run(h.request())
	hasPrefix(t, f, "SNAPSHOT_UNKNOWN (check)")
	mentions(t, f.Error(), rpSnapshotID, "s2", "repository main")
	h = newPGHost(t)
	req := h.request()
	req.Source = "s7"
	_, f = h.run(req)
	hasPrefix(t, f, "SNAPSHOT_UNKNOWN (check)")
	mentions(t, f.Error(), "s7")
	if n := len(h.server.ran("psql")); n != 0 {
		t.Errorf("psql ran %d times", n)
	}
}

func TestAListOfSnapshotsThatCannotBeReadIsAFailureOfTheCheck(t *testing.T) {
	h := newPGHost(t)
	h.repo.listError = errors.New("connection refused")
	_, f := h.run(h.request())
	if f == nil || f.Stage != "check" {
		t.Fatalf("failure %v", f)
	}
}

// A host that does not word the failure of the repository, as the SDK says it does,
// still gets a refusal with a reason and a stage.
func TestAFailureOfTheRepositoryThatTheHostDidNotWordIsStillARefusal(t *testing.T) {
	h := newPGHost(t)
	h.host.rawListError = errors.New("odd")
	_, f := h.run(h.request())
	hasPrefix(t, f, "RESTIC_FAILED (check): odd")
	if !errors.Is(f, h.host.rawListError) {
		t.Error("the cause is lost")
	}
}

func TestAPlanThatBreaksTheRulesIsRefusedBeforeAnythingRuns(t *testing.T) {
	h := newPGHost(t)
	req := h.request()
	req.Plan.Name = ""
	_, f := h.run(req)
	hasPrefix(t, f, "TARGET_INVALID (check)")
	mentions(t, f.Error(), "the restore plan is invalid", "/database")
	if len(h.server.ran("psql")) != 0 {
		t.Error("psql ran")
	}
}

// Scenario: Секрет пароля, который не прочитать, называется, а не считается пустым.
func TestASecretThatIsNotThereOrUnusableFailsTheStepBeforeAnyProgramRuns(t *testing.T) {
	tests := map[string]struct {
		set   func(h *rpHostRig)
		words []string
	}{
		"unknown":        {func(h *rpHostRig) { delete(h.host.secrets, rpPasswordRef) }, []string{`"pg-restore"`}},
		"empty":          {func(h *rpHostRig) { h.host.secrets[rpPasswordRef] = []byte("\r\n") }, []string{`"pg-restore"`, "is empty"}},
		"holds a NUL":    {func(h *rpHostRig) { h.host.secrets[rpPasswordRef] = []byte("a\x00b") }, []string{"NUL"}},
		"only a newline": {func(h *rpHostRig) { h.host.secrets[rpPasswordRef] = []byte("\n") }, []string{"is empty"}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			h := newPGHost(t)
			tt.set(h)
			_, f := h.run(h.request())
			hasPrefix(t, f, "SECRET_INVALID (check)")
			mentions(t, f.Error(), tt.words...)
			if len(h.server.ran("psql")) != 0 || len(h.repo.dumped()) != 0 {
				t.Error("something ran")
			}
		})
	}
}

// Scenario: Восстановление в кластер источника пишет предупреждение и продолжается.
func TestRestoreIntoTheClusterOfTheSourceWarnsAndGoesOn(t *testing.T) {
	h := newPGHost(t)
	h.repo.snapshots = []sdk.SnapshotInfo{snapshotP("postgresql.system_identifier=7412345678901234567"), snapshotG()}
	h.repo.snapshots[0].Tags = slices.DeleteFunc(h.repo.snapshots[0].Tags, func(t string) bool { return t == "postgresql.system_identifier=9999" })
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	want := "the target is the cluster the snapshot was taken from (system identifier 7412345678901234567)"
	if !slices.Contains(h.lines(sdk.LevelWarn), want) {
		t.Errorf("no WARN %q in\n%s", want, h.logText())
	}
}

// Scenario: Другой кластер не даёт предупреждения.
func TestAnotherClusterGivesNoWarning(t *testing.T) {
	h := newPGHost(t)
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	if strings.Contains(h.logText(), "the cluster the snapshot was taken from") || strings.Contains(h.logText(), "not compared") {
		t.Errorf("log:\n%s", h.logText())
	}
}

// Scenario: Без метки или без права сравнение пропускается без отказа.
func TestWithoutTheTagOrTheRightTheClustersAreNotComparedAndNothingIsRefused(t *testing.T) {
	tests := map[string]func(h *rpHostRig){
		"no tag": func(h *rpHostRig) {
			h.repo.snapshots[0].Tags = slices.DeleteFunc(h.repo.snapshots[0].Tags, func(t string) bool { return strings.HasPrefix(t, "postgresql.system_identifier=") })
		},
		"no right": func(h *rpHostRig) {
			h.server.systemIDError = "ERROR:  permission denied for function pg_control_system"
		},
		"not a number": func(h *rpHostRig) { h.server.systemID = "x" },
	}
	for name, setup := range tests {
		t.Run(name, func(t *testing.T) {
			h := newPGHost(t)
			setup(h)
			if _, f := h.run(h.request()); f != nil {
				t.Fatal(f)
			}
			info := false
			for _, l := range h.lines(sdk.LevelInfo) {
				info = info || strings.Contains(l, "not compared with the one of the snapshot")
			}
			if !info || strings.Contains(h.logText(), "the cluster the snapshot was taken from") {
				t.Errorf("log:\n%s", h.logText())
			}
		})
	}
}

// Scenario: Режим pg_restore выбирается по числу объектов и ёмкости таблицы блокировок.
func TestTheModeOfPgRestoreFollowsTheObjectsAndTheLockTable(t *testing.T) {
	tests := []struct {
		objects int
		share   float64
		single  bool
		mode    string
		shareIn string
	}{
		{10, 0.5, true, "single transaction", "0.5"},
		{3199, 0.5, true, "single transaction", "0.5"},
		{3200, 0.5, false, "separate transactions", "0.5"},
		{9000, 0.5, false, "separate transactions", "0.5"},
		{10, 0, false, "separate transactions", "0"},
		{6399, 1, true, "single transaction", "1"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d objects, share %v", tt.objects, tt.share), func(t *testing.T) {
			h := newPGHost(t)
			h.share = tt.share
			h.server.toc = tt.objects
			if _, f := h.run(h.request()); f != nil {
				t.Fatal(f)
			}
			restores := h.server.ran("pg_restore")
			last := restores[len(restores)-1]
			if last.has("--single-transaction") != tt.single || !last.has("--exit-on-error") {
				t.Errorf("pg_restore args %q", last.Args)
			}
			want := fmt.Sprintf("pg_restore mode: %s (%d objects, lock capacity 6400, share %s)", tt.mode, tt.objects, tt.shareIn)
			if !slices.Contains(h.lines(sdk.LevelInfo), want) {
				t.Errorf("no INFO %q in\n%s", want, h.logText())
			}
		})
	}
}

// The plugin built without the setting of the host takes the default share.
func TestWithoutASettingOfTheHostTheShareIsHalfOfTheLockTable(t *testing.T) {
	h := newPGHost(t)
	p := h.plugin()
	p.LockShare = nil
	h.server.toc = 3200
	h.host.step = context.Background()
	if _, err := p.Restore(context.Background(), h.host, h.request().Plan.config(), sdk.RestoreRequest{CommandID: "c1", SnapshotID: rpSnapshotID, Repository: "main", Source: "s2"}); err != nil {
		t.Fatal(err)
	}
	want := "pg_restore mode: separate transactions (3200 objects, lock capacity 6400, share 0.5)"
	if !slices.Contains(h.lines(sdk.LevelInfo), want) || postgresql.DefaultLockShare != 0.5 {
		t.Errorf("no INFO %q in\n%s", want, h.logText())
	}
}

func TestTheCapacityOfTheLockTableCountsPreparedTransactionsToo(t *testing.T) {
	h := newPGHost(t)
	h.server.prepared = 100
	h.server.toc = 6400 // 64 * 200 = 12800 * 0.5
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	if !slices.Contains(h.lines(sdk.LevelInfo), "pg_restore mode: separate transactions (6400 objects, lock capacity 12800, share 0.5)") {
		t.Errorf("log:\n%s", h.logText())
	}
}

// twoDumps plays restic dump for a test of the table of contents: the first
// dump writes until its reader is gone and then waits to be stopped, the second
// writes the archive and notes whether CREATE DATABASE ran before it.
type twoDumps struct {
	h             *rpHostRig
	stopped       bool
	afterCreation bool
}

func (d *twoDumps) dump(ctx context.Context, _, path string, w io.Writer) error {
	if path != "/app.dump" {
		return nil
	}
	events := d.h.server.eventLog()
	if slices.Contains(events, "pg_restore -l") {
		d.afterCreation = slices.Contains(events, "create database")
		_, err := io.WriteString(w, "PGDMP-full")
		return err
	}
	_, _ = io.WriteString(w, "PGDMP-head")
	_, err := io.WriteString(w, strings.Repeat("x", 1<<20))
	if err != nil {
		<-ctx.Done()
		d.stopped = true
	}
	return err
}

// Scenario: Оглавление читается из начала потока, а поток останавливается.
func TestTheTableOfContentsIsReadFromTheHeadOfTheStreamAndTheStreamStops(t *testing.T) {
	h := newPGHost(t)
	d := &twoDumps{h: h}
	h.repo.dumpHook = d.dump
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	if n := h.dumpsOf("/app.dump"); n != 2 || !d.stopped || !d.afterCreation {
		t.Errorf("dumps %d, first stopped %v, second after CREATE DATABASE: %v", n, d.stopped, d.afterCreation)
	}
	before(t, h.server.eventLog(), "pg_restore -l", "create database")
	if toc := h.server.ran("pg_restore")[1]; !toc.has("-l") || toc.Stdin != "PGDMP" {
		t.Errorf("pg_restore -l: %+v", toc)
	}
}

// Scenario: Ошибка чтения оглавления — отказ до создания базы.
func TestAFailedReadOfTheTableOfContentsIsRefusedBeforeCreatingTheDatabase(t *testing.T) {
	h := newPGHost(t)
	h.server.tocError = "pg_restore: error: did not find magic string in file header"
	_, f := h.run(h.request())
	hasPrefix(t, f, "PG_RESTORE_FAILED (check)")
	mentions(t, f.Error(), "magic string", "nothing was restored")
	h.untouched(t)
}

func TestAStorageThatFailsWhileTheTableOfContentsIsReadIsTheFailureOfTheStorage(t *testing.T) {
	h := newPGHost(t)
	h.repo.dumpHook = func(context.Context, string, string, io.Writer) error { return errStorage }
	h.server.tocError = "pg_restore: error: unexpected end of file"
	_, f := h.run(h.request())
	hasPrefix(t, f, "BACKEND_UNAVAILABLE (check)")
}
