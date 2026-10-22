// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restorejournal_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/restorejournal"
)

// The journal of the previous release (ADR 0066 point 4): one file per command,
// the whole file is the entry of a plugin. It is read for one release, never
// written.

type legacyRig struct {
	*rig
	legacy string
}

func newLegacyRig(t *testing.T) *legacyRig {
	t.Helper()
	r := &rig{dir: filepath.Join(t.TempDir(), "restore-journal"), fs: &faultyFS{FS: hostsetup.OS{}}}
	legacy := filepath.Join(filepath.Dir(r.dir), "pg-restore")
	r.store = restorejournal.New(restorejournal.Options{
		Dir: r.dir, FS: r.fs, Owner: hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid()},
		Legacy: []restorejournal.Legacy{{Dir: legacy, Plugin: "journaltest"}},
	})
	return &legacyRig{rig: r, legacy: legacy}
}

func (l *legacyRig) file(id string) string { return filepath.Join(l.legacy, id+".json") }

// Scenario: Прежняя запись разбирается плагином postgresql (the file is the entry of the plugin).
func TestAFileOfTheLegacyJournalIsTheEntryOfItsPlugin(t *testing.T) {
	l := newLegacyRig(t)
	mkdir(t, l.legacy)
	writeFile(t, l.file("c1"), `{"command_id":"c1"}`)
	records, bad, err := l.store.List()
	want := restorejournal.Record{Plugin: "journaltest", CommandID: "c1", Entry: []byte(`{"command_id":"c1"}`), Path: l.file("c1")}
	if err != nil || len(bad) != 0 || len(records) != 1 || !reflect.DeepEqual(records[0], want) {
		t.Fatalf("List = %+v, %+v, %v", records, bad, err)
	}
}

// Scenario: Испорченная прежняя запись называется и остаётся (the host does not judge the content).
func TestAFileOfTheLegacyJournalThatIsNoJSONIsStillHandedToThePlugin(t *testing.T) {
	l := newLegacyRig(t)
	mkdir(t, l.legacy)
	writeFile(t, l.file("c1"), "{broken")
	records, bad, err := l.store.List()
	if err != nil || len(bad) != 0 || len(records) != 1 || string(records[0].Entry) != "{broken" {
		t.Fatalf("List = %+v, %+v, %v", records, bad, err)
	}
	assertContent(t, l.file("c1"), "{broken")
}

// Scenario: Запись одной команды в двух журналах разбирается по новому.
func TestACommandInBothJournalsIsListedOnceFromTheNewOne(t *testing.T) {
	l := newLegacyRig(t)
	mkdir(t, l.legacy)
	writeFile(t, l.file("c1"), "old")
	writeFile(t, l.file("c2"), "old2")
	if err := l.store.Save("journaltest", "c1", []byte("new")); err != nil {
		t.Fatal(err)
	}
	records, _, err := l.store.List()
	if err != nil || len(records) != 2 {
		t.Fatalf("List = %+v, %v", records, err)
	}
	wantRecord(t, records[0], "c1", "new", l.path("c1"))
	wantRecord(t, records[1], "c2", "old2", l.file("c2"))
}

func wantRecord(t *testing.T, got restorejournal.Record, id, entry, path string) {
	t.Helper()
	if got.CommandID != id || string(got.Entry) != entry || got.Path != path {
		t.Errorf("record = %+v, want %s %q at %s", got, id, entry, path)
	}
}

func TestACommandWhoseNewEntryIsUnreadableIsNotTakenFromTheLegacyJournal(t *testing.T) {
	l := newLegacyRig(t)
	mkdir(t, l.dir)
	mkdir(t, l.legacy)
	writeFile(t, l.path("c1"), `{"plugin": `)
	writeFile(t, l.file("c1"), "old")
	records, bad, err := l.store.List()
	if err != nil || len(records) != 0 || len(bad) != 1 || bad[0].Path != l.path("c1") {
		t.Fatalf("List = %+v, %+v, %v", records, bad, err)
	}
}

// Scenario: Без прежнего журнала агент стартует как обычно.
func TestWithoutALegacyJournalListIsAsBefore(t *testing.T) {
	l := newLegacyRig(t)
	if err := l.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	records, bad, err := l.store.List()
	if err != nil || len(bad) != 0 || len(records) != 1 {
		t.Fatalf("List = %+v, %+v, %v", records, bad, err)
	}
	if _, err := os.Stat(l.legacy); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the legacy directory was made: %v", err)
	}
}

