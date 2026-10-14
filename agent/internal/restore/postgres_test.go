// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore_test

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

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/restore"
)

// Rules Р60 and Р62 (A8c-3): a snapshot of PostgreSQL is laid out as files
// and the commands that restore it are told, not run.

func pgDatabase(db, file string, extra ...string) restore.Snapshot {
	tags := append([]string{"postgresql.database=" + db, "postgresql.part=database", "postgresql.pg_dump_version=16.4"}, extra...)
	return restore.Snapshot{ID: id("bbbb1111"), ShortID: "bbbb1111", Time: at(6).UTC(), Paths: []string{"/" + file}, Tags: tags}
}

func pgGlobals(of restore.Snapshot, hour int, prefix, file string) restore.Snapshot {
	return restore.Snapshot{ID: id(prefix), ShortID: prefix, Time: at(hour).UTC(), Paths: []string{"/" + file},
		Tags: []string{"postgresql.database=app", "postgresql.part=globals", "postgresql.main_snapshot=" + of.ID}}
}

func TestTheKindOfASnapshotComesFromItsTags(t *testing.T) {
	for tags, want := range map[string]restore.Kind{
		"postgresql.part=database": restore.KindDatabase,
		"postgresql.part=globals":  restore.KindGlobals,
		"postgresql.part=other":    restore.KindDirectory,
		"sard.source=s1":           restore.KindDirectory,
		"":                         restore.KindDirectory,
	} {
		s := restore.Snapshot{Tags: strings.Fields(tags)}
		if got := restore.KindOf(s); got != want {
			t.Errorf("%q: kind %v, want %v", tags, got, want)
		}
	}
}

func TestADatabaseSnapshotIsPlannedWithTheNewestGlobalsSnapshotOfIt(t *testing.T) {
	p := pgDatabase("app", "app.dump")
	older, newer := pgGlobals(p, 7, "cccc1111", "app.globals.sql"), pgGlobals(p, 8, "cccc2222", "app.globals.sql")
	other := pgGlobals(restore.Snapshot{ID: id("eeee1111")}, 9, "cccc3333", "app.globals.sql")
	plan, f := restore.PlanPostgres([]restore.Snapshot{newer, other, p, older}, p)
	if f != nil {
		t.Fatal(f)
	}
	want := []restore.PGFile{{SnapshotID: p.ID, Path: "/app.dump", Name: "app.dump"}, {SnapshotID: newer.ID, Path: "/app.globals.sql", Name: "app.globals.sql"}}
	if !slices.Equal(plan.Files, want) || plan.Database != "app" || plan.Version != "16.4" {
		t.Errorf("plan %+v", plan)
	}
}

func TestWithoutAGlobalsSnapshotOnlyTheDumpIsPlanned(t *testing.T) {
	p := pgDatabase("app", "app.dump")
	plan, f := restore.PlanPostgres([]restore.Snapshot{p}, p)
	if f != nil || len(plan.Files) != 1 || plan.Files[0].Name != "app.dump" {
		t.Fatalf("plan %+v, %v", plan, f)
	}
}

func TestAGlobalsSnapshotChosenByItselfIsPlannedAlone(t *testing.T) {
	g := pgGlobals(pgDatabase("app", "app.dump"), 7, "cccc1111", "app.globals.sql")
	plan, f := restore.PlanPostgres([]restore.Snapshot{g}, g)
	if f != nil || len(plan.Files) != 1 || plan.Files[0].SnapshotID != g.ID || plan.Database != "app" {
		t.Fatalf("plan %+v, %v", plan, f)
	}
}

func TestTheNameOfTheDatabaseIsDecodedAndTheNameOfTheFileIsKept(t *testing.T) {
	p := pgDatabase("my%20db", "my%20db.dump")
	plan, f := restore.PlanPostgres([]restore.Snapshot{p}, p)
	if f != nil || plan.Database != "my db" || plan.Files[0].Name != "my%20db.dump" {
		t.Fatalf("plan %+v, %v", plan, f)
	}
}

