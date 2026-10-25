// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package verifier_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/verifier"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	snapshotID = "6c719cbc6c719cbc6c719cbc6c719cbc6c719cbc6c719cbc6c719cbc6c719cbc"
	ownMachine = "1111111111111111111111111111111111111111111111111111111111111111"
	jobID      = "0d8f0b4e-6b0e-4f7a-9c55-3a1d2b7f9e01"
	workDir    = "/var/lib/sard-agent/verify"
)

var t0 = time.Date(2026, 10, 25, 4, 30, 0, 0, time.UTC)

// --- the clock: time moves only when a test or a fake moves it

type fakeTimer struct {
	at      time.Time
	f       func()
	stopped bool
}

func (t *fakeTimer) Stop() bool {
	was := !t.stopped
	t.stopped = true
	return was
}

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) executor.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{at: c.now.Add(d), f: f}
	c.timers = append(c.timers, t)
	return t
}

// Advance moves the time on and fires the timers that fall due.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var due []*fakeTimer
	for _, t := range c.timers {
		if !t.stopped && !t.at.After(c.now) {
			t.stopped = true
			due = append(due, t)
		}
	}
	c.mu.Unlock()
	for _, t := range due {
		t.f()
	}
}

// --- the repository

type checkReply struct {
	report restic.CheckReport
	err    error
	// snapshots replaces the listing after this check, as a prune would.
	snapshots []restic.Snapshot
}

type fakeStore struct {
	clock     *fakeClock
	snapshots []restic.Snapshot
	listErr   error
	checks    []checkReply
	percents  []int
	restored  []string
	// restoreTook is how long the restore takes; restoreErr how it ends.
	restoreTook time.Duration
	restoreErr  error
}

func (s *fakeStore) Snapshots(_ context.Context, tags []string) ([]restic.Snapshot, error) {
	if len(tags) != 0 {
		panic("the verifier lists every snapshot once and filters itself")
	}
	return s.snapshots, s.listErr
}

func (s *fakeStore) VerifyData(_ context.Context, percent int) (restic.CheckReport, error) {
	s.percents = append(s.percents, percent)
	if len(s.checks) == 0 {
		return restic.CheckReport{}, nil
	}
	reply := s.checks[0]
	s.checks = s.checks[1:]
	if reply.snapshots != nil {
		s.snapshots = reply.snapshots
	}
	return reply.report, reply.err
}

func (s *fakeStore) RestoreVerified(ctx context.Context, id, target string) error {
	s.restored = append(s.restored, id+" -> "+target)
	s.clock.Advance(s.restoreTook)
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	return s.restoreErr
}

func (s *fakeStore) Dump(context.Context, string, string, io.Writer) error {
	return errors.New("not used by files")
}

// --- the disk of the verifier

type fakeDisk struct {
	volume    verifier.Volume
	volumeErr error
	entries   []string
	made      []string
	removed   []string
	removeErr error
}

func (d *fakeDisk) Volume(string) (verifier.Volume, error) { return d.volume, d.volumeErr }

func (d *fakeDisk) MakeDir(path string) error {
	d.made = append(d.made, path)
	return nil
}

func (d *fakeDisk) RemoveAll(path string) error {
	d.removed = append(d.removed, path)
	return d.removeErr
}

func (d *fakeDisk) Entries(string) ([]string, error) { return d.entries, nil }

// --- a plugin like files: a tree, a fingerprint completed after the backup

type fakePlugin struct {
	name string
	// copied is what Fingerprint measures in the copy.
	copied    sdk.Fingerprint
	measure   error
	measured  []sdk.FingerprintTarget
	restoreTo string
	size      uint64
	sized     bool
}

func (p *fakePlugin) Name() string { return p.name }

func (p *fakePlugin) Fingerprint(_ context.Context, _ sdk.Host, _ sdk.Config, t sdk.FingerprintTarget) (sdk.Fingerprint, error) {
	p.measured = append(p.measured, t)
	return p.copied, p.measure
}

func (p *fakePlugin) RestoreInto(ctx context.Context, h sdk.RestoreHost, _ sdk.Config, req sdk.RestoreRequest) (sdk.RestoredCopy, error) {
	root, err := h.RestoreTree(ctx, req.SnapshotID)
	if err != nil {
		return sdk.RestoredCopy{}, err
	}
	p.restoreTo = root
	return sdk.RestoredCopy{Root: root, Result: sdk.RestoreResult{Target: root}}, nil
}

func (p *fakePlugin) CopySize(sdk.Fingerprint) (uint64, bool) { return p.size, p.sized }

// completing is a plugin whose fingerprint is a snapshot of its own (files).
type completing struct{ *fakePlugin }

func (completing) CompleteFingerprint(context.Context, sdk.Host, sdk.Config, sdk.Dump, sdk.BackupTotals) (sdk.Fingerprint, error) {
	return sdk.Fingerprint{}, nil
}

// --- the reporter of the step

