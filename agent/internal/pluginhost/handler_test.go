// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

var (
	_ executor.Registry = (*pluginhost.Handlers)(nil)
	_ executor.Reporter = (*reporter)(nil)
)

const (
	backup  = agentv1.Action_ACTION_BACKUP
	restore = agentv1.Action_ACTION_RESTORE
	verify  = agentv1.Action_ACTION_VERIFY
	run     = agentv1.Action_ACTION_RUN
)

type handlersFixture struct {
	handlers   *pluginhost.Handlers
	repo       *repo
	restoreDir string
	stderr     []io.Writer // what each repository lookup was given
	commands   []string
}

func newHandlers(t *testing.T, plugins ...sdk.Plugin) *handlersFixture {
	t.Helper()
	reg, err := sdk.NewRegistry(plugins...)
	if err != nil {
		t.Fatal(err)
	}
	f := &handlersFixture{repo: &repo{}, restoreDir: filepath.Join(t.TempDir(), "restore")}
	secrets := pluginhost.NewSecrets(map[string]string{"pg": "/etc/sard/pg"}, (&files{data: map[string]string{"/etc/sard/pg": "s3cret"}}).read)
	repos := func(name, commandID string, stderr io.Writer) (restic.Repository, bool) {
		f.stderr = append(f.stderr, stderr)
		f.commands = append(f.commands, commandID)
		return f.repo, name == "main"
	}
	f.handlers, err = pluginhost.NewHandlers(reg, secrets, repos, f.restoreDir)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *handlersFixture) run(t *testing.T, step *agentv1.RunStep) (*agentv1.StepResult, error) {
	t.Helper()
	h, ok := f.handlers.Handler(step.GetPlugin())
	if !ok {
		t.Fatalf("no handler for %q", step.GetPlugin())
	}
	return h.Run(context.Background(), step, &reporter{})
}

// restic writes its stderr to the step's tool output (A7c).
func TestTheRepositoryOfAStepWritesStderrToTheStepsOutput(t *testing.T) {
	f := newHandlers(t, &plugin{})
	h, _ := f.handlers.Handler("fake")
	r := &reporter{}
	if _, err := h.Run(context.Background(), step(backup, `{}`), r); err != nil {
		t.Fatal(err)
	}
	if len(f.stderr) != 1 || f.commands[0] != "cmd-1" {
		t.Fatalf("lookups = %d, for %q", len(f.stderr), f.commands)
	}
	if _, err := f.stderr[0].Write([]byte("Fatal: x\n")); err != nil {
		t.Fatal(err)
	}
	if got := r.outputText(); got != "Fatal: x\n" {
		t.Fatalf("output = %q", got)
	}
}

func step(action agentv1.Action, cfg string) *agentv1.RunStep {
	return &agentv1.RunStep{CommandId: "cmd-1", Plugin: "fake", ConfigJson: cfg, Action: action, RepositoryName: "main"}
}

func TestHandlersKnowTheRegisteredPluginsOnly(t *testing.T) {
	f := newHandlers(t, &plugin{})
	if _, ok := f.handlers.Handler("fake"); !ok {
		t.Error("no handler for a registered plugin")
	}
	if _, ok := f.handlers.Handler("oracle"); ok {
		t.Error("a handler for an unknown plugin")
	}
}

func TestActionsOfAPluginFollowWhatItImplements(t *testing.T) {
	f := newHandlers(t, &plugin{})
	h, _ := f.handlers.Handler("fake")
	if got := h.Actions(); !slices.Equal(got, []agentv1.Action{backup, restore}) {
		t.Errorf("plugin: %v", got)
	}
	f = newHandlers(t, &verifier{})
	h, _ = f.handlers.Handler("fake")
	if got := h.Actions(); !slices.Equal(got, []agentv1.Action{backup, restore, verify}) {
		t.Errorf("verifier: %v", got)
	}
}

