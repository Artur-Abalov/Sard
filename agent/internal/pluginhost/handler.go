// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// Repositories returns the restic repository configured under name for the
// step commandID: its stderr copied to the step's output (A7c), its runs of
// restic logged with the command_id.
type Repositories func(name, commandID string, stderr io.Writer) (restic.Repository, bool)

// Handlers adapts the plugins of a registry to the executor.
type Handlers struct {
	sources map[string]*Source
	repos   Repositories
	// restoreDir holds one new directory per verify command.
	restoreDir string
	// console restores for the console (R2); nil: a host without it.
	console *consolerestore.Engine
}

// Option configures Handlers.
type Option func(*Handlers)

// WithConsoleRestore lets plugins that implement sdk.Restorer be restored
// from the console, into the restore directory of the engine.
func WithConsoleRestore(e *consolerestore.Engine) Option {
	return func(h *Handlers) { h.console = e }
}

var _ executor.Registry = (*Handlers)(nil)

// NewHandlers compiles every plugin's schema against the host's secrets.
func NewHandlers(reg *sdk.Registry, secrets *Secrets, repos Repositories, restoreDir string, opts ...Option) (*Handlers, error) {
	h := &Handlers{sources: make(map[string]*Source), repos: repos, restoreDir: restoreDir}
	for _, opt := range opts {
		opt(h)
	}
	for _, name := range reg.Names() {
		p, _ := reg.Get(name)
		src, err := NewSource(p, secrets)
		if err != nil {
			return nil, err
		}
		h.sources[name] = src
	}
	return h, nil
}

// Handler implements executor.Registry.
func (h *Handlers) Handler(plugin string) (executor.Handler, bool) {
	src, ok := h.sources[plugin]
	if !ok {
		return nil, false
	}
	return &handler{Handlers: h, src: src}, true
}

// handler runs the steps of one plugin.
type handler struct {
	*Handlers
	src *Source
}

// Actions: every plugin backs up; restore needs sdk.Restorer, verify
// sdk.Verifier.
func (h *handler) Actions() []agentv1.Action {
	actions := []agentv1.Action{agentv1.Action_ACTION_BACKUP}
	if h.src.CanRestore() {
		actions = append(actions, agentv1.Action_ACTION_RESTORE)
	}
	if h.src.CanVerify() {
		actions = append(actions, agentv1.Action_ACTION_VERIFY)
	}
	return actions
}

// Run implements executor.Handler.
func (h *handler) Run(ctx context.Context, step *agentv1.RunStep, r executor.Reporter) (*agentv1.StepResult, error) {
	if !slices.Contains(h.Actions(), step.GetAction()) {
		return nil, rejected(fmt.Errorf("plugin %q does not support %s", h.src.Plugin().Name(), step.GetAction()))
	}
	repo, ok := h.repos(step.GetRepositoryName(), step.GetCommandId(), r.Output())
	if !ok {
		return nil, rejected(fmt.Errorf("unknown repository %q", step.GetRepositoryName()))
	}
	switch step.GetAction() {
	case agentv1.Action_ACTION_BACKUP:
		return h.backup(ctx, step, repo, r)
	case agentv1.Action_ACTION_RESTORE:
		return h.restore(ctx, step, repo, r)
	default:
		return h.verify(ctx, step, repo, r)
	}
}

func (h *handler) backup(ctx context.Context, step *agentv1.RunStep, repo restic.Repository, r executor.Reporter) (*agentv1.StepResult, error) {
	tags, err := resticTags(step.GetTags())
	if err != nil {
		return nil, rejected(err)
	}
	sum, err := h.src.Backup(ctx, sdk.Config(step.GetConfigJson()), repo, tags, r)
	err = nameRepository(err, step.GetRepositoryName())
	if sum.SnapshotID == "" {
		return nil, classify(err)
	}
	return &agentv1.StepResult{Output: &agentv1.StepResult_Backup{Backup: BackupOutput(sum)}}, classify(err)
}

