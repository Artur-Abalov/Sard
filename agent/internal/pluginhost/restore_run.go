// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// stageRestore is the stage of a failure whose plugin named none that fits.
const stageRestore consolerestore.Stage = "restore"

// restoreByPlugin restores the snapshot by the plugin itself (sdk.RestoreRunner,
// R2c): the host makes no directory, it gives the plugin the capabilities of
// sdk.RestoreHost and words what the plugin returns.
func (h *handler) restoreByPlugin(ctx context.Context, step *agentv1.RunStep, repo restic.Repository, r executor.Reporter) (*agentv1.StepResult, error) {
	if h.journal == nil {
		return nil, rejected(errors.New("the restore journal is not enabled on this host"))
	}
	host := h.newRestoreHost(ctx, step, repo, r)
	// A plugin that goes on after its step ended cannot write the entry again.
	defer host.journal.close()
	req := sdk.RestoreRequest{
		CommandID: step.GetCommandId(), SnapshotID: step.GetSnapshotId(),
		Repository: step.GetRepositoryName(), Source: step.GetTags()[consolerestore.SourceTag],
	}
	res, err := h.src.runner.Restore(ctx, host, sdk.Config(step.GetConfigJson()), req)
	if err != nil {
		failure := h.pluginFailure(err)
		if failure.retain {
			h.journal.keep(step.GetCommandId())
		}
		h.logLeftover(step.GetCommandId(), failure.Failure)
		return failedRestore(failure.Failure), failure
	}
	return &agentv1.StepResult{Output: &agentv1.StepResult_Restore{Restore: restoreOutput(res)}}, nil
}

// restoreOutput is the output of a restore the plugin did itself: a database
// is its own target.
func restoreOutput(res sdk.RestoreResult) *agentv1.RestoreOutput {
	return &agentv1.RestoreOutput{
		Target: firstNonEmpty(res.Target, res.Database), Database: res.Database, FilesRestored: res.Files, BytesRestored: res.Bytes,
		TablesRestored: res.Tables, RolesRestored: res.Roles,
	}
}

// logLeftover tells the log of the agent what the rollback of a plugin could not remove.
func (h *handler) logLeftover(commandID string, f *consolerestore.Failure) {
	if len(f.Leftover) > 0 {
		h.log.Error("the rollback of a restore could not remove what it left; remove it by hand",
			"command_id", commandID, "plugin", h.src.plugin.Name(), "left", strings.Join(f.Leftover, ", "), "error", f.Cleanup)
	}
}

// pluginFailure is a restore the plugin refused, worded as the console words
// the failures of the restore it does itself. The cause stays reachable.
type pluginFailure struct {
	*consolerestore.Failure
	cause error
	// retain: the plugin asked to keep the journal entry of the command.
	retain bool
}

func (f *pluginFailure) Unwrap() []error { return []error{f.Failure, f.cause} }

// pluginFailure words the error a plugin returned from Restore. A reason or a
// stage that is no code never reaches the server: PLUGIN_FAILED takes its place.
func (h *handler) pluginFailure(err error) *pluginFailure {
	var re *sdk.RestoreError
	if !errors.As(err, &re) {
		return &pluginFailure{Failure: &consolerestore.Failure{Reason: refusal.PluginFailed, Stage: stageRestore, Detail: err.Error()}, cause: err}
	}
	f := &consolerestore.Failure{
		Reason: refusal.Reason(re.Reason), Stage: consolerestore.Stage(re.Stage), Detail: re.Detail,
		Leftover: slices.Clone(re.Leftover), Cleanup: re.Cleanup,
	}
	if re.Stage == "" {
		f.Stage = stageRestore
	}
	switch {
	case !sdk.ValidReason(re.Reason):
		f.Reason, f.Detail = refusal.PluginFailed, fmt.Sprintf("plugin %q refused the restore with a reason that is no reason code", h.src.plugin.Name())
	case !sdk.ValidStage(string(f.Stage)):
		f.Reason, f.Stage, f.Detail = refusal.PluginFailed, stageRestore, fmt.Sprintf("plugin %q refused the restore at a stage that is no stage name", h.src.plugin.Name())
	}
	return &pluginFailure{Failure: f, cause: err, retain: re.Retain}
}

// restoreHost is the sdk.RestoreHost of one step.
type restoreHost struct {
	*host
	ctx        context.Context // of the step
	words      *consolerestore.Classifier
	repo       restic.Repository
	repository string
	journal    *stepJournal
	// commandID names the directory of RestoreTree, newTarget makes it.
	commandID string
	newTarget func(id string) (string, error)
}

var _ sdk.RestoreHost = (*restoreHost)(nil)

func (h *handler) newRestoreHost(ctx context.Context, step *agentv1.RunStep, repo restic.Repository, r executor.Reporter) *restoreHost {
	return &restoreHost{
		host: &host{secrets: h.src.secrets, plugin: h.src.plugin.Name(), r: r}, ctx: ctx, words: h.words, repo: repo, repository: step.GetRepositoryName(),
		commandID: step.GetCommandId(), newTarget: h.newTarget,
		journal: &stepJournal{store: h.journal, plugin: h.src.plugin.Name(), commandID: step.GetCommandId()},
	}
}

var phases = map[sdk.Phase]agentv1.StepPhase{
	sdk.PhasePreparing: agentv1.StepPhase_STEP_PHASE_PREPARING,
	sdk.PhaseRestoring: agentv1.StepPhase_STEP_PHASE_RESTORING,
}