func TestASnapshotThatDoesNotLookLikeOneOfTheAgentIsRefused(t *testing.T) {
	good := pgDatabase("app", "app.dump")
	for name, mutate := range map[string]func(*restore.Snapshot){
		"two paths":      func(s *restore.Snapshot) { s.Paths = []string{"/a.dump", "/b.dump"} },
		"no paths":       func(s *restore.Snapshot) { s.Paths = nil },
		"relative":       func(s *restore.Snapshot) { s.Paths = []string{"app.dump"} },
		"nested":         func(s *restore.Snapshot) { s.Paths = []string{"/x/app.dump"} },
		"root":           func(s *restore.Snapshot) { s.Paths = []string{"/"} },
		"dotdot":         func(s *restore.Snapshot) { s.Paths = []string{"/.."} },
		"no database":    func(s *restore.Snapshot) { s.Tags = []string{"postgresql.part=database"} },
		"broken escape":  func(s *restore.Snapshot) { s.Tags = []string{"postgresql.part=database", "postgresql.database=a%zz"} },
		"empty database": func(s *restore.Snapshot) { s.Tags = []string{"postgresql.part=database", "postgresql.database="} },
	} {
		s := good
		mutate(&s)
		if _, f := restore.PlanPostgres([]restore.Snapshot{s}, s); f == nil || f.Reason != refusal.ResticOutputUnexpected || f.Class != refusal.ClassAgentError {
			t.Errorf("%s: failure %v", name, f)
		}
	}
}

// fakeDumper gives the content of each file by snapshot id and path.
type fakeDumper struct {
	content map[string]string
	// failAfter: the dump writes this many bytes of the content and fails with err.
	failAfter int
	err       error
	asked     []string
}

func (d *fakeDumper) Dump(_ context.Context, snapshotID, path string, w io.Writer) error {
	d.asked = append(d.asked, snapshotID+" "+path)
	data := d.content[snapshotID+" "+path]
	if d.err != nil {
		_, _ = io.WriteString(w, data[:min(d.failAfter, len(data))])
		return d.err
	}
	_, err := io.WriteString(w, data)
	return err
}

func pgContent(p, g restore.Snapshot) map[string]string {
	return map[string]string{p.ID + " " + p.Paths[0]: "DUMP-BYTES", g.ID + " " + g.Paths[0]: "GLOBALS-BYTES"}
}

func fullPlan(t *testing.T) (restore.PGPlan, map[string]string) {
	t.Helper()
	p := pgDatabase("app", "app.dump")
	g := pgGlobals(p, 7, "cccc1111", "app.globals.sql")
	plan, f := restore.PlanPostgres([]restore.Snapshot{p, g}, p)
	if f != nil {
		t.Fatal(f)
	}
	return plan, pgContent(p, g)
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range list {
		names = append(names, e.Name())
	}
	return names
}

func TestTheFilesAreWrittenToTheRootOfTheTargetWithOwnerAndModeOfRoot(t *testing.T) {
	dir, fsys := t.TempDir(), newRecordingFS()
	// Set after t.TempDir, which could not make its directory under it unless run as root.
	defer syscall.Umask(syscall.Umask(0o277)) // a umask that would take the owner's write bit
	held, f := openTarget(fsys, dir)
	if f != nil {
		t.Fatal(f)
	}
	defer held.Release()
	plan, content := fullPlan(t)
	if f := restore.WritePostgres(context.Background(), &fakeDumper{content: content}, held, plan, names("x1"), repoTarget()); f != nil {
		t.Fatal(f)
	}
	if got := entries(t, dir); !slices.Equal(got, []string{"app.dump", "app.globals.sql"}) {
		t.Fatalf("entries %v", got)
	}
	for name, want := range map[string]string{"app.dump": "DUMP-BYTES", "app.globals.sql": "GLOBALS-BYTES"} {
		data, _ := os.ReadFile(filepath.Join(dir, name))
		info, _ := os.Stat(filepath.Join(dir, name))
		if string(data) != want || info.Mode() != 0o600 {
			t.Errorf("%s: %q, %v", name, data, info.Mode())
		}
	}
}

func TestOwnerAndModeAreSetBeforeTheFirstByteOfContent(t *testing.T) {
	dir, fsys := t.TempDir(), newRecordingFS()
	held, _ := openTarget(fsys, dir)
	defer held.Release()
	plan, content := fullPlan(t)
	if f := restore.WritePostgres(context.Background(), &fakeDumper{content: content}, held, plan, names("x1", "x2"), repoTarget()); f != nil {
		t.Fatal(f)
	}
	want := []string{
		"chown .app.dump.tmp-x1 0:0", "chmod .app.dump.tmp-x1 -rw-------", "write .app.dump.tmp-x1",
		"chown .app.globals.sql.tmp-x2 0:0", "chmod .app.globals.sql.tmp-x2 -rw-------", "write .app.globals.sql.tmp-x2",
	}
	if got := fsys.files.events; !slices.Equal(got, want) {
		t.Errorf("events\n got %q\nwant %q", got, want)
	}
}