func TestOnlyTheFilesOfAnEntryAreTakenFromTheLegacyJournal(t *testing.T) {
	l := newLegacyRig(t)
	mkdir(t, l.legacy)
	writeFile(t, l.file("c1"), "e1")
	writeFile(t, filepath.Join(l.legacy, ".hidden.json"), "x")
	writeFile(t, filepath.Join(l.legacy, "c2.json.tmp"), "x")
	writeFile(t, filepath.Join(l.legacy, "README"), "x")
	records, bad, err := l.store.List()
	if err != nil || len(bad) != 0 || len(records) != 1 || records[0].CommandID != "c1" {
		t.Fatalf("List = %+v, %+v, %v", records, bad, err)
	}
	assertContent(t, filepath.Join(l.legacy, ".hidden.json"), "x")
	assertContent(t, filepath.Join(l.legacy, "c2.json.tmp"), "x")
	assertContent(t, filepath.Join(l.legacy, "README"), "x")
}

func TestALegacyFileThatCannotBeReadIsNamedWithTheErrorOfTheRead(t *testing.T) {
	l := newLegacyRig(t)
	mkdir(t, filepath.Join(l.legacy, "dir.json"))
	_, bad, err := l.store.List()
	if err != nil || len(bad) != 1 || bad[0].CommandID != "dir" || bad[0].Path != l.file("dir") || !errors.Is(bad[0].Err, syscall.EISDIR) {
		t.Fatalf("List = %+v, %v", bad, err)
	}
}

func TestALegacyJournalThatCannotBeReadIsAnErrorAndTheNewEntriesAreStillListed(t *testing.T) {
	l := newLegacyRig(t)
	if err := l.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.legacy, []byte("a file"), 0o600); err != nil {
		t.Fatal(err)
	}
	records, _, err := l.store.List()
	if err == nil || len(records) != 1 || records[0].CommandID != "c1" {
		t.Fatalf("List = %+v, %v", records, err)
	}
}

func TestANewAndALegacyJournalThatCannotBeReadAreBothNamedInTheError(t *testing.T) {
	l := newLegacyRig(t)
	if err := os.WriteFile(l.dir, []byte("a file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.legacy, []byte("a file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := l.store.List(); err == nil {
		t.Error("List returned no error")
	}
}

// Scenario: Прежняя запись удаляется после записи результата и не переносится в новый журнал.
func TestRemoveDeletesTheLegacyEntryToo(t *testing.T) {
	l := newLegacyRig(t)
	mkdir(t, l.legacy)
	writeFile(t, l.file("c1"), "old")
	writeFile(t, l.file("c2"), "old2")
	writeFile(t, filepath.Join(l.legacy, "c1.json.tmp"), "half")
	if err := l.store.Remove("c1"); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{l.file("c1"), filepath.Join(l.legacy, "c1.json.tmp"), l.path("c1")} {
		if _, err := os.Stat(gone); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: %v", gone, err)
		}
	}
	assertContent(t, l.file("c2"), "old2")
}

func TestRemoveDeletesTheNewAndTheLegacyEntryOfACommandInBothJournals(t *testing.T) {
	l := newLegacyRig(t)
	mkdir(t, l.legacy)
	writeFile(t, l.file("c1"), "old")
	if err := l.store.Save("journaltest", "c1", []byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := l.store.Remove("c1"); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{l.file("c1"), l.path("c1")} {
		if _, err := os.Stat(gone); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: %v", gone, err)
		}
	}
}

func TestRemoveOfACommandWithoutALegacyJournalIsNoError(t *testing.T) {
	l := newLegacyRig(t)
	if err := l.store.Remove("c1"); err != nil {
		t.Errorf("Remove: %v", err)
	}
}

func TestAFailedRemovalOfTheLegacyEntryIsAnErrorThatNamesIt(t *testing.T) {
	l := newLegacyRig(t)
	mkdir(t, filepath.Join(l.legacy, "c1.json"))
	writeFile(t, filepath.Join(l.legacy, "c1.json", "x"), "x")
	if err := l.store.Remove("c1"); err == nil {
		t.Error("Remove returned no error for a directory with files")
	}
}

// Scenario: Новый агент не пишет в прежний журнал.
func TestSaveNeverWritesToTheLegacyJournal(t *testing.T) {
	l := newLegacyRig(t)
	if err := l.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(l.legacy); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the legacy directory was made: %v", err)
	}
	mkdir(t, l.legacy)
	if err := l.store.Save("journaltest", "c2", []byte("e2")); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(l.legacy); len(entries) != 0 {
		t.Errorf("the legacy directory holds %d files", len(entries))
	}
}

func TestTheRootIsNoNameOfAnEntry(t *testing.T) {
	r := newRig(t)
	mkdir(t, r.dir)
	if err := r.store.Save("journaltest", "/", []byte("x")); err == nil {
		t.Error("Save accepted /")
	}
	if err := r.store.Remove("/"); err == nil {
		t.Error("Remove accepted /")
	}
	if _, err := os.Stat(filepath.Join(r.dir, ".json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("an entry at <dir>/.json: %v", err)
	}
}
