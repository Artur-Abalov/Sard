// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/pluginhost/journaltest"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// saves is the plugin T that saves the entries in turn and keeps the errors.
func saves(errs *[]error, entries ...[]byte) *journaltest.Plugin {
	return &journaltest.Plugin{RestoreFn: func(_ context.Context, h sdk.RestoreHost, _ sdk.Config, _ sdk.RestoreRequest) (sdk.RestoreResult, error) {
		for _, e := range entries {
			*errs = append(*errs, h.Journal().Save(e))
		}
		return sdk.RestoreResult{}, nil
	}}
}

// Scenario: Запись журнала лежит в конверте с плагином, командой и записью плагина.
func TestTheEntryThePluginSavesLiesInAnEnvelopeWithThePluginTheCommandAndItsBytes(t *testing.T) {
	var errs []error
	r := newOwnRig(t, saves(&errs, []byte("e1")))
	if _, err := r.run(r.step()); err != nil || errs[0] != nil {
		t.Fatal(err, errs)
	}
	plugin, id, entry, found := r.envelopeOf("c1")
	if !found || plugin != "journaltest" || id != "c1" || string(entry) != "e1" {
		t.Errorf("envelope = %q %q %q (%v)", plugin, id, entry, found)
	}
}

// Scenario: Запись журнала пишется через временный файл с fsync, rename и fsync каталога (права).
func TestTheJournalDirectoryOfTheHostIsOwnerOnly(t *testing.T) {
	var errs []error
	r := newOwnRig(t, saves(&errs, []byte("e1")))
	if _, err := r.run(r.step()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(r.journalDir())
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("journal directory: %v, %v", info, err)
	}
}

// Scenario: Повторное сохранение заменяет запись целиком.
func TestSavingAgainReplacesTheEntryAsAWhole(t *testing.T) {
	var errs []error
	r := newOwnRig(t, saves(&errs, []byte("e1"), []byte("e2")))
	if _, err := r.run(r.step()); err != nil {
		t.Fatal(err)
	}
	if got := r.entryOf("c1"); got != "e2" {
		t.Errorf("entry = %q", got)
	}
	r.noTemporaryFiles()
}

func (r *ownRig) noTemporaryFiles() {
	r.t.Helper()
	entries, err := os.ReadDir(r.journalDir())
	if err != nil {
		r.t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			r.t.Errorf("%s is left in the journal", e.Name())
		}
	}
}

// Scenario: Сбой сохранения оставляет прежнюю запись и не оставляет временного файла.
func TestAFailedSaveReturnsAnErrorToThePluginAndKeepsThePreviousEntry(t *testing.T) {
	for name, fail := range map[string]func(*faultyFS){
		"ENOSPC on write": func(f *faultyFS) { f.writeErr = syscall.ENOSPC },
		"EIO on fsync":    func(f *faultyFS) { f.syncErr = syscall.EIO },
		"EIO on rename":   func(f *faultyFS) { f.renameErr = syscall.EIO },
	} {
		t.Run(name, func(t *testing.T) {
			var errs []error
			var r *ownRig
			p := &journaltest.Plugin{RestoreFn: func(_ context.Context, h sdk.RestoreHost, _ sdk.Config, _ sdk.RestoreRequest) (sdk.RestoreResult, error) {
				errs = append(errs, h.Journal().Save([]byte("e1")))
				fail(r.journalFS)
				errs = append(errs, h.Journal().Save([]byte("e2")))
				return sdk.RestoreResult{}, nil
			}}
			r = newOwnRig(t, p)
			if _, err := r.run(r.step()); err != nil {
				t.Fatal(err)
			}
			if errs[0] != nil || errs[1] == nil {
				t.Fatalf("errors = %v", errs)
			}
			if got := r.entryOf("c1"); got != "e1" {
				t.Errorf("entry = %q", got)
			}
			r.noTemporaryFiles()
		})
	}
}

// Scenario: Каталог журнала, который нельзя создать, — ошибка сохранения без следов.
func TestAJournalDirectoryThatCannotBeMadeIsAnErrorForThePlugin(t *testing.T) {
	var errs []error
	r := newOwnRig(t, saves(&errs, []byte("e1")))
	r.journalFS.mkdirErr = syscall.EROFS
	if _, err := r.run(r.step()); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(errs[0], syscall.EROFS) {
		t.Errorf("error = %v", errs[0])
	}
	if _, err := os.Stat(r.journalDir()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the journal directory exists: %v", err)
	}
}

