// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Rules "Снимок PostgreSQL выкладывается файлами дампа с командами
// восстановления" (A8c-3, Р60, Р62) and "Снимок плагина с выгрузкой выкладывается
// хостом файлами по плану плагина" (R2c-3): the plugin plans, the agent lays out.

var _ sdk.Extractor = Plugin{}

func exID(prefix string) string { return prefix + strings.Repeat("0", 64-len(prefix)) }

func exTime(hour int) time.Time { return time.Date(2026, 10, 11, hour, 0, 0, 0, time.UTC) }

func exDatabase(db, file string, extra ...string) sdk.SnapshotInfo {
	tags := append([]string{"postgresql.database=" + db, "postgresql.part=database", "postgresql.pg_dump_version=16.4"}, extra...)
	return sdk.SnapshotInfo{ID: exID("bbbb1111"), Time: exTime(6), Paths: []string{"/" + file}, Tags: tags}
}

func exGlobals(of sdk.SnapshotInfo, hour int, prefix, file string) sdk.SnapshotInfo {
	return sdk.SnapshotInfo{ID: exID(prefix), Time: exTime(hour), Paths: []string{"/" + file},
		Tags: []string{"postgresql.database=app", "postgresql.part=globals", "postgresql.main_snapshot=" + of.ID}}
}

func planExtract(t *testing.T, s sdk.SnapshotInfo, all ...sdk.SnapshotInfo) (sdk.ExtractPlan, error) {
	t.Helper()
	plan, err := Plugin{}.PlanExtract(sdk.ExtractRequest{Snapshot: s, Snapshots: all, Target: "/srv/pg 1"})
	if err == nil {
		if verr := plan.Validate(); verr != nil {
			t.Fatalf("the plan is not valid: %v", verr)
		}
	}
	return plan, err
}

func hintText(plan sdk.ExtractPlan) string { return strings.Join(plan.Hints, "\n") }

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

func TestASnapshotThatIsNotADumpOrGlobalsIsATree(t *testing.T) {
	for tags, want := range map[string]bool{
		"postgresql.part=database":       false,
		"postgresql.part=globals":        false,
		"postgresql.part=other":          true,
		"postgresql.part=":               true,
		"postgresql.server_version=16.4": true,
		"":                               true,
	} {
		s := sdk.SnapshotInfo{ID: exID("bbbb1111"), Tags: strings.Fields(tags)}
		plan, err := Plugin{}.PlanExtract(sdk.ExtractRequest{Snapshot: s})
		if isTree := err == nil && len(plan.Files) == 0 && len(plan.Hints) == 0; isTree != want {
			t.Errorf("%q: plan %+v, %v; a tree: %v, want %v", tags, plan, err, isTree, want)
		}
	}
}

func TestADatabaseSnapshotIsPlannedWithTheNewestGlobalsSnapshotOfIt(t *testing.T) {
	p := exDatabase("app", "app.dump")
	older, newer := exGlobals(p, 7, "cccc1111", "app.globals.sql"), exGlobals(p, 8, "cccc2222", "app.globals.sql")
	other := exGlobals(sdk.SnapshotInfo{ID: exID("eeee1111")}, 9, "cccc3333", "app.globals.sql")
	plan, err := planExtract(t, p, newer, other, p, older)
	if err != nil {
		t.Fatal(err)
	}
	want := []sdk.ExtractFile{{SnapshotID: p.ID, Path: "/app.dump", Name: "app.dump"}, {SnapshotID: newer.ID, Path: "/app.globals.sql", Name: "app.globals.sql"}}
	if !slices.Equal(plan.Files, want) {
		t.Errorf("files %+v", plan.Files)
	}
}

func TestWithoutAGlobalsSnapshotOnlyTheDumpIsPlanned(t *testing.T) {
	p := exDatabase("app", "app.dump")
	plan, err := planExtract(t, p, p)
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Name != "app.dump" {
		t.Fatalf("plan %+v, %v", plan, err)
	}
}

func TestAGlobalsSnapshotChosenByItselfIsPlannedAlone(t *testing.T) {
	g := exGlobals(exDatabase("app", "app.dump"), 7, "cccc1111", "app.globals.sql")
	plan, err := planExtract(t, g, g)
	if err != nil || len(plan.Files) != 1 || plan.Files[0].SnapshotID != g.ID {
		t.Fatalf("plan %+v, %v", plan, err)
	}
}

func TestTheNameOfTheDatabaseIsDecodedAndTheNameOfTheFileIsKept(t *testing.T) {
	p := exDatabase("my%20db", "my%20db.dump")
	plan, err := planExtract(t, p, p)
	if err != nil || plan.Files[0].Name != "my%20db.dump" || !strings.Contains(hintText(plan), "createdb 'my db'") {
		t.Fatalf("plan %+v, %v", plan, err)
	}
}