// Phase implements sdk.RestoreHost; a phase the host does not know is ignored.
func (h *restoreHost) Phase(p sdk.Phase) {
	if phase, ok := phases[p]; ok {
		h.enter(phase)
	}
}

// Journal implements sdk.RestoreHost.
func (h *restoreHost) Journal() sdk.Journal { return h.journal }

// Snapshots implements sdk.RestoreHost: the snapshots of the repository of the step.
func (h *restoreHost) Snapshots(ctx context.Context) ([]sdk.SnapshotInfo, error) {
	listed, err := h.repo.Snapshots(ctx, nil)
	if err != nil {
		return nil, h.repositoryError(h.words.Snapshots(h.ctx, h.repository, err), err)
	}
	snapshots := make([]sdk.SnapshotInfo, len(listed))
	for i, s := range listed {
		snapshots[i] = sdk.SnapshotInfo{ID: s.ID, Time: s.Time, Tags: slices.Clone(s.Tags), Paths: slices.Clone(s.Paths)}
	}
	return snapshots, nil
}

// Dump implements sdk.RestoreHost: restic dump of one file, its bytes straight
// to w. A receiver that fails stops restic and gets its own error back; a
// context of the plugin that ends while the step goes on is no failure of the
// step.
func (h *restoreHost) Dump(ctx context.Context, snapshotID, path string, w io.Writer) error {
	// A receiver that fails stops restic: it may not notice that its stdout is gone.
	dumpCtx, stop := context.WithCancel(ctx)
	defer stop()
	watched := &failureWriter{w: w, onFailure: stop}
	err := h.repo.Dump(dumpCtx, snapshotID, path, watched)
	switch {
	case err == nil:
		return nil
	case watched.failure() != nil:
		return fmt.Errorf("the receiver of the snapshot stream failed: %w", watched.failure())
	case ctx.Err() != nil && h.ctx.Err() == nil:
		return fmt.Errorf("the snapshot stream was stopped: %w", context.Cause(ctx))
	}
	return h.repositoryError(h.words.Dump(h.ctx, h.repository, err), err)
}

// RestoreTree implements sdk.RestoreHost: restic restore of the whole snapshot
// into restoreDir/<command id>, which must not exist yet; a failed restore
// removes it.
func (h *restoreHost) RestoreTree(ctx context.Context, snapshotID string) (string, error) {
	dir, err := h.newTarget(h.commandID)
	if err != nil {
		return "", &sdk.RestoreError{Reason: string(refusal.PluginFailed), Detail: fmt.Sprintf("cannot make the directory of the copy: %v", err), Err: err}
	}
	if err := h.repo.Restore(ctx, snapshotID, dir); err != nil {
		_ = os.RemoveAll(dir)
		return "", h.repositoryError(h.words.Dump(h.ctx, h.repository, err), err)
	}
	return dir, nil
}

// repositoryError hands the failure of the repository, worded by the host, to
// the plugin; the plugin sets the stage and returns it.
func (h *restoreHost) repositoryError(f *consolerestore.Failure, cause error) error {
	return &sdk.RestoreError{Reason: string(f.Reason), Detail: f.Detail, Err: cause}
}

// failureWriter remembers the failures of the writer and calls onFailure on the first.
type failureWriter struct {
	w         io.Writer
	onFailure func()

	mu  sync.Mutex
	err error
}

func (f *failureWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if err != nil {
		f.mu.Lock()
		f.err = errors.Join(f.err, err)
		f.mu.Unlock()
		f.onFailure()
	}
	return n, err
}

func (f *failureWriter) failure() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

// SecretFor implements sdk.Host: the value is remembered, so that the journal
// can refuse an entry that holds it.
func (h *restoreHost) SecretFor(name string, target sdk.Target) (sdk.Secret, error) {
	secret, err := h.host.SecretFor(name, target)
	if err == nil {
		h.journal.noteSecret(name, secret.Value)
	}
	return secret, err
}

// stepJournal is the sdk.Journal of one command. It refuses an entry that
// holds the value of a secret the plugin read in this step (ADR 0008: an entry
// names secrets, it never holds their values).
type stepJournal struct {
	store     *Journal
	plugin    string
	commandID string

	mu     sync.Mutex
	read   map[string][]byte
	closed bool
}

// close ends the journal: a plugin that goes on after the host stopped
// waiting for it cannot change the entry any more.
func (j *stepJournal) close() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.closed = true
}

func (j *stepJournal) noteSecret(name string, value []byte) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.read == nil {
		j.read = map[string][]byte{}
	}
	j.read[name] = slices.Clone(value)
}

// leaked names a secret of the step whose value the entry holds, "" if none.
func (j *stepJournal) leakedLocked(entry []byte) string {
	for _, name := range slices.Sorted(maps.Keys(j.read)) {
		if value := j.read[name]; len(value) > 0 && bytes.Contains(entry, value) {
			return name
		}
	}
	return ""
}

// Save implements sdk.Journal.
func (j *stepJournal) Save(entry []byte) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return errors.New("the journal is closed: the host does not wait for this plugin any more")
	}
	if name := j.leakedLocked(entry); name != "" {
		return fmt.Errorf("the journal entry holds the value of the secret %q: an entry names a secret, it never holds its value", name)
	}
	return j.store.save(j.plugin, j.commandID, entry)
}
