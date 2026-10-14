// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// Rule "Снимок PostgreSQL выкладывается файлами дампа с командами
// восстановления" (A8c-3, Р62).

// files are the names in dir, sorted.
func (h *setupHost) names(dir string) []string {
	h.t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		h.t.Fatal(err)
	}
	var names []string
	for _, e := range list {
		names = append(names, e.Name())
	}
	return names
}

func (h *setupHost) dumpCalls() []fakeCall { return h.restic.callsTo(h.path("base"), "dump") }

// assertPGFile: the file of the target has the content, mode 0600 and owner root:root.
func (h *setupHost) assertPGFile(name, want string) {
	h.t.Helper()
	path := filepath.Join(h.target(), name)
	info, err := os.Stat(path)
	ok(h.t, err)
	if h.fileContent(path) != want || info.Mode().Perm() != 0o600 {
		h.t.Errorf("%s: %q, %v", name, h.fileContent(path), info.Mode())
	}
	if o, found := h.fsys.ownerOf(path); !found || o.uid != 0 || o.gid != 0 {
		h.t.Errorf("%s: owner %+v", name, o)
	}
}

func TestADatabaseSnapshotLaysOutTheDumpAndTheGlobalsInTheRootOfTheTarget(t *testing.T) {
	h := restoreHost(t)
	// After restoreHost: under this umask t.TempDir could not make its
	// directory unless the test runs as root.
	defer syscall.Umask(syscall.Umask(0o277))
	code, stdout, stderr := h.restore(idP)
	assertCode(t, code, exitOK)
	if stderr != "" || len(h.restoreCalls()) != 0 || !strings.Contains(stdout, "root only") {
		t.Errorf("stderr %q, restore calls %v, stdout %q", stderr, h.restoreCalls(), stdout)
	}
	if got := h.names(h.target()); !slices.Equal(got, []string{"app.dump", "app.globals.sql"}) {
		t.Fatalf("target holds %v", got)
	}
	h.assertPGFile("app.dump", "DUMP-BYTES")
	h.assertPGFile("app.globals.sql", "GLOBALS-BYTES")
}

// firstEvent is the index of the first event with the prefix about the file called name.
func firstEvent(events []string, prefix, name string) int {
	return slices.IndexFunc(events, func(e string) bool { return strings.HasPrefix(e, prefix) && strings.Contains(e, name) })
}

func TestAPostgreSQLFileGetsItsOwnerBeforeTheFirstByteOfContent(t *testing.T) {
	h := restoreHost(t)
	h.restore(idP)
	for _, name := range []string{".app.dump.tmp-", ".app.globals.sql.tmp-"} {
		chown, write := firstEvent(h.fsys.events, "chown ", name), firstEvent(h.fsys.events, "write ", name)
		if chown < 0 || write < 0 || chown > write {
			t.Errorf("%s: chown at %d, first write at %d in %v", name, chown, write, h.fsys.events)
		}
	}
}

func TestTheDumpIsReadByResticAsTheServiceUserWithoutLock(t *testing.T) {
	h := restoreHost(t)
	h.restore(idP)
	calls := h.dumpCalls()
	if len(calls) != 2 {
		t.Fatalf("dump calls %v", calls)
	}
	for _, c := range calls {
		if c.runAs == nil || c.runAs.UID != serviceUID || !slices.Contains(c.args, "--no-lock") {
			t.Errorf("dump ran as %+v with %q", c.runAs, c.args)
		}
	}
	if calls[0].args[len(calls[0].args)-2] != idP || calls[0].args[len(calls[0].args)-1] != "/app.dump" {
		t.Errorf("first dump %q", calls[0].args)
	}
}

// assertInOrder: the parts are in the text, one after the other.
func assertInOrder(t *testing.T, text string, parts ...string) {
	t.Helper()
	at := 0
	for _, want := range parts {
		i := strings.Index(text[at:], want)
		if i < 0 {
			t.Fatalf("%q not found in order in\n%s", want, text)
		}
		at += i + len(want)
	}
}