func TestASnapshotThatDoesNotLookLikeOneOfTheAgentIsRefused(t *testing.T) {
	good := exDatabase("app", "app.dump")
	for name, mutate := range map[string]func(*sdk.SnapshotInfo){
		"two paths":      func(s *sdk.SnapshotInfo) { s.Paths = []string{"/a.dump", "/b.dump"} },
		"no paths":       func(s *sdk.SnapshotInfo) { s.Paths = nil },
		"relative":       func(s *sdk.SnapshotInfo) { s.Paths = []string{"app.dump"} },
		"nested":         func(s *sdk.SnapshotInfo) { s.Paths = []string{"/x/app.dump"} },
		"root":           func(s *sdk.SnapshotInfo) { s.Paths = []string{"/"} },
		"dotdot":         func(s *sdk.SnapshotInfo) { s.Paths = []string{"/.."} },
		"no database":    func(s *sdk.SnapshotInfo) { s.Tags = []string{"postgresql.part=database"} },
		"broken escape":  func(s *sdk.SnapshotInfo) { s.Tags = []string{"postgresql.part=database", "postgresql.database=a%zz"} },
		"empty database": func(s *sdk.SnapshotInfo) { s.Tags = []string{"postgresql.part=database", "postgresql.database="} },
	} {
		s := good
		mutate(&s)
		_, err := Plugin{}.PlanExtract(sdk.ExtractRequest{Snapshot: s, Snapshots: []sdk.SnapshotInfo{s}})
		if err == nil || !strings.Contains(err.Error(), "bbbb1111") || !strings.Contains(err.Error(), "cannot be restored as a directory") || !strings.Contains(err.Error(), "nothing was written") {
			t.Errorf("%s: error %v", name, err)
		}
	}
}

func TestGlobalsOfADatabaseAreOnlyThoseThatSayWhichSnapshotTheyBelongTo(t *testing.T) {
	p := exDatabase("app", "app.dump")
	noMain := exGlobals(p, 9, "cccc1111", "app.globals.sql")
	noMain.Tags = []string{"postgresql.part=globals"}
	notGlobals := exGlobals(p, 10, "cccc2222", "app.other")
	notGlobals.Tags = []string{"postgresql.part=other", "postgresql.main_snapshot=" + p.ID}
	plan, err := planExtract(t, p, p, noMain, notGlobals)
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Name != "app.dump" {
		t.Fatalf("plan %+v, %v", plan, err)
	}
}

func TestAMalformedGlobalsSnapshotRefusesTheWholeRestore(t *testing.T) {
	p := exDatabase("app", "app.dump")
	g := exGlobals(p, 7, "cccc1111", "app.globals.sql")
	g.Paths = []string{"/a", "/b"}
	_, err := Plugin{}.PlanExtract(sdk.ExtractRequest{Snapshot: p, Snapshots: []sdk.SnapshotInfo{p, g}})
	if err == nil || !strings.Contains(err.Error(), "cccc1111") || !strings.Contains(err.Error(), "2 paths") {
		t.Fatalf("error %v", err)
	}
}

func TestTheHintsNameTheCommandsInOrderWithQuotedPaths(t *testing.T) {
	p := exDatabase("app", "app.dump")
	plan, _ := planExtract(t, p, p, exGlobals(p, 7, "cccc1111", "app.globals.sql"))
	text := hintText(plan)
	inOrder(t, text, "psql -X -d postgres -f '/srv/pg 1/app.globals.sql'", "createdb 'app'", "pg_restore -d 'app' '/srv/pg 1/app.dump'")
	for _, want := range []string{"not older than 16.4", "database is not restored", "root only"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "--no-owner") {
		t.Errorf("no-owner with globals:\n%s", text)
	}
	inOrder(t, text, "You run these commands; pg_restore and psql must be not older than 16.4:")
}

func TestTheHintsWithoutGlobalsTellSoAndDropOwnersWithoutPsql(t *testing.T) {
	p := exDatabase("app", "app.dump")
	plan, _ := planExtract(t, p, p)
	text := hintText(plan)
	inOrder(t, text, "no global objects", "createdb 'app'", "pg_restore --no-owner --no-acl -d 'app' '/srv/pg 1/app.dump'")
	if strings.Contains(text, "psql") {
		t.Errorf("psql in\n%s", text)
	}
	inOrder(t, text, "You run these commands; pg_restore must be not older than 16.4:")
}

func TestTheHintsOfAGlobalsSnapshotHoldPsqlOnly(t *testing.T) {
	g := exGlobals(exDatabase("app", "app.dump"), 7, "cccc1111", "app.globals.sql")
	plan, _ := planExtract(t, g, g)
	text := hintText(plan)
	if !strings.Contains(text, "psql -X -d postgres -f '/srv/pg 1/app.globals.sql'") || strings.Contains(text, "pg_restore") || strings.Contains(text, "createdb") {
		t.Errorf("hints\n%s", text)
	}
	if !strings.Contains(text, "You run these commands; psql must be not older than the version of pg_dump that made the backup:") {
		t.Errorf("hints\n%s", text)
	}
}

func TestAQuoteInTheNameOfTheDatabaseIsEscaped(t *testing.T) {
	p := exDatabase("o%27brien", "o%27brien.dump")
	plan, _ := planExtract(t, p, p)
	text := hintText(plan)
	if !strings.Contains(text, `createdb 'o'\''brien'`) || !strings.Contains(text, `pg_restore --no-owner --no-acl -d 'o'\''brien' '/srv/pg 1/o%27brien.dump'`) {
		t.Errorf("hints\n%s", text)
	}
}

func TestWithoutTheVersionTagTheHintsDoNotInventOne(t *testing.T) {
	p := exDatabase("app", "app.dump")
	p.Tags = slices.DeleteFunc(p.Tags, func(s string) bool { return strings.HasPrefix(s, "postgresql.pg_dump_version") })
	plan, _ := planExtract(t, p, p)
	if text := hintText(plan); !strings.Contains(text, "not older than the version of pg_dump") {
		t.Errorf("hints\n%s", text)
	}
}

func TestTheHintsStartWithTheOwnerOfTheFiles(t *testing.T) {
	p := exDatabase("app", "app.dump")
	plan, _ := planExtract(t, p, p)
	if len(plan.Hints) < 2 || !strings.Contains(plan.Hints[0], "root only (root:root, mode 0600)") || !strings.Contains(plan.Hints[1], "The database is not restored") {
		t.Errorf("hints %q", plan.Hints)
	}
}
