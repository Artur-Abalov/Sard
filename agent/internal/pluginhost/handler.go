// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/verifier"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/proto"
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
	// journal keeps the entries of plugins that restore themselves (R2c); nil:
	// a host where none can.
	journal *Journal
	// words words the failures of restic that reach those plugins.
	words *consolerestore.Classifier
	// verifier runs the VERIFY steps of a verifier's host (V5); nil elsewhere.
	verifier *verifier.Verifier
	log      *slog.Logger
}

// Option configures Handlers.
type Option func(*Handlers)

// WithConsoleRestore lets plugins that implement sdk.Restorer be restored
// from the console, into the restore directory of the engine.
func WithConsoleRestore(e *consolerestore.Engine) Option {
	return func(h *Handlers) { h.console = e }
}

// WithRestoreJournal lets plugins that restore themselves (sdk.RestoreRunner)
// keep a journal entry of the command in the store.
func WithRestoreJournal(j *Journal) Option {
	return func(h *Handlers) { h.journal = j }
}

// WithRepositoryWords sets how the failures of restic are worded for the
// plugins that restore themselves; default: the repositories just by their names.
func WithRepositoryWords(c *consolerestore.Classifier) Option {
	return func(h *Handlers) { h.words = c }
}

// WithVerifier lets the plugins that restore and measure a copy run VERIFY
// steps with v (V5): only a verifier's host has one.
func WithVerifier(v *verifier.Verifier) Option {
	return func(h *Handlers) { h.verifier = v }
}

// WithLogger sets the log of the agent the handlers write to; default: none.
func WithLogger(l *slog.Logger) Option {
	return func(h *Handlers) { h.log = l }
}

var _ executor.Registry = (*Handlers)(nil)

