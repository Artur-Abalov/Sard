// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restorejournal_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/restorejournal"
)

type rig struct {
	dir   string
	store *restorejournal.Store
	fs    *faultyFS
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{dir: filepath.Join(t.TempDir(), "restore-journal"), fs: &faultyFS{FS: hostsetup.OS{}}}
	r.store = restorejournal.New(restorejournal.Options{
		Dir: r.dir, FS: r.fs, Owner: hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid()},
	})
	return r
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) path(id string) string { return filepath.Join(r.dir, id+".json") }

func (r *rig) envelope(t *testing.T, id string) (plugin, commandID string, entry []byte) {
	t.Helper()
	data, err := os.ReadFile(r.path(id))
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Plugin    string `json:"plugin"`
		CommandID string `json:"command_id"`
		Entry     []byte `json:"entry"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	return env.Plugin, env.CommandID, env.Entry
}

func (r *rig) entry(t *testing.T, id string) string {
	t.Helper()
	_, _, e := r.envelope(t, id)
	return string(e)
}

func (r *rig) noTemporaryFiles(t *testing.T) {
	t.Helper()
	names, err := os.ReadDir(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if !strings.HasSuffix(n.Name(), ".json") || strings.HasPrefix(n.Name(), ".") {
			t.Errorf("%s is left in the journal", n.Name())
		}
	}
}

func (r *rig) fill(t *testing.T, plugin string, n int) {
	t.Helper()
	for i := range n {
		if err := r.store.Save(plugin, fmt.Sprintf("other%02d", i), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
}

// faultyFS fails the operations asked for.
type faultyFS struct {
	hostsetup.FS
	writeErr, syncErr, renameErr, mkdirErr, readDirErr error

	mu  sync.Mutex
	ops []string
}

func (f *faultyFS) record(op string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops = append(f.ops, op)
}

func (f *faultyFS) ReadDir(path string) ([]fs.DirEntry, error) {
	if f.readDirErr != nil {
		return nil, f.readDirErr
	}
	return f.FS.ReadDir(path)
}

func (f *faultyFS) SyncDir(dir string) error {
	f.record("sync directory")
	return f.FS.SyncDir(dir)
}

func (f *faultyFS) CreateTemp(dir, pattern string) (hostsetup.File, error) {
	file, err := f.FS.CreateTemp(dir, pattern)
	return &faultyFile{File: file, fs: f}, err
}

func (f *faultyFS) Rename(oldpath, newpath string) error {
	f.record("rename")
	if f.renameErr != nil {
		return f.renameErr
	}
	return f.FS.Rename(oldpath, newpath)
}

func (f *faultyFS) OpenRootDir() (hostsetup.Dir, error) {
	d, err := f.FS.OpenRootDir()
	return faultyDir{Dir: d, fs: f}, err
}

type faultyFile struct {
	hostsetup.File
	fs *faultyFS
}

func (f *faultyFile) Write(p []byte) (int, error) {
	if f.fs.writeErr != nil {
		return 0, f.fs.writeErr
	}
	return f.File.Write(p)
}

func (f *faultyFile) Sync() error {
	f.fs.record("sync file")
	if f.fs.syncErr != nil {
		return f.fs.syncErr
	}
	return f.File.Sync()
}

type faultyDir struct {
	hostsetup.Dir
	fs *faultyFS
}

func (d faultyDir) Open(name string) (hostsetup.Dir, error) {
	c, err := d.Dir.Open(name)
	return faultyDir{Dir: c, fs: d.fs}, err
}

func (d faultyDir) Mkdir(name string, perm fs.FileMode) (hostsetup.Dir, error) {
	if d.fs.mkdirErr != nil {
		return nil, &fs.PathError{Op: "mkdirat", Path: name, Err: d.fs.mkdirErr}
	}
	c, err := d.Dir.Mkdir(name, perm)
	return faultyDir{Dir: c, fs: d.fs}, err
}

// Scenario: Запись журнала пишется через временный файл с fsync, rename и fsync каталога.
func TestTheEntryIsWrittenToATemporaryFileSyncedRenamedAndTheDirectorySynced(t *testing.T) {
	r := newRig(t)
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	// The directory is also synced when it is made; the entry's own order is what counts.
	ops := r.fs.ops
	if len(ops) < 3 || !slices.Equal(ops[len(ops)-3:], []string{"sync file", "rename", "sync directory"}) {
		t.Errorf("operations = %q", ops)
	}
}

// Scenario: Запись журнала лежит в конверте с плагином, командой и записью плагина.
func TestTheEntryLiesInAnEnvelopeWithThePluginTheCommandAndTheBytesOfThePlugin(t *testing.T) {
	r := newRig(t)
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	plugin, id, entry := r.envelope(t, "c1")
	if plugin != "journaltest" || id != "c1" || string(entry) != "e1" {
		t.Errorf("envelope = %q %q %q", plugin, id, entry)
	}
}

// Scenario: Запись журнала пишется через временный файл с fsync, rename и fsync каталога (права и владелец).
func TestTheEntryAndTheDirectoryHaveTheModesAndTheOwnerOfTheService(t *testing.T) {
	r := newRig(t)
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]fs.FileMode{r.dir: 0o700, r.path("c1"): 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
			t.Errorf("%s: mode %v, owner %d", path, info.Mode().Perm(), info.Sys().(*syscall.Stat_t).Uid)
		}
	}
}

// Scenario: Повторное сохранение заменяет запись целиком.
func TestSavingAgainReplacesTheWholeEntry(t *testing.T) {
	r := newRig(t)
	for _, e := range []string{"e1", "e2"} {
		if err := r.store.Save("journaltest", "c1", []byte(e)); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.entry(t, "c1"); got != "e2" {
		t.Errorf("entry = %q", got)
	}
	r.noTemporaryFiles(t)
}

// Scenario: Сбой сохранения оставляет прежнюю запись и не оставляет временного файла.
func TestAFailedSaveKeepsThePreviousEntryAndLeavesNoTemporaryFile(t *testing.T) {
	for name, fail := range map[string]func(*faultyFS){
		"ENOSPC on write": func(f *faultyFS) { f.writeErr = syscall.ENOSPC },
		"EIO on fsync":    func(f *faultyFS) { f.syncErr = syscall.EIO },
		"EIO on rename":   func(f *faultyFS) { f.renameErr = syscall.EIO },
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
				t.Fatal(err)
			}
			fail(r.fs)
			if err := r.store.Save("journaltest", "c1", []byte("e2")); err == nil {
				t.Fatal("Save returned no error")
			}
			if got := r.entry(t, "c1"); got != "e1" {
				t.Errorf("entry = %q", got)
			}
			r.noTemporaryFiles(t)
		})
	}
}

// Scenario: Каталог журнала, который нельзя создать, — ошибка сохранения без следов.
func TestAJournalDirectoryThatCannotBeMadeIsAnErrorWithoutTraces(t *testing.T) {
	r := newRig(t)
	r.fs.mkdirErr = syscall.EROFS
	err := r.store.Save("journaltest", "c1", []byte("e1"))
	if !errors.Is(err, syscall.EROFS) {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(r.dir); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("the journal directory exists: %v", statErr)
	}
}

// Scenario: Запись больше предела не сохраняется, прежняя остаётся.
func TestAnEntryOverTheLimitIsNotSavedAndThePreviousOneStays(t *testing.T) {
	r := newRig(t)
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	err := r.store.Save("journaltest", "c1", make([]byte, 1048577))
	if err == nil || !strings.Contains(err.Error(), "1048576 bytes") {
		t.Fatalf("err = %v", err)
	}
	if got := r.entry(t, "c1"); got != "e1" {
		t.Errorf("entry = %q", got)
	}
}

// Scenario: Запись ровно в предел сохраняется.
func TestAnEntryOfExactlyTheLimitIsSaved(t *testing.T) {
	r := newRig(t)
	want := bytes.Repeat([]byte{7}, 1048576)
	if err := r.store.Save("journaltest", "c1", want); err != nil {
		t.Fatal(err)
	}
	if got := r.entry(t, "c1"); got != string(want) {
		t.Error("the entry is not the bytes saved")
	}
}

// Scenario: Пустая запись сохраняется.
func TestAnEmptyEntryIsSaved(t *testing.T) {
	for name, entry := range map[string][]byte{"empty": {}, "nil": nil} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			if err := r.store.Save("journaltest", "c1", entry); err != nil {
				t.Fatal(err)
			}
			if _, _, got := r.envelope(t, "c1"); len(got) != 0 {
				t.Errorf("entry = %q", got)
			}
			records, bad, err := r.store.List()
			if err != nil || len(bad) != 0 || len(records) != 1 || len(records[0].Entry) != 0 {
				t.Errorf("List = %+v, %+v, %v", records, bad, err)
			}
		})
	}
}

// Scenario: Новая запись плагина сверх предела числа записей не сохраняется.
func TestANewEntryOverTheLimitOfEntriesOfThePluginIsNotSaved(t *testing.T) {
	r := newRig(t)
	r.fill(t, "journaltest", 64)
	err := r.store.Save("journaltest", "c1", []byte("e1"))
	if err == nil || !strings.Contains(err.Error(), "64 entries") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(r.path("c1")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("c1 exists: %v", statErr)
	}
}

// Scenario: Предел числа записей не мешает переписать уже существующую запись.
func TestTheLimitOfEntriesDoesNotStopRewritingAnExistingOne(t *testing.T) {
	r := newRig(t)
	r.fill(t, "journaltest", 63)
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	if err := r.store.Save("journaltest", "c1", []byte("e2")); err != nil {
		t.Fatal(err)
	}
	if got := r.entry(t, "c1"); got != "e2" {
		t.Errorf("entry = %q", got)
	}
}

// Scenario: Предел числа записей считается для каждого плагина отдельно.
func TestTheLimitOfEntriesIsCountedForEachPluginOnItsOwn(t *testing.T) {
	r := newRig(t)
	r.fill(t, "journaltest2", 64)
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	if got := r.entry(t, "c1"); got != "e1" {
		t.Errorf("entry = %q", got)
	}
}

func TestSixtyThreeEntriesLeaveRoomForOneMore(t *testing.T) {
	r := newRig(t)
	r.fill(t, "journaltest", 63)
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
}

// An unreadable file of the journal is no entry of the plugin: it does not count.
func TestAnUnreadableFileDoesNotCountAgainstThePlugin(t *testing.T) {
	r := newRig(t)
	r.fill(t, "journaltest", 63)
	if err := os.WriteFile(r.path("junk"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
}

// Scenario: Одновременные шаги двух команд пишут свои записи, не мешая друг другу.
func TestEntriesOfTwoCommandsWrittenAtOnceAreEachWhole(t *testing.T) {
	r := newRig(t)
	var wg sync.WaitGroup
	for _, id := range []string{"c1", "c2"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 100 {
				if err := r.store.Save("journaltest", id, fmt.Appendf(nil, "%s-%d", id, i)); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	for _, id := range []string{"c1", "c2"} {
		if got, want := r.entry(t, id), id+"-99"; got != want {
			t.Errorf("%s: entry = %q, want %q", id, got, want)
		}
	}
	r.noTemporaryFiles(t)
}

func TestACommandIdThatIsNotAFileNameIsRefused(t *testing.T) {
	r := newRig(t)
	for _, id := range []string{"", ".", "..", "a/b", "../x", ".hidden", ".c1", "a/..", "/abs"} {
		if err := r.store.Save("journaltest", id, nil); err == nil {
			t.Errorf("%q: Save returned no error", id)
		}
	}
	if _, err := os.Stat(r.dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the journal was made for a name that is no file: %v", err)
	}
}

func TestListGivesTheEntriesWithTheirPluginsAndPaths(t *testing.T) {
	r := newRig(t)
	for id, plugin := range map[string]string{"c2": "journaltest2", "c1": "journaltest"} {
		if err := r.store.Save(plugin, id, []byte("e-"+id)); err != nil {
			t.Fatal(err)
		}
	}
	records, bad, err := r.store.List()
	if err != nil || len(bad) != 0 || len(records) != 2 {
		t.Fatalf("List = %+v, %+v, %v", records, bad, err)
	}
	for i, want := range []restorejournal.Record{
		{Plugin: "journaltest", CommandID: "c1", Entry: []byte("e-c1"), Path: r.path("c1")},
		{Plugin: "journaltest2", CommandID: "c2", Entry: []byte("e-c2"), Path: r.path("c2")},
	} {
		if !reflect.DeepEqual(records[i], want) {
			t.Errorf("record %d = %+v, want %+v", i, records[i], want)
		}
	}
}

func TestListOfAJournalThatWasNeverMadeIsEmpty(t *testing.T) {
	r := newRig(t)
	records, bad, err := r.store.List()
	if err != nil || len(records) != 0 || len(bad) != 0 {
		t.Errorf("List = %+v, %+v, %v", records, bad, err)
	}
}

// Scenario: Испорченная запись журнала называется и остаётся.
func TestListNamesTheFilesThatAreNoEntries(t *testing.T) {
	for name, content := range map[string]string{
		"not JSON":               `{"plugin": `,
		"no plugin":              `{"command_id": "c1", "entry": ""}`,
		"another command id":     `{"plugin": "journaltest", "command_id": "c7", "entry": ""}`,
		"entry that is no bytes": `{"plugin": "journaltest", "command_id": "c1", "entry": 7}`,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			mkdir(t, r.dir)
			writeFile(t, r.path("c1"), content)
			assertNamed(t, r, "c1", content)
		})
	}
}

// assertNamed: the journal names the file of the command as no entry and leaves it as it was.
func assertNamed(t *testing.T, r *rig, id, content string) {
	t.Helper()
	records, bad, err := r.store.List()
	if err != nil || len(records) != 0 || len(bad) != 1 {
		t.Fatalf("List = %+v, %+v, %v", records, bad, err)
	}
	if bad[0].CommandID != id || bad[0].Path != r.path(id) || bad[0].Err == nil {
		t.Errorf("unreadable = %+v", bad[0])
	}
	assertContent(t, r.path(id), content)
}

func assertContent(t *testing.T, path, want string) {
	t.Helper()
	if got, err := os.ReadFile(path); err != nil || string(got) != want {
		t.Errorf("%s: %q, %v", path, got, err)
	}
}

func TestListSweepsTheTemporaryFilesACrashLeft(t *testing.T) {
	r := newRig(t)
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	temp := filepath.Join(r.dir, ".c1.json.tmp-123")
	if err := os.WriteFile(temp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	records, bad, err := r.store.List()
	if err != nil || len(records) != 1 || len(bad) != 0 {
		t.Fatalf("List = %+v, %+v, %v", records, bad, err)
	}
	if _, err := os.Stat(temp); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the temporary file stays: %v", err)
	}
}

func TestListLeavesFilesThatAreNotItsOwnAndNamesADirectoryThatLooksLikeAnEntry(t *testing.T) {
	r := newRig(t)
	mkdir(t, filepath.Join(r.dir, "dir.json"))
	writeFile(t, filepath.Join(r.dir, "README"), "x")
	records, bad, err := r.store.List()
	if err != nil || len(records) != 0 || len(bad) != 1 || bad[0].CommandID != "dir" {
		t.Fatalf("List = %+v, %+v, %v", records, bad, err)
	}
	if _, err := os.Stat(filepath.Join(r.dir, "README")); err != nil {
		t.Errorf("a file that is not ours went: %v", err)
	}
}

func TestListNamesTheReasonAnEntryCannotBeRead(t *testing.T) {
	r := newRig(t)
	mkdir(t, filepath.Join(r.dir, "dir.json"))
	_, bad, err := r.store.List()
	if err != nil || len(bad) != 1 {
		t.Fatalf("List = %+v, %v", bad, err)
	}
	if !errors.Is(bad[0].Err, syscall.EISDIR) || strings.Contains(bad[0].Err.Error(), "unreadable") {
		t.Errorf("the reason is %v: it must be the error of the read itself", bad[0].Err)
	}
}

func TestListNeitherReadsNorSweepsHiddenFilesThatAreNoTemporaryFiles(t *testing.T) {
	r := newRig(t)
	mkdir(t, r.dir)
	hidden := filepath.Join(r.dir, ".c9.json")
	writeFile(t, hidden, `{"plugin": "journaltest", "command_id": ".c9", "entry": ""}`)
	writeFile(t, filepath.Join(r.dir, ".keep"), "x")
	writeFile(t, filepath.Join(r.dir, "c2.json.tmp-1"), "x") // a temporary file of ours starts with a dot
	records, bad, err := r.store.List()
	if err != nil || len(records) != 0 || len(bad) != 0 {
		t.Fatalf("List = %+v, %+v, %v", records, bad, err)
	}
	for _, name := range []string{".c9.json", ".keep", "c2.json.tmp-1"} {
		if _, err := os.Stat(filepath.Join(r.dir, name)); err != nil {
			t.Errorf("%s went: %v", name, err)
		}
	}
}

func TestListFailsWhenTheJournalCannotBeRead(t *testing.T) {
	r := newRig(t)
	if err := os.WriteFile(r.dir, []byte("a file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.store.List(); err == nil {
		t.Error("List returned no error")
	}
}

func TestRemoveDeletesTheEntryAndAMissingOneIsNoError(t *testing.T) {
	r := newRig(t)
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := r.store.Remove("c1"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(r.path("c1")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("c1 stays: %v", err)
	}
	if err := newRig(t).store.Remove("c1"); err != nil {
		t.Errorf("Remove from a journal never made: %v", err)
	}
}

func TestRemoveRefusesANameThatIsNoFile(t *testing.T) {
	r := newRig(t)
	mkdir(t, r.dir)
	writeFile(t, filepath.Join(r.dir, ".c9.json"), "x")
	for _, id := range []string{"", ".", "..", "../x", "a/b", ".c9"} {
		if err := r.store.Remove(id); err == nil {
			t.Errorf("Remove(%q) returned no error", id)
		}
	}
	assertContent(t, filepath.Join(r.dir, ".c9.json"), "x")
}

func TestPathIsTheFileOfTheCommand(t *testing.T) {
	r := newRig(t)
	if got := r.store.Path("c1"); got != r.path("c1") {
		t.Errorf("Path = %q", got)
	}
}

func TestANewEntryIsNotSavedWhenTheJournalCannotBeCounted(t *testing.T) {
	r := newRig(t)
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	r.fs.readDirErr = syscall.EIO
	if err := r.store.Save("journaltest", "c2", []byte("e2")); !errors.Is(err, syscall.EIO) {
		t.Errorf("err = %v", err)
	}
	if _, err := os.Stat(r.path("c2")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("c2 exists: %v", err)
	}
}