func TestAnOwnerThatCannotBeSetLeavesNoFile(t *testing.T) {
	dir, fsys := t.TempDir(), newRecordingFS()
	fsys.files.failChown = true
	held, _ := openTarget(fsys, dir)
	defer held.Release()
	plan, content := fullPlan(t)
	d := &fakeDumper{content: content}
	f := restore.WritePostgres(context.Background(), d, held, plan, names("x1"), repoTarget())
	wantPG(t, f, refusal.TargetWrite, refusal.ClassWrite)
	if len(entries(t, dir)) != 0 || len(d.asked) != 0 {
		t.Errorf("left %v, dumped %v", entries(t, dir), d.asked)
	}
}

func TestATempNameThatIsTakenIsReplacedByAnotherName(t *testing.T) {
	held, f := openTarget(newRecordingFS(), t.TempDir())
	if f != nil {
		t.Fatal(f)
	}
	defer held.Release()
	mustWrite(t, filepath.Join(held.Path, ".app.dump.tmp-x1"), "other") // turned up after the target was judged
	plan, content := fullPlan(t)
	if f := restore.WritePostgres(context.Background(), &fakeDumper{content: content}, held, plan, names("x1", "x2"), repoTarget()); f != nil {
		t.Fatal(f)
	}
	data, _ := os.ReadFile(filepath.Join(held.Path, ".app.dump.tmp-x1"))
	if string(data) != "other" {
		t.Errorf("the file that was there was touched: %q", data)
	}
}

func TestAFailureInTheMiddleOfTheDumpLeavesNeitherTempNorFinalFile(t *testing.T) {
	dir := t.TempDir()
	held, _ := openTarget(newRecordingFS(), dir)
	defer held.Release()
	plan, content := fullPlan(t)
	d := &fakeDumper{content: content, failAfter: 4, err: restic.ErrNetwork}
	f := restore.WritePostgres(context.Background(), d, held, plan, names("x1"), repoTarget())
	wantPG(t, f, refusal.BackendUnavailable, refusal.ClassTemporary, "no dump file")
	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("left %v", got)
	}
}

func TestACancelledDumpIsInterruptedAndLeavesNoFile(t *testing.T) {
	plan, content := fullPlan(t)
	dir := t.TempDir()
	held, _ := openTarget(newRecordingFS(), dir)
	defer held.Release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := &fakeDumper{content: content, failAfter: 2, err: errors.New("signal: terminated")}
	f := restore.WritePostgres(ctx, d, held, plan, names("x1"), repoTarget())
	wantPG(t, f, refusal.Interrupted, refusal.ClassTemporary, "no dump file")
	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("left %v", got)
	}
}

func TestNoSpaceForTheDumpIsATargetWriteFailureWithoutTraces(t *testing.T) {
	dir, fsys := t.TempDir(), newRecordingFS()
	fsys.files.failWritePrefix, fsys.files.failWrite = ".app.dump.tmp-", syscall.ENOSPC
	held, _ := openTarget(fsys, dir)
	defer held.Release()
	plan, content := fullPlan(t)
	// restic, whose output cannot be written, ends with an error of its own
	d := &fakeDumper{content: content, err: errors.New("restic dump: exit status 1: broken pipe")}
	f := restore.WritePostgres(context.Background(), &writeFailing{fakeDumper: d}, held, plan, names("x1"), repoTarget())
	wantPG(t, f, refusal.TargetWrite, refusal.ClassWrite, "no space left on device", dir)
	if len(entries(t, dir)) != 0 {
		t.Errorf("left %v", entries(t, dir))
	}
}

// writeFailing is a dumper whose restic ends with an error after the write of its output failed.
type writeFailing struct{ *fakeDumper }

func (d *writeFailing) Dump(ctx context.Context, id, path string, w io.Writer) error {
	if _, err := io.WriteString(w, "x"); err != nil {
		return d.err
	}
	return nil
}

func TestAFailureOfTheSecondFileTakesTheFirstBack(t *testing.T) {
	dir, fsys := t.TempDir(), newRecordingFS()
	fsys.files.failWritePrefix, fsys.files.failWrite = ".app.globals.sql.tmp-", syscall.EIO
	held, _ := openTarget(fsys, dir)
	defer held.Release()
	plan, content := fullPlan(t)
	f := restore.WritePostgres(context.Background(), &fakeDumper{content: content}, held, plan, names("x1", "x2"), repoTarget())
	wantPG(t, f, refusal.TargetWrite, refusal.ClassWrite)
	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("left %v", got)
	}
}