// nameRepository prefixes a failure of the repository with its name. A
// snapshot written without some files says which paths, not which repository;
// an invalid request is the plugin's fault, not the repository's.
func nameRepository(err error, name string) error {
	var failure *repositoryError
	if !errors.As(err, &failure) || errors.Is(err, restic.ErrUnreadableSource) || errors.Is(err, restic.ErrInvalidRequest) {
		return err
	}
	return fmt.Errorf("repository %q: %w", name, err)
}

// BackupOutput maps restic's summary to the protocol (OQ-018).
func BackupOutput(sum restic.BackupSummary) *agentv1.BackupOutput {
	return &agentv1.BackupOutput{
		SnapshotId:   sum.SnapshotID,
		TotalBytes:   sum.TotalBytes,
		AddedBytes:   sum.AddedBytes,
		RepositoryId: sum.RepositoryID,
	}
}

// resticTags turns step tags into sorted "key=value" restic tags.
func resticTags(tags map[string]string) ([]string, error) {
	out := make([]string, 0, len(tags))
	for k, v := range tags {
		if k == "" || strings.Contains(k+v, ",") {
			return nil, fmt.Errorf("tag %q=%q: restic tags need a key and no commas", k, v)
		}
		out = append(out, k+"="+v)
	}
	slices.Sort(out)
	return out, nil
}

// restore restores the snapshot for the console (R2): into a new directory
// of the restore directory of the host, all or nothing.
func (h *handler) restore(ctx context.Context, step *agentv1.RunStep, repo restic.Repository, r executor.Reporter) (*agentv1.StepResult, error) {
	req, err := h.restoreRequest(step)
	if err != nil {
		return nil, rejected(err)
	}
	out, failure := h.console.Restore(ctx, repo, req, r)
	if failure != nil {
		return failedRestore(failure), failure
	}
	restored := &agentv1.RestoreOutput{Target: out.Target, FilesRestored: out.Files, BytesRestored: out.Bytes}
	return &agentv1.StepResult{Output: &agentv1.StepResult_Restore{Restore: restored}}, nil
}

// Interrupted words the failure of a restore the previous run of the agent
// did not finish (ВК14): the temporary directories it left were removed at
// start, so nothing was restored, except what is in left (ВК15). Steps of
// other actions keep the failure of the executor.
func Interrupted(left []consolerestore.Leftover) func(commandID string, action agentv1.Action) *executor.Interruption {
	byCommand := map[string]consolerestore.Leftover{}
	for _, l := range left {
		byCommand[l.Name] = l
	}
	return func(commandID string, action agentv1.Action) *executor.Interruption {
		if action != agentv1.Action_ACTION_RESTORE {
			return nil
		}
		l, ok := byCommand[commandID]
		if !ok {
			return &executor.Interruption{Message: executor.InterruptedMessage + "; nothing was restored"}
		}
		return &executor.Interruption{
			Message: fmt.Sprintf("%s (%s): %s; left behind: %s (%v); remove it by hand", refusal.CleanupFailed, consolerestore.StageRollback, executor.InterruptedMessage, l.Path, l.Err),
			Output:  &agentv1.RestoreOutput{Leftover: l.Path},
		}
	}
}

// failedRestore carries the output of a failed restore: only what the
// rollback could not remove.
func failedRestore(f *consolerestore.Failure) *agentv1.StepResult {
	if len(f.Leftover) == 0 {
		return nil
	}
	left := &agentv1.RestoreOutput{Leftover: strings.Join(f.Leftover, ", ")}
	return &agentv1.StepResult{Output: &agentv1.StepResult_Restore{Restore: left}}
}

