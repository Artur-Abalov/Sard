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
	fullID         = "4f1c2a9e4f1c2a9e4f1c2a9e4f1c2a9e4f1c2a9e4f1c2a9e4f1c2a9e4f1c2a9e"
	restoreSchema  = `{"type": "object", "properties": {"destination": {"type": "string", "minLength": 1}, "paths": {"type": "array", "items": {"type": "string"}}, "token": {"type": "string", "format": "sard-secret"}}, "required": ["destination"], "additionalProperties": false}`
	restoreConfigD = `{"destination": "s1/20261010T030000Z"}`
)

// restorer is a plugin the console can restore into a directory.
type restorer struct {
	plugin
	plan func(cfg sdk.Config) (sdk.RestorePlan, error)
}

func (*restorer) RestoreConfigSchema() []byte { return []byte(restoreSchema) }

func (r *restorer) PlanRestore(cfg sdk.Config) (sdk.RestorePlan, error) {
	if r.plan != nil {
		return r.plan(cfg)
	}
	return sdk.RestorePlan{Destination: "s1/20261010T030000Z"}, nil
}

// consoleHost is the handlers of a host with a restore directory.
type consoleHost struct {
	*handlersFixture
	dir string
}

func newConsoleHost(t *testing.T, p sdk.Plugin) *consoleHost {
	t.Helper()
	reg, err := sdk.NewRegistry(p)
	if err != nil {
		t.Fatal(err)
	}
	f := &handlersFixture{repo: &repo{}, restoreDir: filepath.Join(t.TempDir(), "verify")}
	f.repo.snapshots = []restic.Snapshot{{ID: fullID, Time: time.Date(2026, 10, 10, 3, 0, 0, 0, time.UTC), Tags: []string{"sard.source=s1"}}}
	f.repo.restoreTo = func(_ context.Context, req restic.RestoreRequest) (restic.RestoreSummary, error) {
		if err := os.MkdirAll(filepath.Join(restoreRoot(req), "srv"), 0o700); err != nil {
			return restic.RestoreSummary{}, err
		}
		return restic.RestoreSummary{Files: 2, Bytes: 1536}, os.WriteFile(filepath.Join(restoreRoot(req), "srv/a"), []byte("A"), 0o600)
	}
	dir := filepath.Join(t.TempDir(), "restore")
	engine := consolerestore.New(consolerestore.Options{
		Dir: dir, FS: hostsetup.OS{}, Owner: hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid(), Mode: 0o700}, Logger: slog.New(slog.DiscardHandler),
	})
	secrets := pluginhost.NewSecrets(map[string]string{"pg": "/etc/sard/pg"}, nil, bound{plugin: "fake"})
	repos := func(name, commandID string, stderr io.Writer) (restic.Repository, bool) {
		f.stderr = append(f.stderr, stderr)
		f.commands = append(f.commands, commandID)
		return f.repo, name == "main"
	}
	f.handlers, err = pluginhost.NewHandlers(reg, secrets, repos, f.restoreDir, pluginhost.WithConsoleRestore(engine))
	if err != nil {
		t.Fatal(err)
	}
	return &consoleHost{handlersFixture: f, dir: dir}
}

func restoreStep(cfg string) *agentv1.RunStep {
	st := step(restore, cfg)
	st.SnapshotId = fullID
	st.Tags = map[string]string{"sard.source": "s1", "sard.run": "r9", "sard.step": "st9"}
	return st
}

// Scenario: Восстановление files — шаг называет цель, число файлов и объём.
func TestARestoreStepRestoresIntoTheDestinationAndReportsTheOutput(t *testing.T) {
	h := newConsoleHost(t, &restorer{})
	res, err := h.run(t, restoreStep(restoreConfigD))
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(h.dir, "s1/20261010T030000Z")
	want := &agentv1.RestoreOutput{Target: target, FilesRestored: 1, BytesRestored: 1}
	if !proto.Equal(res.GetRestore(), want) {
		t.Errorf("output = %v, want %v", res.GetRestore(), want)
	}
	if _, err := os.Stat(filepath.Join(target, "srv/a")); err != nil {
		t.Error(err)
	}
	if got := h.repo.restoreReqs[0]; got.SnapshotID != fullID || got.Include != nil {
		t.Errorf("restic request = %+v", got)
	}
}

