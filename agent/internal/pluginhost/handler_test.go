// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	// readOnly, if set, is the repository instead of repo (a verifier's, V5).
	readOnly restic.Repository
}

func newHandlers(t *testing.T, plugins ...sdk.Plugin) *handlersFixture {
	t.Helper()
	return newHandlersWith(t, nil, plugins...)
}

func newHandlersWith(t *testing.T, opts []pluginhost.Option, plugins ...sdk.Plugin) *handlersFixture {
	t.Helper()
	reg, err := sdk.NewRegistry(plugins...)
	if err != nil {
		t.Fatal(err)
	}
	f := &handlersFixture{repo: &repo{}, restoreDir: filepath.Join(t.TempDir(), "restore")}
	secrets := pluginhost.NewSecrets(map[string]string{"pg": "/etc/sard/pg"}, (&files{data: map[string]string{"/etc/sard/pg": "s3cret"}}).read, bound{plugin: "fake"})
	repos := func(name, commandID string, stderr io.Writer) (restic.Repository, bool) {
		f.stderr = append(f.stderr, stderr)
		f.commands = append(f.commands, commandID)
		if f.readOnly != nil {
			return f.readOnly, name == "main"
		}
		return f.repo, name == "main"
	}
	f.handlers, err = pluginhost.NewHandlers(reg, secrets, repos, f.restoreDir, opts...)
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
	if got := h.Actions(); !slices.Equal(got, []agentv1.Action{backup}) {
		t.Errorf("plugin: %v", got)
	}
	f = newHandlers(t, &restorer{})
	h, _ = f.handlers.Handler("fake")
	if got := h.Actions(); !slices.Equal(got, []agentv1.Action{backup, restore}) {
		t.Errorf("restorer: %v", got)
	}
}

func TestNewHandlersRejectsAPluginWhoseSchemaDoesNotCompile(t *testing.T) {
	reg, _ := sdk.NewRegistry(&badSchema{})
	if _, err := pluginhost.NewHandlers(reg, pluginhost.NewSecrets(nil, nil, nil), nil, ""); err == nil {
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
		if !errors.Is(err, cause) || res != nil || !strings.Contains(err.Error(), `repository "main": `) || strings.Contains(strings.TrimSuffix(err.Error(), "\n"+checkHint), "\n") {
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
		"verify":                 func() *agentv1.RunStep { s := step(verify, `{}`); s.SnapshotId = "snap"; return s }(),
		"restore without a snap": step(restore, `{}`),
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

// errStreamExit3 is what the restic wrapper returns for a --stdin backup that
// exits 3: a failure without a snapshot, not a partial one.
var errStreamExit3 = errors.New("restic backup: exit code 3 on a stream: the stream may not have been stored completely")

// A stream that restic ends with exit 3 is not "partial with a snapshot": the
// step fails without an output and the message names the repository.
func TestMainStreamExit3FailsWithoutOutputAndNamesTheRepository(t *testing.T) {
	f := newHandlers(t, withExtra())
	f.readOnly = &seqRepo{sums: make([]restic.BackupSummary, 1), errs: map[int]error{0: errStreamExit3}}
	res, err := f.run(t, step(backup, `{}`))
	if res != nil || !errors.Is(err, errStreamExit3) || errors.Is(err, restic.ErrUnreadableSource) || !strings.Contains(err.Error(), `repository "main": `) {
		t.Fatalf("Run = %v, %v", res, err)
	}
}

func TestExtraFileStreamExit3FailsKeepingTheMainSnapshotAndNamesTheRepository(t *testing.T) {
	f := newHandlers(t, withExtra(globals))
	f.readOnly = &seqRepo{
		sums: []restic.BackupSummary{{SnapshotID: "main", RepositoryID: "rid", TotalBytes: 100}, {}},
		errs: map[int]error{1: errStreamExit3},
	}
	res, err := f.run(t, step(backup, `{}`))
	if res.GetBackup().GetSnapshotId() != "main" || !errors.Is(err, errStreamExit3) || strings.Contains(err.Error(), "unreadable paths") || !strings.Contains(err.Error(), `repository "main": `) {
		t.Fatalf("Run = %v, %v", res, err)
	}
}

func TestFingerprintStreamExit3FailsKeepingTheMainSnapshotAndNamesTheRepository(t *testing.T) {
	f := newHandlers(t, newCompleter())
	f.readOnly = &seqRepo{
		sums: []restic.BackupSummary{{SnapshotID: "main", RepositoryID: "rid", TotalBytes: 100}, {}},
		errs: map[int]error{1: errStreamExit3},
	}
	res, err := f.run(t, step(backup, `{}`))
	if res.GetBackup().GetSnapshotId() != "main" || len(res.GetBackup().GetFingerprint()) != 0 || !errors.Is(err, errStreamExit3) || strings.Contains(err.Error(), "unreadable paths") || !strings.Contains(err.Error(), `repository "main": `) {
		t.Fatalf("Run = %v, %v", res, err)
	}
}