func TestAFinalNameThatTurnedUpIsNotReplaced(t *testing.T) {
	dir, fsys := t.TempDir(), newRecordingFS()
	fsys.files.failRename = true
	held, _ := openTarget(fsys, dir)
	defer held.Release()
	plan, content := fullPlan(t)
	f := restore.WritePostgres(context.Background(), &fakeDumper{content: content}, held, plan, names("x1"), repoTarget())
	wantPG(t, f, refusal.TargetWrite, refusal.ClassWrite, "app.dump")
	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("left %v", got)
	}
}

func TestADirectoryThatCannotBeSyncedFailsTheWrite(t *testing.T) {
	dir, fsys := t.TempDir(), newRecordingFS()
	fsys.failSync = true
	held, _ := openTarget(fsys, dir)
	defer held.Release()
	plan, content := fullPlan(t)
	f := restore.WritePostgres(context.Background(), &fakeDumper{content: content}, held, plan, names("x1"), repoTarget())
	wantPG(t, f, refusal.TargetWrite, refusal.ClassWrite)
	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("left %v", got)
	}
}

func hintsOf(t *testing.T, plan restore.PGPlan) string {
	t.Helper()
	return strings.Join(plan.Hints("/srv/pg 1"), "\n")
}

func inOrder(t *testing.T, text string, parts ...string) {
	t.Helper()
	at := 0
	for _, p := range parts {
		i := strings.Index(text[at:], p)
		if i < 0 {
			t.Fatalf("%q not found in order in\n%s", p, text)
		}
		at += i + len(p)
	}
}

func TestTheHintsNameTheCommandsInOrderWithQuotedPaths(t *testing.T) {
	plan, _ := fullPlan(t)
	text := hintsOf(t, plan)
	inOrder(t, text, "psql -X -d postgres -f '/srv/pg 1/app.globals.sql'", "createdb 'app'", "pg_restore -d 'app' '/srv/pg 1/app.dump'")
	for _, want := range []string{"not older than 16.4", "database is not restored", "root only"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "--no-owner") {
		t.Errorf("no-owner with globals:\n%s", text)
	}
}

func TestTheHintsWithoutGlobalsTellSoAndDropOwnersWithoutPsql(t *testing.T) {
	p := pgDatabase("app", "app.dump")
	plan, _ := restore.PlanPostgres([]restore.Snapshot{p}, p)
	text := hintsOf(t, plan)
	inOrder(t, text, "no global objects", "createdb 'app'", "pg_restore --no-owner --no-acl -d 'app' '/srv/pg 1/app.dump'")
	if strings.Contains(text, "psql") {
		t.Errorf("psql in\n%s", text)
	}
}

func TestTheHintsOfAGlobalsSnapshotHoldPsqlOnly(t *testing.T) {
	g := pgGlobals(pgDatabase("app", "app.dump"), 7, "cccc1111", "app.globals.sql")
	plan, _ := restore.PlanPostgres([]restore.Snapshot{g}, g)
	text := hintsOf(t, plan)
	if !strings.Contains(text, "psql -X -d postgres -f '/srv/pg 1/app.globals.sql'") || strings.Contains(text, "pg_restore") || strings.Contains(text, "createdb") {
		t.Errorf("hints\n%s", text)
	}
}

func TestAQuoteInTheNameOfTheDatabaseIsEscaped(t *testing.T) {
	p := pgDatabase("o%27brien", "o%27brien.dump")
	plan, _ := restore.PlanPostgres([]restore.Snapshot{p}, p)
	text := hintsOf(t, plan)
	if !strings.Contains(text, `createdb 'o'\''brien'`) || !strings.Contains(text, `pg_restore --no-owner --no-acl -d 'o'\''brien' '/srv/pg 1/o%27brien.dump'`) {
		t.Errorf("hints\n%s", text)
	}
}

func TestWithoutTheVersionTagTheHintsDoNotInventOne(t *testing.T) {
	p := pgDatabase("app", "app.dump")
	p.Tags = slices.DeleteFunc(p.Tags, func(s string) bool { return strings.HasPrefix(s, "postgresql.pg_dump_version") })
	plan, _ := restore.PlanPostgres([]restore.Snapshot{p}, p)
	if text := hintsOf(t, plan); !strings.Contains(text, "not older than the version of pg_dump") {
		t.Errorf("hints\n%s", text)
	}
}

func wantPG(t *testing.T, f *refusal.Failure, reason refusal.Reason, class refusal.Class, mentions ...string) {
	t.Helper()
	if f == nil || f.Reason != reason || f.Class != class {
		t.Fatalf("failure %v, want %s of class %v", f, reason, class)
	}
	for _, m := range mentions {
		if !strings.Contains(f.Detail, m) {
			t.Errorf("%q is not in %q", m, f.Detail)
		}
	}
}