func TestTheSummaryPrintsTheCommandsInOrderAndRunsNone(t *testing.T) {
	h := restoreHost(t)
	code, stdout, _ := h.restore(idP)
	assertCode(t, code, exitOK)
	assertInOrder(t, stdout, "psql -X -d postgres -f '"+h.target()+"/app.globals.sql'", "createdb 'app'", "pg_restore -d 'app' '"+h.target()+"/app.dump'")
	assertInOrder(t, stdout, "database is not restored", "not older than 16.4")
	for _, c := range h.restic.calls {
		if !slices.Contains([]string{"version", "snapshots", "dump"}, c.sub) {
			t.Errorf("restic %s was run", c.sub)
		}
	}
}

func TestWithoutAGlobalsSnapshotOnlyTheDumpIsWritten(t *testing.T) {
	h := restoreHost(t)
	h.base().snapshots = slices.DeleteFunc(h.base().snapshots, func(s restic.Snapshot) bool { return s.ID == idG })
	code, stdout, _ := h.restore(idP)
	assertCode(t, code, exitOK)
	if got := h.names(h.target()); !slices.Equal(got, []string{"app.dump"}) {
		t.Fatalf("target holds %v", got)
	}
	if !strings.Contains(stdout, "no global objects") || !strings.Contains(stdout, "pg_restore --no-owner --no-acl -d 'app' '"+h.target()+"/app.dump'") || strings.Contains(stdout, "psql") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestAGlobalsSnapshotChosenByItselfWritesOnlyItsFile(t *testing.T) {
	h := restoreHost(t)
	code, stdout, _ := h.restore("cccc1111")
	assertCode(t, code, exitOK)
	if got := h.names(h.target()); !slices.Equal(got, []string{"app.globals.sql"}) {
		t.Fatalf("target holds %v", got)
	}
	if !strings.Contains(stdout, "psql -X -d postgres -f '"+h.target()+"/app.globals.sql'") || strings.Contains(stdout, "pg_restore") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestTheNewestOfSeveralGlobalsSnapshotsIsTaken(t *testing.T) {
	h := restoreHost(t)
	g2 := "cccc2222" + strings.Repeat("0", 56)
	h.base().snapshots = append(h.base().snapshots, restic.Snapshot{ID: g2, Time: time.Date(2026, 10, 11, 6, 1, 7, 0, msk), Hostname: "db1", Paths: []string{"/app.globals.sql"},
		Tags: []string{"postgresql.database=app", "postgresql.part=globals", "postgresql.main_snapshot=" + idP, "sard.source=s2"}})
	h.base().restoreState.files[g2] = []snapFile{{"/app.globals.sql", "GLOBALS-2"}}
	h.restore(idP)
	if got := h.fileContent(h.target() + "/app.globals.sql"); got != "GLOBALS-2" {
		t.Errorf("globals %q", got)
	}
}

func TestANameWithASpaceIsDecodedInTheCommandsAndKeptInTheFiles(t *testing.T) {
	h := restoreHost(t)
	id := "bbbb2222" + strings.Repeat("0", 56)
	h.base().snapshots = []restic.Snapshot{{ID: id, Time: time.Now(), Hostname: "db1", Paths: []string{"/my%20db.dump"},
		Tags: []string{"postgresql.database=my%20db", "postgresql.part=database", "postgresql.pg_dump_version=16.4"}}}
	h.base().restoreState.files[id] = []snapFile{{"/my%20db.dump", "D"}}
	code, stdout, _ := h.restore(id)
	assertCode(t, code, exitOK)
	if got := h.names(h.target()); !slices.Equal(got, []string{"my%20db.dump"}) {
		t.Fatalf("target holds %v", got)
	}
	for _, want := range []string{"createdb 'my db'", "pg_restore --no-owner --no-acl -d 'my db' '" + h.target() + "/my%20db.dump'"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("no %q in\n%s", want, stdout)
		}
	}
}

func TestAQuoteInTheNameOfTheDatabaseIsEscaped(t *testing.T) {
	h := restoreHost(t)
	id := "bbbb3333" + strings.Repeat("0", 56)
	h.base().snapshots = []restic.Snapshot{{ID: id, Time: time.Now(), Hostname: "db1", Paths: []string{"/o%27brien.dump"},
		Tags: []string{"postgresql.database=o%27brien", "postgresql.part=database"}}}
	h.base().restoreState.files[id] = []snapFile{{"/o%27brien.dump", "D"}}
	_, stdout, _ := h.restore(id)
	if !strings.Contains(stdout, `createdb 'o'\''brien'`) {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestPathWithAPostgreSQLSnapshotIsAUsageErrorAndTheTargetIsUntouched(t *testing.T) {
	h := restoreHost(t)
	code, _, stderr := h.restore("bbbb1111", "--path", "/app.dump")
	assertCode(t, code, exitUsage)
	if !strings.Contains(stderr, "--path") || !strings.Contains(stderr, "PostgreSQL") {
		t.Errorf("stderr %q", stderr)
	}
	h.assertAbsent(h.target())
	if len(h.dumpCalls()) != 0 || len(h.log.lines) != 0 {
		t.Errorf("dump %v, audit %v", h.dumpCalls(), h.log.lines)
	}
}

func TestAFailureInTheMiddleOfTheDumpLeavesNoFileAndIsTemporary(t *testing.T) {
	h := restoreHost(t)
	h.base().restoreState.dumpFatal = "Fatal: read tcp 10.0.0.1:1: connection reset by peer"
	code, _, stderr := h.restore(idP)
	assertRefusal(t, code, stderr, exitTemporary, "BACKEND_UNAVAILABLE")
	if !strings.Contains(stderr, "no dump file") {
		t.Errorf("stderr %q", stderr)
	}
	h.assertAbsent(h.target())
	if len(h.log.lines) != 1 || !strings.Contains(h.log.lines[0], "restore to "+h.target()+" failed: BACKEND_UNAVAILABLE") {
		t.Errorf("audit %q", h.log.lines)
	}
}

func TestNoSpaceForTheDumpIsATargetWriteFailureWithoutTemporaryFiles(t *testing.T) {
	h := restoreHost(t)
	ok(t, os.MkdirAll(h.target(), 0o700))
	h.fsys.failOn, h.fsys.failPath, h.fsys.failErr = "write", ".app.dump.tmp-", syscall.ENOSPC
	code, _, stderr := h.restore(idP)
	assertRefusal(t, code, stderr, exitWrite, "TARGET_WRITE")
	if got := h.names(h.target()); len(got) != 0 {
		t.Errorf("target holds %v", got)
	}
}

func TestSuccessIsRecordedInTheAuditLog(t *testing.T) {
	h := restoreHost(t)
	h.restore(idP)
	if len(h.log.lines) != 1 || !strings.Contains(h.log.lines[0], "repository base snapshot bbbb1111 restored to "+h.target()) {
		t.Errorf("audit %q", h.log.lines)
	}
}

func TestAnOldSnapshotOfADirectoryStaysADirectory(t *testing.T) {
	h := restoreHost(t)
	code, _, _ := h.restore(idM)
	assertCode(t, code, exitOK)
	if len(h.restoreCalls()) != 1 || len(h.dumpCalls()) != 0 {
		t.Errorf("restore %v, dump %v", h.restoreCalls(), h.dumpCalls())
	}
}

// A8c-3: the help of repo restore says what happens to a PostgreSQL snapshot.
func TestRestoreHelpSaysPostgreSQLFilesAreWrittenAndCommandsPrinted(t *testing.T) {
	h := newSetupHost(t)
	_, stdout, _ := h.sudo("repo", "restore", "--help")
	flat := strings.Join(strings.Fields(stdout), " ")
	for _, want := range []string{"<database>.dump", "<database>.globals.sql", "root:root", "0600", "commands", "database itself is not restored", "--path does not apply"} {
		if !strings.Contains(flat, want) {
			t.Errorf("help lacks %q:\n%s", want, stdout)
		}
	}
}

// Р83: a snapshot tagged as PostgreSQL that is not shaped as the agent
// writes it is refused before the target is made.
func TestAMalformedPostgreSQLSnapshotIsRefusedWithoutAnyWrite(t *testing.T) {
	setTags := func(id string, mutate func(*restic.Snapshot)) func(*setupHost) {
		return func(h *setupHost) {
			for i := range h.base().snapshots {
				if h.base().snapshots[i].ID == id {
					mutate(&h.base().snapshots[i])
				}
			}
		}
	}
	dropTag := func(s *restic.Snapshot) {
		s.Tags = slices.DeleteFunc(s.Tags, func(t string) bool { return strings.HasPrefix(t, "postgresql.database=") })
	}
	for name, c := range map[string]struct {
		arrange func(*setupHost)
		short   string
	}{
		"two paths":      {setTags(idP, func(s *restic.Snapshot) { s.Paths = []string{"/app.dump", "/other.dump"} }), "bbbb1111"},
		"nested path":    {setTags(idP, func(s *restic.Snapshot) { s.Paths = []string{"/dir/app.dump"} }), "bbbb1111"},
		"no database":    {setTags(idP, dropTag), "bbbb1111"},
		"broken escape":  {setTags(idP, func(s *restic.Snapshot) { dropTag(s); s.Tags = append(s.Tags, "postgresql.database=%zz") }), "bbbb1111"},
		"globals broken": {setTags(idG, func(s *restic.Snapshot) { s.Paths = []string{"/app.globals.sql", "/x.sql"} }), "cccc1111"},
	} {
		h := restoreHost(t)
		c.arrange(h)
		code, _, stderr := h.restore(idP)
		assertRefusal(t, code, stderr, exitAgentError, "RESTIC_OUTPUT_UNEXPECTED")
		for _, want := range []string{c.short, "cannot be restored as a directory", "nothing was written"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("%s: stderr lacks %q: %s", name, want, stderr)
			}
		}
		h.assertNothingWritten(name)
	}
}

// assertNothingWritten: no target, no restic call that reads the snapshot, no audit line.
func (h *setupHost) assertNothingWritten(name string) {
	h.t.Helper()
	h.assertAbsent(h.target())
	if len(h.dumpCalls()) != 0 || len(h.restoreCalls()) != 0 || len(h.log.lines) != 0 {
		h.t.Errorf("%s: dump %v restore %v audit %q", name, h.dumpCalls(), h.restoreCalls(), h.log.lines)
	}
}

// Р84: no tag of the version of pg_dump is no refusal, and no number is invented.
func TestWithoutTheVersionTagTheSummaryNamesNoVersion(t *testing.T) {
	h := restoreHost(t)
	for i := range h.base().snapshots {
		h.base().snapshots[i].Tags = slices.DeleteFunc(h.base().snapshots[i].Tags, func(s string) bool { return strings.HasPrefix(s, "postgresql.pg_dump_version") })
	}
	code, stdout, _ := h.restore(idP)
	assertCode(t, code, exitOK)
	line := ""
	for _, l := range strings.Split(stdout, "\n") {
		if strings.Contains(l, "not older than") {
			line = l
		}
	}
	if !strings.Contains(line, "pg_restore and psql") || !strings.Contains(line, "not older than the version of pg_dump that made the backup") || strings.ContainsAny(line, "0123456789") {
		t.Errorf("line %q", line)
	}
}

// Р85: the temporary file of a created target that cannot be made.
func TestAFileThatCannotBeMadeInACreatedTargetIsWrittenToTheAuditLog(t *testing.T) {
	h := restoreHost(t)
	h.fsys.failOn, h.fsys.failPath, h.fsys.failErr = "createtemp", ".app.dump.tmp-", syscall.EROFS
	code, _, stderr := h.restore(idP)
	assertRefusal(t, code, stderr, exitWrite, "TARGET_WRITE")
	if len(h.dumpCalls()) != 0 {
		t.Error("restic dump was called")
	}
	want := "repository base snapshot bbbb1111 restore to " + h.target() + " failed: TARGET_WRITE"
	if len(h.log.lines) != 1 || !strings.Contains(h.log.lines[0], want) {
		t.Errorf("audit %q", h.log.lines)
	}
}

// Р86: a failure of the globals file takes the finished dump back.
func TestAFailureToWriteTheGlobalsRemovesTheDumpToo(t *testing.T) {
	h := restoreHost(t)
	ok(t, os.MkdirAll(h.target(), 0o700))
	h.fsys.failOn, h.fsys.failPath, h.fsys.failErr = "write", ".app.globals.sql.tmp-", syscall.ENOSPC
	code, _, stderr := h.restore(idP)
	assertRefusal(t, code, stderr, exitWrite, "TARGET_WRITE")
	if got := h.names(h.target()); len(got) != 0 {
		t.Errorf("target holds %v", got)
	}
}

func TestTheSummaryNamesTheSnapshotTheTargetAndEveryFile(t *testing.T) {
	h := restoreHost(t)
	code, stdout, _ := h.restore(idP)
	assertCode(t, code, exitOK)
	assertInOrder(t, stdout, "repository base: snapshot "+idP+" restored", "  target:   "+h.target(),
		"  file:     "+h.target()+"/app.dump\n", "  file:     "+h.target()+"/app.globals.sql\n", "root only")
}

func TestATargetThatCannotBeMadeForAPostgreSQLSnapshotIsATargetWriteFailure(t *testing.T) {
	h := restoreHost(t)
	h.fsys.failOn, h.fsys.failPath, h.fsys.failErr = "mkdir", filepath.Base(h.target()), syscall.ENOSPC
	code, _, stderr := h.restore(idP)
	assertRefusal(t, code, stderr, exitWrite, "TARGET_WRITE")
	if !strings.Contains(stderr, "cannot create the target") || len(h.dumpCalls()) != 0 {
		t.Errorf("stderr %q, dumps %v", stderr, h.dumpCalls())
	}
	h.assertAbsent(h.target())
}

func TestTheTargetIsLetGoBeforeTheOutcomeIsRecorded(t *testing.T) {
	for name, id := range map[string]string{"postgresql": idP, "directory": idF} {
		h := restoreHost(t)
		var held []string
		h.log.onWrite = func() { held = openDescriptors(t, h.target()) }
		code, _, _ := h.restore(id)
		assertCode(t, code, exitOK)
		if len(h.log.lines) != 1 || len(held) != 0 {
			t.Errorf("%s: audit %q, the target was held at the record: %v", name, h.log.lines, held)
		}
	}
}

// Р87: a file swapped in the target is a write failure, audited as one.
func TestASwappedPostgreSQLFileIsATargetWriteFailureWithAnAuditLine(t *testing.T) {
	h := restoreHost(t)
	h.fsys.beforeMove = func(dir, old string) {
		if strings.HasPrefix(old, ".app.dump.tmp-") {
			_ = os.Remove(filepath.Join(dir, old))
			ok(t, os.WriteFile(filepath.Join(dir, old), []byte("FOREIGN"), 0o644))
		}
	}
	code, _, stderr := h.restore(idP)
	assertRefusal(t, code, stderr, exitWrite, "TARGET_WRITE")
	for _, want := range []string{filepath.Join(h.target(), "app.dump"), "is not the file the agent wrote"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %s", want, stderr)
		}
	}
	if got := h.fileContent(h.target() + "/app.dump"); got != "FOREIGN" {
		t.Errorf("app.dump %q", got)
	}
	want := "repository base snapshot bbbb1111 restore to " + h.target() + " failed: TARGET_WRITE"
	if len(h.log.lines) != 1 || !strings.Contains(h.log.lines[0], want) {
		t.Errorf("audit %q", h.log.lines)
	}
}

// Р88: what an interrupted restore left makes the target not empty, and is named.
func TestAnInterruptedPostgreSQLRestoreLeftoverIsNamedAndKept(t *testing.T) {
	for _, ref := range []string{idP, idF} {
		h := restoreHost(t)
		left := filepath.Join(h.target(), ".app.dump.tmp-x1")
		ok(t, os.MkdirAll(h.target(), 0o700))
		ok(t, os.WriteFile(left, []byte("HALF"), 0o640))
		code, _, stderr := h.restore(ref)
		assertRefusal(t, code, stderr, exitUsage, "TARGET_NOT_EMPTY")
		for _, want := range []string{left, "the remains of an interrupted restore", "remove it"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr lacks %q: %s", want, stderr)
			}
		}
		if h.fileContent(left) != "HALF" || len(h.dumpCalls()) != 0 || len(h.restoreCalls()) != 0 || len(h.log.lines) != 0 {
			t.Errorf("content %q, dump %v, restore %v, audit %q", h.fileContent(left), h.dumpCalls(), h.restoreCalls(), h.log.lines)
		}
	}
}

func TestANameNotOfTheFormTempIsNotCalledTheRemains(t *testing.T) {
	for _, name := range []string{".tmp-x1", "app.dump.tmp-x1", ".app.dump"} {
		h := restoreHost(t)
		ok(t, os.MkdirAll(h.target(), 0o700))
		ok(t, os.WriteFile(filepath.Join(h.target(), name), []byte("x"), 0o640))
		code, _, stderr := h.restore(idP)
		assertRefusal(t, code, stderr, exitUsage, "TARGET_NOT_EMPTY")
		if strings.Contains(stderr, "remains of an interrupted") {
			t.Errorf("%s: %s", name, stderr)
		}
	}
}