func TestNewHandlersRejectsAPluginWhoseSchemaDoesNotCompile(t *testing.T) {
	reg, _ := sdk.NewRegistry(&badSchema{})
	if _, err := pluginhost.NewHandlers(reg, pluginhost.NewSecrets(nil, nil), nil, ""); err == nil {
		t.Fatal("NewHandlers accepted an invalid schema")
	}
}

// OQ-018: the backup output carries the repository id restic reported.
func TestBackupOutputCarriesTheSnapshotAndRepositoryID(t *testing.T) {
	f := newHandlers(t, &plugin{})
	st := step(backup, `{}`)
	st.Tags = map[string]string{"workflow": "nightly", "run": "7"}
	res, err := f.run(t, st)
	if err != nil {
		t.Fatal(err)
	}
	want := &agentv1.BackupOutput{SnapshotId: "snap", RepositoryId: "repo-id", TotalBytes: 42}
	if !proto.Equal(res.GetBackup(), want) {
		t.Errorf("output = %v, want %v", res.GetBackup(), want)
	}
	if tags := f.repo.requests[0].Tags; !slices.Equal(tags, []string{"run=7", "workflow=nightly"}) {
		t.Errorf("tags = %q", tags)
	}
}

// A snapshot written without some files is a failure that keeps its output.
func TestPartialBackupFailsWithItsOutput(t *testing.T) {
	f := newHandlers(t, &plugin{})
	f.repo.err, f.repo.partial = &restic.PartialError{}, true
	res, err := f.run(t, step(backup, `{}`))
	if !errors.Is(err, restic.ErrUnreadableSource) || errors.Is(err, executor.ErrRejected) || res.GetBackup().GetSnapshotId() != "snap" {
		t.Fatalf("Run = %v, %v", res, err)
	}
}

// A6b Ф11: what went wrong with the repository names the repository, and the
// reason survives.
func TestRepositoryFailuresNameTheRepository(t *testing.T) {
	for name, cause := range map[string]error{
		"locked":   restic.ErrLocked,
		"missing":  restic.ErrNoRepository,
		"password": restic.ErrWrongPassword,
		"fatal":    &restic.ExitError{Code: 1, Message: "Fatal: unable to open repository"},
		"no start": errors.New("restic backup: fork/exec restic: no such file or directory"),
	} {
		f := newHandlers(t, &plugin{})
		f.repo.err = cause
		res, err := f.run(t, step(backup, `{}`))
		if !errors.Is(err, cause) || res != nil || !strings.Contains(err.Error(), `repository "main": `) || strings.Contains(err.Error(), "\n") {
			t.Errorf("%s: Run = %v, %v", name, res, err)
		}
	}
	if !strings.Contains(restic.ErrLocked.Error(), "locked by another process") {
		t.Errorf("text = %q", restic.ErrLocked)
	}
}

func TestPluginFailuresDoNotNameTheRepository(t *testing.T) {
	f := newHandlers(t, &plugin{prepare: func(context.Context, sdk.Host, sdk.Config) error {
		return errors.New("/missing: no such file or directory")
	}})
	_, err := f.run(t, step(backup, `{}`))
	if err == nil || strings.Contains(err.Error(), "main") {
		t.Errorf("err = %v", err)
	}
}

func TestBackupFailureHasNoOutput(t *testing.T) {
	f := newHandlers(t, &plugin{})
	f.repo.err = restic.ErrLocked
	res, err := f.run(t, step(backup, `{}`))
	if !errors.Is(err, restic.ErrLocked) || errors.Is(err, executor.ErrRejected) || res != nil {
		t.Fatalf("Run = %v, %v", res, err)
	}
}

