// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// Reporter receives a step's phases, progress and log lines; the
// executor's Reporter implements it.
type Reporter interface {
	Progress(phase agentv1.StepPhase, bytesProcessed, bytesTotal uint64)
	ProgressFiles(phase agentv1.StepPhase, bytesProcessed, bytesTotal, filesProcessed, filesTotal uint64)
	Log(level agentv1.LogLevel, text string)
	// Secret is the content of a secret as the step started with it.
	Secret(name string) ([]byte, bool)
}

// Source is a plugin ready to run on this host: its schema compiled
// against the host's secrets.
type Source struct {
	plugin  sdk.Plugin
	schema  *Schema
	secrets *Secrets
	// restorer and restoreSchema are set for a plugin the console can restore.
	restorer      sdk.Restorer
	restoreSchema *Schema
	// runner is set for a plugin that restores itself (R2c); recoverer for one
	// that recovers its journal entries at start.
	runner    sdk.RestoreRunner
	recoverer sdk.Recoverer
}

// NewSource compiles the plugin's ConfigSchema; a schema that does not
// compile is an error.
func NewSource(p sdk.Plugin, secrets *Secrets) (*Source, error) {
	schema, err := CompileSchema(p.Name(), p.ConfigSchema(), secrets.Has)
	if err != nil {
		return nil, err
	}
	src := &Source{plugin: p, schema: schema, secrets: secrets}
	if r, ok := p.(sdk.Restorer); ok {
		src.restorer = r
		src.runner, _ = p.(sdk.RestoreRunner)
		if src.restoreSchema, err = CompileSchema(p.Name()+"/restore", r.RestoreConfigSchema(), secrets.Has); err != nil {
			return nil, err
		}
	}
	src.recoverer, _ = p.(sdk.Recoverer)
	return src, nil
}

// CanRestore reports whether the plugin implements sdk.Restorer.
func (s *Source) CanRestore() bool { return s.restorer != nil }

// PlanRestore validates the restore config against the plugin's restore
// schema and gives the plan the plugin makes of it, checked against the rules
// of a plan. Any violation is a *sdk.ConfigError. The plan of a plugin that
// restores itself is not used, so its rules are not applied.
func (s *Source) PlanRestore(cfg sdk.Config) (sdk.RestorePlan, error) {
	if err := s.restoreSchema.Validate(cfg); err != nil {
		return sdk.RestorePlan{}, err
	}
	plan, err := s.restorer.PlanRestore(cfg)
	if err != nil {
		return sdk.RestorePlan{}, err
	}
	if s.runner != nil {
		return plan, nil
	}
	return plan, plan.Validate()
}

// Plugin returns the plugin.
func (s *Source) Plugin() sdk.Plugin { return s.plugin }

// Backup stores a snapshot of the source in repo, tagged with tags:
// the config is validated, then Prepare (PREPARING) and Dump (DUMPING)
// run, then restic stores the paths or the stream (UPLOADING), then the extra
// files and the fingerprint (see withFingerprint). An invalid
// config or unknown secret is returned before anything runs, as a
// *sdk.ConfigError.
func (s *Source) Backup(ctx context.Context, cfg sdk.Config, repo restic.Repository, tags []string, r Reporter) (Backup, error) {
	if err := s.schema.Validate(cfg); err != nil {
		return Backup{}, err
	}
	h := &host{secrets: s.secrets, r: r}
	d, err := s.prepareAndDump(ctx, h, cfg)
	if err != nil {
		return Backup{}, err
	}
	release := releaseOnce(d)
	defer release()
	merged, err := s.withPluginTags(tags, d.Tags)
	if err != nil {
		return Backup{}, err
	}
	early, merged, err := s.withFingerprintTag(merged, d)
	if err != nil {
		return Backup{}, err
	}
	h.enter(agentv1.StepPhase_STEP_PHASE_UPLOADING)
	sum, err := s.store(ctx, h, cfg, repo, d, merged, tags, r, release)
	if err != nil {
		return Backup{BackupSummary: sum}, err
	}
	return s.withFingerprint(ctx, h, cfg, repo, tags, d, Backup{BackupSummary: sum}, early)
}

// store makes the main snapshot with the tags merged, releases what the dump
// held for it, then makes the extra snapshots with the tags of the step.
func (s *Source) store(ctx context.Context, h *host, cfg sdk.Config, repo restic.Repository, d sdk.Dump, merged, step []string, r Reporter, release func()) (restic.BackupSummary, error) {
	sum, err := s.upload(ctx, h, cfg, repo, d, merged, r)
	release()
	if err != nil {
		return sum, err
	}
	return s.backupExtra(ctx, repo, step, sum, d.Extra)
}

// releaseOnce is d.Release, called at most once; nothing without one.
func releaseOnce(d sdk.Dump) func() {
	if d.Release == nil {
		return func() {}
	}
	return sync.OnceFunc(d.Release)
}

// upload stores the dump itself: the paths or the stream. A failure of the
// stream is the plugin's, any other the repository's.
func (s *Source) upload(ctx context.Context, h *host, cfg sdk.Config, repo restic.Repository, d sdk.Dump, tags []string, r Reporter) (restic.BackupSummary, error) {
	var streamErr error
	sum, err := repo.Backup(ctx, s.request(h, cfg, d, tags, &streamErr), func(p restic.Progress) {
		r.ProgressFiles(agentv1.StepPhase_STEP_PHASE_UPLOADING, p.BytesDone, p.TotalBytes, p.FilesDone, p.TotalFiles)
	})
	switch {
	case err == nil:
		return sum, nil
	case errors.Is(err, streamErr): // no streamErr is nil, which no err is
		return sum, err // the plugin's failure, not the repository's
	}
	return sum, &repositoryError{err}
}