// NewHandlers compiles every plugin's schema against the host's secrets.
func NewHandlers(reg *sdk.Registry, secrets *Secrets, repos Repositories, restoreDir string, opts ...Option) (*Handlers, error) {
	h := &Handlers{sources: make(map[string]*Source), repos: repos, restoreDir: restoreDir, log: slog.New(slog.DiscardHandler),
		words: consolerestore.NewClassifier(nil)}
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

// Actions: every plugin backs up; restore needs sdk.Restorer; verify needs
// the verifier of a verifier's host and a plugin it can verify (V5).
func (h *handler) Actions() []agentv1.Action {
	actions := []agentv1.Action{agentv1.Action_ACTION_BACKUP}
	if h.src.CanRestore() {
		actions = append(actions, agentv1.Action_ACTION_RESTORE)
	}
	if h.verifier != nil && verifier.Supports(h.src.plugin) {
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
	var result *agentv1.StepResult
	var err error
	switch step.GetAction() {
	case agentv1.Action_ACTION_BACKUP:
		result, err = h.backup(ctx, step, repo, r)
	case agentv1.Action_ACTION_VERIFY:
		result, err = h.verify(ctx, step, repo, r)
	default:
		result, err = h.restore(ctx, step, repo, r)
	}
	return result, withCheckHint(err)
}

// verify runs the restore check of a verifier over the repository of the step,
// which it reads without a lock (V5).
func (h *handler) verify(ctx context.Context, step *agentv1.RunStep, repo restic.Repository, r executor.Reporter) (*agentv1.StepResult, error) {
	store, ok := repo.(verifier.Store)
	if !ok {
		return nil, rejected(fmt.Errorf("repository %q cannot be read by a verifier", step.GetRepositoryName()))
	}
	return h.verifier.Run(ctx, step, h.src.plugin.(verifier.Plugin), store, r)
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

// BackupOutput maps the outcome of a backup to the protocol (OQ-018, V2).
func BackupOutput(b Backup) *agentv1.BackupOutput {
	return &agentv1.BackupOutput{
		SnapshotId:        b.SnapshotID,
		TotalBytes:        b.TotalBytes,
		AddedBytes:        b.AddedBytes,
		RepositoryId:      b.RepositoryID,
		Fingerprint:       b.Fingerprint,
		FingerprintSha256: b.FingerprintSHA256,
		TotalFiles:        b.TotalFiles,
		UnreadableCount:   proto.Uint64(uint64(b.UnreadableCount)),
		UnreadablePaths:   b.UnreadablePaths,
	}
}

// reservedStepTag: a key that would make the snapshot a part of another one.
func reservedStepTag(key string) bool { return strings.HasSuffix(key, "."+sdk.MainSnapshotTag) }

// resticTags turns step tags into sorted "key=value" restic tags.
func resticTags(tags map[string]string) ([]string, error) {
	out := make([]string, 0, len(tags))
	for k, v := range tags {
		if k == "" || strings.Contains(k+v, ",") {
			return nil, fmt.Errorf("tag %q=%q: restic tags need a key and no commas", k, v)
		}
		if reservedStepTag(k) {
			return nil, fmt.Errorf("tag %q: a key that ends in .%s is the agent's own", k, sdk.MainSnapshotTag)
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
	if h.src.runner != nil {
		return h.restoreByPlugin(ctx, step, repo, r)
	}
	out, failure := h.console.Restore(ctx, repo, req, r)
	if failure != nil {
		return failedRestore(failure), failure
	}
	restored := &agentv1.RestoreOutput{Target: out.Target, FilesRestored: out.Files, BytesRestored: out.Bytes}
	return &agentv1.StepResult{Output: &agentv1.StepResult_Restore{Restore: restored}}, nil
}

// Interrupted words the outcome of a restore of files the previous run of the
// agent did not finish (ВК14) from what the start found in .sard-staging: a
// completed move is a success with the totals of the marker, a directory that
// could not be removed is CLEANUP_FAILED, anything else (no root, no marker, a
// root that was removed, no directory at all) is a failure with nothing
// restored. Steps of other actions keep the failure of the executor.
func Interrupted(found []consolerestore.Evidence) func(commandID string, action agentv1.Action) *executor.Interruption {
	byCommand := map[string]consolerestore.Evidence{}
	for _, ev := range found {
		byCommand[ev.Name] = ev
	}
	return func(commandID string, action agentv1.Action) *executor.Interruption {
		if action != agentv1.Action_ACTION_RESTORE {
			return nil
		}
		ev := byCommand[commandID]
		switch ev.Verdict {
		case consolerestore.Completed:
			return &executor.Interruption{Succeeded: true, Message: "agent restarted after the restore completed", Output: completedOutput(ev)}
		case consolerestore.CleanupFailed:
			return &executor.Interruption{
				Message: fmt.Sprintf("%s (%s): %s; left behind: %s (%v); remove it by hand", refusal.CleanupFailed, consolerestore.StageRollback, executor.InterruptedMessage, ev.Path, ev.Err),
				Output:  &agentv1.RestoreOutput{Leftover: ev.Path},
			}
		}
		return &executor.Interruption{Message: executor.InterruptedMessage + "; nothing was restored"}
	}
}

// completedOutput is the outcome of a restore that completed before the
// restart: the totals of the marker.
func completedOutput(ev consolerestore.Evidence) *agentv1.RestoreOutput {
	return &agentv1.RestoreOutput{Target: ev.Destination, FilesRestored: ev.Files, BytesRestored: ev.Bytes}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
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
	if err := h.checkRestoreStep(step); err != nil {
		return consolerestore.Request{}, err
	}
	plan, err := h.src.PlanRestore(sdk.Config(step.GetConfigJson()))
	if err != nil {
		return consolerestore.Request{}, err
	}
	return consolerestore.Request{
		CommandID: step.GetCommandId(), Repository: step.GetRepositoryName(), SnapshotID: step.GetSnapshotId(),
		Source: step.GetTags()[consolerestore.SourceTag], Plan: plan,
	}, nil
}

// checkRestoreStep judges the step itself: every restore names the source of
// the snapshot, whoever does it.
func (h *handler) checkRestoreStep(step *agentv1.RunStep) error {
	if h.console == nil && h.src.runner == nil {
		return errors.New("restore from the console is not enabled on this host")
	}
	id := step.GetCommandId()
	switch {
	case !consolerestore.UsableName(id):
		return fmt.Errorf("command id %q cannot name a temporary directory", id)
	case !isSnapshotID(step.GetSnapshotId()):
		return fmt.Errorf("snapshot_id %q: want 64 lower-case hexadecimal characters, the full id of the snapshot", step.GetSnapshotId())
	}
	return h.checkSourceTag(step)
}

// checkSourceTag: only snapshots of the source the step names are restored.
func (h *handler) checkSourceTag(step *agentv1.RunStep) error {
	if step.GetTags()[consolerestore.SourceTag] == "" {
		return fmt.Errorf("tag %s is required: it names the source the snapshot belongs to", consolerestore.SourceTag)
	}
	return nil
}

// isSnapshotID: 64 lower-case hexadecimal characters.
func isSnapshotID(id string) bool {
	return len(id) == 64 && !strings.ContainsFunc(id, func(r rune) bool { return (r < '0' || r > '9') && (r < 'a' || r > 'f') })
}

// newTarget creates restoreDir/id, 0700; an existing one is never reused.
func (h *handler) newTarget(id string) (string, error) {
	if !consolerestore.UsableName(id) {
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
