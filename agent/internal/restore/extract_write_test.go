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
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/restore"
)

// Rule Р62 (A8c-3, R2c-3): the files a plugin plans for a snapshot are laid out in
// the root of the target, each one whole or not at all.

// fakeDumper gives the content of each file by snapshot id and path.
type fakeDumper struct {
	content map[string]string
	// failAfter: the dump writes this many bytes of the content and fails with err.
	failAfter int
	err       error
	asked     []string
	// during runs after the content was written, before the dump ends.
	during func()
}

func (d *fakeDumper) Dump(_ context.Context, snapshotID, path string, w io.Writer) error {
	d.asked = append(d.asked, snapshotID+" "+path)
	data := d.content[snapshotID+" "+path]
	if d.err != nil {
		_, _ = io.WriteString(w, data[:min(d.failAfter, len(data))])
		if d.during != nil {
			d.during()
		}
		return d.err
	}
	_, err := io.WriteString(w, data)
	return err
}

func fullPlan(t *testing.T) ([]restore.File, map[string]string) {
	t.Helper()
	dump, globals := id("bbbb1111"), id("cccc1111")
	files := []restore.File{{SnapshotID: dump, Path: "/app.dump", Name: "app.dump"}, {SnapshotID: globals, Path: "/app.globals.sql", Name: "app.globals.sql"}}
	return files, map[string]string{dump + " /app.dump": "DUMP-BYTES", globals + " /app.globals.sql": "GLOBALS-BYTES"}
}

// wantFailure: f is the reason of the class, and its detail holds each mention.
func wantFileFailure(t *testing.T, f *refusal.Failure, reason refusal.Reason, class refusal.Class, mentions ...string) {
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
	files, content := fullPlan(t)
	if f := restore.WriteFiles(context.Background(), &fakeDumper{content: content}, held, files, names("x1"), repoTarget()); f != nil {
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
	files, content := fullPlan(t)
	if f := restore.WriteFiles(context.Background(), &fakeDumper{content: content}, held, files, names("x1", "x2"), repoTarget()); f != nil {
		t.Fatal(f)
	}
	want := []string{
		"chown .app.dump.tmp-x1 0:0", "chmod .app.dump.tmp-x1 -rw-------", "write .app.dump.tmp-x1",
		"chown .app.globals.sql.tmp-x2 0:0", "chmod .app.globals.sql.tmp-x2 -rw-------", "write .app.globals.sql.tmp-x2",
	}
	got := slices.DeleteFunc(slices.Clone(fsys.files.events), func(e string) bool { return strings.HasPrefix(e, "lstat ") || e == "syncdir" })
	if !slices.Equal(got, want) {
		t.Errorf("events\n got %q\nwant %q", got, want)
	}
}

func TestAnOwnerThatCannotBeSetLeavesNoFile(t *testing.T) {
	dir, fsys := t.TempDir(), newRecordingFS()
	fsys.files.failChown = true
	held, _ := openTarget(fsys, dir)
	defer held.Release()
	files, content := fullPlan(t)
	d := &fakeDumper{content: content}
	f := restore.WriteFiles(context.Background(), d, held, files, names("x1"), repoTarget())
	wantFileFailure(t, f, refusal.TargetWrite, refusal.ClassWrite)
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
	files, content := fullPlan(t)
	if f := restore.WriteFiles(context.Background(), &fakeDumper{content: content}, held, files, names("x1", "x2"), repoTarget()); f != nil {
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
	files, content := fullPlan(t)
	d := &fakeDumper{content: content, failAfter: 4, err: restic.ErrNetwork}
	f := restore.WriteFiles(context.Background(), d, held, files, names("x1"), repoTarget())
	wantFileFailure(t, f, refusal.BackendUnavailable, refusal.ClassTemporary, "the command can be repeated", "; there is no dump file in "+dir)
	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("left %v", got)
	}
}

func TestACancelledDumpIsInterruptedAndLeavesNoFile(t *testing.T) {
	files, content := fullPlan(t)
	dir := t.TempDir()
	held, _ := openTarget(newRecordingFS(), dir)
	defer held.Release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := &fakeDumper{content: content, failAfter: 2, err: errors.New("signal: terminated")}
	f := restore.WriteFiles(ctx, d, held, files, names("x1"), repoTarget())
	wantFileFailure(t, f, refusal.Interrupted, refusal.ClassTemporary, "no dump file")
	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("left %v", got)
	}
}