// Scenario: Запись больше предела не сохраняется, прежняя остаётся.
func TestAnEntryOverTheLimitIsAnErrorForThePluginAndThePreviousOneStays(t *testing.T) {
	var errs []error
	r := newOwnRig(t, saves(&errs, []byte("e1"), make([]byte, 1048577)))
	if _, err := r.run(r.step()); err != nil {
		t.Fatal(err)
	}
	if errs[1] == nil || !strings.Contains(errs[1].Error(), "1048576 bytes") || r.entryOf("c1") != "e1" {
		t.Errorf("errors = %v, entry %q", errs, r.entryOf("c1"))
	}
}

// Scenario: Запись ровно в предел сохраняется. Пустая запись сохраняется.
func TestEntriesOfTheLimitAndOfNothingAreSaved(t *testing.T) {
	for name, entry := range map[string][]byte{"limit": bytes.Repeat([]byte{1}, 1048576), "empty": {}} {
		t.Run(name, func(t *testing.T) {
			var errs []error
			r := newOwnRig(t, saves(&errs, entry))
			if _, err := r.run(r.step()); err != nil || errs[0] != nil {
				t.Fatal(err, errs)
			}
			if got := r.entryOf("c1"); got != string(entry) {
				t.Errorf("the entry has %d bytes, want %d", len(got), len(entry))
			}
		})
	}
}

