// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package verifier_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/sandbox"
	"github.com/Artur-Abalov/sard/agent/internal/verifier"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

const digest = "sha256:1711000000000000000000000000000000000000000000000000000000000000"

// fakeBox is a sandbox: the programs run in it are recorded.
type fakeBox struct {
	ran       []sdk.Process
	removed   bool
	removeErr error
}

func (b *fakeBox) Image() sandbox.Image {
	return sandbox.Image{Name: "postgres", Version: "17.11", Digest: digest}
}

func (b *fakeBox) Run(_ context.Context, p sdk.Process) (int, error) {
	b.ran = append(b.ran, p)
	return 0, nil
}

func (b *fakeBox) Remove(context.Context) error {
	b.removed = true
	return b.removeErr
}

type fakeSandboxes struct {
	box      *fakeBox
	startErr error
	started  []string // job, data dir, password
	spec     sdk.SandboxSpec
	swept    []string
	sweepErr error
}

func (s *fakeSandboxes) Start(_ context.Context, job string, spec sdk.SandboxSpec, dataDir, password string) (verifier.Sandbox, error) {
	s.started = append(s.started, job, dataDir, password)
	s.spec = spec
	if s.startErr != nil {
		return nil, s.startErr
	}
	return s.box, nil
}

func (s *fakeSandboxes) Sweep(context.Context) ([]string, error) { return s.swept, s.sweepErr }

// database is a plugin like postgresql: its copy runs in a sandbox.
type database struct {
	*fakePlugin
	readyErr  error
	openErr   error
	passwords []string
	hosts     []string
	reference *sdk.Fingerprint
}

func (d *database) Sandbox(f *sdk.Fingerprint) (sdk.SandboxSpec, error) {
	d.reference = f
	return sdk.SandboxSpec{Image: "postgres", Major: "17", DataDir: "/var/lib/postgresql/data", PasswordEnv: "POSTGRES_PASSWORD"}, nil
}

func (d *database) SandboxReady(ctx context.Context, h sdk.SandboxHost, _ *sdk.Fingerprint) error {
	pw, _ := h.Secret(sdk.SandboxPassword)
	d.passwords = append(d.passwords, string(pw))
	_, _ = h.Run(ctx, sdk.Process{Path: "pg_isready"})
	return d.readyErr
}

func (d *database) OpenCopy(ctx context.Context, h sdk.SandboxHost, _ sdk.RestoredCopy) error {
	_, _ = h.Run(ctx, sdk.Process{Path: "psql"})
	return d.openErr
}

func (d *database) RestoreInto(ctx context.Context, h sdk.RestoreHost, _ sdk.Config, _ sdk.RestoreRequest) (sdk.RestoredCopy, error) {
	_, inSandbox := h.(sdk.SandboxHost)
	d.hosts = append(d.hosts, map[bool]string{true: "sandbox", false: "plain"}[inSandbox])
	d.clock.Advance(d.measureTook)
	return sdk.RestoredCopy{Config: sdk.Config(`{"database":"copy"}`), Result: sdk.RestoreResult{Target: "database copy"}}, nil
}

func dbRig(t *testing.T) (*rig, *database, *fakeSandboxes) {
	t.Helper()
	r, _ := streamingRig(t)
	boxes := &fakeSandboxes{box: &fakeBox{}}
	r.opts.Sandboxes = boxes
	return r, &database{fakePlugin: r.plugin}, boxes
}

var dbStages = []stage{
	{0, "snapshot", passed}, {0, "fingerprint_tag", passed}, {0, "work_volume", passed}, {1, "restic_check", passed},
	{0, "sandbox", passed}, {2, "restore", passed}, {3, "start", passed}, {4, "fingerprint", passed}, {0, "cleanup", passed},
}

// Check 1 for a database: the sandbox comes up, the copy is restored into it,
// opens, matches the source, and the sandbox goes.
func TestADatabaseIsVerifiedInItsSandboxToLevelFive(t *testing.T) {
	r, db, boxes := dbRig(t)

	result, err := newVerifier(r).Run(t.Context(), r.step, db, r.store, r.reporter)

	v := result.GetVerify()
	if err != nil || v.GetVerdict() != agentv1.VerifyVerdict_VERIFY_VERDICT_PASSED || !slices.Equal(stagesOf(v), dbStages) {
		t.Fatalf("stages %v, verdict %v, err %v", stagesOf(v), v.GetVerdict(), err)
	}
	requireRestoredInTheSandbox(t, db, boxes)
}