// Invalid configs, unknown secrets and tags restic cannot store are
// rejected before the plugin runs.
func TestInvalidStepsAreRejectedBeforeThePluginRuns(t *testing.T) {
	for name, st := range map[string]*agentv1.RunStep{
		"invalid config":         step(backup, `{"x": 1}`),
		"unknown secret":         step(backup, `{"token": "mysql"}`),
		"tag with a comma":       func() *agentv1.RunStep { s := step(backup, `{}`); s.Tags = map[string]string{"a": "b,c"}; return s }(),
		"tag with an empty key":  func() *agentv1.RunStep { s := step(backup, `{}`); s.Tags = map[string]string{"": "b"}; return s }(),
		"unknown repository":     func() *agentv1.RunStep { s := step(backup, `{}`); s.RepositoryName = "nas"; return s }(),
		"unsupported action":     step(run, `{}`),
		"verify without Verify":  func() *agentv1.RunStep { s := step(verify, `{}`); s.SnapshotId = "snap"; return s }(),
		"restore without a snap": step(restore, `{}`),
		"verify without a snap":  step(verify, `{}`),
	} {
		p := &plugin{}
		f := newHandlers(t, p)
		res, err := f.run(t, st)
		if !errors.Is(err, executor.ErrRejected) || res != nil {
			t.Errorf("%s: Run = %v, %v", name, res, err)
		}
		if len(p.calls) != 0 || len(f.repo.requests) != 0 || len(f.repo.restores) != 0 {
			t.Errorf("%s: ran %q, %d backups, %d restores", name, p.calls, len(f.repo.requests), len(f.repo.restores))
		}
	}
}

func TestRejectionNamesTheViolation(t *testing.T) {
	_, err := newHandlers(t, &plugin{}).run(t, step(backup, `{"token": "mysql"}`))
	want := `step rejected: invalid plugin config: /token: unknown secret "mysql"`
	if err == nil || err.Error() != want || !errors.Is(err, sdk.ErrUnknownSecret) {
		t.Fatalf("err = %v, want %s", err, want)
	}
}

func TestRestoreWritesIntoAFreshDirectoryOfTheCommand(t *testing.T) {
	f := newHandlers(t, &plugin{})
	st := step(restore, `{}`)
	st.SnapshotId = "snap"
	res, err := f.run(t, st)
	target := filepath.Join(f.restoreDir, "cmd-1")
	if err != nil || res.GetRestore().GetTarget() != target {
		t.Fatalf("Run = %v, %v", res, err)
	}
	if !slices.Equal(f.repo.restores, []string{"snap " + target}) {
		t.Errorf("restores = %q", f.repo.restores)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("target: %v, %v", info, err)
	}
	// The same command id again never restores over the first copy.
	if _, err := f.run(t, st); err == nil || len(f.repo.restores) != 1 {
		t.Errorf("second restore: %v, %d restores", err, len(f.repo.restores))
	}
}

func TestRestoreRejectsACommandIDThatIsNotADirectoryName(t *testing.T) {
	for _, id := range []string{"", ".", "..", "a/b", "../x"} {
		st := step(restore, `{}`)
		st.SnapshotId, st.CommandId = "snap", id
		f := newHandlers(t, &plugin{})
		if _, err := f.run(t, st); !errors.Is(err, executor.ErrRejected) || len(f.repo.restores) != 0 {
			t.Errorf("%q: err = %v", id, err)
		}
	}
}

func TestRestoreFailure(t *testing.T) {
	f := newHandlers(t, &plugin{})
	f.repo.restoreErr = restic.ErrNoRepository
	st := step(restore, `{}`)
	st.SnapshotId = "snap"
	if res, err := f.run(t, st); !errors.Is(err, restic.ErrNoRepository) || res != nil {
		t.Fatalf("Run = %v, %v", res, err)
	}
}

func TestVerifyRestoresChecksAndRemovesTheCopy(t *testing.T) {
	v := &verifier{}
	f := newHandlers(t, v)
	st := step(verify, `{}`)
	st.SnapshotId = "snap"
	res, err := f.run(t, st)
	target := filepath.Join(f.restoreDir, "cmd-1")
	want := &agentv1.VerifyOutput{SnapshotId: "snap", Checks: []*agentv1.CheckResult{{Name: "fake", Passed: true}}}
	if err != nil || !proto.Equal(res.GetVerify(), want) {
		t.Fatalf("Run = %v, %v", res, err)
	}
	if !slices.Equal(v.calls, []string{"verify {} " + target}) {
		t.Errorf("calls = %q", v.calls)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the restored copy was kept: %v", err)
	}
}