// prepareAndDump runs Prepare (PREPARING) and Dump (DUMPING).
func (s *Source) prepareAndDump(ctx context.Context, h *host, cfg sdk.Config) (sdk.Dump, error) {
	h.enter(agentv1.StepPhase_STEP_PHASE_PREPARING)
	if err := s.plugin.Prepare(ctx, h, cfg); err != nil {
		return sdk.Dump{}, fmt.Errorf("prepare: %w", err)
	}
	h.enter(agentv1.StepPhase_STEP_PHASE_DUMPING)
	d, err := s.plugin.Dump(ctx, h, cfg)
	if err != nil {
		return sdk.Dump{}, fmt.Errorf("dump: %w", err)
	}
	return d, nil
}

// backupExtra stores the extra files of a dump, each as a snapshot of its own
// linked to the main snapshot main. The result is the main summary with the
// bytes of the extra snapshots added; a failure keeps the main summary.
func (s *Source) backupExtra(ctx context.Context, repo restic.Repository, step []string, main restic.BackupSummary, extra []sdk.ExtraFile) (restic.BackupSummary, error) {
	sum := main
	for _, f := range extra {
		tags, err := s.withPluginTags(step, f.Tags)
		if err != nil {
			return main, err
		}
		tags = append(tags, s.plugin.Name()+".main_snapshot="+main.SnapshotID)
		got, err := repo.Backup(ctx, restic.BackupRequest{Tags: tags, StdinFilename: f.Name, Stdin: func(_ context.Context, w io.Writer) error {
			_, err := w.Write(f.Content)
			return err
		}}, nil)
		if err != nil {
			return main, &repositoryError{fmt.Errorf("snapshot of %q: %w", f.Name, err)}
		}
		sum.TotalBytes += got.TotalBytes
		sum.AddedBytes += got.AddedBytes
		sum.AddedBytesRaw += got.AddedBytesRaw
	}
	return sum, nil
}

// withPluginTags returns the tags of the step followed by the sorted tags of
// a plugin, "<plugin>.<key>=<value>". A plugin tag the step already has is an
// error.
func (s *Source) withPluginTags(step []string, tags map[string]string) ([]string, error) {
	out := slices.Clone(step)
	added := make([]string, 0, len(tags))
	for k, v := range tags {
		key := s.plugin.Name() + "." + k
		if slices.ContainsFunc(step, func(t string) bool { return strings.HasPrefix(t, key+"=") }) {
			return nil, fmt.Errorf("tag %q of the plugin is a tag of the step", key)
		}
		added = append(added, key+"="+v)
	}
	slices.Sort(added)
	return append(out, added...), nil
}

// repositoryError marks a failure of restic or of the repository, as
// opposed to one of the plugin, so that the handler can name the repository.
type repositoryError struct{ err error }

func (e *repositoryError) Error() string { return e.err.Error() }
func (e *repositoryError) Unwrap() error { return e.err }

// request turns a dump into a restic request.
// A failure of the stream is also stored in *streamErr.
func (s *Source) request(h sdk.Host, cfg sdk.Config, d sdk.Dump, tags []string, streamErr *error) restic.BackupRequest {
	req := restic.BackupRequest{Paths: d.Paths, Excludes: d.Excludes, Tags: tags, OneFileSystem: d.OneFileSystem}
	if !d.Streamed() {
		return req
	}
	// Paths, excludes and one-file-system stay in the request so that
	// restic's validation rejects a dump that mixes them with a stream.
	req.StdinFilename = d.Filename
	req.Stdin = func(ctx context.Context, w io.Writer) error {
		err := s.plugin.Stream(ctx, h, cfg, d, w)
		*streamErr = err
		return err
	}
	return req
}

// host is the sdk.Host of one step; progress goes to the current phase.
type host struct {
	secrets *Secrets
	r       Reporter
	phase   atomic.Int32
}

func (h *host) enter(phase agentv1.StepPhase) {
	h.phase.Store(int32(phase))
	h.r.Progress(phase, 0, 0)
}

// Secret gives the value the step started with, the one the log masker knows;
// a name the step did not start with is read from its file.
func (h *host) Secret(name string) ([]byte, error) {
	if v, ok := h.r.Secret(name); ok {
		return v, nil
	}
	return h.secrets.Secret(name)
}

func (h *host) Progress(done, total uint64) {
	h.r.Progress(agentv1.StepPhase(h.phase.Load()), done, total)
}

// levels maps SDK log levels to the protocol's; others are INFO.
var levels = map[sdk.Level]agentv1.LogLevel{
	sdk.LevelDebug: agentv1.LogLevel_LOG_LEVEL_DEBUG,
	sdk.LevelInfo:  agentv1.LogLevel_LOG_LEVEL_INFO,
	sdk.LevelWarn:  agentv1.LogLevel_LOG_LEVEL_WARN,
	sdk.LevelError: agentv1.LogLevel_LOG_LEVEL_ERROR,
}

func (h *host) Log(level sdk.Level, text string) {
	l, ok := levels[level]
	if !ok {
		l = agentv1.LogLevel_LOG_LEVEL_INFO
	}
	h.r.Log(l, text)
}
