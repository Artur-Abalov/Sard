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
}

// NewSource compiles the plugin's ConfigSchema; a schema that does not
// compile is an error.
func NewSource(p sdk.Plugin, secrets *Secrets) (*Source, error) {
	schema, err := CompileSchema(p.Name(), p.ConfigSchema(), secrets.Has)
	if err != nil {
		return nil, err
	}
	return &Source{plugin: p, schema: schema, secrets: secrets}, nil
}

// Plugin returns the plugin.
func (s *Source) Plugin() sdk.Plugin { return s.plugin }

// CanVerify reports whether the plugin implements sdk.Verifier.
func (s *Source) CanVerify() bool {
	_, ok := s.plugin.(sdk.Verifier)
	return ok
}

// Backup stores a snapshot of the source in repo, tagged with tags:
// the config is validated, then Prepare (PREPARING) and Dump (DUMPING)
// run, then restic stores the paths or the stream (UPLOADING). An invalid
// config or unknown secret is returned before anything runs, as a
// *sdk.ConfigError.
func (s *Source) Backup(ctx context.Context, cfg sdk.Config, repo restic.Repository, tags []string, r Reporter) (restic.BackupSummary, error) {
	if err := s.schema.Validate(cfg); err != nil {
		return restic.BackupSummary{}, err
	}
	h := &host{secrets: s.secrets, r: r}
	h.enter(agentv1.StepPhase_STEP_PHASE_PREPARING)
	if err := s.plugin.Prepare(ctx, h, cfg); err != nil {
		return restic.BackupSummary{}, fmt.Errorf("prepare: %w", err)
	}
	h.enter(agentv1.StepPhase_STEP_PHASE_DUMPING)
	d, err := s.plugin.Dump(ctx, h, cfg)
	if err != nil {
		return restic.BackupSummary{}, fmt.Errorf("dump: %w", err)
	}
	merged, err := s.withPluginTags(tags, d.Tags)
	if err != nil {
		return restic.BackupSummary{}, err
	}
	h.enter(agentv1.StepPhase_STEP_PHASE_UPLOADING)
	var streamErr error
	sum, err := repo.Backup(ctx, s.request(h, cfg, d, merged, &streamErr), func(p restic.Progress) {
		r.ProgressFiles(agentv1.StepPhase_STEP_PHASE_UPLOADING, p.BytesDone, p.TotalBytes, p.FilesDone, p.TotalFiles)
	})
	switch {
	case err != nil && streamErr != nil && errors.Is(err, streamErr):
		return sum, err // the plugin's failure, not the repository's
	case err != nil:
		return sum, &repositoryError{err}
	}
	return s.backupExtra(ctx, repo, tags, sum, d.Extra)
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

// Verify checks a restored copy at restoredPath (VERIFYING). A plugin that
// is not an sdk.Verifier returns sdk.ErrNotImplemented.
func (s *Source) Verify(ctx context.Context, cfg sdk.Config, restoredPath string, r Reporter) error {
	v, ok := s.plugin.(sdk.Verifier)
	if !ok {
		return fmt.Errorf("plugin %q: verify: %w", s.plugin.Name(), sdk.ErrNotImplemented)
	}
	if err := s.schema.Validate(cfg); err != nil {
		return err
	}
	h := &host{secrets: s.secrets, r: r}
	h.enter(agentv1.StepPhase_STEP_PHASE_VERIFYING)
	return v.Verify(ctx, h, cfg, restoredPath)
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