// requireRestoredInTheSandbox: the copy went into the sandbox chosen by the
// fingerprint, which ran the probe and the opening, and was removed.
func requireRestoredInTheSandbox(t *testing.T, db *database, boxes *fakeSandboxes) {
	t.Helper()
	if !slices.Equal(db.hosts, []string{"sandbox"}) || !boxes.box.removed || len(boxes.box.ran) != 2 {
		t.Fatalf("hosts %v, removed %v, ran %v", db.hosts, boxes.box.removed, boxes.box.ran)
	}
	if db.reference == nil || db.reference.Plugin != "files" {
		t.Errorf("the sandbox was chosen without the fingerprint: %v", db.reference)
	}
}

func TestTheSandboxGetsAFreshPasswordAndTheDataDirectoryOfTheJob(t *testing.T) {
	r, db, boxes := dbRig(t)

	_, _ = newVerifier(r).Run(t.Context(), r.step, db, r.store, r.reporter)

	if boxes.started[0] != jobID || boxes.started[1] != workDir+"/"+jobID+"/data" || len(boxes.started[2]) != 32 {
		t.Fatalf("started %q", boxes.started)
	}
	if db.passwords[0] != boxes.started[2] || !slices.Contains(r.disk.made, workDir+"/"+jobID+"/data") {
		t.Fatalf("passwords %q, made %q", db.passwords, r.disk.made)
	}
	again, _, boxes2 := dbRig(t)
	_, _ = newVerifier(again).Run(t.Context(), again.step, &database{fakePlugin: again.plugin}, again.store, again.reporter)
	if boxes2.started[2] == boxes.started[2] {
		t.Error("two checks got the same password")
	}
}

func TestEveryStageFromTheSandboxOnNamesItsImage(t *testing.T) {
	r, db, _ := dbRig(t)

	result, _ := newVerifier(r).Run(t.Context(), r.step, db, r.store, r.reporter)

	for _, s := range result.GetVerify().GetStages() {
		want := digest
		if slices.Contains([]string{"snapshot", "fingerprint_tag", "work_volume", "restic_check"}, s.GetName()) {
			want = ""
		}
		if s.GetImageDigest() != want {
			t.Errorf("%s: digest %q", s.GetName(), s.GetImageDigest())
		}
	}
	if s := find(result.GetVerify(), "sandbox"); !strings.Contains(s.GetOutput(), "postgres 17.11") {
		t.Errorf("sandbox %v", s)
	}
}

// Check 5: an extension the image lacks is no fault of the backup.
func TestASandboxThatCannotHoldTheCopyCannotVerifyAndNamesWhy(t *testing.T) {
	r, db, boxes := dbRig(t)
	db.readyErr = &sdk.SandboxRefusal{Reason: "the image has no extension postgis"}

	result, err := newVerifier(r).Run(t.Context(), r.step, db, r.store, r.reporter)

	s := find(result.GetVerify(), "sandbox")
	if s.GetStatus() != cannotRun || !strings.Contains(s.GetOutput(), "no extension postgis") || result.GetVerify().GetVerdict() != agentv1.VerifyVerdict_VERIFY_VERDICT_CANNOT_VERIFY {
		t.Fatalf("stage %v, err %v", s, err)
	}
	if !boxes.box.removed || len(db.hosts) != 0 {
		t.Fatalf("removed %v, restored %v", boxes.box.removed, db.hosts)
	}
}

func TestASandboxThatDoesNotStartOrComeUpCannotVerify(t *testing.T) {
	for name, spoil := range map[string]func(*database, *fakeSandboxes){
		"no image":   func(_ *database, b *fakeSandboxes) { b.startErr = sandbox.ErrNoImage },
		"no runtime": func(_ *database, b *fakeSandboxes) { b.startErr = sandbox.ErrNoRuntime },
		"not ready":  func(d *database, _ *fakeSandboxes) { d.readyErr = errors.New("connection refused") },
	} {
		r, db, boxes := dbRig(t)
		spoil(db, boxes)

		result, _ := newVerifier(r).Run(t.Context(), r.step, db, r.store, r.reporter)

		if s := find(result.GetVerify(), "sandbox"); s.GetStatus() != cannotRun {
			t.Errorf("%s: stage %v", name, s)
		}
	}
}

func TestAVerifierWithoutSandboxesCannotVerifyADatabase(t *testing.T) {
	r, db, _ := dbRig(t)
	r.opts.Sandboxes = nil

	result, _ := newVerifier(r).Run(t.Context(), r.step, db, r.store, r.reporter)

	if s := find(result.GetVerify(), "sandbox"); s.GetStatus() != cannotRun || !strings.Contains(s.GetOutput(), "no sandbox runtime") {
		t.Fatalf("stage %v", s)
	}
}