func TestNoSpaceForTheDumpIsATargetWriteFailureWithoutTraces(t *testing.T) {
	dir, fsys := t.TempDir(), newRecordingFS()
	fsys.files.failWritePrefix, fsys.files.failWrite = ".app.dump.tmp-", syscall.ENOSPC
	held, _ := openTarget(fsys, dir)
	defer held.Release()
	files, content := fullPlan(t)
	// restic, whose output cannot be written, ends with an error of its own
	d := &fakeDumper{content: content, err: errors.New("restic dump: exit status 1: broken pipe")}
	f := restore.WriteFiles(context.Background(), &writeFailing{fakeDumper: d}, held, files, names("x1"), repoTarget())
	wantFileFailure(t, f, refusal.TargetWrite, refusal.ClassWrite, "no space left on device", dir)
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
	files, content := fullPlan(t)
	f := restore.WriteFiles(context.Background(), &fakeDumper{content: content}, held, files, names("x1", "x2"), repoTarget())
	wantFileFailure(t, f, refusal.TargetWrite, refusal.ClassWrite)
	if strings.Contains(f.Detail, "not removed") || strings.Contains(f.Detail, "not the file") {
		t.Errorf("a file that was taken back is said to stay: %q", f.Detail)
	}
	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("left %v", got)
	}
}

func TestAFinalNameThatTurnedUpIsNotReplaced(t *testing.T) {
	dir, fsys := t.TempDir(), newRecordingFS()
	fsys.files.failRename = true
	held, _ := openTarget(fsys, dir)
	defer held.Release()
	files, content := fullPlan(t)
	f := restore.WriteFiles(context.Background(), &fakeDumper{content: content}, held, files, names("x1"), repoTarget())
	wantFileFailure(t, f, refusal.TargetWrite, refusal.ClassWrite, "app.dump")
	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("left %v", got)
	}
}

func TestADirectoryThatCannotBeSyncedFailsTheWrite(t *testing.T) {
	dir, fsys := t.TempDir(), newRecordingFS()
	fsys.failSync = true
	held, _ := openTarget(fsys, dir)
	defer held.Release()
	files, content := fullPlan(t)
	f := restore.WriteFiles(context.Background(), &fakeDumper{content: content}, held, files, names("x1"), repoTarget())
	wantFileFailure(t, f, refusal.TargetWrite, refusal.ClassWrite)
	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("left %v", got)
	}
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

func TestAFileThatCannotBeMadeIsATargetWriteFailureAndNothingIsDumped(t *testing.T) {
	dir, fsys := t.TempDir(), newRecordingFS()
	fsys.files.failCreate = syscall.EROFS
	held, _ := openTarget(fsys, dir)
	defer held.Release()
	files, content := fullPlan(t)
	d := &fakeDumper{content: content}
	f := restore.WriteFiles(context.Background(), d, held, files, names("x1"), repoTarget())
	wantFileFailure(t, f, refusal.TargetWrite, refusal.ClassWrite, "cannot make a file in the target "+dir, "read-only file system")
	if len(d.asked) != 0 || len(entries(t, dir)) != 0 {
		t.Errorf("dumped %v, left %v", d.asked, entries(t, dir))
	}
}

// writeWithTaken writes the dump of a plan of one file when the temporary
// names n1..n<taken> are in use and n1..n9 are offered.
func writeWithTaken(t *testing.T, taken int) (*refusal.Failure, []string) {
	t.Helper()
	files, content := fullPlan(t)
	files = files[:1]
	held, _ := openTarget(newRecordingFS(), t.TempDir())
	defer held.Release()
	var offered []string
	for i := 1; i <= 9; i++ {
		offered = append(offered, "n"+strconv.Itoa(i))
		if i <= taken {
			mustWrite(t, filepath.Join(held.Path, ".app.dump.tmp-n"+strconv.Itoa(i)), "other")
		}
	}
	d := &fakeDumper{content: content}
	return restore.WriteFiles(context.Background(), d, held, files, names(offered...), repoTarget()), d.asked
}

func TestATempNameIsLookedForEightTimes(t *testing.T) {
	if f, asked := writeWithTaken(t, 7); f != nil || len(asked) != 1 {
		t.Errorf("seven names taken: %v, dumped %v", f, asked)
	}
	f, asked := writeWithTaken(t, 8)
	wantFileFailure(t, f, refusal.TargetWrite, refusal.ClassWrite, "cannot find a free name for a temporary file")
	if len(asked) != 0 {
		t.Errorf("eight names taken: dumped %v", asked)
	}
}

