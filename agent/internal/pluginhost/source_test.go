// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// event is one Reporter call.
type event struct {
	phase             agentv1.StepPhase
	done, total       uint64
	files, filesTotal uint64
	level             agentv1.LogLevel
	text              string
}

type reporter struct {
	mu     sync.Mutex
	events []event
}

func (r *reporter) Progress(phase agentv1.StepPhase, done, total uint64) {
	r.ProgressFiles(phase, done, total, 0, 0)
}

func (r *reporter) ProgressFiles(phase agentv1.StepPhase, done, total, files, filesTotal uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event{phase: phase, done: done, total: total, files: files, filesTotal: filesTotal})
}

func (r *reporter) Log(level agentv1.LogLevel, text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event{level: level, text: text})
}

func (r *reporter) all() []event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

// plugin is a scripted sdk.Plugin; each step can be replaced.
type plugin struct {
	calls   []string
	prepare func(context.Context, sdk.Host, sdk.Config) error
	dump    func(context.Context, sdk.Host, sdk.Config) (sdk.Dump, error)
	stream  func(context.Context, sdk.Host, sdk.Config, sdk.Dump, io.Writer) error
}

const pluginSchema = `{"type": "object", "properties": {"token": {"type": "string", "format": "sard-secret"}}, "additionalProperties": false}`

func (*plugin) Name() string         { return "fake" }
func (*plugin) Version() string      { return "1.0.0" }
func (*plugin) ConfigSchema() []byte { return []byte(pluginSchema) }

func (p *plugin) Prepare(ctx context.Context, h sdk.Host, cfg sdk.Config) error {
	p.calls = append(p.calls, "prepare "+string(cfg))
	if p.prepare == nil {
		return nil
	}
	return p.prepare(ctx, h, cfg)
}

func (p *plugin) Dump(ctx context.Context, h sdk.Host, cfg sdk.Config) (sdk.Dump, error) {
	p.calls = append(p.calls, "dump "+string(cfg))
	if p.dump == nil {
		return sdk.Dump{Paths: []string{"/srv/data"}, Excludes: []string{"*.tmp"}}, nil
	}
	return p.dump(ctx, h, cfg)
}

func (p *plugin) Stream(ctx context.Context, h sdk.Host, cfg sdk.Config, d sdk.Dump, w io.Writer) error {
	p.calls = append(p.calls, "stream "+string(cfg)+" "+d.Filename)
	return p.stream(ctx, h, cfg, d, w)
}

// verifier is a plugin that can check a restored copy.
type verifier struct {
	plugin
	err error
}

func (v *verifier) Verify(_ context.Context, h sdk.Host, cfg sdk.Config, restoredPath string) error {
	v.calls = append(v.calls, "verify "+string(cfg)+" "+restoredPath)
	h.Progress(1, 2)
	return v.err
}

// repo is a fake restic repository; a streamed request is read to the end.
type repo struct {
	requests []restic.BackupRequest
	stdin    bytes.Buffer
	progress []restic.Progress
	err      error
	partial  bool // err comes with the summary, as for restic's exit 3

	restores   []string
	restoreErr error
}

var summary = restic.BackupSummary{SnapshotID: "snap", RepositoryID: "repo-id", TotalBytes: 42}

func (r *repo) ID(context.Context) (string, error)   { return "repo-id", nil }
func (r *repo) Init(context.Context) (string, error) { return "repo-id", nil }

func (r *repo) Restore(_ context.Context, snapshotID, target string) error {
	r.restores = append(r.restores, snapshotID+" "+target)
	return r.restoreErr
}

func (r *repo) Backup(ctx context.Context, req restic.BackupRequest, progress func(restic.Progress)) (restic.BackupSummary, error) {
	r.requests = append(r.requests, req)
	if req.Stdin != nil {
		if err := req.Stdin(ctx, &r.stdin); err != nil {
			return restic.BackupSummary{}, fmt.Errorf("restic backup: stream: %w", err)
		}
	}
	for _, p := range r.progress {
		progress(p)
	}
	if r.err != nil && !r.partial {
		return restic.BackupSummary{}, r.err
	}
	return summary, r.err
}