// restoreRequest checks everything that can be checked before the repository
// or the disk is touched: the host, the command, the snapshot, the config.
func (h *handler) restoreRequest(step *agentv1.RunStep) (consolerestore.Request, error) {
	id := step.GetCommandId()
	source := step.GetTags()[consolerestore.SourceTag]
	switch {
	case h.console == nil:
		return consolerestore.Request{}, errors.New("restore from the console is not enabled on this host")
	case !usableName(id):
		return consolerestore.Request{}, fmt.Errorf("command id %q cannot name a temporary directory", id)
	case !isSnapshotID(step.GetSnapshotId()):
		return consolerestore.Request{}, fmt.Errorf("snapshot_id %q: want 64 lower-case hexadecimal characters, the full id of the snapshot", step.GetSnapshotId())
	case source == "":
		return consolerestore.Request{}, fmt.Errorf("tag %s is required: it names the source the snapshot belongs to", consolerestore.SourceTag)
	}
	plan, err := h.src.PlanRestore(sdk.Config(step.GetConfigJson()))
	if err != nil {
		return consolerestore.Request{}, err
	}
	return consolerestore.Request{CommandID: id, Repository: step.GetRepositoryName(), SnapshotID: step.GetSnapshotId(), Source: source, Plan: plan}, nil
}

// isSnapshotID: 64 lower-case hexadecimal characters.
func isSnapshotID(id string) bool {
	return len(id) == 64 && !strings.ContainsFunc(id, func(r rune) bool { return (r < '0' || r > '9') && (r < 'a' || r > 'f') })
}

func usableName(id string) bool {
	return id != "" && id != "." && id != ".." && filepath.Base(id) == id
}

// verify restores the snapshot, lets the plugin check the copy and
// removes it. A failed check fails the step with the check in the output.
func (h *handler) verify(ctx context.Context, step *agentv1.RunStep, repo restic.Repository, r executor.Reporter) (*agentv1.StepResult, error) {
	cfg := sdk.Config(step.GetConfigJson())
	if err := h.src.schema.Validate(cfg); err != nil {
		return nil, rejected(err)
	}
	target, err := h.restoreSnapshot(ctx, step, repo, r)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(target) }()
	err = h.src.Verify(ctx, cfg, target, r)
	check := &agentv1.CheckResult{Name: h.src.Plugin().Name(), Passed: err == nil}
	if err != nil {
		check.Detail = err.Error()
	}
	out := &agentv1.VerifyOutput{SnapshotId: step.GetSnapshotId(), Checks: []*agentv1.CheckResult{check}}
	return &agentv1.StepResult{Output: &agentv1.StepResult_Verify{Verify: out}}, err
}

// restoreSnapshot restores the step's snapshot into a new directory named
// after the command (RESTORING).
func (h *handler) restoreSnapshot(ctx context.Context, step *agentv1.RunStep, repo restic.Repository, r executor.Reporter) (string, error) {
	if step.GetSnapshotId() == "" {
		return "", rejected(errors.New("snapshot_id is required: the latest snapshot by tags is not supported yet"))
	}
	target, err := h.newTarget(step.GetCommandId())
	if err != nil {
		return "", err
	}
	r.Progress(agentv1.StepPhase_STEP_PHASE_RESTORING, 0, 0)
	if err := repo.Restore(ctx, step.GetSnapshotId(), target); err != nil {
		return "", err
	}
	return target, nil
}

// newTarget creates restoreDir/id, 0700; an existing one is never reused.
func (h *handler) newTarget(id string) (string, error) {
	if !usableName(id) {
		return "", rejected(fmt.Errorf("command id %q cannot name a restore directory", id))
	}
	if err := os.MkdirAll(h.restoreDir, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(h.restoreDir, id)
	return target, os.Mkdir(target, 0o700)
}

// classify rejects the step for an invalid config or unknown secret.
func classify(err error) error {
	if errors.Is(err, sdk.ErrInvalidConfig) || errors.Is(err, sdk.ErrUnknownSecret) {
		return rejected(err)
	}
	return err
}

// OutputLevel is the log level of a line of restic's stderr: WARN for the
// errors restic reports, INFO for the rest.
func OutputLevel(line string) agentv1.LogLevel {
	if restic.ErrorLine(line) {
		return agentv1.LogLevel_LOG_LEVEL_WARN
	}
	return agentv1.LogLevel_LOG_LEVEL_INFO
}

func rejected(err error) error { return fmt.Errorf("%w: %w", executor.ErrRejected, err) }