func TestAFileThatCannotBeSyncedLeavesNoFile(t *testing.T) {
	dir, fsys := t.TempDir(), newRecordingFS()
	fsys.files.failFileSync = true
	held, _ := openTarget(fsys, dir)
	defer held.Release()
	files, content := fullPlan(t)
	f := restore.WriteFiles(context.Background(), &fakeDumper{content: content}, held, files, names("x1"), repoTarget())
	wantFileFailure(t, f, refusal.TargetWrite, refusal.ClassWrite, "cannot save "+dir+"/app.dump", "input/output error")
	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("left %v", got)
	}
}

func TestEveryFileIsClosedWhateverTheOutcome(t *testing.T) {
	for name, arrange := range map[string]func(*recordingFS, string) *fakeDumper{
		"success":     func(*recordingFS, string) *fakeDumper { return &fakeDumper{} },
		"dump failed": func(*recordingFS, string) *fakeDumper { return &fakeDumper{err: restic.ErrNetwork} },
		"owner":       func(f *recordingFS, _ string) *fakeDumper { f.files.failChown = true; return &fakeDumper{} },
		"stat":        func(f *recordingFS, _ string) *fakeDumper { f.files.failFileStat = true; return &fakeDumper{} },
		"sync":        func(f *recordingFS, _ string) *fakeDumper { f.files.failFileSync = true; return &fakeDumper{} },
		"rename":      func(f *recordingFS, _ string) *fakeDumper { f.files.failRename = true; return &fakeDumper{} },
		"directory":   func(f *recordingFS, _ string) *fakeDumper { f.failSync = true; return &fakeDumper{} },
		"swapped": func(f *recordingFS, dir string) *fakeDumper {
			f.files.beforeRename = func(old, _ string) { swap(t, dir, old, "FOREIGN") }
			return &fakeDumper{}
		},
	} {
		fsys, dir := newRecordingFS(), t.TempDir()
		d := arrange(fsys, dir)
		files, content := fullPlan(t)
		d.content = content
		held, _ := openTarget(fsys, dir)
		_ = restore.WriteFiles(context.Background(), d, held, files, names("x1", "x2"), repoTarget())
		held.Release()
		fsys.mu.Lock()
		closed := slices.Clone(fsys.files.closed)
		fsys.mu.Unlock()
		want := []string{".app.dump.tmp-x1", ".app.globals.sql.tmp-x2"}
		if name != "success" {
			want = want[:1] // the first failure ends the write
		}
		if !slices.Equal(closed, want) {
			t.Errorf("%s: closed %v, want %v", name, closed, want)
		}
	}
}

func TestTheFirstErrorOfTheFileIsTheOneReported(t *testing.T) {
	dir, fsys := t.TempDir(), newRecordingFS()
	fsys.files.failWritePrefix, fsys.files.failWrite, fsys.files.failWriteLater = ".app.dump.tmp-", syscall.ENOSPC, syscall.EIO
	held, _ := openTarget(fsys, dir)
	defer held.Release()
	files, _ := fullPlan(t)
	two := &twoWrites{err: errors.New("restic dump: exit status 1")}
	f := restore.WriteFiles(context.Background(), two, held, files, names("x1"), repoTarget())
	wantFileFailure(t, f, refusal.TargetWrite, refusal.ClassWrite, "no space left on device")
	if strings.Contains(f.Detail, "input/output error") {
		t.Errorf("a later error was reported: %q", f.Detail)
	}
}

// twoWrites is a dumper that writes twice, whatever the first write says.
type twoWrites struct{ err error }

func (d *twoWrites) Dump(_ context.Context, _, _ string, w io.Writer) error {
	_, _ = io.WriteString(w, "a")
	_, _ = io.WriteString(w, "b")
	return d.err
}

// Р87: after the move every file is looked for by its name, without following
// a link, and compared with the file the agent wrote; what is not that file is
// never removed.

// swap puts a file of someone else under the name, in place of what is there.
func swap(t *testing.T, dir, name, content string) {
	t.Helper()
	_ = os.Remove(filepath.Join(dir, name))
	mustWrite(t, filepath.Join(dir, name), content)
}

func foreignContent(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return string(data)
}

func TestEveryFileIsComparedWithTheOneWrittenAfterTheMoveAndBeforeTheDirectorySync(t *testing.T) {
	dir, fsys := t.TempDir(), newRecordingFS()
	held, _ := openTarget(fsys, dir)
	defer held.Release()
	files, content := fullPlan(t)
	if f := restore.WriteFiles(context.Background(), &fakeDumper{content: content}, held, files, names("x1", "x2"), repoTarget()); f != nil {
		t.Fatal(f)
	}
	var seen []string
	for _, e := range fsys.files.events {
		if strings.HasPrefix(e, "lstat ") || e == "syncdir" {
			seen = append(seen, e)
		}
	}
	if want := []string{"lstat app.dump", "syncdir", "lstat app.globals.sql", "syncdir"}; !slices.Equal(seen, want) {
		t.Errorf("events %q, want %q", seen, want)
	}
}