func TestFailedVerificationIsAFailedCheck(t *testing.T) {
	v := &verifier{err: errors.New("db.sql differs")}
	f := newHandlers(t, v)
	st := step(verify, `{}`)
	st.SnapshotId = "snap"
	res, err := f.run(t, st)
	check := res.GetVerify().GetChecks()[0]
	if !errors.Is(err, v.err) || errors.Is(err, executor.ErrRejected) || check.GetPassed() || !strings.Contains(check.GetDetail(), "differs") {
		t.Fatalf("Run = %v, %v", res, err)
	}
}

func TestVerifyStopsWhenTheRestoreFails(t *testing.T) {
	v := &verifier{}
	f := newHandlers(t, v)
	f.repo.restoreErr = restic.ErrLocked
	st := step(verify, `{}`)
	st.SnapshotId = "snap"
	if res, err := f.run(t, st); !errors.Is(err, restic.ErrLocked) || res != nil || len(v.calls) != 0 {
		t.Fatalf("Run = %v, %v, calls %q", res, err, v.calls)
	}
}

func TestRestoreFailsWhenTheRestoreDirCannotBeCreated(t *testing.T) {
	f := newHandlers(t, &plugin{})
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	reg, _ := sdk.NewRegistry(&plugin{})
	h, err := pluginhost.NewHandlers(reg, pluginhost.NewSecrets(nil, nil), func(string, string, io.Writer) (restic.Repository, bool) { return f.repo, true }, filepath.Join(blocker, "restore"))
	if err != nil {
		t.Fatal(err)
	}
	handler, _ := h.Handler("fake")
	st := step(restore, `{}`)
	st.SnapshotId = "snap"
	if _, err := handler.Run(context.Background(), st, &reporter{}); err == nil || errors.Is(err, executor.ErrRejected) || len(f.repo.restores) != 0 {
		t.Fatalf("err = %v", err)
	}
}

func TestVerifyRejectsAnInvalidConfigBeforeRestoring(t *testing.T) {
	f := newHandlers(t, &verifier{})
	st := step(verify, `{"x": 1}`)
	st.SnapshotId = "snap"
	if _, err := f.run(t, st); !errors.Is(err, executor.ErrRejected) || len(f.repo.restores) != 0 {
		t.Fatalf("err = %v, restores %q", err, f.repo.restores)
	}
}

// deferredRestore is a plugin whose restore is announced but not written
// yet (A6b Ф5: the files plugin).
type deferredRestore struct{ plugin }

func (*deferredRestore) RestoreNotImplemented() string {
	return "restore for the fake plugin is not implemented yet"
}

func TestRestoreOfAPluginThatCannotRestoreYetFailsWithoutTouchingTheDisk(t *testing.T) {
	f := newHandlers(t, &deferredRestore{})
	st := step(restore, `{}`)
	st.SnapshotId = "abc"
	res, err := f.run(t, st)
	if res != nil || err == nil || errors.Is(err, executor.ErrRejected) || err.Error() != "restore for the fake plugin is not implemented yet" {
		t.Fatalf("Run = %v, %v", res, err)
	}
	if len(f.repo.restores) != 0 || len(f.repo.requests) != 0 {
		t.Errorf("restic ran: %q", f.repo.restores)
	}
	if _, err := os.Stat(f.restoreDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("restore dir: %v", err)
	}
}

func TestRestoreOfAPluginThatCannotRestoreYetStillRejectsAMissingSnapshot(t *testing.T) {
	f := newHandlers(t, &deferredRestore{})
	if _, err := f.run(t, step(restore, `{}`)); !errors.Is(err, executor.ErrRejected) {
		t.Fatalf("err = %v", err)
	}
}