func TestTheStepGivesThePlanOfThePluginToTheEngine(t *testing.T) {
	h := newConsoleHost(t, &restorer{plan: func(cfg sdk.Config) (sdk.RestorePlan, error) {
		return sdk.RestorePlan{Destination: "x/y", Paths: []string{"/srv"}}, nil
	}})
	h.repo.holds = map[string]bool{"/srv": true}
	if _, err := h.run(t, restoreStep(restoreConfigD)); err != nil {
		t.Fatal(err)
	}
	if got := h.repo.restoreReqs[0].Include; len(got) != 1 || got[0] != "/srv" {
		t.Errorf("include = %q", got)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "x/y/srv/a")); err != nil {
		t.Error(err)
	}
}

// Scenario: Плагин без восстановления из консоли отклоняет команду восстановления.
func TestAPluginWithoutARestoreFromTheConsoleRejectsARestoreStep(t *testing.T) {
	h := newConsoleHost(t, &plugin{})
	_, err := h.run(t, restoreStep(`{}`))
	if !errors.Is(err, executor.ErrRejected) || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("err = %v", err)
	}
	if len(h.repo.restoreReqs) != 0 {
		t.Error("restic ran")
	}
}

// Scenario: Неверный id снимка отклоняется без обращения к хранилищу.
func TestAnInvalidSnapshotIDIsRejectedWithoutTouchingTheStorage(t *testing.T) {
	for name, id := range map[string]string{
		"empty": "", "short": "aaaa1111", "upper case": strings.ToUpper(fullID), "65 characters": fullID + "a", "not hex": strings.Repeat("g", 64),
	} {
		t.Run(name, func(t *testing.T) {
			h := newConsoleHost(t, &restorer{})
			st := restoreStep(restoreConfigD)
			st.SnapshotId = id
			_, err := h.run(t, st)
			if !errors.Is(err, executor.ErrRejected) || !strings.Contains(err.Error(), "snapshot_id") {
				t.Fatalf("err = %v", err)
			}
			if len(h.repo.restoreReqs) != 0 {
				t.Error("restic ran")
			}
		})
	}
}

// Scenario: Недопустимое назначение отклоняется по схеме.
func TestAConfigOutsideTheRestoreSchemaIsRejectedBeforeAnythingIsWritten(t *testing.T) {
	for name, cfg := range map[string]string{
		"no destination": `{}`, "an overwrite": `{"destination": "x", "overwrite": true}`, "unknown secret": `{"destination": "x", "token": "nope"}`,
	} {
		t.Run(name, func(t *testing.T) {
			h := newConsoleHost(t, &restorer{})
			_, err := h.run(t, restoreStep(cfg))
			if !errors.Is(err, executor.ErrRejected) || !errors.Is(err, sdk.ErrInvalidConfig) {
				t.Fatalf("err = %v", err)
			}
			if _, statErr := os.Stat(h.dir); !os.IsNotExist(statErr) {
				t.Errorf("the restore directory exists: %v", statErr)
			}
		})
	}
}

func TestAPlanThePluginRefusesIsRejectedNamingTheField(t *testing.T) {
	h := newConsoleHost(t, &restorer{plan: func(sdk.Config) (sdk.RestorePlan, error) {
		return sdk.RestorePlan{}, (sdk.RestorePlan{Destination: "/etc"}).Validate()
	}})
	_, err := h.run(t, restoreStep(restoreConfigD))
	if !errors.Is(err, executor.ErrRejected) || !strings.Contains(err.Error(), "/destination") {
		t.Fatalf("err = %v", err)
	}
}

func TestAPlanThatBreaksTheRulesOfThePlanIsRejectedEvenIfThePluginDoesNotCheckIt(t *testing.T) {
	h := newConsoleHost(t, &restorer{plan: func(sdk.Config) (sdk.RestorePlan, error) {
		return sdk.RestorePlan{Destination: "../etc"}, nil
	}})
	_, err := h.run(t, restoreStep(restoreConfigD))
	if !errors.Is(err, executor.ErrRejected) || !strings.Contains(err.Error(), "/destination") {
		t.Fatalf("err = %v", err)
	}
}