type fakeReporter struct {
	mu     sync.Mutex
	stages []*agentv1.VerifyStage
	logs   []string
}

func (r *fakeReporter) Progress(agentv1.StepPhase, uint64, uint64)                      {}
func (r *fakeReporter) ProgressFiles(agentv1.StepPhase, uint64, uint64, uint64, uint64) {}
func (r *fakeReporter) Secret(string) ([]byte, bool)                                    { return nil, false }
func (r *fakeReporter) Output() io.Writer                                               { return io.Discard }

func (r *fakeReporter) Log(_ agentv1.LogLevel, text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, text)
}

func (r *fakeReporter) Stage(s *agentv1.VerifyStage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stages = append(r.stages, s)
}

// --- the fixture: a files source with a fingerprint, a verifier that may run it

func source() sdk.Fingerprint {
	return sdk.Fingerprint{Plugin: "files", Format: sdk.FingerprintFormat, Facts: []sdk.Fact{
		{Name: "bytes", Rule: sdk.RuleExact, Value: "2048"},
		{Name: "control:/srv/data", Rule: sdk.RuleExact, Value: "nonempty"},
		{Name: "files", Rule: sdk.RuleExact, Value: "3"},
		{Name: "stat:/srv/data/a", Rule: sdk.RuleInfo, Value: "1024"},
	}}
}

func encoded(t *testing.T, f sdk.Fingerprint) ([]byte, string) {
	t.Helper()
	b, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b)
	return b, hex.EncodeToString(h[:])
}

type rig struct {
	t        *testing.T
	clock    *fakeClock
	store    *fakeStore
	disk     *fakeDisk
	plugin   *fakePlugin
	reporter *fakeReporter
	step     *agentv1.RunStep
	opts     verifier.Options
}

func newRig(t *testing.T) *rig {
	t.Helper()
	fp, hash := encoded(t, source())
	clock := &fakeClock{now: t0}
	r := &rig{
		t:     t,
		clock: clock,
		store: &fakeStore{clock: clock, restoreTook: 90 * time.Second, snapshots: []restic.Snapshot{
			{ID: snapshotID, Tags: []string{"sard.source=s1"}},
			{ID: "f1" + snapshotID[2:], Tags: []string{"sard.source=s1", "files.main_snapshot=" + snapshotID, "files.part=fingerprint", "sard.fp=" + hash}},
		}},
		disk:     &fakeDisk{volume: verifier.Volume{Free: 1 << 40, NoExec: true}},
		plugin:   &fakePlugin{name: "files", copied: source(), size: 2048, sized: true},
		reporter: &fakeReporter{},
		step: &agentv1.RunStep{
			CommandId: "c1", Plugin: "files", Action: agentv1.Action_ACTION_VERIFY, RepositoryName: "shared",
			SnapshotId: snapshotID, ConfigJson: "{}", Tags: map[string]string{"sard.source": "s1"},
			Verify: &agentv1.VerifyTask{
				JobId: jobID, RepositoryId: "r1", Fingerprint: fp, FingerprintSha256: hash,
				Deadline:         timestamppb.New(t0.Add(24 * time.Hour)),
				SourceMachineIds: []string{"2222222222222222222222222222222222222222222222222222222222222222"},
			},
		},
	}
	r.opts = verifier.Options{
		WorkDir: workDir, MachineID: ownMachine, CheckPercent: 10, Clock: clock, Disk: r.disk,
		Limits: verifier.Limits{Prepare: 10 * time.Minute, Check: 2 * time.Hour, Restore: 4 * time.Hour, Fingerprint: time.Hour},
		Space:  verifier.Space{Factor: 2, Headroom: 1 << 30},
	}
	return r
}

func (r *rig) run() (*agentv1.StepResult, error) {
	r.t.Helper()
	return newVerifier(r).Run(context.Background(), r.step, completing{r.plugin}, r.store, r.reporter)
}

func newVerifier(r *rig) *verifier.Verifier { return verifier.New(r.opts) }

// stage is a stage as the tests compare it: level, name, status.
type stage struct {
	level  uint32
	name   string
	status agentv1.StageStatus
}

func stagesOf(v *agentv1.VerifyOutput) []stage {
	out := make([]stage, len(v.GetStages()))
	for i, s := range v.GetStages() {
		out[i] = stage{s.GetLevel(), s.GetName(), s.GetStatus()}
	}
	return out
}

func find(v *agentv1.VerifyOutput, name string) *agentv1.VerifyStage {
	for _, s := range v.GetStages() {
		if s.GetName() == name {
			return s
		}
	}
	return nil
}

const (
	passed        = agentv1.StageStatus_STAGE_STATUS_PASSED
	failed        = agentv1.StageStatus_STAGE_STATUS_FAILED
	cannotRun     = agentv1.StageStatus_STAGE_STATUS_CANNOT_RUN
	notApplicable = agentv1.StageStatus_STAGE_STATUS_NOT_APPLICABLE
)