func TestADumpSwappedBeforeTheMoveIsATargetWriteFailureAndTheForeignFileStays(t *testing.T) {
	dir, fsys := t.TempDir(), newRecordingFS()
	fsys.files.beforeRename = func(old, _ string) {
		if strings.HasPrefix(old, ".app.dump.tmp-") {
			swap(t, dir, old, "FOREIGN")
		}
	}
	held, _ := openTarget(fsys, dir)
	defer held.Release()
	files, content := fullPlan(t)
	f := restore.WriteFiles(context.Background(), &fakeDumper{content: content}, held, files, names("x1"), repoTarget())
	wantFileFailure(t, f, refusal.TargetWrite, refusal.ClassWrite, filepath.Join(dir, "app.dump"), "is not the file the agent wrote", "was not removed", "new target")
	if n := strings.Count(f.Detail, "not the file the agent wrote"); n != 1 {
		t.Errorf("the failure says it %d times: %q", n, f.Detail)
	}
	if got := foreignContent(t, dir, "app.dump"); got != "FOREIGN" || len(entries(t, dir)) != 1 {
		t.Errorf("app.dump %q, entries %v", got, entries(t, dir))
	}
}

func TestANameThatVanishedAfterTheMoveIsTheSameFailure(t *testing.T) {
	dir, fsys := t.TempDir(), newRecordingFS()
	fsys.files.afterRename = func(name string) { _ = os.Remove(filepath.Join(dir, name)) }
	held, _ := openTarget(fsys, dir)
	defer held.Release()
	files, content := fullPlan(t)
	f := restore.WriteFiles(context.Background(), &fakeDumper{content: content}, held, files, names("x1"), repoTarget())
	wantFileFailure(t, f, refusal.TargetWrite, refusal.ClassWrite, filepath.Join(dir, "app.dump"), "is not the file the agent wrote")
}

func TestASwappedGlobalsFileTakesTheDumpBackAndStays(t *testing.T) {
	dir, fsys := t.TempDir(), newRecordingFS()
	fsys.files.beforeRename = func(old, _ string) {
		if strings.HasPrefix(old, ".app.globals.sql.tmp-") {
			swap(t, dir, old, "FOREIGN-G")
		}
	}
	held, _ := openTarget(fsys, dir)
	defer held.Release()
	files, content := fullPlan(t)
	f := restore.WriteFiles(context.Background(), &fakeDumper{content: content}, held, files, names("x1", "x2"), repoTarget())
	wantFileFailure(t, f, refusal.TargetWrite, refusal.ClassWrite, "app.globals.sql", "is not the file the agent wrote")
	if got := entries(t, dir); !slices.Equal(got, []string{"app.globals.sql"}) || foreignContent(t, dir, "app.globals.sql") != "FOREIGN-G" {
		t.Errorf("entries %v", got)
	}
}

func TestAForeignFileUnderTheNameOfAFinishedDumpIsNotRemovedWhenTakingBack(t *testing.T) {
	dir, fsys := t.TempDir(), newRecordingFS()
	fsys.files.failWritePrefix, fsys.files.failWrite = ".app.globals.sql.tmp-", syscall.ENOSPC
	fsys.files.beforeWrite = func(name string) {
		if strings.HasPrefix(name, ".app.globals.sql.tmp-") {
			swap(t, dir, "app.dump", "FOREIGN")
		}
	}
	held, _ := openTarget(fsys, dir)
	defer held.Release()
	files, content := fullPlan(t)
	f := restore.WriteFiles(context.Background(), &fakeDumper{content: content}, held, files, names("x1", "x2"), repoTarget())
	wantFileFailure(t, f, refusal.TargetWrite, refusal.ClassWrite, filepath.Join(dir, "app.dump"), "not removed", "not the file the agent wrote")
	if got := entries(t, dir); !slices.Equal(got, []string{"app.dump"}) || foreignContent(t, dir, "app.dump") != "FOREIGN" {
		t.Errorf("entries %v", got)
	}
}