// Scenario: Неизвестный репозиторий отклоняет шаг.
func TestARestoreIntoAnUnknownRepositoryIsRejected(t *testing.T) {
	h := newConsoleHost(t, &restorer{})
	st := restoreStep(restoreConfigD)
	st.RepositoryName = "nope"
	_, err := h.run(t, st)
	if !errors.Is(err, executor.ErrRejected) || !strings.Contains(err.Error(), `"nope"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestARestoreWithoutTheSourceTagIsRejected(t *testing.T) {
	h := newConsoleHost(t, &restorer{})
	st := restoreStep(restoreConfigD)
	st.Tags = nil
	_, err := h.run(t, st)
	if !errors.Is(err, executor.ErrRejected) || !strings.Contains(err.Error(), "sard.source") {
		t.Fatalf("err = %v", err)
	}
}

func TestACommandIDThatIsNotADirectoryNameIsRejected(t *testing.T) {
	for _, id := range []string{"", ".", "..", "a/b", "../x"} {
		h := newConsoleHost(t, &restorer{})
		st := restoreStep(restoreConfigD)
		st.CommandId = id
		if _, err := h.run(t, st); !errors.Is(err, executor.ErrRejected) {
			t.Errorf("%q: err = %v", id, err)
		}
	}
}

// Scenario: Неудачное восстановление называет причину и стадию.
func TestAFailedRestoreIsAFailureWithTheReasonAndTheStage(t *testing.T) {
	h := newConsoleHost(t, &restorer{})
	h.repo.restoreTo = func(context.Context, restic.RestoreRequest) (restic.RestoreSummary, error) {
		return restic.RestoreSummary{}, errors.New("boom")
	}
	res, err := h.run(t, restoreStep(restoreConfigD))
	if err == nil || errors.Is(err, executor.ErrRejected) || res != nil {
		t.Fatalf("Run = %v, %v", res, err)
	}
	if !strings.HasPrefix(err.Error(), "RESTIC_FAILED (restore): ") || !strings.HasSuffix(err.Error(), "; nothing was restored") {
		t.Errorf("message = %q", err)
	}
}

// stuckFS cannot remove the directories named in stuck: EIO.
type stuckFS struct {
	hostsetup.FS
	stuck string
}

func (f stuckFS) OpenRootDir() (hostsetup.Dir, error) {
	d, err := f.FS.OpenRootDir()
	return stuckDir{d, f.stuck}, err
}

type stuckDir struct {
	hostsetup.Dir
	stuck string
}

func (d stuckDir) Open(name string) (hostsetup.Dir, error) {
	c, err := d.Dir.Open(name)
	return stuckDir{c, d.stuck}, err
}

func (d stuckDir) Mkdir(name string, perm fs.FileMode) (hostsetup.Dir, error) {
	c, err := d.Dir.Mkdir(name, perm)
	return stuckDir{c, d.stuck}, err
}

func (d stuckDir) RemoveDir(name string) error {
	if name == d.stuck {
		return &fs.PathError{Op: "unlinkat", Path: name, Err: syscall.EIO}
	}
	return d.Dir.RemoveDir(name)
}

// Scenario: Неудачное удаление временного каталога — отдельный исход с тем, что осталось.
func TestARestoreThatCannotRollBackReportsWhatIsLeftInTheOutput(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "restore")
	reg, _ := sdk.NewRegistry(&restorer{})
	repoStub := &repo{snapshots: []restic.Snapshot{{ID: fullID, Tags: []string{"sard.source=s1"}}}}
	repoStub.restoreTo = func(context.Context, restic.RestoreRequest) (restic.RestoreSummary, error) {
		return restic.RestoreSummary{}, errors.New("boom")
	}
	engine := consolerestore.New(consolerestore.Options{
		Dir: dir, FS: stuckFS{hostsetup.OS{}, "cmd-1"}, Owner: hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid(), Mode: 0o700}, Logger: slog.New(slog.DiscardHandler),
	})
	handlers, err := pluginhost.NewHandlers(reg, pluginhost.NewSecrets(nil, nil, nil), func(string, string, io.Writer) (restic.Repository, bool) { return repoStub, true }, t.TempDir(), pluginhost.WithConsoleRestore(engine))
	if err != nil {
		t.Fatal(err)
	}
	h, _ := handlers.Handler("fake")
	res, err := h.Run(context.Background(), restoreStep(restoreConfigD), &reporter{})
	staging := filepath.Join(dir, ".sard-staging", "cmd-1")
	if err == nil || !strings.HasPrefix(err.Error(), "CLEANUP_FAILED (rollback): RESTIC_FAILED (restore)") || !strings.Contains(err.Error(), staging) {
		t.Fatalf("err = %v", err)
	}
	if got := res.GetRestore(); got.GetLeftover() != staging || got.GetTarget() != "" || got.GetFilesRestored() != 0 {
		t.Errorf("output = %v", res)
	}
}

// R2a (ВК14, ВК15). Scenario: Прерванное восстановление сообщает о провале и об откате.
func TestAnInterruptedRestoreSaysThatNothingWasRestored(t *testing.T) {
	interrupted := pluginhost.Interrupted(nil)
	got := interrupted("c1", restore)
	want := "interrupted: agent restarted before the step finished; nothing was restored"
	if got == nil || got.Message != want || got.Output != nil {
		t.Fatalf("Interruption = %+v", got)
	}
}

func TestOnlyAnInterruptedRestoreIsWorded(t *testing.T) {
	interrupted := pluginhost.Interrupted(nil)
	for _, action := range []agentv1.Action{backup, verify, run} {
		if got := interrupted("c1", action); got != nil {
			t.Errorf("%v: %+v", action, got)
		}
	}
}

// Scenario: Неудачная уборка при старте называется в результате прерванного шага.
func TestAnInterruptedRestoreWhoseDirectoryCouldNotBeRemovedNamesWhatIsLeft(t *testing.T) {
	interrupted := pluginhost.Interrupted([]consolerestore.Evidence{
		{Name: "c1", Path: "/r/.sard-staging/c1", Verdict: consolerestore.CleanupFailed, Err: errors.New("unlinkat: input/output error")},
		{Name: "other", Path: "/r/.sard-staging/other", Verdict: consolerestore.CleanupFailed, Err: errors.New("x")},
	})
	got := interrupted("c1", restore)
	want := "CLEANUP_FAILED (rollback): interrupted: agent restarted before the step finished; " +
		"left behind: /r/.sard-staging/c1 (unlinkat: input/output error); remove it by hand"
	if got == nil || got.Message != want || got.Output.GetLeftover() != "/r/.sard-staging/c1" {
		t.Fatalf("Interruption = %+v", got)
	}
	if other := interrupted("c2", restore); other == nil || other.Output != nil || !strings.HasSuffix(other.Message, "nothing was restored") {
		t.Errorf("another restore: %+v", other)
	}
}

// restoreRoot is where a restic that plays by the rules of the request writes.
func restoreRoot(req restic.RestoreRequest) string {
	if req.Staging != nil {
		return filepath.Join(req.Staging.Name(), "root")
	}
	return req.Target
}

// Scenario: Убитое после переноса восстановление сообщает об успехе с итогом из метки (ВК14, случай 2).
func TestAnInterruptedRestoreWhoseMoveHappenedSucceedsWithTheTotalsOfTheMarker(t *testing.T) {
	interrupted := pluginhost.Interrupted([]consolerestore.Evidence{
		{Name: "c1", Path: "/r/.sard-staging/c1", Verdict: consolerestore.Completed, Destination: "/r/s1/20261010T030000Z", Files: 2, Bytes: 4},
	})
	got := interrupted("c1", restore)
	if got == nil || !got.Succeeded || !strings.Contains(got.Message, "agent restarted after the restore completed") {
		t.Fatalf("Interruption = %+v", got)
	}
	want := &agentv1.RestoreOutput{Target: "/r/s1/20261010T030000Z", FilesRestored: 2, BytesRestored: 4}
	if !proto.Equal(got.Output, want) {
		t.Errorf("output = %v, want %v", got.Output, want)
	}
}

// Scenario: Убитое во время restic: the evidence says the directory was removed.
func TestAnInterruptedRestoreWhoseRootWasRemovedSaysThatNothingWasRestored(t *testing.T) {
	interrupted := pluginhost.Interrupted([]consolerestore.Evidence{
		{Name: "c1", Path: "/r/.sard-staging/c1", Verdict: consolerestore.NothingRestored},
	})
	got := interrupted("c1", restore)
	if got == nil || got.Succeeded || got.Output != nil || !strings.HasSuffix(got.Message, "; nothing was restored") {
		t.Fatalf("Interruption = %+v", got)
	}
}