func newSource(t *testing.T, p sdk.Plugin) *pluginhost.Source {
	t.Helper()
	secrets := pluginhost.NewSecrets(map[string]string{"pg": "/etc/sard/pg"}, (&files{data: map[string]string{"/etc/sard/pg": "s3cret"}}).read)
	s, err := pluginhost.NewSource(p, secrets)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var (
	preparing = agentv1.StepPhase_STEP_PHASE_PREPARING
	dumping   = agentv1.StepPhase_STEP_PHASE_DUMPING
	uploading = agentv1.StepPhase_STEP_PHASE_UPLOADING
	verifying = agentv1.StepPhase_STEP_PHASE_VERIFYING
)

func TestBackupByPathsRunsPrepareDumpAndResticInPhases(t *testing.T) {
	p := &plugin{}
	r := &repo{progress: []restic.Progress{{BytesDone: 10, TotalBytes: 100}, {BytesDone: 100, TotalBytes: 100}}}
	var rep reporter
	sum, err := newSource(t, p).Backup(context.Background(), []byte(`{}`), r, []string{"run=7"}, &rep)
	if err != nil || sum != summary {
		t.Fatalf("Backup = %+v, %v", sum, err)
	}
	if want := []string{"prepare {}", "dump {}"}; !slices.Equal(p.calls, want) {
		t.Errorf("calls = %q, want %q", p.calls, want)
	}
	want := []event{
		{phase: preparing}, {phase: dumping}, {phase: uploading},
		{phase: uploading, done: 10, total: 100}, {phase: uploading, done: 100, total: 100},
	}
	if got := rep.all(); !slices.Equal(got, want) {
		t.Errorf("events = %+v\nwant %+v", got, want)
	}
}

// A6b Ф4: restic's file counters reach the reporter with the byte counters.
func TestFileCountersOfResticAreReportedWhileUploading(t *testing.T) {
	r := &repo{progress: []restic.Progress{{BytesDone: 100, TotalBytes: 1024, FilesDone: 1, TotalFiles: 9}, {BytesDone: 1024, TotalBytes: 1024, FilesDone: 9, TotalFiles: 9}}}
	var rep reporter
	if _, err := newSource(t, &plugin{}).Backup(context.Background(), []byte(`{}`), r, nil, &rep); err != nil {
		t.Fatal(err)
	}
	got := rep.all()
	want := []event{
		{phase: uploading, done: 100, total: 1024, files: 1, filesTotal: 9},
		{phase: uploading, done: 1024, total: 1024, files: 9, filesTotal: 9},
	}
	if !slices.Equal(got[len(got)-2:], want) {
		t.Errorf("events = %+v", got)
	}
}

func TestBackupByPathsAsksResticForThePathsOfTheDump(t *testing.T) {
	r := &repo{}
	if _, err := newSource(t, &plugin{}).Backup(context.Background(), []byte(`{}`), r, []string{"run=7"}, &reporter{}); err != nil {
		t.Fatal(err)
	}
	req := r.requests[0]
	want := restic.BackupRequest{Paths: []string{"/srv/data"}, Excludes: []string{"*.tmp"}, Tags: []string{"run=7"}}
	if !slices.Equal(req.Paths, want.Paths) || !slices.Equal(req.Excludes, want.Excludes) || !slices.Equal(req.Tags, want.Tags) {
		t.Errorf("request = %+v, want %+v", req, want)
	}
	if req.Stdin != nil || req.StdinFilename != "" {
		t.Errorf("a dump by paths is streamed: %+v", req)
	}
}

func TestOneFileSystemOfTheDumpReachesRestic(t *testing.T) {
	p := &plugin{dump: func(context.Context, sdk.Host, sdk.Config) (sdk.Dump, error) {
		return sdk.Dump{Paths: []string{"/srv"}, OneFileSystem: true}, nil
	}}
	r := &repo{}
	if _, err := newSource(t, p).Backup(context.Background(), []byte(`{}`), r, nil, &reporter{}); err != nil {
		t.Fatal(err)
	}
	if !r.requests[0].OneFileSystem {
		t.Errorf("request = %+v", r.requests[0])
	}
}

func TestStreamedDumpGoesToResticStdin(t *testing.T) {
	p := &plugin{
		dump: func(context.Context, sdk.Host, sdk.Config) (sdk.Dump, error) {
			return sdk.Dump{Filename: "db.sql"}, nil
		},
		stream: func(_ context.Context, h sdk.Host, _ sdk.Config, _ sdk.Dump, w io.Writer) error {
			h.Progress(6, 0)
			_, err := io.WriteString(w, "CREATE")
			return err
		},
	}
	r := &repo{}
	var rep reporter
	if _, err := newSource(t, p).Backup(context.Background(), []byte(`{}`), r, nil, &rep); err != nil {
		t.Fatal(err)
	}
	if req := r.requests[0]; req.StdinFilename != "db.sql" || len(req.Paths) != 0 || req.Stdin == nil {
		t.Errorf("request = %+v", req)
	}
	if r.stdin.String() != "CREATE" || !slices.Equal(p.calls, []string{"prepare {}", "dump {}", "stream {} db.sql"}) {
		t.Errorf("stdin = %q, calls = %q", r.stdin.String(), p.calls)
	}
	if got := rep.all(); !slices.Contains(got, event{phase: uploading, done: 6}) {
		t.Errorf("the stream's progress is not reported while uploading: %+v", got)
	}
}

func TestStreamFailureIsTheBackupError(t *testing.T) {
	broken := errors.New("pg_dump exited with 1")
	p := &plugin{
		dump: func(context.Context, sdk.Host, sdk.Config) (sdk.Dump, error) {
			return sdk.Dump{Filename: "db.sql"}, nil
		},
		stream: func(context.Context, sdk.Host, sdk.Config, sdk.Dump, io.Writer) error { return broken },
	}
	if _, err := newSource(t, p).Backup(context.Background(), []byte(`{}`), &repo{}, nil, &reporter{}); !errors.Is(err, broken) {
		t.Fatalf("err = %v", err)
	}
}

// An invalid config or an unknown secret stops the step before the plugin
// or restic run, and before any phase is reported.
func TestInvalidConfigIsRejectedBeforeAnythingRuns(t *testing.T) {
	for cfg, want := range map[string]error{
		`{"x": 1}`:          sdk.ErrInvalidConfig,
		`{"token": "none"}`: sdk.ErrUnknownSecret,
		`not json`:          sdk.ErrInvalidConfig,
	} {
		p, r, rep := &plugin{}, &repo{}, &reporter{}
		_, err := newSource(t, p).Backup(context.Background(), []byte(cfg), r, nil, rep)
		if !errors.Is(err, want) {
			t.Errorf("%s: err = %v, want %v", cfg, err, want)
		}
		if len(p.calls) != 0 || len(r.requests) != 0 || len(rep.all()) != 0 {
			t.Errorf("%s: calls %q, requests %d, events %+v", cfg, p.calls, len(r.requests), rep.all())
		}
	}
}

func TestPrepareAndDumpFailuresStopTheBackup(t *testing.T) {
	broken := errors.New("broken")
	for name, p := range map[string]*plugin{
		"prepare: broken": {prepare: func(context.Context, sdk.Host, sdk.Config) error { return broken }},
		"dump: broken": {dump: func(context.Context, sdk.Host, sdk.Config) (sdk.Dump, error) {
			return sdk.Dump{}, broken
		}},
	} {
		r := &repo{}
		_, err := newSource(t, p).Backup(context.Background(), []byte(`{}`), r, nil, &reporter{})
		if !errors.Is(err, broken) || err.Error() != name || len(r.requests) != 0 {
			t.Errorf("%s: err = %v, requests %d", name, err, len(r.requests))
		}
	}
}

func TestResticFailureIsReturned(t *testing.T) {
	r := &repo{err: restic.ErrLocked}
	if _, err := newSource(t, &plugin{}).Backup(context.Background(), []byte(`{}`), r, nil, &reporter{}); !errors.Is(err, restic.ErrLocked) {
		t.Fatalf("err = %v", err)
	}
}

func TestHostGivesSecretsByNameAndForwardsLogs(t *testing.T) {
	var value []byte
	var unknown error
	p := &plugin{prepare: func(_ context.Context, h sdk.Host, _ sdk.Config) error {
		value, _ = h.Secret("pg")
		_, unknown = h.Secret("mysql")
		for _, l := range []sdk.Level{sdk.LevelDebug, sdk.LevelInfo, sdk.LevelWarn, sdk.LevelError, 0} {
			h.Log(l, fmt.Sprint("level ", int(l)))
		}
		return nil
	}}
	var rep reporter
	if _, err := newSource(t, p).Backup(context.Background(), []byte(`{"token": "pg"}`), &repo{}, nil, &rep); err != nil {
		t.Fatal(err)
	}
	if string(value) != "s3cret" || !errors.Is(unknown, sdk.ErrUnknownSecret) {
		t.Errorf("Secret: %q, %v", value, unknown)
	}
	var logs []event
	for _, e := range rep.all() {
		if e.text != "" {
			logs = append(logs, e)
		}
	}
	want := []event{
		{level: agentv1.LogLevel_LOG_LEVEL_DEBUG, text: "level 1"},
		{level: agentv1.LogLevel_LOG_LEVEL_INFO, text: "level 2"},
		{level: agentv1.LogLevel_LOG_LEVEL_WARN, text: "level 3"},
		{level: agentv1.LogLevel_LOG_LEVEL_ERROR, text: "level 4"},
		{level: agentv1.LogLevel_LOG_LEVEL_INFO, text: "level 0"},
	}
	if !slices.Equal(logs, want) {
		t.Errorf("logs = %+v", logs)
	}
}

func TestVerifyRunsTheVerifierInTheVerifyingPhase(t *testing.T) {
	v := &verifier{}
	var rep reporter
	if err := newSource(t, v).Verify(context.Background(), []byte(`{}`), "/restore", &rep); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(v.calls, []string{"verify {} /restore"}) {
		t.Errorf("calls = %q", v.calls)
	}
	if want := []event{{phase: verifying}, {phase: verifying, done: 1, total: 2}}; !slices.Equal(rep.all(), want) {
		t.Errorf("events = %+v", rep.all())
	}
	v.err = errors.New("checksum mismatch")
	if err := newSource(t, v).Verify(context.Background(), []byte(`{}`), "/restore", &rep); !errors.Is(err, v.err) {
		t.Errorf("err = %v", err)
	}
	if err := newSource(t, v).Verify(context.Background(), []byte(`{"x":1}`), "/restore", &rep); !errors.Is(err, sdk.ErrInvalidConfig) {
		t.Errorf("err = %v", err)
	}
}

func TestVerifyOfAPluginThatCannotVerify(t *testing.T) {
	s := newSource(t, &plugin{})
	if s.CanVerify() {
		t.Error("CanVerify of a plugin without Verify")
	}
	if !newSource(t, &verifier{}).CanVerify() {
		t.Error("CanVerify of a verifier")
	}
	if err := s.Verify(context.Background(), []byte(`{}`), "/restore", &reporter{}); !errors.Is(err, sdk.ErrNotImplemented) {
		t.Fatalf("err = %v", err)
	}
}

func TestSourceNeedsAValidSchema(t *testing.T) {
	bad := &badSchema{}
	if _, err := pluginhost.NewSource(bad, pluginhost.NewSecrets(nil, nil)); err == nil {
		t.Fatal("NewSource accepted an invalid schema")
	}
}

type badSchema struct{ plugin }

func (*badSchema) ConfigSchema() []byte { return []byte(`{"type": 5}`) }