func fillJournal(t *testing.T, r *ownRig, plugin string, n int) {
	t.Helper()
	for i := range n {
		if err := r.store.Save(plugin, fmt.Sprintf("other%02d", i), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
}

// Scenario: Новая запись плагина сверх предела числа записей не сохраняется.
func TestANewEntryOverTheLimitOfEntriesOfThePluginIsAnErrorForThePlugin(t *testing.T) {
	var errs []error
	r := newOwnRig(t, saves(&errs, []byte("e1")))
	fillJournal(t, r, "journaltest", 64)
	if _, err := r.run(r.step()); err != nil {
		t.Fatal(err)
	}
	if errs[0] == nil || !strings.Contains(errs[0].Error(), "64 entries") || r.hasEntry("c1") {
		t.Errorf("error = %v, c1 exists: %v", errs[0], r.hasEntry("c1"))
	}
}

// Scenario: Предел числа записей не мешает переписать уже существующую запись.
func TestTheLimitOfEntriesDoesNotStopThePluginRewritingItsEntry(t *testing.T) {
	var errs []error
	r := newOwnRig(t, saves(&errs, []byte("e2")))
	fillJournal(t, r, "journaltest", 63)
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.run(r.step()); err != nil || errs[0] != nil {
		t.Fatal(err, errs)
	}
	if got := r.entryOf("c1"); got != "e2" {
		t.Errorf("entry = %q", got)
	}
}

// Scenario: Предел числа записей считается для каждого плагина отдельно.
func TestTheLimitOfEntriesIsCountedForEachPluginOnItsOwn(t *testing.T) {
	var errs []error
	r := newOwnRig(t, saves(&errs, []byte("e1")), &journaltest.Plugin{PluginName: "journaltest2"})
	fillJournal(t, r, "journaltest2", 64)
	if _, err := r.run(r.step()); err != nil || errs[0] != nil {
		t.Fatal(err, errs)
	}
	if !r.hasEntry("c1") {
		t.Error("c1 is not saved")
	}
}

// Scenario: Плагин пишет только запись своей команды.
func TestAPluginWritesOnlyTheEntryOfItsOwnCommand(t *testing.T) {
	var errs []error
	r := newOwnRig(t, saves(&errs, []byte("e1")), &journaltest.Plugin{PluginName: "journaltest2"})
	if err := r.store.Save("journaltest2", "c2", []byte("theirs")); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(r.entryPath("c2"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.run(r.step()); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(r.entryPath("c2"))
	if err != nil || !bytes.Equal(before, after) {
		t.Errorf("the entry of c2 changed: %q, %v", after, err)
	}
	if got := r.entryOf("c1"); got != "e1" {
		t.Errorf("entry of c1 = %q", got)
	}
}

// Scenario: Запись со значением секрета не сохраняется.
func TestAnEntryHoldingTheValueOfASecretThePluginReadIsNotSaved(t *testing.T) {
	var saveErr error
	p := &journaltest.Plugin{RestoreFn: func(_ context.Context, h sdk.RestoreHost, _ sdk.Config, _ sdk.RestoreRequest) (sdk.RestoreResult, error) {
		value, err := secretOf(h, "pg-restore")
		if err != nil {
			return sdk.RestoreResult{}, err
		}
		saveErr = h.Journal().Save([]byte(`{"password": "` + string(value) + `"}`))
		return sdk.RestoreResult{}, nil
	}}
	r := newOwnRig(t, p)
	res, rep, err := r.runReported(r.step())
	if err != nil {
		t.Fatal(err)
	}
	if saveErr == nil || !strings.Contains(saveErr.Error(), "pg-restore") {
		t.Errorf("error = %v", saveErr)
	}
	if r.hasEntry("c1") {
		t.Error("the entry is saved")
	}
	assertSecretNotDisclosed(t, append(rep.texts(), r.logs.String(), fmt.Sprint(res), fmt.Sprint(saveErr)))
}

// assertSecretNotDisclosed: none of the texts holds the value P2.
func assertSecretNotDisclosed(t *testing.T, texts []string) {
	t.Helper()
	for _, text := range texts {
		if strings.Contains(text, secretP2) {
			t.Errorf("the secret is disclosed: %q", text)
		}
	}
}

func (r *reporter) texts() []string {
	var out []string
	for _, e := range r.all() {
		out = append(out, e.text)
	}
	return out
}

// An entry may hold what looks like a secret the plugin did not read, and the name of one it did.
func TestAnEntryNamingASecretOrHoldingAValueThePluginNeverReadIsSaved(t *testing.T) {
	var saveErrs []error
	p := &journaltest.Plugin{RestoreFn: func(_ context.Context, h sdk.RestoreHost, _ sdk.Config, _ sdk.RestoreRequest) (sdk.RestoreResult, error) {
		saveErrs = append(saveErrs, h.Journal().Save([]byte(`{"password_ref": "pg-restore"}`)))
		saveErrs = append(saveErrs, h.Journal().Save([]byte(secretP2)))
		return sdk.RestoreResult{}, nil
	}}
	r := newOwnRig(t, p)
	if _, err := r.run(r.step()); err != nil {
		t.Fatal(err)
	}
	if saveErrs[0] != nil || saveErrs[1] != nil {
		t.Errorf("errors = %v", saveErrs)
	}
}

func TestASecretThatHoldsNothingNeverBlocksAnEntry(t *testing.T) {
	p := &journaltest.Plugin{RestoreFn: func(_ context.Context, h sdk.RestoreHost, _ sdk.Config, _ sdk.RestoreRequest) (sdk.RestoreResult, error) {
		if _, err := secretOf(h, "pg-restore"); err != nil {
			return sdk.RestoreResult{}, err
		}
		return sdk.RestoreResult{}, h.Journal().Save([]byte("e1"))
	}}
	r := newOwnRig(t, p)
	rep := &reporter{snapshot: map[string][]byte{"pg-restore": {}}}
	h, _ := r.handlers.Handler("journaltest")
	if _, err := h.Run(context.Background(), r.step(), rep); err != nil {
		t.Fatal(err)
	}
}

// Scenario: Одновременные шаги двух команд пишут свои записи, не мешая друг другу.
func TestStepsOfTwoCommandsAtOnceWriteTheirOwnEntries(t *testing.T) {
	p := &journaltest.Plugin{RestoreFn: func(_ context.Context, h sdk.RestoreHost, _ sdk.Config, req sdk.RestoreRequest) (sdk.RestoreResult, error) {
		for i := range 100 {
			if err := h.Journal().Save(fmt.Appendf(nil, "%s-%d", req.CommandID, i)); err != nil {
				return sdk.RestoreResult{}, err
			}
		}
		return sdk.RestoreResult{}, nil
	}}
	r := newOwnRig(t, p)
	var wg sync.WaitGroup
	for _, id := range []string{"c1", "c2"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			step := r.step()
			step.CommandId = id
			if _, err := r.run(step); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for _, id := range []string{"c1", "c2"} {
		if got := r.entryOf(id); got != id+"-99" {
			t.Errorf("%s: entry = %q", id, got)
		}
	}
	r.noTemporaryFiles()
}