func TestInvalidRequestDoesNotNameTheRepository(t *testing.T) {
	f := newHandlers(t, &plugin{})
	f.repo.err = fmt.Errorf("%w: no paths", restic.ErrInvalidRequest)
	_, err := f.run(t, step(backup, `{}`))
	if !errors.Is(err, restic.ErrInvalidRequest) || strings.Contains(err.Error(), `repository "main"`) {
		t.Errorf("err = %v", err)
	}
}

// Restic saw the files, the repository is fine: the unreadable paths are the
// news, not the repository.
func TestPartialBackupDoesNotNameTheRepository(t *testing.T) {
	f := newHandlers(t, &plugin{})
	f.repo.err, f.repo.partial = &restic.PartialError{Items: []restic.ItemError{{Item: "/a"}}}, true
	_, err := f.run(t, step(backup, `{}`))
	if !errors.Is(err, restic.ErrUnreadableSource) || strings.Contains(err.Error(), `repository "main"`) {
		t.Errorf("err = %v", err)
	}
}

// A secret the plugin's config names but the host does not have is the
// step's fault, like an invalid config.
func TestAnUnknownSecretRejectsTheStep(t *testing.T) {
	f := newHandlers(t, &plugin{prepare: func(context.Context, sdk.Host, sdk.Config) error {
		return fmt.Errorf("token: %w", sdk.ErrUnknownSecret)
	}})
	if _, err := f.run(t, step(backup, `{}`)); !errors.Is(err, executor.ErrRejected) || !errors.Is(err, sdk.ErrUnknownSecret) {
		t.Errorf("err = %v", err)
	}
}

// The restore starts with a RESTORING report without counters.
func TestRestoreReportsTheRestoringPhaseBeforeRestic(t *testing.T) {
	f := newHandlers(t, &plugin{})
	st := step(restore, `{}`)
	st.SnapshotId = "snap"
	h, _ := f.handlers.Handler("fake")
	var rep reporter
	if _, err := h.Run(context.Background(), st, &rep); err != nil {
		t.Fatal(err)
	}
	want := []event{{phase: agentv1.StepPhase_STEP_PHASE_RESTORING}}
	if got := rep.all(); !slices.Equal(got, want) {
		t.Errorf("events = %+v, want %+v", got, want)
	}
}

// A dump that fails while streaming is the plugin's failure: the message does
// not blame the repository. A repository that fails while the plugin is still
// writing is the repository's.
func TestStreamFailureDoesNotNameTheRepository(t *testing.T) {
	broken := errors.New("pg_dump failed: connection lost")
	f := newHandlers(t, streamer(func(context.Context, sdk.Host, sdk.Config, sdk.Dump, io.Writer) error { return broken }))
	_, err := f.run(t, step(backup, `{}`))
	if !errors.Is(err, broken) || strings.Contains(err.Error(), `repository "main"`) {
		t.Errorf("err = %v", err)
	}
}

func TestRepositoryFailureDuringAStreamNamesTheRepository(t *testing.T) {
	f := newHandlers(t, streamer(func(_ context.Context, _ sdk.Host, _ sdk.Config, _ sdk.Dump, w io.Writer) error {
		_, err := w.Write([]byte("x"))
		return err
	}))
	f.repo.err = restic.ErrLocked
	_, err := f.run(t, step(backup, `{}`))
	if !errors.Is(err, restic.ErrLocked) || !strings.Contains(err.Error(), `repository "main": `) {
		t.Errorf("err = %v", err)
	}
}

func streamer(stream func(context.Context, sdk.Host, sdk.Config, sdk.Dump, io.Writer) error) *plugin {
	return &plugin{
		dump: func(context.Context, sdk.Host, sdk.Config) (sdk.Dump, error) {
			return sdk.Dump{Filename: "db.dump"}, nil
		},
		stream: stream,
	}
}