func TestACopyThatDoesNotOpenFailsAtLevelThree(t *testing.T) {
	r, db, _ := dbRig(t)
	db.openErr = errors.New("database \"copy\" does not exist")

	result, _ := newVerifier(r).Run(t.Context(), r.step, db, r.store, r.reporter)

	if s := find(result.GetVerify(), "start"); s.GetStatus() != failed || !strings.Contains(s.GetOutput(), "does not open in postgres 17.11") {
		t.Fatalf("stage %v", s)
	}
}

// Level 5 is the time of the restore and of opening the copy.
func TestTheRestoreTimeOfADatabaseIsTheRestoreAndTheOpening(t *testing.T) {
	r, db, _ := dbRig(t)
	r.plugin.measureTook = 30 * time.Second
	opening := &slowOpen{database: db, clock: r.clock}

	result, _ := newVerifier(r).Run(t.Context(), r.step, opening, r.store, r.reporter)

	if d := result.GetVerify().GetRestoreDuration().AsDuration(); d != 30*time.Second+20*time.Second {
		t.Fatalf("restore time %v", d)
	}
}

type slowOpen struct {
	*database
	clock *fakeClock
}

func (s *slowOpen) OpenCopy(ctx context.Context, h sdk.SandboxHost, c sdk.RestoredCopy) error {
	s.clock.Advance(20 * time.Second)
	return s.database.OpenCopy(ctx, h, c)
}

// Fingerprint gets the sandbox, as RestoreInto does.
func (s *slowOpen) Fingerprint(ctx context.Context, h sdk.Host, cfg sdk.Config, t sdk.FingerprintTarget) (sdk.Fingerprint, error) {
	return s.fakePlugin.Fingerprint(ctx, h, cfg, t)
}

func TestASandboxThatCannotBeRemovedIsSaidWithItsLabel(t *testing.T) {
	r, db, boxes := dbRig(t)
	boxes.box.removeErr = errors.New("conflict")

	result, err := newVerifier(r).Run(t.Context(), r.step, db, r.store, r.reporter)

	s := find(result.GetVerify(), "cleanup")
	if s.GetStatus() != cannotRun || !strings.Contains(s.GetOutput(), "sard.verify.job="+jobID) || err == nil {
		t.Fatalf("stage %v", s)
	}
	r2, db2, boxes2 := dbRig(t)
	boxes2.box.removeErr = errors.New("conflict")
	r2.disk.removeErr = errors.New("busy")
	result, _ = newVerifier(r2).Run(t.Context(), r2.step, db2, r2.store, r2.reporter)
	if s := find(result.GetVerify(), "cleanup"); !strings.Contains(s.GetOutput(), "sard.verify.job") || !strings.Contains(s.GetOutput(), "busy") {
		t.Fatalf("stage %v", s)
	}
}

// Check 7: at start the sandboxes of crashed checks go with their directories.
func TestAtStartTheSandboxesOfEarlierVerificationsAreRemovedFirst(t *testing.T) {
	r, _, boxes := dbRig(t)
	boxes.swept = []string{"aa11"}
	r.disk.entries = []string{jobID}

	removed, err := newVerifier(r).Sweep(t.Context())

	if err != nil || !slices.Equal(removed, []string{"aa11", workDir + "/" + jobID}) {
		t.Fatalf("removed %q, err %v", removed, err)
	}
	boxes.sweepErr = errors.New("daemon down")
	if removed, err := newVerifier(r).Sweep(t.Context()); err == nil || len(removed) != 1 {
		t.Fatalf("removed %q, err %v", removed, err)
	}
}

// A snapshot without fingerprint tells no version: the plugin picks.
func TestASnapshotWithoutFingerprintStartsTheSandboxWithoutAReference(t *testing.T) {
	r, db, _ := dbRig(t)
	r.step.Verify.Fingerprint, r.step.Verify.FingerprintSha256 = nil, ""
	r.store.snapshots[0].Tags = []string{"sard.source=s1"}

	result, _ := newVerifier(r).Run(t.Context(), r.step, db, r.store, r.reporter)

	if db.reference != nil || find(result.GetVerify(), "start").GetStatus() != passed {
		t.Fatalf("reference %v, stages %v", db.reference, stagesOf(result.GetVerify()))
	}
}