func TestAForeignFileUnderTheNameOfTheTempFileIsNotRemovedWhenTheDumpFails(t *testing.T) {
	dir := t.TempDir()
	held, _ := openTarget(newRecordingFS(), dir)
	defer held.Release()
	files, content := fullPlan(t)
	d := &fakeDumper{content: content, failAfter: 2, err: restic.ErrNetwork, during: func() { swap(t, dir, ".app.dump.tmp-x1", "FOREIGN") }}
	f := restore.WriteFiles(context.Background(), d, held, files, names("x1"), repoTarget())
	wantFileFailure(t, f, refusal.BackendUnavailable, refusal.ClassTemporary, ".app.dump.tmp-x1", "not removed")
	if foreignContent(t, dir, ".app.dump.tmp-x1") != "FOREIGN" {
		t.Error("the foreign file was removed")
	}
}

func TestAnUnreadableWrittenFileIsATargetWriteFailureAndIsNotRemovedUnchecked(t *testing.T) {
	dir, fsys := t.TempDir(), newRecordingFS()
	fsys.files.failFileStat = true
	held, _ := openTarget(fsys, dir)
	defer held.Release()
	files, content := fullPlan(t)
	f := restore.WriteFiles(context.Background(), &fakeDumper{content: content}, held, files, names("x1"), repoTarget())
	wantFileFailure(t, f, refusal.TargetWrite, refusal.ClassWrite, ".app.dump.tmp-x1", "was not removed")
	fsys.mu.Lock()
	closed := slices.Clone(fsys.files.closed)
	fsys.mu.Unlock()
	if !slices.Equal(closed, []string{".app.dump.tmp-x1"}) {
		t.Errorf("closed %v: the file is let go when it cannot be looked at", closed)
	}
	if got := entries(t, dir); !slices.Equal(got, []string{".app.dump.tmp-x1"}) {
		t.Errorf("entries %v: a name whose identity cannot be confirmed is not unlinked", got)
	}
}

// The temp file is held open while its name is judged for removal: a writer of
// the target cannot then give the inode of a deleted file to a new one.
func TestTheTempFileIsStillOpenWhenItsNameIsJudgedForRemoval(t *testing.T) {
	for name, arrange := range map[string]func(*recordingFS) *fakeDumper{
		"dump failed": func(*recordingFS) *fakeDumper { return &fakeDumper{failAfter: 2, err: restic.ErrNetwork} },
		"owner":       func(f *recordingFS) *fakeDumper { f.files.failChown = true; return &fakeDumper{} },
	} {
		fsys := newRecordingFS()
		d := arrange(fsys)
		files, content := fullPlan(t)
		d.content = content
		var judged []bool
		fsys.files.onLstat = func(n string) {
			if n == ".app.dump.tmp-x1" {
				fsys.mu.Lock()
				judged = append(judged, slices.Contains(fsys.files.closed, n))
				fsys.mu.Unlock()
			}
		}
		held, _ := openTarget(fsys, t.TempDir())
		_ = restore.WriteFiles(context.Background(), d, held, files, names("x1"), repoTarget())
		held.Release()
		if !slices.Equal(judged, []bool{false}) {
			t.Errorf("%s: judged with the file closed? %v, want exactly one judgement with it open", name, judged)
		}
	}
}

// Р88: the temporary file of a PostgreSQL restore that was killed makes the
// target not empty, and the refusal names it as the remains of it.
func TestATempFileOfAKilledRestoreOfFilesIsNamedAsTheRemainsInTheRefusal(t *testing.T) {
	for _, name := range []string{".app.dump.tmp-x1", ".app.globals.sql.tmp-x2", ".a.tmp-"} {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, name), "HALF")
		_, f := check(t, restore.Target{Path: dir})
		wantFailure(t, f, refusal.TargetNotEmpty, filepath.Join(dir, name), "the remains of an interrupted restore", "remove it")
		if data, _ := os.ReadFile(filepath.Join(dir, name)); string(data) != "HALF" {
			t.Errorf("%s was changed: %q", name, data)
		}
	}
}

func TestOtherNamesAreNotCalledTheRemainsOfAnInterruptedRestore(t *testing.T) {
	for _, name := range []string{".tmp-x1", "..tmp-x1", "app.dump.tmp-x1", ".app.dump", "notes.txt"} {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, name), "x")
		_, f := check(t, restore.Target{Path: dir})
		wantFailure(t, f, refusal.TargetNotEmpty)
		if strings.Contains(f.Detail, "remains of an interrupted") {
			t.Errorf("%s: %s", name, f.Detail)
		}
	}
}

func TestTheFirstOfSeveralRemainsIsTheOneNamed(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".a.dump.tmp-1"), "x")
	mustMkdir(t, dir, ".sard-restore-x")
	_, f := check(t, restore.Target{Path: dir})
	if f == nil || strings.Count(f.Detail, "remains of an interrupted") != 1 {
		t.Fatalf("failure %v", f)
	}
}
